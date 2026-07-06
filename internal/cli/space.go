package cli

import (
	"fmt"
	"io"

	"github.com/tammersaleh/confluence-cli/internal/auth"
	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/confluenceurl"
	"github.com/tammersaleh/confluence-cli/internal/filesystem"
	"github.com/tammersaleh/confluence-cli/internal/output"
	"github.com/tammersaleh/confluence-cli/internal/sync"
)

type SpaceCmd struct {
	Sync SpaceSyncCmd `cmd:"" help:"Sync a Confluence space to local Markdown files."`
	Info SpaceInfoCmd `cmd:"" help:"Show space details."`
	List SpaceListCmd `cmd:"" help:"List spaces."`
}

type SpaceListCmd struct {
	Limit  int    `default:"25" help:"Page size requested from the API."`
	Cursor string `help:"Opaque pagination cursor from a prior _meta.next_cursor."`
	All    bool   `help:"Fetch every page (loop until the cursor is exhausted)."`
}

func (c *SpaceListCmd) Run(cli *CLI) error {
	// Space listing is site-wide: the site comes from --site or the single
	// stored default (no URL arg to derive it from).
	client, _, err := cli.NewClientForSite("")
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	cursor := c.Cursor
	var next string
	for {
		spaces, n, err := client.ListSpaces(ctx, cursor, c.Limit)
		if err != nil {
			return cli.ClassifyError(err)
		}
		next = n

		for _, s := range spaces {
			row := map[string]any{
				"id":          s.ID,
				"key":         s.Key,
				"name":        s.Name,
				"type":        s.Type,
				"status":      s.Status,
				"homepage_id": s.HomepageID,
			}
			if err := p.PrintItem(row); err != nil {
				return err
			}
		}

		if !c.All {
			break
		}
		if next == "" {
			break
		}
		cursor = next
	}

	if c.All {
		return p.PrintMeta(output.Meta{})
	}
	return p.PrintMeta(output.Meta{HasMore: next != "", NextCursor: next})
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

type SpaceInfoCmd struct {
	Refs []string `arg:"" name:"key-id-or-url" help:"Space keys, IDs, or URLs (one site per invocation)."`
}

func (c *SpaceInfoCmd) Run(cli *CLI) error {
	// spaceLookup binds each caller input to the resolved lookup. byID selects
	// GetSpaceByID (a numeric id) over GetSpace (a space key).
	type spaceLookup struct {
		input string
		byID  bool
		value string
	}
	var lookups []spaceLookup
	var urlRefs []confluenceurl.Ref
	var siteURLArg string // a raw URL arg, used for --site match reporting

	for _, arg := range c.Refs {
		r, matched, perr := confluenceurl.Parse(arg)
		if matched && perr != nil {
			return &output.Error{
				Err:    "invalid_input",
				Detail: perr.Error(),
				Hint:   "Pass a space key (e.g. ENG), a numeric space id, or a space URL (…/wiki/spaces/ENG).",
				Input:  arg,
				Code:   output.ExitGeneral,
			}
		}
		if !matched {
			// Bare arg: all-digits is a space id, otherwise a space key.
			lookups = append(lookups, spaceLookup{input: arg, byID: isDigits(arg), value: arg})
			continue
		}
		// A URL. Only space and page URLs carry a space key.
		if r.Kind != confluenceurl.KindSpace && r.Kind != confluenceurl.KindPage || r.SpaceKey == "" {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "URL does not identify a space",
				Hint:   "Pass a space key (e.g. ENG), a numeric space id, or a space URL (…/wiki/spaces/ENG).",
				Input:  arg,
				Code:   output.ExitGeneral,
			}
		}
		urlRefs = append(urlRefs, r)
		siteURLArg = arg
		lookups = append(lookups, spaceLookup{input: arg, byID: false, value: r.SpaceKey})
	}

	siteHint := ""
	if len(urlRefs) > 0 {
		site, cerr := confluenceurl.CommonSite(urlRefs)
		if cerr != nil {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "all spaces must be on one site",
				Hint:   "Split into one invocation per site.",
				Code:   output.ExitGeneral,
			}
		}
		siteHint = site
		if cli.Site != "" {
			if err := requireSiteMatch(cli.Site, siteURLArg); err != nil {
				return err
			}
		}
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	errCount := 0
	for _, l := range lookups {
		var space *confluence.Space
		var lerr error
		if l.byID {
			space, lerr = client.GetSpaceByID(ctx, l.value)
		} else {
			space, lerr = client.GetSpace(ctx, l.value)
		}
		if lerr != nil {
			oErr := cli.ClassifyError(lerr)
			oErr.Input = l.input
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}

		row := map[string]any{
			"input":       l.input,
			"id":          space.ID,
			"key":         space.Key,
			"name":        space.Name,
			"type":        space.Type,
			"status":      space.Status,
			"homepage_id": space.HomepageID,
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
	}

	if err := p.PrintMeta(output.Meta{ErrorCount: errCount}); err != nil {
		return err
	}
	if errCount > 0 {
		return &output.ExitError{Code: output.ExitGeneral}
	}
	return nil
}

// isDigits reports whether s is non-empty and all ASCII digits. A numeric space
// arg is looked up by id; anything else is treated as a space key.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
