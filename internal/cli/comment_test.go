package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/confluence-cli/internal/output"
)

// commentServer serves footer-comments (two pages, to prove draining) and
// inline-comments (one page) for page 123.
func commentServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/pages/123/footer-comments":
			if r.URL.Query().Get("cursor") == "F2" {
				_, _ = w.Write([]byte(`{"results":[{"id":"f2","body":{"storage":{"value":"<p>two</p>"}},"version":{"authorId":"u2","createdAt":"t2"},"_links":{"webui":"/w/f2"}}],"_links":{"next":""}}`))
				return
			}
			next := srv.URL + "/wiki/api/v2/pages/123/footer-comments?cursor=F2"
			_, _ = w.Write([]byte(`{"results":[{"id":"f1","body":{"storage":{"value":"<p>one</p>"}},"version":{"authorId":"u1","createdAt":"t1"},"_links":{"webui":"/w/f1"}}],"_links":{"next":"` + next + `"}}`))
		case "/wiki/api/v2/pages/123/inline-comments":
			_, _ = w.Write([]byte(`{"results":[{"id":"i1","body":{"storage":{"value":"<p>inline</p>"}},"version":{"authorId":"u3","createdAt":"t3"},"_links":{"webui":"/w/i1"}}],"_links":{"next":""}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
}

func TestCommentList_BothDrained(t *testing.T) {
	clearCredEnv(t)
	server := commentServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &CommentListCmd{Page: "123"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	// f1, f2 (drained), i1, then meta.
	if len(lines) != 4 {
		t.Fatalf("expected 3 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "f1" || lines[0]["kind"] != "footer" {
		t.Errorf("first row unexpected: %v", lines[0])
	}
	if lines[1]["id"] != "f2" || lines[1]["kind"] != "footer" {
		t.Errorf("second (drained) footer row unexpected: %v", lines[1])
	}
	if lines[2]["id"] != "i1" || lines[2]["kind"] != "inline" {
		t.Errorf("third row should be inline: %v", lines[2])
	}
	if lines[0]["body"] != "<p>one</p>" || lines[0]["author_id"] != "u1" || lines[0]["created_at"] != "t1" {
		t.Errorf("footer row fields unexpected: %v", lines[0])
	}
	if _, ok := lines[3]["_meta"].(map[string]any); !ok {
		t.Errorf("last line not meta: %v", lines[3])
	}
}

func TestCommentList_FooterOnly(t *testing.T) {
	clearCredEnv(t)
	server := commentServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &CommentListCmd{Page: "123", Footer: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 footer rows + meta, got %d: %q", len(lines), out.String())
	}
	for i, id := range []string{"f1", "f2"} {
		if lines[i]["id"] != id || lines[i]["kind"] != "footer" {
			t.Errorf("row %d unexpected: %v", i, lines[i])
		}
	}
}

func TestCommentList_URLDerivesSite(t *testing.T) {
	clearCredEnv(t)
	server := commentServer(t)
	defer server.Close()

	// No CONFLUENCE_SITE: the site comes from the URL arg.
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &CommentListCmd{Page: server.URL + "/wiki/spaces/ENG/pages/123", Inline: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 inline row + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "i1" || lines[0]["kind"] != "inline" {
		t.Errorf("inline row unexpected: %v", lines[0])
	}
}

func TestCommentList_NotFound(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &CommentListCmd{Page: "123"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "page_not_found" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want page_not_found/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

// commentAddServer serves POST /wiki/api/v2/footer-comments, echoing a fixed id
// and webui link. It records the last posted body representation.
func commentAddServer(t *testing.T, gotRepr *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/footer-comments" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if gotRepr != nil {
			var payload struct {
				Body struct {
					Representation string `json:"representation"`
				} `json:"body"`
			}
			_ = json.Unmarshal(body, &payload)
			*gotRepr = payload.Body.Representation
		}
		_, _ = w.Write([]byte(`{"id":"c-new","body":{"storage":{"value":"<p>hi</p>"}},"_links":{"webui":"/w/c-new"}}`))
	}))
}

func TestCommentAdd_Storage(t *testing.T) {
	clearCredEnv(t)
	var gotRepr string
	server := commentAddServer(t, &gotRepr)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader("<p>hello</p>"))

	cmd := &CommentAddCmd{Page: "123", BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if gotRepr != "storage" {
		t.Errorf("posted representation = %q, want storage", gotRepr)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "c-new" || row["kind"] != "footer" || row["page_id"] != "123" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["body_format"] != "storage" {
		t.Errorf("body_format = %v, want storage", row["body_format"])
	}
	if wu, _ := row["web_url"].(string); !strings.HasSuffix(wu, "/w/c-new") {
		t.Errorf("web_url = %v, want suffix /w/c-new", row["web_url"])
	}
}

func TestCommentAdd_ADF(t *testing.T) {
	clearCredEnv(t)
	var gotRepr string
	server := commentAddServer(t, &gotRepr)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader(`{"type":"doc","version":1,"content":[]}`))

	cmd := &CommentAddCmd{Page: "123", BodyFormat: "adf"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if gotRepr != "atlas_doc_format" {
		t.Errorf("posted representation = %q, want atlas_doc_format", gotRepr)
	}
	lines := parseLines(t, out.String())
	if lines[0]["body_format"] != "atlas_doc_format" {
		t.Errorf("body_format = %v, want atlas_doc_format", lines[0]["body_format"])
	}
}

func TestCommentAdd_MissingBody(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("CONFLUENCE_SITE", "https://acme.atlassian.net")
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader("   \n"))

	cmd := &CommentAddCmd{Page: "123", BodyFormat: "storage"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "missing_body" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want missing_body/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

func TestCommentAdd_MissingBodyFormat(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("CONFLUENCE_SITE", "https://acme.atlassian.net")
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader("<p>hi</p>"))

	cmd := &CommentAddCmd{Page: "123"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

// inlineCommentAddServer serves POST /wiki/api/v2/inline-comments, echoing a
// fixed id and webui link. It records the last posted textSelection.
func inlineCommentAddServer(t *testing.T, gotSel *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/inline-comments" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if gotSel != nil {
			var payload struct {
				InlineCommentProperties struct {
					TextSelection string `json:"textSelection"`
				} `json:"inlineCommentProperties"`
			}
			_ = json.Unmarshal(body, &payload)
			*gotSel = payload.InlineCommentProperties.TextSelection
		}
		_, _ = w.Write([]byte(`{"id":"ic-new","body":{"storage":{"value":"<p>hi</p>"}},"_links":{"webui":"/w/ic-new"}}`))
	}))
}

func TestCommentAdd_Inline(t *testing.T) {
	clearCredEnv(t)
	var gotSel string
	server := inlineCommentAddServer(t, &gotSel)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader("<p>hello</p>"))

	cmd := &CommentAddCmd{Page: "123", BodyFormat: "storage", Inline: true, SelectionText: "foo", MatchCount: 1}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if gotSel != "foo" {
		t.Errorf("posted textSelection = %q, want foo", gotSel)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "ic-new" || row["kind"] != "inline" || row["page_id"] != "123" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["selection_text"] != "foo" {
		t.Errorf("selection_text = %v, want foo", row["selection_text"])
	}
	if row["body_format"] != "storage" {
		t.Errorf("body_format = %v, want storage", row["body_format"])
	}
}

func TestCommentAdd_InlineMissingSelection(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("CONFLUENCE_SITE", "https://acme.atlassian.net")
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader("<p>hi</p>"))

	cmd := &CommentAddCmd{Page: "123", BodyFormat: "storage", Inline: true, MatchCount: 1}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

func TestCommentAdd_InlineBadMatchIndex(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("CONFLUENCE_SITE", "https://acme.atlassian.net")
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader("<p>hi</p>"))

	// match-index >= match-count.
	cmd := &CommentAddCmd{Page: "123", BodyFormat: "storage", Inline: true, SelectionText: "foo", MatchCount: 1, MatchIndex: 1}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}
