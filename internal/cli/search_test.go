package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// searchServer serves /wiki/rest/api/search. The first page links to a second
// (cursor=C2) that returns the final result. gotCQL captures the last cql seen.
func searchServer(t *testing.T, gotCQL *string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if gotCQL != nil {
			*gotCQL = r.URL.Query().Get("cql")
		}
		if r.URL.Query().Get("cursor") == "C2" {
			_, _ = w.Write([]byte(`{"results":[{"content":{"id":"2","title":"Second","type":"page","space":{"key":"OPS"}},"excerpt":"two","url":"/wiki/x/2"}],"_links":{"next":""}}`))
			return
		}
		next := srv.URL + "/wiki/rest/api/search?cursor=C2"
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","title":"First","type":"page","space":{"key":"ENG"}},"excerpt":"one","url":"/wiki/x/1"}],"_links":{"next":"` + next + `"}}`))
	}))
	return srv
}

func TestSearch_SinglePage(t *testing.T) {
	clearCredEnv(t)
	var gotCQL string
	server := searchServer(t, &gotCQL)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &SearchCmd{CQL: "type=page", Limit: 25}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if gotCQL != "type=page" {
		t.Errorf("cql passed through = %q, want type=page", gotCQL)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["id"] != "1" || row["title"] != "First" || row["type"] != "page" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["space_key"] != "ENG" || row["excerpt"] != "one" || row["url"] != "/wiki/x/1" {
		t.Errorf("row missing space_key/excerpt/url: %v", row)
	}
	meta := lines[1]["_meta"].(map[string]any)
	if meta["next_cursor"] != "C2" || meta["has_more"] != true {
		t.Errorf("meta unexpected: %v", meta)
	}
}

func TestSearch_All(t *testing.T) {
	clearCredEnv(t)
	server := searchServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &SearchCmd{CQL: "type=page", Limit: 25, All: true}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "1" || lines[1]["id"] != "2" {
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
