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

func TestSpaceSync_SiteFlagMismatch(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{Site: "https://other.atlassian.net"}
	c.SetOutput(&out, &errBuf)

	cmd := &SpaceSyncCmd{
		URL: "https://acme.atlassian.net/wiki/spaces/ENG",
		Dir: t.TempDir(),
	}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

func TestSpaceSync_SiteFlagMatchesURL(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	// --site agrees with the URL's site, so validation passes and the command
	// proceeds to credential resolution, which fails (no creds) with ExitAuth.
	c := &CLI{Site: "https://acme.atlassian.net/wiki"}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

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
		t.Errorf("matching --site should pass validation and fail on missing creds; got Err=%q Code=%d", oErr.Err, oErr.Code)
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

// spaceInfoServer serves /wiki/api/v2/spaces?keys=KEY (key lookup) and
// /wiki/api/v2/spaces/{id} (id lookup). A key with no match returns empty
// results (-> ErrSpaceNotFound); the "missing" key does so.
func spaceInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const idPrefix = "/wiki/api/v2/spaces/"
		switch {
		case r.URL.Path == "/wiki/api/v2/spaces":
			key := r.URL.Query().Get("keys")
			if key == "MISSING" {
				_, _ = w.Write([]byte(`{"results":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"id":"12345","key":"` + key + `","name":"Engineering","type":"global","status":"current","homepageId":"999"}]}`))
		case strings.HasPrefix(r.URL.Path, idPrefix):
			id := strings.TrimPrefix(r.URL.Path, idPrefix)
			_, _ = w.Write([]byte(`{"id":"` + id + `","key":"ENG","name":"Engineering","type":"global","status":"current","homepageId":"999"}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestSpaceInfo_Key(t *testing.T) {
	clearCredEnv(t)
	server := spaceInfoServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &SpaceInfoCmd{Refs: []string{"ENG"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	row := lines[0]
	if row["input"] != "ENG" || row["id"] != "12345" || row["key"] != "ENG" {
		t.Errorf("row shape unexpected: %v", row)
	}
	if row["type"] != "global" || row["status"] != "current" || row["homepage_id"] != "999" {
		t.Errorf("row missing type/status/homepage_id: %v", row)
	}
}

func TestSpaceInfo_NumericID(t *testing.T) {
	clearCredEnv(t)
	server := spaceInfoServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &SpaceInfoCmd{Refs: []string{"55555"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["input"] != "55555" || lines[0]["id"] != "55555" {
		t.Errorf("id lookup row unexpected: %v", lines[0])
	}
}

func TestSpaceInfo_URLDerivesSite(t *testing.T) {
	clearCredEnv(t)
	server := spaceInfoServer(t)
	defer server.Close()

	// No CONFLUENCE_SITE: the site comes from the URL arg.
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &SpaceInfoCmd{Refs: []string{server.URL + "/wiki/spaces/ENG"}}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := parseLines(t, out.String())
	if len(lines) != 2 {
		t.Fatalf("expected 1 row + 1 meta, got %d: %q", len(lines), out.String())
	}
	if lines[0]["key"] != "ENG" {
		t.Errorf("key = %v, want ENG", lines[0]["key"])
	}
}

func TestSpaceInfo_PerItemNotFound(t *testing.T) {
	clearCredEnv(t)
	server := spaceInfoServer(t)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &SpaceInfoCmd{Refs: []string{"ENG", "MISSING"}}
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
	if lines[0]["key"] != "ENG" {
		t.Errorf("first row should be success: %v", lines[0])
	}
	if lines[1]["error"] != "space_not_found" || lines[1]["input"] != "MISSING" {
		t.Errorf("second row should be inline error for MISSING: %v", lines[1])
	}
	meta := lines[2]["_meta"].(map[string]any)
	if meta["error_count"] != float64(1) {
		t.Errorf("error_count = %v, want 1", meta["error_count"])
	}
}

func TestSpaceInfo_MixedSiteURLs(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &SpaceInfoCmd{Refs: []string{
		"https://acme.atlassian.net/wiki/spaces/ENG",
		"https://other.atlassian.net/wiki/spaces/OPS",
	}}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}
