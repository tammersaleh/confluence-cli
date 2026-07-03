package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/auth"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

// currentUserServer returns an httptest.Server that answers the current-user
// endpoint with the given status and (for 200s) a canned user body.
func currentUserServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/user/current" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"accountId":"a1","displayName":"Ada","email":"ada@x"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthLogin_Success(t *testing.T) {
	clearCredEnv(t)
	srv := currentUserServer(t, http.StatusOK)

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	var out, errBuf bytes.Buffer
	c := &CLI{Site: srv.URL}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(credPath)
	c.SetInput(strings.NewReader("s3cr3t\n"))

	cmd := &AuthLoginCmd{Email: "ada@x"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	canon, err := auth.CanonicalSiteURL(srv.URL)
	if err != nil {
		t.Fatalf("CanonicalSiteURL: %v", err)
	}

	creds, err := auth.LoadCredentials(credPath)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	sc, ok := creds.Sites[canon]
	if !ok {
		t.Fatalf("site %q not stored; have %v", canon, creds.Sites)
	}
	if sc.Email != "ada@x" || sc.APIToken != "s3cr3t" {
		t.Errorf("stored creds = %+v, want email=ada@x token=s3cr3t (trimmed)", sc)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 stdout lines, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"status":"logged_in"`) || !strings.Contains(lines[0], `"account_id":"a1"`) {
		t.Errorf("row line unexpected: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"_meta"`) {
		t.Errorf("second line not meta: %q", lines[1])
	}
}

func TestAuthLogin_ValidationFailureDoesNotSave(t *testing.T) {
	clearCredEnv(t)
	srv := currentUserServer(t, http.StatusUnauthorized)

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	var out, errBuf bytes.Buffer
	c := &CLI{Site: srv.URL}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(credPath)
	c.SetInput(strings.NewReader("bad-token"))

	cmd := &AuthLoginCmd{Email: "ada@x"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Code != output.ExitAuth {
		t.Errorf("Code = %d, want %d", oErr.Code, output.ExitAuth)
	}

	// The credentials file must not have been created/updated.
	creds, lerr := auth.LoadCredentials(credPath)
	if lerr != nil {
		t.Fatalf("LoadCredentials: %v", lerr)
	}
	if len(creds.Sites) != 0 {
		t.Errorf("credentials were written on validation failure: %v", creds.Sites)
	}
}

func TestAuthLogin_EmptyStdin(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{Site: "https://acme.atlassian.net"}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "credentials.json"))
	c.SetInput(strings.NewReader("   \n"))

	cmd := &AuthLoginCmd{Email: "ada@x"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "missing_token" {
		t.Errorf("Err = %q, want missing_token", oErr.Err)
	}
}

func TestAuthLogin_BadSite(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{Site: "   "}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "credentials.json"))
	c.SetInput(strings.NewReader("tok"))

	cmd := &AuthLoginCmd{Email: "ada@x"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" || oErr.Code != output.ExitGeneral {
		t.Errorf("got Err=%q Code=%d, want invalid_input/%d", oErr.Err, oErr.Code, output.ExitGeneral)
	}
}

func TestAuthStatus_ZeroSites(t *testing.T) {
	clearCredEnv(t)
	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &AuthStatusCmd{}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected only _meta line, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"_meta"`) {
		t.Errorf("line not meta: %q", lines[0])
	}
}

func TestAuthStatus_OneStoredSite(t *testing.T) {
	clearCredEnv(t)
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := auth.SaveCredentials(credPath, &auth.Credentials{Sites: map[string]auth.SiteCredentials{
		"https://acme.atlassian.net": {Email: "a@x", APIToken: "s3cr3t-value"},
	}}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(credPath)

	cmd := &AuthStatusCmd{}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected site row + meta, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"source":"stored"`) ||
		!strings.Contains(lines[0], `"is_default":true`) ||
		!strings.Contains(lines[0], `"site":"https://acme.atlassian.net"`) {
		t.Errorf("stored row unexpected: %q", lines[0])
	}
	// Token value must never appear.
	if strings.Contains(out.String(), "s3cr3t-value") {
		t.Errorf("token value leaked into output: %q", out.String())
	}
}

