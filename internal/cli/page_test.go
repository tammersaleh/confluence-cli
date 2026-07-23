package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/confluence-cli/internal/output"
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

		subtype := ""
		if id == "live1" {
			subtype = `"subtype": "live",`
		}

		_, _ = w.Write([]byte(`{
			"id": "` + id + `",
			"title": "Page ` + id + `",
			"spaceId": "space1",
			"authorId": "user456",
			"createdAt": "2024-01-15T10:30:00.000Z",
			` + subtype + `
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

func TestPageGet_Subtype(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, nil)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	// A live doc reports subtype; a regular page omits it entirely.
	cmd := &PageGetCmd{Refs: []string{"live1", "123"}, BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	lines := parseLines(t, out.String())
	if lines[0]["subtype"] != "live" {
		t.Errorf("live1 subtype = %v, want live", lines[0]["subtype"])
	}
	if _, ok := lines[1]["subtype"]; ok {
		t.Errorf("regular page should omit subtype, got %v", lines[1]["subtype"])
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

func TestPageGet_ResolveAuthors(t *testing.T) {
	clearCredEnv(t)
	userCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wiki/rest/api/user":
			userCalls++
			if r.URL.Query().Get("accountId") != "user456" {
				t.Errorf("unexpected accountId: %s", r.URL.Query().Get("accountId"))
			}
			_, _ = w.Write([]byte(`{"accountId":"user456","displayName":"Ada Lovelace","email":"ada@example.com"}`))
		case strings.HasPrefix(r.URL.Path, "/wiki/api/v2/pages/"):
			id := strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/pages/")
			_, _ = w.Write([]byte(`{
				"id": "` + id + `",
				"title": "Page ` + id + `",
				"spaceId": "space1",
				"authorId": "user456",
				"createdAt": "2024-01-15T10:30:00.000Z",
				"body": {"storage": {"value": "<p>Hi</p>"}},
				"version": {"number": 1, "createdAt": "2024-06-20T14:45:00.000Z"},
				"_links": {"webui": "/spaces/TEST/pages/` + id + `/Page"}
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	// Two pages by the same author: author_name is emitted on both, but the user
	// lookup is cached (one call, not two).
	cmd := &PageGetCmd{Refs: []string{"100", "200"}, BodyFormat: "storage", ResolveAuthors: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if lines[0]["author_name"] != "Ada Lovelace" || lines[1]["author_name"] != "Ada Lovelace" {
		t.Errorf("author_name not resolved: %v, %v", lines[0], lines[1])
	}
	if userCalls != 1 {
		t.Errorf("user lookups = %d, want 1 (cached)", userCalls)
	}
}

func TestPageGet_ResolveAuthorsBestEffort(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wiki/rest/api/user":
			// The author lookup fails; the row must still be emitted, sans name.
			w.WriteHeader(http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/wiki/api/v2/pages/"):
			id := strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/pages/")
			_, _ = w.Write([]byte(`{
				"id": "` + id + `",
				"title": "Page ` + id + `",
				"spaceId": "space1",
				"authorId": "ghost",
				"createdAt": "2024-01-15T10:30:00.000Z",
				"body": {"storage": {"value": "<p>Hi</p>"}},
				"version": {"number": 1, "createdAt": "2024-06-20T14:45:00.000Z"},
				"_links": {"webui": "/spaces/TEST/pages/` + id + `/Page"}
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := newPageGetCLI(t, &out, &errBuf)

	cmd := &PageGetCmd{Refs: []string{"100"}, BodyFormat: "storage", ResolveAuthors: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if lines[0]["id"] != "100" {
		t.Fatalf("row missing: %v", lines[0])
	}
	if _, ok := lines[0]["author_name"]; ok {
		t.Errorf("author_name should be absent on failed lookup: %v", lines[0])
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

// pageDescendantsServer serves /wiki/api/v2/pages/{id}/descendants. When the
// request cursor is "C2" it returns the second (final) page; otherwise the
// first page links to a second (cursor=C2). ids in notFound return 404.
func pageDescendantsServer(t *testing.T, notFound map[string]bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		const suffix = "/descendants"
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
			_, _ = w.Write([]byte(`{"results":[{"id":"11","title":"Grandchild","type":"page","parentId":"10","depth":2}],"_links":{"next":""}}`))
			return
		}
		next := srv.URL + prefix + id + suffix + "?cursor=C2"
		_, _ = w.Write([]byte(`{"results":[{"id":"10","title":"Child","type":"page","parentId":"1","depth":1}],"_links":{"next":"` + next + `"}}`))
	}))
	return srv
}

func TestPageDescendants_BareID(t *testing.T) {
	clearCredEnv(t)
	server := pageDescendantsServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageDescendantsCmd{Ref: "1", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "10" || row["title"] != "Child" || row["type"] != "page" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["depth"] != float64(1) || row["parent_id"] != "1" {
		t.Errorf("depth/parent_id unexpected: %v", row)
	}
	meta := lines[1]["_meta"].(map[string]any)
	if meta["next_cursor"] != "C2" || meta["has_more"] != true {
		t.Errorf("meta unexpected: %v", meta)
	}
}

func TestPageDescendants_All(t *testing.T) {
	clearCredEnv(t)
	server := pageDescendantsServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageDescendantsCmd{Ref: "1", Limit: 25, All: true}
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
	if lines[1]["depth"] != float64(2) {
		t.Errorf("depth unexpected: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["has_more"] != false {
		t.Errorf("has_more = %v, want false", meta["has_more"])
	}
	if _, ok := meta["next_cursor"]; ok {
		t.Errorf("next_cursor should be omitted in --all mode: %v", meta)
	}
}

func TestPageDescendants_URLDerivesSite(t *testing.T) {
	clearCredEnv(t)
	server := pageDescendantsServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageDescendantsCmd{Ref: server.URL + "/wiki/spaces/ENG/pages/1/Title", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if lines[0]["id"] != "10" {
		t.Errorf("row unexpected: %v", lines[0])
	}
}

func TestPageDescendants_NotFound(t *testing.T) {
	clearCredEnv(t)
	server := pageDescendantsServer(t, map[string]bool{"404": true})
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &PageDescendantsCmd{Ref: "404", Limit: 25}
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

// --- write command tests ---

func setEnvCreds(t *testing.T, siteURL string) {
	t.Helper()
	t.Setenv("CONFLUENCE_SITE", siteURL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")
}

func newWriteCLI(t *testing.T, out, errBuf *bytes.Buffer, stdin string) *CLI {
	t.Helper()
	c := &CLI{}
	c.SetOutput(out, errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))
	c.SetInput(strings.NewReader(stdin))
	return c
}

// capturedCreate records the decoded POST body of a create call.
type capturedCreate struct {
	spaceID    string
	title      string
	parentID   string
	subtype    string
	subtypeSet bool
	hasBody    bool
	bodyRep    string
	bodyVal    string
}

// pageCreateServer serves the space lookup and POST /pages, capturing the body.
func pageCreateServer(t *testing.T, cap *capturedCreate) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/wiki/api/v2/spaces":
			_, _ = w.Write([]byte(`{"results":[{"id":"12345","key":"ENG","name":"Engineering"}]}`))
		case r.URL.Path == "/wiki/api/v2/pages" && r.Method == http.MethodPost:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode POST body: %v", err)
			}
			cap.spaceID, _ = payload["spaceId"].(string)
			cap.title, _ = payload["title"].(string)
			cap.parentID, _ = payload["parentId"].(string)
			cap.subtype, cap.subtypeSet = payload["subtype"].(string)
			if b, ok := payload["body"].(map[string]any); ok {
				cap.hasBody = true
				cap.bodyRep, _ = b["representation"].(string)
				cap.bodyVal, _ = b["value"].(string)
			}
			resp := map[string]any{
				"id":        "999",
				"title":     cap.title,
				"spaceId":   "12345",
				"authorId":  "user1",
				"createdAt": "2024-01-01T00:00:00Z",
				"version":   map[string]any{"number": 1},
				"_links":    map[string]any{"webui": "/spaces/ENG/pages/999/T"},
			}
			if cap.parentID != "" {
				resp["parentId"] = cap.parentID
			}
			// Echo the subtype the client sent, mimicking Confluence's response
			// for a live-doc create.
			if cap.subtype != "" {
				resp["subtype"] = cap.subtype
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestPageCreate_StorageBody(t *testing.T) {
	clearCredEnv(t)
	var cap capturedCreate
	server := pageCreateServer(t, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>Hi</p>")

	cmd := &PageCreateCmd{Space: "ENG", Title: "New Page", BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if cap.bodyRep != "storage" || cap.bodyVal != "<p>Hi</p>" {
		t.Errorf("body sent = %q/%q, want storage/<p>Hi</p>", cap.bodyRep, cap.bodyVal)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "999" || row["title"] != "New Page" || row["space_id"] != "12345" || row["version"] != float64(1) {
		t.Errorf("row shape unexpected: %v", row)
	}
	if _, ok := row["body"]; ok {
		t.Errorf("row should not echo body: %v", row)
	}
}

func TestPageCreate_Live(t *testing.T) {
	clearCredEnv(t)
	var cap capturedCreate
	server := pageCreateServer(t, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>Hi</p>")

	cmd := &PageCreateCmd{Space: "ENG", Title: "Live Page", BodyFormat: "storage", Live: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.subtype != "live" {
		t.Errorf("subtype sent = %q, want live", cap.subtype)
	}
	row := parseLines(t, out.String())[0]
	if row["subtype"] != "live" {
		t.Errorf("row subtype = %v, want live", row["subtype"])
	}
}

func TestPageCreate_NotLiveOmitsSubtype(t *testing.T) {
	clearCredEnv(t)
	var cap capturedCreate
	server := pageCreateServer(t, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>Hi</p>")

	cmd := &PageCreateCmd{Space: "ENG", Title: "Plain", BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.subtypeSet {
		t.Errorf("subtype should be omitted from request when not --live, got %q", cap.subtype)
	}
	row := parseLines(t, out.String())[0]
	if _, ok := row["subtype"]; ok {
		t.Errorf("row should omit subtype for a regular page, got %v", row["subtype"])
	}
}

func TestPageCreate_ADFBody(t *testing.T) {
	clearCredEnv(t)
	var cap capturedCreate
	server := pageCreateServer(t, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	adf := `{"version":1,"type":"doc","content":[]}`
	c := newWriteCLI(t, &out, &errBuf, adf)

	cmd := &PageCreateCmd{Space: "ENG", Title: "ADF Page", BodyFormat: "adf"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.bodyRep != "atlas_doc_format" {
		t.Errorf("body representation = %q, want atlas_doc_format", cap.bodyRep)
	}
}

func TestPageCreate_MarkdownBody(t *testing.T) {
	clearCredEnv(t)
	var cap capturedCreate
	server := pageCreateServer(t, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "# Title\n\nHello **world**")

	cmd := &PageCreateCmd{Space: "ENG", Title: "MD Page", BodyFormat: "markdown"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.bodyRep != "storage" {
		t.Errorf("body representation = %q, want storage", cap.bodyRep)
	}
	if !strings.Contains(cap.bodyVal, "<h1>Title</h1>") || !strings.Contains(cap.bodyVal, "<strong>world</strong>") {
		t.Errorf("body value not converted from markdown: %q", cap.bodyVal)
	}
}

func TestPageCreate_NoBody(t *testing.T) {
	clearCredEnv(t)
	var cap capturedCreate
	server := pageCreateServer(t, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	// Empty pipe is present-but-empty: no body, and no --body-format required.
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageCreateCmd{Space: "ENG", Title: "Empty"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.hasBody {
		t.Errorf("expected no body sent, got rep=%q val=%q", cap.bodyRep, cap.bodyVal)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 2 || lines[0]["id"] != "999" {
		t.Errorf("expected created row + meta: %q", out.String())
	}
}

func TestPageCreate_BodyNoFormat(t *testing.T) {
	clearCredEnv(t)
	setEnvCreds(t, "https://acme.atlassian.net")

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>x</p>")

	cmd := &PageCreateCmd{Space: "ENG", Title: "T"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

func TestPageCreate_ParentResolves(t *testing.T) {
	clearCredEnv(t)
	var cap capturedCreate
	server := pageCreateServer(t, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageCreateCmd{Space: "ENG", Title: "Child", Parent: "555"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.parentID != "555" {
		t.Errorf("parentId sent = %q, want 555", cap.parentID)
	}
	lines := parseLines(t, out.String())
	if lines[0]["parent_id"] != "555" {
		t.Errorf("row parent_id = %v, want 555", lines[0]["parent_id"])
	}
}

// capturedUpdate records whether a PUT happened and its decoded body.
type capturedUpdate struct {
	called       bool
	title        string
	status       string
	version      int
	bodyRep      string
	bodyVal      string
	hasParent    bool
	parentID     string
	inlineDrains int
}

// pageUpdateServer serves the preflight GET, the PUT, and an empty
// inline-comments list (so the guard passes). GET returns curBody under
// whichever body-format is requested.
func pageUpdateServer(t *testing.T, curVersion int, curTitle, curBody string, cap *capturedUpdate) *httptest.Server {
	return pageUpdateServerWithInline(t, curVersion, curTitle, curBody, "", cap)
}

// pageUpdateServerWithInline is pageUpdateServer plus a caller-supplied
// inline-comments results array (JSON, without the surrounding results wrapper),
// used to exercise the inline-comment guard.
func pageUpdateServerWithInline(t *testing.T, curVersion int, curTitle, curBody, inlineResults string, cap *capturedUpdate) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if id, ok := strings.CutSuffix(rest, "/inline-comments"); ok {
			_ = id
			cap.inlineDrains++
			_, _ = w.Write([]byte(`{"results":[` + inlineResults + `],"_links":{"next":""}}`))
			return
		}
		id := rest
		switch r.Method {
		case http.MethodGet:
			bodyField := `"storage": {"value": ` + jsonQuote(curBody) + `}`
			if r.URL.Query().Get("body-format") == "atlas_doc_format" {
				bodyField = `"atlas_doc_format": {"value": ` + jsonQuote(curBody) + `}`
			}
			_, _ = w.Write([]byte(`{
				"id": "` + id + `",
				"title": ` + jsonQuote(curTitle) + `,
				"spaceId": "space1",
				"status": "current",
				"parentId": "100",
				"authorId": "user1",
				"createdAt": "2024-01-01T00:00:00Z",
				"body": {` + bodyField + `},
				"version": {"number": ` + fmt.Sprint(curVersion) + `, "createdAt": "2024-06-20T00:00:00Z"},
				"_links": {"webui": "/spaces/T/pages/` + id + `/P"}
			}`))
		case http.MethodPut:
			cap.called = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			cap.title, _ = payload["title"].(string)
			cap.status, _ = payload["status"].(string)
			cap.parentID, cap.hasParent = payload["parentId"].(string)
			if v, ok := payload["version"].(map[string]any); ok {
				if n, ok := v["number"].(float64); ok {
					cap.version = int(n)
				}
			}
			if b, ok := payload["body"].(map[string]any); ok {
				cap.bodyRep, _ = b["representation"].(string)
				cap.bodyVal, _ = b["value"].(string)
			}
			resp := map[string]any{
				"id":        id,
				"title":     cap.title,
				"spaceId":   "space1",
				"authorId":  "user1",
				"createdAt": "2024-01-01T00:00:00Z",
				"version":   map[string]any{"number": cap.version},
				"_links":    map[string]any{"webui": "/spaces/T/pages/" + id + "/P"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func TestPageUpdate_Conflict(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	server := pageUpdateServer(t, 8, "Cur", "<p>cur</p>", &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 7, Title: "New"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "version_conflict" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want version_conflict/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
	if cap.called {
		t.Errorf("PUT should not be called on version conflict")
	}
	if cap.inlineDrains != 0 {
		t.Errorf("guard must not run on a version conflict (drains=%d)", cap.inlineDrains)
	}
}

func TestPageUpdate_TitleOnlyPreservesBodyAsADF(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	adf := `{"type":"doc","version":1,"content":[]}`
	server := pageUpdateServer(t, 5, "Old Title", adf, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "") // no body piped

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5, Title: "New Title"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !cap.called {
		t.Fatalf("PUT not called")
	}
	if cap.title != "New Title" {
		t.Errorf("title sent = %q, want New Title", cap.title)
	}
	// No-body update preserves the body as ADF (exact passthrough), not storage.
	if cap.bodyRep != "atlas_doc_format" || cap.bodyVal != adf {
		t.Errorf("body sent = %q/%q, want atlas_doc_format/%s (preserved)", cap.bodyRep, cap.bodyVal, adf)
	}
	if cap.status != "current" {
		t.Errorf("status sent = %q, want current (preserved)", cap.status)
	}
	if cap.hasParent {
		t.Errorf("ordinary update must not send parentId")
	}
	if cap.version != 6 {
		t.Errorf("version sent = %d, want 6", cap.version)
	}
	lines := parseLines(t, out.String())
	if lines[0]["previous_version"] != float64(5) || lines[0]["version"] != float64(6) || lines[0]["updated"] != true {
		t.Errorf("row unexpected: %v", lines[0])
	}
}

func TestPageUpdate_TitleUnchangedIsNoOp(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	server := pageUpdateServer(t, 5, "Same Title", `{"type":"doc","version":1,"content":[]}`, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5, Title: "Same Title"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.called {
		t.Errorf("no-op title update must not PUT")
	}
	lines := parseLines(t, out.String())
	if lines[0]["updated"] != false || lines[0]["version"] != float64(5) {
		t.Errorf("expected updated=false version=5, got %v", lines[0])
	}
}

func TestPageUpdate_NothingToUpdate(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5}
	err := cmd.Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_input" {
		t.Fatalf("expected invalid_input for no-op, got %v", err)
	}
}

func TestPageUpdate_BodyUpdate(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	server := pageUpdateServer(t, 5, "Old", "<p>current</p>", &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>new body</p>")

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5, BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.bodyVal != "<p>new body</p>" {
		t.Errorf("body sent = %q, want <p>new body</p>", cap.bodyVal)
	}
	// Title omitted -> preserved from current.
	if cap.title != "Old" {
		t.Errorf("title sent = %q, want Old (preserved)", cap.title)
	}
	lines := parseLines(t, out.String())
	if lines[0]["previous_version"] != float64(5) {
		t.Errorf("previous_version = %v, want 5", lines[0]["previous_version"])
	}
}

// An open inline comment blocks a body-replacing update; no PUT, and the fatal
// error carries the blocking comment in data.
func TestPageUpdate_GuardBlocksUnresolved(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	inline := `{"id":"ic1","body":{"storage":{"value":"<p>q</p>"}},"version":{"authorId":"u1"},"resolutionStatus":"open","properties":{"inlineOriginalSelection":"target","inlineMarkerRef":"m1"},"_links":{"webui":"https://x/w/ic1"}}`
	server := pageUpdateServerWithInline(t, 5, "T", "<p>cur</p>", inline, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>new</p>")

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5, BodyFormat: "storage"}
	err := cmd.Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "unresolved_inline_comments" {
		t.Fatalf("expected unresolved_inline_comments, got %v", err)
	}
	if cap.called {
		t.Errorf("guard must prevent the PUT")
	}
	blockers, ok := oErr.Data["blocking_comments"].([]map[string]any)
	if !ok || len(blockers) != 1 {
		t.Fatalf("expected 1 blocking comment in data, got %v", oErr.Data)
	}
	b := blockers[0]
	if b["id"] != "ic1" || b["resolution_status"] != "open" || b["original_selection"] != "target" || b["inline_marker_ref"] != "m1" {
		t.Errorf("blocker payload unexpected: %v", b)
	}
	if out.String() != "" {
		t.Errorf("nothing should reach stdout on a fatal refusal, got %q", out.String())
	}
}

// A resolved inline comment does not block; the update proceeds.
func TestPageUpdate_GuardAllowsResolved(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	inline := `{"id":"ic1","body":{"storage":{"value":"<p>q</p>"}},"version":{"authorId":"u1"},"resolutionStatus":"resolved"}`
	server := pageUpdateServerWithInline(t, 5, "T", "<p>cur</p>", inline, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>new</p>")

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5, BodyFormat: "storage"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !cap.called {
		t.Errorf("resolved comments must not block the PUT")
	}
}

// A no-body (title-only) update re-sends exact ADF and is NOT guarded, even with
// an open inline comment present: it never drains and the PUT lands.
func TestPageUpdate_TitleOnlySkipsGuard(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	inline := `{"id":"ic1","resolutionStatus":"open","properties":{"inlineOriginalSelection":"t","inlineMarkerRef":"m1"}}`
	server := pageUpdateServerWithInline(t, 5, "Old", `{"type":"doc","version":1,"content":[]}`, inline, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "") // no body

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5, Title: "New"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !cap.called {
		t.Errorf("title-only update should PUT")
	}
	if cap.inlineDrains != 0 {
		t.Errorf("title-only update must not run the inline-comment guard (drains=%d)", cap.inlineDrains)
	}
}

// The override skips the inspection entirely (no drain) and lets the write land.
func TestPageUpdate_GuardOverrideSkipsInspection(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	inline := `{"id":"ic1","body":{"storage":{"value":"<p>q</p>"}},"version":{"authorId":"u1"},"resolutionStatus":"open"}`
	server := pageUpdateServerWithInline(t, 5, "T", "<p>cur</p>", inline, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "<p>new</p>")

	cmd := &PageUpdateCmd{Ref: "123", IfVersion: 5, BodyFormat: "storage", AllowUnresolvedInlineComments: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !cap.called {
		t.Errorf("override should allow the PUT")
	}
	if cap.inlineDrains != 0 {
		t.Errorf("override must skip the inline-comment request entirely (drains=%d)", cap.inlineDrains)
	}
}

type moveServerConfig struct {
	srcID, dstID   string
	srcVersion     int
	srcParentID    string
	srcSpaceID     string
	srcBody        string // ADF
	dstSpaceID     string
	ancestorsOfDst []string
	putStatus      int // 0 => 200 OK; otherwise the PUT returns this status
}

// pageMoveServer serves the source GET (ADF), destination GET (storage), source
// ancestors, and the reparent PUT. A move does not drain inline comments, so any
// inline-comments request is an unexpected call and fails the test.
func pageMoveServer(t *testing.T, cfg moveServerConfig, cap *capturedUpdate) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/wiki/api/v2/pages/"
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		switch {
		case rest == cfg.srcID+"/inline-comments":
			t.Errorf("page move must not drain inline comments")
			w.WriteHeader(http.StatusNotFound)
		case rest == cfg.dstID+"/ancestors":
			parts := make([]string, 0, len(cfg.ancestorsOfDst))
			for _, id := range cfg.ancestorsOfDst {
				parts = append(parts, `{"id":`+jsonQuote(id)+`,"type":"page"}`)
			}
			_, _ = w.Write([]byte(`{"results":[` + strings.Join(parts, ",") + `],"_links":{"next":""}}`))
		case rest == cfg.srcID && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{
				"id": ` + jsonQuote(cfg.srcID) + `,
				"title": "Source",
				"spaceId": ` + jsonQuote(cfg.srcSpaceID) + `,
				"status": "current",
				"parentId": ` + jsonQuote(cfg.srcParentID) + `,
				"body": {"atlas_doc_format": {"value": ` + jsonQuote(cfg.srcBody) + `}},
				"version": {"number": ` + fmt.Sprint(cfg.srcVersion) + `},
				"_links": {"webui": "/spaces/T/pages/` + cfg.srcID + `/S"}
			}`))
		case rest == cfg.dstID && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{
				"id": ` + jsonQuote(cfg.dstID) + `,
				"title": "Dest",
				"spaceId": ` + jsonQuote(cfg.dstSpaceID) + `,
				"status": "current",
				"body": {"storage": {"value": "<p>d</p>"}},
				"version": {"number": 1},
				"_links": {"webui": "/spaces/T/pages/` + cfg.dstID + `/D"}
			}`))
		case rest == cfg.srcID && r.Method == http.MethodPut:
			if cfg.putStatus != 0 {
				w.WriteHeader(cfg.putStatus)
				_, _ = w.Write([]byte(`{"message":"conflict"}`))
				return
			}
			cap.called = true
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			cap.title, _ = payload["title"].(string)
			cap.status, _ = payload["status"].(string)
			cap.parentID, cap.hasParent = payload["parentId"].(string)
			if v, ok := payload["version"].(map[string]any); ok {
				if n, ok := v["number"].(float64); ok {
					cap.version = int(n)
				}
			}
			if b, ok := payload["body"].(map[string]any); ok {
				cap.bodyRep, _ = b["representation"].(string)
				cap.bodyVal, _ = b["value"].(string)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": cfg.srcID, "title": cap.title, "spaceId": cfg.srcSpaceID,
				"version": map[string]any{"number": cap.version},
				"_links":  map[string]any{"webui": "/spaces/T/pages/" + cfg.srcID + "/S"},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestPageMove_SameSpace(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	adf := `{"type":"doc","version":1,"content":[]}`
	server := pageMoveServer(t, moveServerConfig{
		srcID: "100", dstID: "200", srcVersion: 5, srcParentID: "50",
		srcSpaceID: "s1", srcBody: adf, dstSpaceID: "s1",
	}, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageMoveCmd{Ref: "100", Parent: "200", IfVersion: 5}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if !cap.called {
		t.Fatalf("PUT not called")
	}
	if !cap.hasParent || cap.parentID != "200" {
		t.Errorf("parentId sent = %q (present=%v), want 200", cap.parentID, cap.hasParent)
	}
	if cap.bodyRep != "atlas_doc_format" || cap.bodyVal != adf {
		t.Errorf("body sent = %q/%q, want ADF preserved", cap.bodyRep, cap.bodyVal)
	}
	if cap.status != "current" || cap.version != 6 {
		t.Errorf("status/version = %q/%d, want current/6", cap.status, cap.version)
	}
	lines := parseLines(t, out.String())
	r := lines[0]
	if r["moved"] != true || r["previous_parent_id"] != "50" || r["parent_id"] != "200" || r["version"] != float64(6) {
		t.Errorf("row unexpected: %v", r)
	}
}

func TestPageMove_CrossSpaceRefused(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	server := pageMoveServer(t, moveServerConfig{
		srcID: "100", dstID: "200", srcVersion: 5, srcParentID: "50",
		srcSpaceID: "s1", srcBody: `{"x":1}`, dstSpaceID: "s2",
	}, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageMoveCmd{Ref: "100", Parent: "200", IfVersion: 5}
	err := cmd.Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "cross_space_move_unsupported" {
		t.Fatalf("expected cross_space_move_unsupported, got %v", err)
	}
	if cap.called {
		t.Errorf("cross-space move must not PUT")
	}
	if oErr.Data["source_space_id"] != "s1" || oErr.Data["destination_space_id"] != "s2" {
		t.Errorf("error data unexpected: %v", oErr.Data)
	}
}

func TestPageMove_SelfParent(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	cmd := &PageMoveCmd{Ref: "100", Parent: "100", IfVersion: 5}
	err := cmd.Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_move" {
		t.Fatalf("expected invalid_move for self-parent, got %v", err)
	}
}

func TestPageMove_NoopWhenAlreadyChild(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	server := pageMoveServer(t, moveServerConfig{
		srcID: "100", dstID: "200", srcVersion: 5, srcParentID: "200",
		srcSpaceID: "s1", srcBody: `{"x":1}`, dstSpaceID: "s1",
	}, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	cmd := &PageMoveCmd{Ref: "100", Parent: "200", IfVersion: 5}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if cap.called {
		t.Errorf("no-op move must not PUT")
	}
	lines := parseLines(t, out.String())
	if lines[0]["moved"] != false || lines[0]["version"] != float64(5) {
		t.Errorf("expected moved=false version=5, got %v", lines[0])
	}
}

func TestPageMove_CycleRefused(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	// Destination 200's ancestors include the source 100 -> moving 100 under 200
	// would create a cycle.
	server := pageMoveServer(t, moveServerConfig{
		srcID: "100", dstID: "200", srcVersion: 5, srcParentID: "50",
		srcSpaceID: "s1", srcBody: `{"x":1}`, dstSpaceID: "s1",
		ancestorsOfDst: []string{"100"},
	}, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	cmd := &PageMoveCmd{Ref: "100", Parent: "200", IfVersion: 5}
	err := cmd.Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "invalid_move" {
		t.Fatalf("expected invalid_move for cycle, got %v", err)
	}
	if cap.called {
		t.Errorf("cycle move must not PUT")
	}
}

func TestPageMove_VersionConflict(t *testing.T) {
	clearCredEnv(t)
	var cap capturedUpdate
	server := pageMoveServer(t, moveServerConfig{
		srcID: "100", dstID: "200", srcVersion: 8, srcParentID: "50",
		srcSpaceID: "s1", srcBody: `{"x":1}`, dstSpaceID: "s1",
	}, &cap)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	cmd := &PageMoveCmd{Ref: "100", Parent: "200", IfVersion: 5}
	err := cmd.Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) || oErr.Err != "version_conflict" {
		t.Fatalf("expected version_conflict, got %v", err)
	}
	if cap.called {
		t.Errorf("version conflict must not PUT")
	}
}

// pageDeleteServer serves the dry-run GET and the DELETE, recording deleted ids.
func pageDeleteServer(t *testing.T, notFound map[string]bool, deleted *[]string) *httptest.Server {
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
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{
				"id": "` + id + `",
				"title": "Page ` + id + `",
				"spaceId": "space1",
				"authorId": "user1",
				"createdAt": "2024-01-01T00:00:00Z",
				"body": {"storage": {"value": "<p>x</p>"}},
				"version": {"number": 3, "createdAt": "2024-06-20T00:00:00Z"},
				"_links": {"webui": "/spaces/T/pages/` + id + `/P"}
			}`))
		case http.MethodDelete:
			*deleted = append(*deleted, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func TestPageDelete_DryRun(t *testing.T) {
	clearCredEnv(t)
	var deleted []string
	server := pageDeleteServer(t, nil, &deleted)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageDeleteCmd{Refs: []string{"1"}, DryRun: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("dry-run should not DELETE, got %v", deleted)
	}
	lines := parseLines(t, out.String())
	row := lines[0]
	if row["would_delete"] != true || row["delete_mode"] != "trash" || row["id"] != "1" || row["version"] != float64(3) {
		t.Errorf("dry-run row unexpected: %v", row)
	}
}

func TestPageDelete_NoYes(t *testing.T) {
	clearCredEnv(t)
	var deleted []string
	server := pageDeleteServer(t, nil, &deleted)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageDeleteCmd{Refs: []string{"1"}}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "confirmation_required" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want confirmation_required/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
	if len(deleted) != 0 {
		t.Errorf("nothing should be deleted without --yes, got %v", deleted)
	}
}

func TestPageDelete_WithYes(t *testing.T) {
	clearCredEnv(t)
	var deleted []string
	server := pageDeleteServer(t, nil, &deleted)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageDeleteCmd{Refs: []string{"1", "2"}, Yes: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(deleted) != 2 {
		t.Errorf("expected 2 DELETEs, got %v", deleted)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["deleted"] != true || lines[0]["delete_mode"] != "trash" || lines[0]["id"] != "1" {
		t.Errorf("first deleted row unexpected: %v", lines[0])
	}
}

func TestPageDelete_PerItem404(t *testing.T) {
	clearCredEnv(t)
	var deleted []string
	server := pageDeleteServer(t, map[string]bool{"404": true}, &deleted)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageDeleteCmd{Refs: []string{"1", "404"}, Yes: true}
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
	if lines[0]["deleted"] != true || lines[0]["id"] != "1" {
		t.Errorf("first row should be success: %v", lines[0])
	}
	if lines[1]["error"] != "page_not_found" || lines[1]["input"] != "404" {
		t.Errorf("second row should be inline 404 error: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["error_count"] != float64(1) {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}

// convertToLiveServer serves POST /cgraphql. Any contentId in failIDs returns a
// GraphQL logical failure; a status in statusFor overrides with that HTTP code.
func convertToLiveServer(t *testing.T, failIDs map[string]bool, statusFor map[string]int, converted *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgraphql" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		vars, _ := body["variables"].(map[string]any)
		input, _ := vars["input"].(map[string]any)
		id, _ := input["contentId"].(string)

		if code, ok := statusFor[id]; ok {
			w.WriteHeader(code)
			return
		}
		if failIDs[id] {
			w.Write([]byte(`{"data":{"convertPageToLiveEditAction":{"success":false,"errors":[{"message":"cannot convert"}]}}}`))
			return
		}
		if converted != nil {
			*converted = append(*converted, id)
		}
		w.Write([]byte(`{"data":{"convertPageToLiveEditAction":{"success":true,"errors":null}}}`))
	}))
}

func TestPageConvertToLive_Success(t *testing.T) {
	clearCredEnv(t)
	var converted []string
	server := convertToLiveServer(t, nil, nil, &converted)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageConvertToLiveCmd{Refs: []string{"1", "2"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if len(converted) != 2 {
		t.Errorf("expected 2 conversions, got %v", converted)
	}
	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["converted"] != true || lines[0]["id"] != "1" || lines[0]["input"] != "1" {
		t.Errorf("first row unexpected: %v", lines[0])
	}
	if _, ok := lines[0]["subtype"]; ok {
		t.Errorf("convert row should not carry subtype: %v", lines[0])
	}
	if !strings.Contains(errBuf.String(), "undocumented") {
		t.Errorf("expected an undocumented-endpoint warning on stderr, got %q", errBuf.String())
	}
	if strings.Count(errBuf.String(), "warning:") != 1 {
		t.Errorf("expected exactly one warning, got %q", errBuf.String())
	}
}

func TestPageConvertToLive_QuietSuppressesWarning(t *testing.T) {
	clearCredEnv(t)
	server := convertToLiveServer(t, nil, nil, nil)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")
	c.Quiet = true

	cmd := &PageConvertToLiveCmd{Refs: []string{"1"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if errBuf.String() != "" {
		t.Errorf("--quiet should suppress the warning, got %q", errBuf.String())
	}
}

func TestPageConvertToLive_InlineFailure(t *testing.T) {
	clearCredEnv(t)
	var converted []string
	server := convertToLiveServer(t, map[string]bool{"bad": true}, nil, &converted)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageConvertToLiveCmd{Refs: []string{"1", "bad"}}
	err := cmd.Run(c)

	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	lines := parseLines(t, out.String())
	if lines[0]["converted"] != true || lines[0]["id"] != "1" {
		t.Errorf("first row should be success: %v", lines[0])
	}
	if lines[1]["error"] != "live_convert_failed" || lines[1]["input"] != "bad" {
		t.Errorf("second row should be inline live_convert_failed: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["error_count"] != float64(1) {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}

func TestPageConvertToLive_StatusErrorNotRemapped(t *testing.T) {
	clearCredEnv(t)
	server := convertToLiveServer(t, nil, map[string]int{"1": http.StatusUnauthorized}, nil)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageConvertToLiveCmd{Refs: []string{"1"}}
	err := cmd.Run(c)
	if !errors.As(err, new(*output.ExitError)) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	lines := parseLines(t, out.String())
	if lines[0]["error"] != "unauthorized" {
		t.Errorf("row error = %v, want unauthorized", lines[0]["error"])
	}
}

func TestPageConvertToLive_NotFoundStableCode(t *testing.T) {
	clearCredEnv(t)
	server := convertToLiveServer(t, nil, map[string]int{"1": http.StatusNotFound}, nil)
	defer server.Close()
	setEnvCreds(t, server.URL)

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageConvertToLiveCmd{Refs: []string{"1"}}
	if err := cmd.Run(c); !errors.As(err, new(*output.ExitError)) {
		t.Fatalf("expected *output.ExitError, got %T: %v", err, err)
	}
	lines := parseLines(t, out.String())
	// A 404 on the undocumented endpoint must classify as a stable code, not a
	// freeform sentence, and must not become live_convert_failed.
	if lines[0]["error"] != "not_found" {
		t.Errorf("row error = %v, want not_found", lines[0]["error"])
	}
}

func TestPageConvertToLive_MixedSiteRejected(t *testing.T) {
	clearCredEnv(t)
	setEnvCreds(t, "https://acme.atlassian.net")

	var out, errBuf bytes.Buffer
	c := newWriteCLI(t, &out, &errBuf, "")

	cmd := &PageConvertToLiveCmd{Refs: []string{
		"https://a.atlassian.net/wiki/spaces/ENG/pages/1",
		"https://b.atlassian.net/wiki/spaces/ENG/pages/2",
	}}
	err := cmd.Run(c)
	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" {
		t.Errorf("Err = %q, want invalid_input", oErr.Err)
	}
	if out.String() != "" {
		t.Errorf("no rows should be emitted on a site conflict, got %q", out.String())
	}
}
