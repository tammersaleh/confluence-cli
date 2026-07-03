package cli

import (
	"fmt"
	"io"

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
	fmt.Fprintf(l.w, format+"\n", args...)
}

type noopLogger struct{}

func (noopLogger) Printf(string, ...interface{}) {}

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

	// --site reconciliation with the URL-derived site is a Phase 1 refinement;
	// the space URL doubles as the site selector here.
	rc, err := cli.ResolveCredentials(c.URL)
	if err != nil {
		return err
	}

	client := confluence.NewClient(baseURL, rc.Email, rc.APIToken)

	ctx, cancel := cli.Context()
	defer cancel()

	// Phase 0 passthrough: a plain text logger to stderr. Phase 2 replaces
	// this with a typed JSONL reporter.
	var logger sync.Logger = noopLogger{}
	if !cli.Quiet {
		logger = writerLogger{w: cli.stderr()}
	}

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
