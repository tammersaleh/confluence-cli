package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/output"
)

// labelServer serves /wiki/api/v2/pages/123/labels over two pages to prove the
// command drains the cursor.
func labelServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/api/v2/pages/123/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("cursor") == "L2" {
			_, _ = w.Write([]byte(`{"results":[{"id":"2","name":"beta","prefix":"global"}],"_links":{"next":""}}`))
			return
		}
		next := srv.URL + "/wiki/api/v2/pages/123/labels?cursor=L2"
		_, _ = w.Write([]byte(`{"results":[{"id":"1","name":"alpha","prefix":"global"}],"_links":{"next":"` + next + `"}}`))
	}))
	return srv
}

func TestLabelList_Drained(t *testing.T) {
	clearCredEnv(t)
	server := labelServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &LabelListCmd{Page: "123"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["id"] != "1" || lines[0]["name"] != "alpha" || lines[0]["prefix"] != "global" {
		t.Errorf("first row unexpected: %v", lines[0])
	}
	if lines[1]["id"] != "2" || lines[1]["name"] != "beta" {
		t.Errorf("second (drained) row unexpected: %v", lines[1])
	}
	if _, ok := lines[2]["_meta"].(map[string]any); !ok {
		t.Errorf("last line not meta: %v", lines[2])
	}
}

func TestLabelList_NotFound(t *testing.T) {
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

	cmd := &LabelListCmd{Page: "123"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "page_not_found" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want page_not_found/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}
