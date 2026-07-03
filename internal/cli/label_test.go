package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

// labelWriteServer echoes the POSTed label name in its results, or 500s when the
// requested label is "boom" (to exercise per-item failure). DELETE returns 204.
func labelWriteServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/content/123/label" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var posted []struct {
				Prefix string `json:"prefix"`
				Name   string `json:"name"`
			}
			_ = json.Unmarshal(body, &posted)
			name := ""
			if len(posted) > 0 {
				name = posted[0].Name
			}
			if name == "boom" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"id":"L-` + name + `","name":"` + name + `","prefix":"global"}]}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func TestLabelAdd_Multiple(t *testing.T) {
	clearCredEnv(t)
	server := labelWriteServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &LabelAddCmd{Page: "123", Labels: []string{"alpha", "beta"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["name"] != "alpha" || lines[0]["added"] != true || lines[0]["page_id"] != "123" {
		t.Errorf("first row unexpected: %v", lines[0])
	}
	if lines[1]["name"] != "beta" || lines[1]["added"] != true {
		t.Errorf("second row unexpected: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if ec, _ := meta["error_count"].(float64); ec != 0 {
		t.Errorf("error_count = %v, want 0", meta["error_count"])
	}
}

func TestLabelAdd_PerItemError(t *testing.T) {
	clearCredEnv(t)
	server := labelWriteServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &LabelAddCmd{Page: "123", Labels: []string{"alpha", "boom"}}
	err := cmd.Run(c)

	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != output.ExitGeneral {
		t.Fatalf("expected *output.ExitError/%d, got %T: %v", output.ExitGeneral, err, err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["added"] != true {
		t.Errorf("first row should be success: %v", lines[0])
	}
	if lines[1]["input"] != "boom" || lines[1]["error"] == nil {
		t.Errorf("second row should be inline error for boom: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if ec, _ := meta["error_count"].(float64); ec != 1 {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}

func TestLabelRemove_Multiple(t *testing.T) {
	clearCredEnv(t)
	server := labelWriteServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &LabelRemoveCmd{Page: "123", Labels: []string{"alpha", "beta"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["name"] != "alpha" || lines[0]["removed"] != true {
		t.Errorf("first row unexpected: %v", lines[0])
	}
	if lines[1]["name"] != "beta" || lines[1]["removed"] != true {
		t.Errorf("second row unexpected: %v", lines[1])
	}
}

func TestLabelRemove_PageNotFound(t *testing.T) {
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

	cmd := &LabelRemoveCmd{Page: "123", Labels: []string{"alpha"}}
	err := cmd.Run(c)

	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != output.ExitGeneral {
		t.Fatalf("expected *output.ExitError/%d, got %T: %v", output.ExitGeneral, err, err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 error row + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["error"] != "page_not_found" || lines[0]["input"] != "alpha" {
		t.Errorf("error row unexpected: %v", lines[0])
	}
	meta := lines[1]["_meta"].(map[string]any)
	if ec, _ := meta["error_count"].(float64); ec != 1 {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}