func TestAuthStatus_EnvActive(t *testing.T) {
	clearCredEnv(t)
	t.Setenv("CONFLUENCE_EMAIL", "env@x")
	t.Setenv("CONFLUENCE_API_TOKEN", "env-secret")
	t.Setenv("CONFLUENCE_SITE", "https://acme.atlassian.net/wiki")

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(filepath.Join(t.TempDir(), "none.json"))

	cmd := &AuthStatusCmd{}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, `"source":"env"`) {
		t.Errorf("expected an env-source row: %q", s)
	}
	if !strings.Contains(s, `"site":"https://acme.atlassian.net"`) {
		t.Errorf("env row missing canonical site: %q", s)
	}
	if !strings.Contains(s, `"env_api_token_set":true`) {
		t.Errorf("env row missing env_api_token_set: %q", s)
	}
	if strings.Contains(s, "env-secret") {
		t.Errorf("token value leaked into output: %q", s)
	}
}

func TestAuthLogout_ExistingRemovesOne(t *testing.T) {
	clearCredEnv(t)
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := auth.SaveCredentials(credPath, &auth.Credentials{Sites: map[string]auth.SiteCredentials{
		"https://acme.atlassian.net":  {Email: "a@x", APIToken: "t1"},
		"https://other.atlassian.net": {Email: "b@x", APIToken: "t2"},
	}}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(credPath)

	cmd := &AuthLogoutCmd{Site: "https://acme.atlassian.net"}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	creds, err := auth.LoadCredentials(credPath)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if _, ok := creds.Sites["https://acme.atlassian.net"]; ok {
		t.Error("logged-out site still present")
	}
	if _, ok := creds.Sites["https://other.atlassian.net"]; !ok {
		t.Error("other site was removed")
	}
	if !strings.Contains(out.String(), `"status":"logged_out"`) {
		t.Errorf("missing logged_out row: %q", out.String())
	}
}

func TestAuthLogout_MissingSite(t *testing.T) {
	clearCredEnv(t)
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := auth.SaveCredentials(credPath, &auth.Credentials{Sites: map[string]auth.SiteCredentials{
		"https://acme.atlassian.net": {Email: "a@x", APIToken: "t1"},
	}}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(credPath)

	cmd := &AuthLogoutCmd{Site: "https://nope.atlassian.net"}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "not_found" {
		t.Errorf("Err = %q, want not_found", oErr.Err)
	}
}

func TestAuthLogout_NoArgSingleSite(t *testing.T) {
	clearCredEnv(t)
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := auth.SaveCredentials(credPath, &auth.Credentials{Sites: map[string]auth.SiteCredentials{
		"https://acme.atlassian.net": {Email: "a@x", APIToken: "t1"},
	}}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(credPath)

	cmd := &AuthLogoutCmd{}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	creds, err := auth.LoadCredentials(credPath)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if len(creds.Sites) != 0 {
		t.Errorf("expected empty store, got %v", creds.Sites)
	}
}

func TestAuthLogout_NoArgMultipleSites(t *testing.T) {
	clearCredEnv(t)
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := auth.SaveCredentials(credPath, &auth.Credentials{Sites: map[string]auth.SiteCredentials{
		"https://acme.atlassian.net":  {Email: "a@x", APIToken: "t1"},
		"https://other.atlassian.net": {Email: "b@x", APIToken: "t2"},
	}}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)
	c.SetCredentialsPath(credPath)

	cmd := &AuthLogoutCmd{}
	err := cmd.Run(c)

	var oErr *output.Error
	if !errors.As(err, &oErr) {
		t.Fatalf("expected *output.Error, got %T: %v", err, err)
	}
	if oErr.Err != "invalid_input" {
		t.Errorf("Err = %q, want invalid_input", oErr.Err)
	}
}
