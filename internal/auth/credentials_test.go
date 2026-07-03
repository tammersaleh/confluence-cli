package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalSiteURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"page url", "https://acme.atlassian.net/wiki/spaces/ENG/pages/123", "https://acme.atlassian.net", false},
		{"wiki suffix", "https://acme.atlassian.net/wiki", "https://acme.atlassian.net", false},
		{"bare host", "acme.atlassian.net", "https://acme.atlassian.net", false},
		{"trailing slash", "https://acme.atlassian.net/", "https://acme.atlassian.net", false},
		{"uppercase host", "https://ACME.atlassian.net/wiki", "https://acme.atlassian.net", false},
		{"wiki suffix bare host", "acme.atlassian.net/wiki", "https://acme.atlassian.net", false},
		{"surrounding whitespace", "  https://acme.atlassian.net/wiki  ", "https://acme.atlassian.net", false},
		{"http scheme preserved", "http://acme.atlassian.net/wiki", "http://acme.atlassian.net", false},
		{"empty", "", "", true},
		{"no host", "https:///wiki", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalSiteURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CanonicalSiteURL(%q) = %q, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalSiteURL(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("CanonicalSiteURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLoadCredentialsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	creds, err := LoadCredentials(path)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if creds.Sites == nil {
		t.Fatal("Sites map is nil, want empty non-nil map")
	}
	if len(creds.Sites) != 0 {
		t.Errorf("Sites has %d entries, want 0", len(creds.Sites))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "credentials.json")
	in := &Credentials{Sites: map[string]SiteCredentials{
		"https://acme.atlassian.net": {Email: "a@example.com", APIToken: "tok", CloudID: "cid"},
	}}
	if err := SaveCredentials(path, in); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}

	out, err := LoadCredentials(path)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	got, ok := out.Sites["https://acme.atlassian.net"]
	if !ok {
		t.Fatal("site missing after round-trip")
	}
	if got != in.Sites["https://acme.atlassian.net"] {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
}

func TestSaveCredentialsAtomicOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")

	if err := SaveCredentials(path, &Credentials{Sites: map[string]SiteCredentials{
		"https://acme.atlassian.net": {Email: "a@example.com", APIToken: "t1"},
	}}); err != nil {
		t.Fatalf("first SaveCredentials: %v", err)
	}
	// Overwrite (exercises os.Rename over an existing target).
	if err := SaveCredentials(path, &Credentials{Sites: map[string]SiteCredentials{
		"https://other.atlassian.net": {Email: "b@example.com", APIToken: "t2"},
	}}); err != nil {
		t.Fatalf("overwrite SaveCredentials: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}

	out, err := LoadCredentials(path)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if _, ok := out.Sites["https://acme.atlassian.net"]; ok {
		t.Error("stale site survived overwrite")
	}
	if _, ok := out.Sites["https://other.atlassian.net"]; !ok {
		t.Error("new site missing after overwrite")
	}

	// No temp files should linger in the directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only credentials.json, got %d entries: %v", len(entries), entries)
	}
}

// clearEnv sets all six recognized env vars to empty so the dev shell can't leak in.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CONFLUENCE_EMAIL", "ATLASSIAN_API_EMAIL",
		"CONFLUENCE_API_TOKEN", "ATLASSIAN_API_KEY",
		"CONFLUENCE_SITE", "ATLASSIAN_SITE",
	} {
		t.Setenv(k, "")
	}
}

// writeCreds writes a credentials file into a temp dir and returns its path.
func writeCreds(t *testing.T, sites map[string]SiteCredentials) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := SaveCredentials(path, &Credentials{Sites: sites}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	return path
}

func TestResolveCredentialsPureEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("CONFLUENCE_EMAIL", "env@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "env-tok")
	t.Setenv("CONFLUENCE_SITE", "https://acme.atlassian.net/wiki")

	path := filepath.Join(t.TempDir(), "none.json")
	rc, err := ResolveCredentials(path, "")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if rc.Site != "https://acme.atlassian.net" {
		t.Errorf("Site = %q", rc.Site)
	}
	if rc.Email != "env@example.com" || rc.APIToken != "env-tok" {
		t.Errorf("got %+v", rc)
	}
}

func TestResolveCredentialsCompatAliases(t *testing.T) {
	clearEnv(t)
	t.Setenv("ATLASSIAN_API_EMAIL", "alias@example.com")
	t.Setenv("ATLASSIAN_API_KEY", "alias-key")
	t.Setenv("ATLASSIAN_SITE", "acme.atlassian.net")

	path := filepath.Join(t.TempDir(), "none.json")
	rc, err := ResolveCredentials(path, "")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if rc.Site != "https://acme.atlassian.net" || rc.Email != "alias@example.com" || rc.APIToken != "alias-key" {
		t.Errorf("got %+v", rc)
	}
}

