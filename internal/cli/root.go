package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tammersaleh/confluence-cli/internal/auth"
	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/httpx"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

// CLI holds global flags and subcommands. Global flags are shared by every
// subcommand via Kong's dependency injection (the *CLI is passed to Run).
type CLI struct {
	Site    string        `help:"Confluence site base URL (e.g. https://acme.atlassian.net)." env:"CONFLUENCE_SITE"`
	Fields  string        `help:"Comma-separated top-level fields to include." env:"CONFLUENCE_FIELDS"`
	Quiet   bool          `short:"q" help:"Suppress stdout output (exit code and stderr only)."`
	Timeout time.Duration `help:"Overall command timeout (e.g. 30s, 2m). Zero means no timeout." env:"CONFLUENCE_TIMEOUT"`
	Trace   bool          `help:"Emit structured diagnostics to stderr as JSONL."`

	// Test-override fields (unexported, so Kong ignores them).
	out             io.Writer
	err             io.Writer
	in              io.Reader
	credentialsPath string
	ctx             context.Context

	Version    VersionCmd    `cmd:"" help:"Show version."`
	Space      SpaceCmd      `cmd:"" help:"Confluence spaces."`
	Page       PageCmd       `cmd:"" aliases:"pages" help:"Read pages."`
	Attachment AttachmentCmd `cmd:"" aliases:"attachments" help:"Page attachments."`
	Search     SearchCmd     `cmd:"" help:"Search with CQL."`
	Comment    CommentCmd    `cmd:"" aliases:"comments" help:"Read page comments."`
	Label      LabelCmd      `cmd:"" aliases:"labels" help:"Read page labels."`
	User       UserCmd       `cmd:"" aliases:"users" help:"Read user information."`
	Auth       AuthCmd       `cmd:"" help:"Manage authentication."`
}

// NewClientForSite resolves credentials for the given site hint (a canonical
// base URL derived from a URL arg, or "" to fall back to --site / the single
// stored default) and returns a ready confluence client.
func (c *CLI) NewClientForSite(siteHint string) (confluence.Client, *auth.ResolvedCredentials, error) {
	rc, err := c.ResolveCredentials(siteHint) // already returns *output.Error on failure
	if err != nil {
		return nil, nil, err
	}
	return confluence.NewClient(rc.Site, rc.Email, rc.APIToken), rc, nil
}

// Context returns a fresh context honoring --timeout and --trace, and cancels
// on SIGINT/SIGTERM so long-running commands (e.g. space sync) unwind cleanly
// instead of dying mid-write. Caller must call cancel - use defer - to release
// the signal handler and any deadline. When --trace is set the context carries
// a JSON-lines tracer that emits events to the CLI's configured stderr writer.
// The context is stashed on the CLI for reuse by later helpers.
func (c *CLI) Context() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	if c.Trace {
		ctx = httpx.WithTracer(ctx, httpx.NewJSONLinesTracer(c.stderr()))
	}
	if c.Timeout > 0 {
		tctx, cancel := context.WithTimeout(ctx, c.Timeout)
		c.ctx = tctx
		return tctx, func() { cancel(); stop() }
	}
	c.ctx = ctx
	return ctx, stop
}

