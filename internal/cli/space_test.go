package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/output"
)

// clearCredEnv unsets every env var ResolveCredentials consults so tests are
// hermetic regardless of the developer's shell.
func clearCredEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CONFLUENCE_EMAIL", "ATLASSIAN_API_EMAIL",
		"CONFLUENCE_API_TOKEN", "ATLASSIAN_API_KEY",
		"CONFLUENCE_SITE", "ATLASSIAN_SITE",
	} {
		t.Setenv(k, "")
	}
}

func TestSpaceSync_InvalidURL(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)

	cmd := &SpaceSyncCmd{URL: "not a url", Dir: t.TempDir()}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" {
		t.Errorf("Err = %q, want invalid_input", oErr.Err)
	}
	if oErr.Code != output.ExitGeneral {
		t.Errorf("Code = %d, want %d", oErr.Code, output.ExitGeneral)
	}
}

func TestSpaceSync_MissingCredentials(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "does-not-exist.json"))

	cmd := &SpaceSyncCmd{
		URL: "https://acme.atlassian.net/wiki/spaces/ENG",
		Dir: t.TempDir(),
	}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("Code = %d, want %d", oErr.Code, output.ExitAuth)
	}
}

func TestSpaceSync_EmptySpaceHappyPath(t *testing.T) {
	clearCredEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/spaces":
			w.Write([]byte(`{"results":[{"id":"12345","key":"ENG","name":"Engineering"}]}`))
		case "/wiki/api/v2/spaces/12345/pages":
			w.Write([]byte(`{"results":[],"_links":{"next":""}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "does-not-exist.json"))

	cmd := &SpaceSyncCmd{
		URL: server.URL + "/wiki/spaces/ENG",
		Dir: t.TempDir(),
	}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 stdout lines, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"synced":true`) || !strings.Contains(lines[0], `"space":"ENG"`) {
		t.Errorf("first line unexpected: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"_meta"`) {
		t.Errorf("second line not meta: %q", lines[1])
	}
}
