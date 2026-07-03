package cli

import (
	"fmt"
	"io"

	"github.com/tammersaleh/confluence-sync/internal/auth"
	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/filesystem"
	"github.com/tammersaleh/confluence-sync/internal/output"
	"github.com/tammersaleh/confluence-sync/internal/sync"
)

type SpaceCmd struct {
	Sync SpaceSyncCmd `cmd:"" help:"Sync a Confluence space to local Markdown files."`
}

type SpaceSyncCmd struct {
	URL    string `arg:"" help:"Confluence space URL."`
	Dir    string `arg:"" help:"Output directory."`
	Prune  bool   `help:"Remove stale local files after sync."`
	DryRun bool   `help:"Show what would be synced without writing."`
}

// writerLogger adapts an io.Writer to sync.Logger.
type writerLogger struct {
	w io.Writer
}

func (l writerLogger) Printf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(l.w, format+"\n", args...)
}

// requireSiteMatch errors when an explicit --site disagrees with the site
// derived from a URL argument. Both are canonicalized before comparison. A
// URL that fails to canonicalize is left for the caller to report.
func requireSiteMatch(flagSite, urlArg string) error {
	fs, err := auth.CanonicalSiteURL(flagSite)
	if err != nil {
		return &output.Error{
			Err:    "invalid_input",
			Detail: fmt.Sprintf("--site %q is not a valid site URL: %v", flagSite, err),
			Input:  flagSite,
			Code:   output.ExitGeneral,
		}
	}
	us, err := auth.CanonicalSiteURL(urlArg)
	if err != nil {
		return nil
	}
	if fs != us {
		return &output.Error{
			Err:    "invalid_input",
			Detail: fmt.Sprintf("--site %s does not match the site in the URL (%s)", fs, us),
			Hint:   "Omit --site (the URL selects the site) or pass a URL on the same site.",
			Input:  urlArg,
			Code:   output.ExitGeneral,
		}
	}
	return nil
}

func (c *SpaceSyncCmd) Run(cli *CLI) error {
	baseURL, spaceKey, err := confluence.ParseSpaceURL(c.URL)
	if err != nil {
		return &output.Error{
			Err:    "invalid_input",
			Detail: err.Error(),
			Hint:   "Expected a Confluence space URL like https://acme.atlassian.net/wiki/spaces/ENG.",
			Input:  c.URL,
			Code:   output.ExitGeneral,
		}
	}

	// The space URL selects the site. If --site is also given it must agree;
	// silently ignoring it would surprise the caller. (Richer site resolution
	// for URL-less commands lands in Phase 1.)
	if cli.Site != "" {
		if err := requireSiteMatch(cli.Site, c.URL); err != nil {
			return err
		}
	}

	rc, err := cli.ResolveCredentials(c.URL)
	if err != nil {
		return err
	}

	client := confluence.NewClient(baseURL, rc.Email, rc.APIToken)

	ctx, cancel := cli.Context()
	defer cancel()

	// Phase 0 passthrough: a plain text logger to stderr. Progress and
	// recoverable warnings (e.g. skipped attachments) go to stderr even under
	// --quiet, which suppresses stdout only. Phase 2 replaces this with a typed
	// JSONL reporter.
	logger := writerLogger{w: cli.stderr()}

	syncer := sync.New(client, filesystem.OS{})
	if err := syncer.Sync(ctx, spaceKey, c.Dir, sync.Options{
		Prune:  c.Prune,
		DryRun: c.DryRun,
		Logger: logger,
	}); err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(map[string]any{
		"synced":     true,
		"space":      spaceKey,
		"output_dir": c.Dir,
		"dry_run":    c.DryRun,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}