// ParsedFields returns the --fields value split into individual field names.
func (c *CLI) ParsedFields() []string {
	if c.Fields == "" {
		return nil
	}
	parts := strings.Split(c.Fields, ",")
	fields := make([]string, 0, len(parts))
	for _, f := range parts {
		f = strings.TrimSpace(f)
		if f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

// SetOutput overrides stdout/stderr for testing.
func (c *CLI) SetOutput(out, errW io.Writer) {
	c.out = out
	c.err = errW
}

// SetInput overrides stdin for testing.
func (c *CLI) SetInput(in io.Reader) {
	c.in = in
}

// Stdin returns the stdin reader, defaulting to os.Stdin.
func (c *CLI) Stdin() io.Reader {
	if c.in != nil {
		return c.in
	}
	return os.Stdin
}

// SetCredentialsPath overrides the credentials file location for testing.
func (c *CLI) SetCredentialsPath(path string) {
	c.credentialsPath = path
}

func (c *CLI) stdout() io.Writer {
	if c.out != nil {
		return c.out
	}
	return os.Stdout
}

func (c *CLI) stderr() io.Writer {
	if c.err != nil {
		return c.err
	}
	return os.Stderr
}

// NewPrinter creates a Printer configured from global CLI flags.
func (c *CLI) NewPrinter() *output.Printer {
	return &output.Printer{
		Out:    c.stdout(),
		Err:    c.stderr(),
		Quiet:  c.Quiet,
		Fields: c.ParsedFields(),
	}
}

// ResolveCredentials merges stored and environment credentials for a site.
// siteFromArg wins over the global --site flag when non-empty. On failure it
// returns an *output.Error already classified as an auth error.
func (c *CLI) ResolveCredentials(siteFromArg string) (*auth.ResolvedCredentials, error) {
	site := siteFromArg
	if site == "" {
		site = c.Site
	}

	path := c.credentialsPath
	if path == "" {
		p, err := auth.DefaultCredentialsPath()
		if err != nil {
			return nil, &output.Error{
				Err:    "not_authed",
				Detail: err.Error(),
				Hint:   "Run 'confluence auth login' or set CONFLUENCE_SITE, CONFLUENCE_EMAIL, and CONFLUENCE_API_TOKEN",
				Code:   output.ExitAuth,
			}
		}
		path = p
	}

	rc, err := auth.ResolveCredentials(path, site)
	if err != nil {
		return nil, &output.Error{
			Err:    "not_authed",
			Detail: err.Error(),
			Hint:   "Run 'confluence auth login' or set CONFLUENCE_SITE, CONFLUENCE_EMAIL, and CONFLUENCE_API_TOKEN",
			Code:   output.ExitAuth,
		}
	}
	return rc, nil
}

// ClassifyError maps Confluence sentinels to structured errors, delegating
// anything else to the domain-agnostic httpx classifier.
//
// Typed transport errors (rate limits, context cancellation/timeout) are
// matched first, before the domain-sentinel switch: a rate-limit error may
// wrap a domain sentinel like ErrAPIError, and it must classify as
// rate_limited, not api_error. Everything else that carries a domain sentinel
// (reachable via errors.Is through StatusError.Unwrap) is classified here;
// residual errors fall through to httpx.
func (c *CLI) ClassifyError(err error) *output.Error {
	// Transport errors httpx owns take precedence over domain sentinels.
	var rl *httpx.RateLimitError
	if errors.As(err, &rl) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return httpx.ClassifyError(err)
	}

	var oErr *output.Error
	switch {
	case errors.Is(err, confluence.ErrUnauthorized):
		oErr = &output.Error{
			Err:    "unauthorized",
			Detail: err.Error(),
			Hint:   "Run 'confluence auth login' or check CONFLUENCE_EMAIL/CONFLUENCE_API_TOKEN",
			Code:   output.ExitAuth,
		}
	case errors.Is(err, confluence.ErrForbidden):
		oErr = &output.Error{
			Err:    "forbidden",
			Detail: err.Error(),
			Hint:   "Your account lacks permission for this resource.",
			Code:   output.ExitAuth,
		}
	case errors.Is(err, confluence.ErrPageNotFound):
		oErr = &output.Error{
			Err:    "page_not_found",
			Detail: err.Error(),
			Hint:   "Check the page ID/URL. Fetch a page with 'confluence page get <id|url>', or list a space's pages with 'confluence page list --space <key>'.",
			Code:   output.ExitGeneral,
		}
	case errors.Is(err, confluence.ErrSpaceNotFound):
		oErr = &output.Error{
			Err:    "space_not_found",
			Detail: err.Error(),
			Hint:   "Check the space key/URL. List spaces with 'confluence space list'.",
			Code:   output.ExitGeneral,
		}
	case errors.Is(err, confluence.ErrAttachmentNotFound):
		oErr = &output.Error{
			Err:    "attachment_not_found",
			Detail: err.Error(),
			Hint:   "Check the attachment ID. List a page's attachments with 'confluence attachment list <page-id|url>'.",
			Code:   output.ExitGeneral,
		}
	case errors.Is(err, confluence.ErrUserNotFound):
		oErr = &output.Error{
			Err:    "user_not_found",
			Detail: err.Error(),
			Hint:   "Check the accountId. Find it via 'confluence user current' or a page's author_id.",
			Code:   output.ExitGeneral,
		}
	case errors.Is(err, confluence.ErrAPIError):
		oErr = &output.Error{
			Err:    "api_error",
			Detail: err.Error(),
			Code:   output.ExitGeneral,
		}
	default:
		return httpx.ClassifyError(err)
	}

	return withEndpoint(oErr, err)
}

// withEndpoint copies the endpoint from a wrapped *confluence.StatusError onto
// the classified error when it doesn't already carry one, so per-page failures
// during a sync stay traceable to the API path that produced them.
func withEndpoint(oErr *output.Error, err error) *output.Error {
	var se *confluence.StatusError
	if errors.As(err, &se) && oErr.Endpoint == "" {
		oErr.Endpoint = se.Endpoint
	}
	return oErr
}
