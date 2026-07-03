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
