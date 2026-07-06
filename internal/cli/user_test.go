package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tammersaleh/confluence-cli/internal/output"
)

func TestUserCurrent_Emits(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/user/current" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"accountId":"5b10","displayName":"Ada","email":"ada@example.com"}`))
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &UserCurrentCmd{}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["account_id"] != "5b10" || row["display_name"] != "Ada" || row["email"] != "ada@example.com" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if _, ok := lines[1]["_meta"].(map[string]any); !ok {
		t.Errorf("last line not meta: %v", lines[1])
	}
}

func TestUserCurrent_AuthError(t *testing.T) {
	clearCredEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &UserCurrentCmd{}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("Code = %d, want %d (ExitAuth)", oErr.Code, output.ExitAuth)
	}
}

// userInfoServer serves /wiki/rest/api/user?accountId=X. Account "missing"
// returns 404 (-> ErrUserNotFound).
func userInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/user" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := r.URL.Query().Get("accountId")
		if id == "missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"accountId":"` + id + `","displayName":"Name-` + id + `","email":"` + id + `@example.com"}`))
	}))
}

func TestUserInfo_Multiple(t *testing.T) {
	clearCredEnv(t)
	server := userInfoServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &UserInfoCmd{IDs: []string{"aaa", "bbb"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 3 {
		t.Fatalf("expected 2 rows + meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["input"] != "aaa" || lines[0]["account_id"] != "aaa" || lines[0]["display_name"] != "Name-aaa" {
		t.Errorf("first row unexpected: %v", lines[0])
	}
	if lines[1]["input"] != "bbb" || lines[1]["email"] != "bbb@example.com" {
		t.Errorf("second row unexpected: %v", lines[1])
	}
}

func TestUserInfo_PerItemNotFound(t *testing.T) {
	clearCredEnv(t)
	server := userInfoServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &UserInfoCmd{IDs: []string{"aaa", "missing"}}
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
	if lines[0]["account_id"] != "aaa" {
		t.Errorf("first row should be success: %v", lines[0])
	}
	if lines[1]["error"] != "user_not_found" || lines[1]["input"] != "missing" {
		t.Errorf("second row should be inline user_not_found for missing: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["error_count"] != float64(1) {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}
