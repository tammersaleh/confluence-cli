package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/output"
)

// pageListServer returns an httptest server that serves a space by key and one
// page of results. When nextCursor is non-empty the first pages response links
// to a second page (cursor=nextCursor) that returns the "second" results.
func pageListServer(t *testing.T, onPagesQuery func(q string)) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/spaces":
			_, _ = w.Write([]byte(`{"results":[{"id":"12345","key":"ENG","name":"Engineering"}]}`))
		case "/wiki/api/v2/spaces/12345/pages":
			if onPagesQuery != nil {
				onPagesQuery(r.URL.RawQuery)
			}
			if r.URL.Query().Get("cursor") == "C2" {
				_, _ = w.Write([]byte(`{"results":[{"id":"200","title":"Second","parentId":"100","parentType":"page"}],"_links":{"next":""}}`))
				return
			}
			next := srv.URL + "/wiki/api/v2/spaces/12345/pages?cursor=C2"
			resp := `{"results":[{"id":"100","title":"First"}],"_links":{"next":"` + next + `"}}`
			_, _ = w.Write([]byte(resp))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
}

func parseLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad JSON line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestPageList_BareKey(t *testing.T) {
	clearCredEnv(t)
	server := pageListServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageListCmd{Space: "ENG", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "100" || row["title"] != "First" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["type"] != "page" || row["space_key"] != "ENG" {
		t.Errorf("row missing type/space_key: %v", row)
	}
	meta, ok := lines[1]["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("last line not _meta: %v", lines[1])
	}
	if meta["next_cursor"] != "C2" {
		t.Errorf("next_cursor = %v, want C2", meta["next_cursor"])
	}
	if meta["has_more"] != true {
		t.Errorf("has_more = %v, want true", meta["has_more"])
	}
}

func TestPageList_URLDerivesSite(t *testing.T) {
	clearCredEnv(t)
	server := pageListServer(t, nil)
	defer server.Close()

	// No CONFLUENCE_SITE: the site is derived from the URL arg.
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageListCmd{Space: server.URL + "/wiki/spaces/ENG", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "100" {
		t.Errorf("row unexpected: %v", lines[0])
	}
}

func TestPageList_LimitPassedToAPI(t *testing.T) {
	clearCredEnv(t)
	var gotQuery string
	server := pageListServer(t, func(q string) { gotQuery = q })
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageListCmd{Space: "ENG", Limit: 7}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !strings.Contains(gotQuery, "limit=7") {
		t.Errorf("pages query = %q, want limit=7", gotQuery)
	}
}

func TestPageList_SinglePageCursor(t *testing.T) {
	clearCredEnv(t)
	server := pageListServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageListCmd{Space: "ENG", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	// Only the first page's single row, then meta.
	if len(lines) != 2 {
		t.Fatalf("expected only first page rows + meta, got %d: %q", len(lines), out.String())
	}
	meta := lines[1]["_meta"].(map[string]any)
	if meta["next_cursor"] != "C2" {
		t.Errorf("next_cursor = %v, want C2", meta["next_cursor"])
	}
}

func TestPageList_All(t *testing.T) {
	clearCredEnv(t)
	server := pageListServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageListCmd{Space: "ENG", Limit: 25, All: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	// Two rows (both pages) + meta.
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "100" || lines[1]["id"] != "200" {
		t.Errorf("rows unexpected: %v, %v", lines[0], lines[1])
	}
	if lines[1]["parent_id"] != "100" || lines[1]["parent_type"] != "page" {
		t.Errorf("second row missing parent fields: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["has_more"] != false {
		t.Errorf("has_more = %v, want false", meta["has_more"])
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Errorf("next_cursor should be omitted in --all mode: %v", meta)
	}
}

func TestPageList_SpaceNotFound(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wiki/api/v2/spaces" {
			// Empty results -> ErrSpaceNotFound.
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		t.Errorf("unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageListCmd{Space: "NOPE", Limit: 25}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "space_not_found" {
		t.Errorf("Err = %q, want space_not_found", oErr.Err)
	}
}

// pageGetServer serves /wiki/api/v2/pages/{id}. Any id in notFound returns 404;
// otherwise it returns a page whose body reflects the requested body-format.
func pageGetServer(t *testing.T, notFound map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, prefix)
		if notFound[id] {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		var body string
		switch r.URL.Query().Get("body-format") {
		case "atlas_doc_format":
			// value is a JSON-encoded ADF document (a string in the API response).
			body = `"body": {"atlas_doc_format": {"value": "{\"version\":1,\"type\":\"doc\",\"content\":[]}"}}`
		case "view":
			body = `"body": {"view": {"value": "<div>rendered</div>"}}`
		default:
			body = `"body": {"storage": {"value": "<p>Hello</p>"}}`
		}

		_, _ = w.Write([]byte(`{
			"id": "` + id + `",
			"title": "Page ` + id + `",
			"spaceId": "space1",
			"authorId": "user456",
			"createdAt": "2024-01-15T10:30:00.000Z",
			` + body + `,
			"version": {"number": 5, "createdAt": "2024-06-20T14:45:00.000Z"},
			"_links": {"webui": "/spaces/TEST/pages/` + id + `/Page"}
		}`))
	}))
}

func newPageGetCLI(t *testing.T, out, errBuf *bytes.Buffer) *CLI {
	t.Helper()
	c := &CLI{}
	c.SetOutput(out, errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	return c
}

func TestPageGet_Storage(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["input"] != "123" || row["id"] != "123" {
		t.Errorf("input/id unexpected: %v", row)
	}
	if row["body"] != "<p>Hello</p>" {
		t.Errorf("body = %v, want <p>Hello</p>", row["body"])
	}
	if row["body_format"] != "storage" {
		t.Errorf("body_format = %v, want storage", row["body_format"])
	}
	if row["title"] != "Page 123" || row["version"] != float64(5) || row["web_url"] == "" {
		t.Errorf("row missing title/version/web_url: %v", row)
	}
}

func TestPageGet_ADFAlias(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "adf"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// The emitted line must carry body as a JSON object, not a quoted string.
	firstLine := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(firstLine, `"body":{`) {
		t.Errorf("expected body as JSON object, got line: %q", firstLine)
	}
	lines := parseLines(t, out.String())
	if lines[0]["body_format"] != "atlas_doc_format" {
		t.Errorf("body_format = %v, want atlas_doc_format", lines[0]["body_format"])
	}
	if _, ok := lines[0]["body"].(map[string]any); !ok {
		t.Errorf("body should decode as an object, got %T: %v", lines[0]["body"], lines[0]["body"])
	}
}

func TestPageGet_View(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "view"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if lines[0]["body"] != "<div>rendered</div>" {
		t.Errorf("body = %v, want <div>rendered</div>", lines[0]["body"])
	}
	if lines[0]["body_format"] != "view" {
		t.Errorf("body_format = %v, want view", lines[0]["body_format"])
	}
}

func TestPageGet_MultipleBareIDs(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"100", "200"}, BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["input"] != "100" || lines[1]["input"] != "200" {
		t.Errorf("rows missing inputs: %v, %v", lines[0], lines[1])
	}
}

func TestPageGet_URLDerivesSite(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, nil)
	defer server.Close()

	// No CONFLUENCE_SITE: the site comes from the URL arg.
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{server.URL + "/wiki/spaces/ENG/pages/777/Title"}, BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if lines[0]["id"] != "777" {
		t.Errorf("id = %v, want 777", lines[0]["id"])
	}
	if lines[0]["input"] != server.URL+"/wiki/spaces/ENG/pages/777/Title" {
		t.Errorf("input = %v, want the URL", lines[0]["input"])
	}
}

func TestPageGet_MixedSiteURLs(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{
		"https://acme.atlassian.net/wiki/spaces/ENG/pages/1/A",
		"https://other.atlassian.net/wiki/spaces/ENG/pages/2/B",
	}, BodyFormat: "storage"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

func TestPageGet_PerItemNotFound(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, map[string]bool{"404": true})
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123", "404"}, BodyFormat: "storage"}
	err := cmd.Run(c)

	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", exitErr.Code, output.ExitGeneral)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected success row + error row + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "123" {
		t.Errorf("first row should be success: %v", lines[0])
	}
	if lines[1]["error"] != "page_not_found" || lines[1]["input"] != "404" {
		t.Errorf("second row should be inline error for 404: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["error_count"] != float64(1) {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}

func TestPageGet_BadBodyFormat(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "xml"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

// pageGetMarkdownServer serves a single page (storage body) plus its attachments
// list. The attachments JSON is served verbatim from atts.
func pageGetMarkdownServer(t *testing.T, storageBody, atts string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if strings.HasSuffix(rest, "/attachments") {
			_, _ = w.Write([]byte(atts))
			return
		}
		id := rest
		_, _ = w.Write([]byte(`{
			"id": "` + id + `",
			"title": "Page ` + id + `",
			"spaceId": "space1",
			"authorId": "user456",
			"createdAt": "2024-01-15T10:30:00.000Z",
			"body": {"storage": {"value": ` + jsonQuote(storageBody) + `}},
			"version": {"number": 5, "createdAt": "2024-06-20T14:45:00.000Z"},
			"_links": {"webui": "/spaces/TEST/pages/` + id + `/Page"}
		}`))
	}))
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestPageGet_Markdown(t *testing.T) {
	clearCredEnv(t)
	storage := `<p>See <ac:image><ri:attachment ri:filename="diagram.png"/></ac:image></p>`
	// DownloadLink lacks the /wiki prefix, exercising the absolutizer's prefixing.
	atts := `{"results":[{"id":"att1","title":"diagram.png","mediaType":"image/png","downloadLink":"/download/attachments/123/diagram.png"}],"_links":{"next":""}}`
	server := pageGetMarkdownServer(t, storage, atts)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "markdown"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["body_format"] != "markdown" {
		t.Errorf("body_format = %v, want markdown", row["body_format"])
	}
	if row["source_body_format"] != "storage" {
		t.Errorf("source_body_format = %v, want storage", row["source_body_format"])
	}
	body, _ := row["body"].(string)
	wantURL := server.URL + "/wiki/download/attachments/123/diagram.png"
	if !strings.Contains(body, wantURL) {
		t.Errorf("body missing absolute attachment URL %q:\n%s", wantURL, body)
	}
	if strings.Contains(body, "_attachments/") {
		t.Errorf("body should not contain local _attachments path:\n%s", body)
	}
}

func TestPageGet_MarkdownNoAttachments(t *testing.T) {
	clearCredEnv(t)
	storage := `<h1>Title</h1><p>Just text.</p>`
	atts := `{"results":[],"_links":{"next":""}}`
	server := pageGetMarkdownServer(t, storage, atts)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "markdown"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["body_format"] != "markdown" || row["source_body_format"] != "storage" {
		t.Errorf("format fields unexpected: %v", row)
	}
	body, _ := row["body"].(string)
	if !strings.Contains(body, "# Title") {
		t.Errorf("body missing converted heading:\n%s", body)
	}
}

func TestPageGet_SiteFlagMismatch(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{Site: "https://other.atlassian.net"}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageGetCmd{Refs: []string{"https://acme.atlassian.net/wiki/spaces/ENG/pages/123"}, BodyFormat: "storage"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

func TestPageGet_MarkdownAttachmentsError(t *testing.T) {
	clearCredEnv(t)
	// The page (storage) fetch succeeds, but listing attachments fails. markdown
	// mode treats that as a per-item error that continues the batch.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if strings.HasSuffix(rest, "/attachments") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		id := rest
		_, _ = w.Write([]byte(`{
			"id": "` + id + `",
			"title": "Page ` + id + `",
			"spaceId": "space1",
			"authorId": "user456",
			"createdAt": "2024-01-15T10:30:00.000Z",
			"body": {"storage": {"value": "<p>Hello</p>"}},
			"version": {"number": 5, "createdAt": "2024-06-20T14:45:00.000Z"},
			"_links": {"webui": "/spaces/TEST/pages/` + id + `/Page"}
		}`))
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "markdown"}
	err := cmd.Run(c)

	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", exitErr.Code, output.ExitGeneral)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected error row + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["error"] == nil || lines[0]["input"] != "123" {
		t.Errorf("first row should be inline error for 123: %v", lines[0])
	}
	meta := lines[1]["_meta"].(map[string]any)
	if meta["error_count"] != float64(1) {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}

func TestPageGet_PerItemForbidden(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"123"}, BodyFormat: "storage"}
	err := cmd.Run(c)

	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != output.ExitGeneral {
		t.Errorf("exit code = %d, want %d", exitErr.Code, output.ExitGeneral)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected error row + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["error"] != "forbidden" || lines[0]["input"] != "123" {
		t.Errorf("first row should be inline forbidden error for 123: %v", lines[0])
	}
	meta := lines[1]["_meta"].(map[string]any)
	if meta["error_count"] != float64(1) {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}

// pageChildrenServer serves /wiki/api/v2/pages/{id}/children. When the request
// cursor is "C2" it returns the second (final) page; otherwise the first page
// links to a second (cursor=C2). ids in notFound return 404.
func pageChildrenServer(t *testing.T, notFound map[string]bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		const suffix = "/children"
		if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
		if notFound[id] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("cursor") == "C2" {
			_, _ = w.Write([]byte(`{"results":[{"id":"11","title":"Child B","status":"current"}],"_links":{"next":""}}`))
			return
		}
		next := srv.URL + prefix + id + suffix + "?cursor=C2"
		_, _ = w.Write([]byte(`{"results":[{"id":"10","title":"Child A","status":"current"}],"_links":{"next":"` + next + `"}}`))
	}))
	return srv
}

