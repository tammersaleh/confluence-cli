package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Credentials struct {
	Sites map[string]SiteCredentials `json:"sites"`
}

type SiteCredentials struct {
	Email    string `json:"email"`
	APIToken string `json:"api_token"`
	CloudID  string `json:"cloud_id,omitempty"`
}

// ResolvedCredentials holds the merged credentials for a single site.
type ResolvedCredentials struct {
	Site     string // canonical site base URL
	Email    string
	APIToken string
	CloudID  string
}

// CanonicalSiteURL normalizes a site/space/page URL to its canonical base:
// scheme + lowercased host, with no path, query, or fragment. Inputs without
// a scheme are treated as bare hosts and default to https.
func CanonicalSiteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty site URL")
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid site URL %q: %w", raw, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("site URL %q has no host", raw)
	}

	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(u.Host), nil
}

// DefaultCredentialsPath returns <config>/confluence-cli/credentials.json.
func DefaultCredentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w", err)
	}
	return filepath.Join(dir, "confluence-cli", "credentials.json"), nil
}

// LoadCredentials reads and parses the credentials file.
// Returns empty Credentials if the file does not exist.
func LoadCredentials(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Credentials{Sites: map[string]SiteCredentials{}}, nil
		}
		return nil, err
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	if creds.Sites == nil {
		creds.Sites = map[string]SiteCredentials{}
	}
	return &creds, nil
}

// SaveCredentials writes credentials to the given path with 0600 permissions.
// Creates parent directories if needed.
func SaveCredentials(path string, creds *Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ResolveCredentials merges environment and stored credentials for a site,
// with environment taking precedence field by field. The site argument, when
// non-empty, has already been resolved from any CLI flag by the caller.
func ResolveCredentials(path string, site string) (*ResolvedCredentials, error) {
	envEmail := firstNonEmpty(os.Getenv("CONFLUENCE_EMAIL"), os.Getenv("ATLASSIAN_API_EMAIL"))
	envToken := firstNonEmpty(os.Getenv("CONFLUENCE_API_TOKEN"), os.Getenv("ATLASSIAN_API_KEY"))
	envSite := firstNonEmpty(os.Getenv("CONFLUENCE_SITE"), os.Getenv("ATLASSIAN_SITE"))

	// Effective site: explicit arg wins, else the env-provided site.
	effectiveSite := site
	if effectiveSite == "" && envSite != "" {
		canon, err := CanonicalSiteURL(envSite)
		if err != nil {
			return nil, err
		}
		effectiveSite = canon
	}

	creds, err := LoadCredentials(path)
	if err != nil {
		return nil, err
	}

	var (
		stored    SiteCredentials
		finalSite string
	)

	if effectiveSite != "" {
		canon, err := CanonicalSiteURL(effectiveSite)
		if err != nil {
			return nil, err
		}
		finalSite = canon
		entry, ok := creds.Sites[canon]
		if ok {
			stored = entry
		} else if envEmail == "" || envToken == "" {
			// A miss is only acceptable when env supplies both fields.
			return nil, fmt.Errorf("site %q not found in credentials", canon)
		}
	} else {
		switch len(creds.Sites) {
		case 1:
			for key, entry := range creds.Sites {
				finalSite = key
				stored = entry
			}
		case 0:
			if envEmail == "" || envToken == "" {
				return nil, errors.New("no credentials found; run 'confluence auth login' first")
			}
		default:
			return nil, errors.New("multiple sites configured; use --site to select one")
		}
	}

	if finalSite == "" {
		return nil, errors.New("no site selected; pass a site URL, set --site, or CONFLUENCE_SITE")
	}

	email := firstNonEmpty(envEmail, stored.Email)
	token := firstNonEmpty(envToken, stored.APIToken)
	if email == "" || token == "" {
		return nil, fmt.Errorf("incomplete credentials for %q (need email and API token)", finalSite)
	}

	return &ResolvedCredentials{
		Site:     finalSite,
		Email:    email,
		APIToken: token,
		CloudID:  stored.CloudID,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
