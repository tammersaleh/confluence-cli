package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/tammersaleh/confluence-cli/internal/auth"
	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

// AuthCmd groups the authentication subcommands.
type AuthCmd struct {
	Login  AuthLoginCmd  `cmd:"" help:"Log in to a site (token on stdin)."`
	Status AuthStatusCmd `cmd:"" help:"Show configured sites and active auth."`
	Logout AuthLogoutCmd `cmd:"" help:"Remove a site's stored credentials."`
}

// credentialsFilePath returns the resolved credentials file location, honoring
// the test override before falling back to the platform default. All auth
// commands go through this so SetCredentialsPath works in tests.
func (c *CLI) credentialsFilePath() (string, error) {
	if c.credentialsPath != "" {
		return c.credentialsPath, nil
	}
	return auth.DefaultCredentialsPath()
}

// envGet returns the first non-empty value among the named env vars.
func envGet(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// configError wraps an unexpected credentials-store I/O failure.
func configError(err error) *output.Error {
	return &output.Error{
		Err:    "config_error",
		Detail: err.Error(),
		Hint:   "Check the credentials file path and permissions.",
		Code:   output.ExitGeneral,
	}
}

// AuthLoginCmd validates credentials against the site and stores them on success.
//
// The site comes from the global --site flag (CONFLUENCE_SITE), not a local
// flag: Kong inherits top-level flags into every subcommand, so a second local
// --site would collide. Only --email is command-local.
type AuthLoginCmd struct {
	Email string `required:"" help:"Account email."`
}

func (c *AuthLoginCmd) Run(cli *CLI) error {
	if strings.TrimSpace(cli.Site) == "" {
		return &output.Error{
			Err:    "invalid_input",
			Detail: "no site given",
			Hint:   "Pass --site https://your-domain.atlassian.net",
			Code:   output.ExitGeneral,
		}
	}
	canon, err := auth.CanonicalSiteURL(cli.Site)
	if err != nil {
		return &output.Error{
			Err:    "invalid_input",
			Detail: err.Error(),
			Hint:   "Site looks like https://your-domain.atlassian.net",
			Input:  cli.Site,
			Code:   output.ExitGeneral,
		}
	}

	// The token only ever arrives on stdin - never via a flag/argv, which would
	// leak it into shell history and process listings.
	raw, err := io.ReadAll(cli.Stdin())
	if err != nil {
		return &output.Error{
			Err:    "invalid_input",
			Detail: fmt.Sprintf("reading token from stdin: %v", err),
			Code:   output.ExitGeneral,
		}
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return &output.Error{
			Err:    "missing_token",
			Detail: "no API token on stdin",
			Hint:   `Pipe the token on stdin: printf %s "$TOKEN" | confluence auth login --site <url> --email <you>`,
			Code:   output.ExitGeneral,
		}
	}

	client := confluence.NewClient(canon, c.Email, token)

	ctx, cancel := cli.Context()
	defer cancel()

	// Validate before persisting: bad credentials must not be written.
	user, err := client.GetCurrentUser(ctx)
	if err != nil {
		return cli.ClassifyError(err)
	}

	path, err := cli.credentialsFilePath()
	if err != nil {
		return configError(err)
	}
	creds, err := auth.LoadCredentials(path)
	if err != nil {
		return configError(err)
	}
	creds.Sites[canon] = auth.SiteCredentials{Email: c.Email, APIToken: token}
	if err := auth.SaveCredentials(path, creds); err != nil {
		return configError(err)
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(map[string]any{
		"site":         canon,
		"email":        c.Email,
		"account_id":   user.AccountID,
		"display_name": user.DisplayName,
		"status":       "logged_in",
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// AuthStatusCmd reports configured sites and any active env override. It never
// prints token values, and always succeeds (exit 0) even with nothing configured.
type AuthStatusCmd struct{}

// Emitted JSONL shape (one line per row, then a _meta trailer):
//
//	Stored site row:
//	  {"site","email","source":"stored","has_token":bool,"is_default":bool}
//	Env override row (only when env supplies both email and token):
//	  {"site","email","source":"env","has_token":true,
//	   "env_site_set":bool,"env_email_set":bool,"env_api_token_set":bool,
//	   "note"?}  // note present only when no site could be selected
//
// is_default is true for a stored site iff it is the only stored site and no
// site override (--site flag or *_SITE env var) is in effect. The env row's
// site is the effective active site: --site/*_SITE if set, else the sole stored
// site. An agent reads these rows to see which sites exist, whether env is
// overriding, and which credentials would actually be used.
func (c *AuthStatusCmd) Run(cli *CLI) error {
	p := cli.NewPrinter()

	envEmail := envGet("CONFLUENCE_EMAIL", "ATLASSIAN_EMAIL")
	envToken := envGet("CONFLUENCE_API_TOKEN", "ATLASSIAN_TOKEN")
	envSite := envGet("CONFLUENCE_SITE", "ATLASSIAN_SITE")

	path, err := cli.credentialsFilePath()
	if err != nil {
		return configError(err)
	}
	creds, err := auth.LoadCredentials(path)
	if err != nil {
		return configError(err)
	}

	// A site override is any explicit site selection: the global --site flag or
	// a *_SITE env var. It disqualifies a lone stored site from being "default".
	override := cli.Site
	if override == "" {
		override = envSite
	}
	hasOverride := strings.TrimSpace(override) != ""

	for _, site := range sortedKeys(creds.Sites) {
		sc := creds.Sites[site]
		if err := p.PrintItem(map[string]any{
			"site":       site,
			"email":      sc.Email,
			"source":     "stored",
			"has_token":  sc.APIToken != "",
			"is_default": len(creds.Sites) == 1 && !hasOverride,
		}); err != nil {
			return err
		}
	}

	if envEmail != "" && envToken != "" {
		activeSite := ""
		if s, err := auth.CanonicalSiteURL(override); err == nil {
			activeSite = s
		} else if len(creds.Sites) == 1 {
			for k := range creds.Sites {
				activeSite = k
			}
		}
		row := map[string]any{
			"site":              activeSite,
			"email":             envEmail,
			"source":            "env",
			"has_token":         true,
			"env_site_set":      envSite != "",
			"env_email_set":     true,
			"env_api_token_set": true,
		}
		if activeSite == "" {
			row["note"] = "env credentials present but no site selected; set CONFLUENCE_SITE or configure a single site"
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}

// AuthLogoutCmd removes a site's stored credentials.
type AuthLogoutCmd struct {
	Site string `arg:"" optional:"" help:"Site to remove (base URL). Omit only if exactly one is stored."`
}

func (c *AuthLogoutCmd) Run(cli *CLI) error {
	path, err := cli.credentialsFilePath()
	if err != nil {
		return configError(err)
	}
	creds, err := auth.LoadCredentials(path)
	if err != nil {
		return configError(err)
	}

	var target string
	if strings.TrimSpace(c.Site) != "" {
		canon, err := auth.CanonicalSiteURL(c.Site)
		if err != nil {
			return &output.Error{
				Err:    "invalid_input",
				Detail: err.Error(),
				Hint:   "Site looks like https://your-domain.atlassian.net",
				Input:  c.Site,
				Code:   output.ExitGeneral,
			}
		}
		target = canon
	} else {
		switch len(creds.Sites) {
		case 1:
			for k := range creds.Sites {
				target = k
			}
		case 0:
			return &output.Error{
				Err:    "not_found",
				Detail: "no stored credentials to remove",
				Hint:   "Run 'confluence auth status' to list configured sites.",
				Code:   output.ExitGeneral,
			}
		default:
			return &output.Error{
				Err:    "invalid_input",
				Detail: "multiple sites configured; specify which to remove",
				Hint:   "Pass a site URL, or run 'confluence auth status' to list configured sites.",
				Code:   output.ExitGeneral,
			}
		}
	}

	if _, ok := creds.Sites[target]; !ok {
		return &output.Error{
			Err:    "not_found",
			Detail: fmt.Sprintf("no stored credentials for %s", target),
			Hint:   "Run 'confluence auth status' to list configured sites.",
			Input:  c.Site,
			Code:   output.ExitGeneral,
		}
	}

	delete(creds.Sites, target)
	if err := auth.SaveCredentials(path, creds); err != nil {
		return configError(err)
	}

	row := map[string]any{
		"site":   target,
		"status": "logged_out",
	}
	if envActiveForSite(target) {
		row["env_still_active"] = true
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(row); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// envActiveForSite reports whether env vars still provide usable credentials for
// site: both email and token set, and the env site (if any) canonicalizes to it.
func envActiveForSite(site string) bool {
	if envGet("CONFLUENCE_EMAIL", "ATLASSIAN_EMAIL") == "" ||
		envGet("CONFLUENCE_API_TOKEN", "ATLASSIAN_TOKEN") == "" {
		return false
	}
	envSite := envGet("CONFLUENCE_SITE", "ATLASSIAN_SITE")
	if envSite == "" {
		return true // no env site pin - env creds apply to any selected site
	}
	canon, err := auth.CanonicalSiteURL(envSite)
	return err == nil && canon == site
}

func sortedKeys(m map[string]auth.SiteCredentials) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