func TestPageChildren_BareID(t *testing.T) {
	clearCredEnv(t)
	server := pageChildrenServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageChildrenCmd{Ref: "1", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "10" || row["title"] != "Child A" || row["type"] != "page" {
		t.Errorf("row shape unexpected: %v", row)
	}
	meta := lines[1]["_meta"].(map[string]any)
	if meta["next_cursor"] != "C2" || meta["has_more"] != true {
		t.Errorf("meta unexpected: %v", meta)
	}
}

func TestPageChildren_All(t *testing.T) {
	clearCredEnv(t)
	server := pageChildrenServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageChildrenCmd{Ref: "1", Limit: 25, All: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "10" || lines[1]["id"] != "11" {
		t.Errorf("rows unexpected: %v, %v", lines[0], lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["has_more"] != false {
		t.Errorf("has_more = %v, want false", meta["has_more"])
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Errorf("next_cursor should be omitted in --all mode: %v", meta)
	}
}

func TestPageChildren_URLDerivesSite(t *testing.T) {
	clearCredEnv(t)
	server := pageChildrenServer(t, nil)
	defer server.Close()

	// No CONFLUENCE_SITE: the site is derived from the URL arg.
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageChildrenCmd{Ref: server.URL + "/wiki/spaces/ENG/pages/1/Title", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if lines[0]["id"] != "10" {
		t.Errorf("row unexpected: %v", lines[0])
	}
}

func TestPageChildren_NotFound(t *testing.T) {
	clearCredEnv(t)
	server := pageChildrenServer(t, map[string]bool{"404": true})
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageChildrenCmd{Ref: "404", Limit: 25}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "page_not_found" {
		t.Errorf("Err = %q, want page_not_found", oErr.Err)
	}
}

// pageAncestorsServer serves /wiki/api/v2/pages/{id}/ancestors. ids in notFound
// return 404; otherwise a fixed root-most-first chain.
func pageAncestorsServer(t *testing.T, notFound map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		const suffix = "/ancestors"
		if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
		if notFound[id] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"1","type":"page"},{"id":"2","type":"page"}],"_links":{"next":""}}`))
	}))
}

func TestPageAncestors_Chain(t *testing.T) {
	clearCredEnv(t)
	server := pageAncestorsServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageAncestorsCmd{Ref: "3"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "1" || lines[0]["type"] != "page" {
		t.Errorf("first ancestor unexpected: %v", lines[0])
	}
	if lines[1]["id"] != "2" {
		t.Errorf("second ancestor unexpected: %v", lines[1])
	}
}

func TestPageAncestors_NotFound(t *testing.T) {
	clearCredEnv(t)
	server := pageAncestorsServer(t, map[string]bool{"404": true})
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageAncestorsCmd{Ref: "404"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "page_not_found" {
		t.Errorf("Err = %q, want page_not_found", oErr.Err)
	}
}

// pageTreeServer serves the endpoints GetPages calls: space key lookup, the
// space crawl list, and per-page parent lookups. Three pages: p2 and p3 are
// children of p1.
func pageTreeServer(t *testing.T) *httptest.Server {
	t.Helper()
	parents := map[string]string{"1": "", "2": "1", "3": "1"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wiki/api/v2/spaces":
			_, _ = w.Write([]byte(`{"results":[{"id":"12345","key":"ENG","name":"Engineering"}]}`))
		case r.URL.Path == "/wiki/api/v2/spaces/12345/pages":
			_, _ = w.Write([]byte(`{"results":[{"id":"1","title":"Root"},{"id":"2","title":"Child A"},{"id":"3","title":"Child B"}],"_links":{"next":""}}`))
		case strings.HasPrefix(r.URL.Path, "/wiki/api/v2/pages/"):
			id := strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/pages/")
			parent, ok := parents[id]
			if !ok {
				t.Errorf("unexpected page id: %s", id)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			body := `{"id":"` + id + `","title":"t"`
			if parent != "" {
				body += `,"parentId":"` + parent + `","parentType":"page"`
			}
			body += `}`
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestPageTree_DFS(t *testing.T) {
	clearCredEnv(t)
	server := pageTreeServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageTreeCmd{Space: "ENG"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 4 {
		t.Fatalf("expected 3 rows + meta, got %d: %q", len(lines), out.String())
	}
	// DFS from root 1, children 2 then 3 (sorted by id).
	if lines[0]["id"] != "1" || lines[0]["depth"] != float64(0) {
		t.Errorf("root row unexpected: %v", lines[0])
	}
	if _, ok := lines[0]["parent_id"]; ok {
		t.Errorf("root should omit parent_id: %v", lines[0])
	}
	if lines[1]["id"] != "2" || lines[1]["depth"] != float64(1) || lines[1]["parent_id"] != "1" {
		t.Errorf("first child row unexpected: %v", lines[1])
	}
	if lines[2]["id"] != "3" || lines[2]["depth"] != float64(1) || lines[2]["parent_id"] != "1" {
		t.Errorf("second child row unexpected: %v", lines[2])
	}
	if lines[0]["type"] != "page" {
		t.Errorf("type should be page: %v", lines[0])
	}
}

func TestPageList_SiteFlagMismatch(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{Site: "https://other.atlassian.net"}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageListCmd{Space: "https://acme.atlassian.net/wiki/spaces/ENG", Limit: 25}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}