func TestResolveCredentialsPrimaryBeatsAlias(t *testing.T) {
	clearEnv(t)
	t.Setenv("CONFLUENCE_EMAIL", "primary@example.com")
	t.Setenv("ATLASSIAN_API_EMAIL", "alias@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "primary-tok")
	t.Setenv("ATLASSIAN_API_KEY", "alias-key")
	t.Setenv("CONFLUENCE_SITE", "primary.atlassian.net")
	t.Setenv("ATLASSIAN_SITE", "alias.atlassian.net")

	path := filepath.Join(t.TempDir(), "none.json")
	rc, err := ResolveCredentials(path, "")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if rc.Email != "primary@example.com" || rc.APIToken != "primary-tok" || rc.Site != "https://primary.atlassian.net" {
		t.Errorf("got %+v", rc)
	}
}

func TestResolveCredentialsEnvOverridesStored(t *testing.T) {
	clearEnv(t)
	t.Setenv("CONFLUENCE_EMAIL", "env@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "env-tok")

	path := writeCreds(t, map[string]SiteCredentials{
		"https://acme.atlassian.net": {Email: "stored@example.com", APIToken: "stored-tok", CloudID: "cid"},
	})

	rc, err := ResolveCredentials(path, "https://acme.atlassian.net")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if rc.Email != "env@example.com" {
		t.Errorf("Email = %q, want env override", rc.Email)
	}
	if rc.APIToken != "env-tok" {
		t.Errorf("APIToken = %q, want env override", rc.APIToken)
	}
	if rc.CloudID != "cid" {
		t.Errorf("CloudID = %q, want stored value", rc.CloudID)
	}
}

func TestResolveCredentialsStoredOnlySingleSite(t *testing.T) {
	clearEnv(t)
	path := writeCreds(t, map[string]SiteCredentials{
		"https://acme.atlassian.net": {Email: "stored@example.com", APIToken: "stored-tok"},
	})

	rc, err := ResolveCredentials(path, "")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if rc.Site != "https://acme.atlassian.net" || rc.Email != "stored@example.com" || rc.APIToken != "stored-tok" {
		t.Errorf("got %+v", rc)
	}
}

func TestResolveCredentialsMultiSiteNoArg(t *testing.T) {
	clearEnv(t)
	path := writeCreds(t, map[string]SiteCredentials{
		"https://acme.atlassian.net":  {Email: "a@example.com", APIToken: "t1"},
		"https://other.atlassian.net": {Email: "b@example.com", APIToken: "t2"},
	})

	_, err := ResolveCredentials(path, "")
	if err == nil {
		t.Fatal("want error for multiple sites without --site")
	}
}

func TestResolveCredentialsSiteArgSelectsAmongMany(t *testing.T) {
	clearEnv(t)
	path := writeCreds(t, map[string]SiteCredentials{
		"https://acme.atlassian.net":  {Email: "a@example.com", APIToken: "t1"},
		"https://other.atlassian.net": {Email: "b@example.com", APIToken: "t2"},
	})

	rc, err := ResolveCredentials(path, "https://other.atlassian.net/wiki/spaces/X")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if rc.Site != "https://other.atlassian.net" || rc.Email != "b@example.com" || rc.APIToken != "t2" {
		t.Errorf("got %+v", rc)
	}
}

func TestResolveCredentialsMissingSite(t *testing.T) {
	clearEnv(t)
	path := writeCreds(t, map[string]SiteCredentials{
		"https://acme.atlassian.net": {Email: "a@example.com", APIToken: "t1"},
	})

	_, err := ResolveCredentials(path, "https://nope.atlassian.net")
	if err == nil {
		t.Fatal("want error for site not in credentials")
	}
}

func TestResolveCredentialsNoCredentials(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "none.json")
	_, err := ResolveCredentials(path, "")
	if err == nil {
		t.Fatal("want error when no credentials and no env")
	}
}

func TestResolveCredentialsEnvCredsNoSite(t *testing.T) {
	clearEnv(t)
	// Full env credentials but no site anywhere (no arg, no *_SITE, no store).
	t.Setenv("CONFLUENCE_EMAIL", "env@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "env-tok")

	path := filepath.Join(t.TempDir(), "none.json")
	_, err := ResolveCredentials(path, "")
	if err == nil {
		t.Fatal("want error when credentials are present but no site is selected")
	}
}

func TestResolveCredentialsIncomplete(t *testing.T) {
	clearEnv(t)
	// Stored entry has email but no token.
	path := writeCreds(t, map[string]SiteCredentials{
		"https://acme.atlassian.net": {Email: "a@example.com"},
	})

	_, err := ResolveCredentials(path, "https://acme.atlassian.net")
	if err == nil {
		t.Fatal("want error for incomplete credentials")
	}
}
