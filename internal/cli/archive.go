package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/confluenceurl"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

// longTaskPollInterval is the wait between long-task polls. A single-page
// archive finishes in well under a second. Tests shorten it.
var longTaskPollInterval = 500 * time.Millisecond

type PageArchiveCmd struct {
	Refs   []string `arg:"" name:"id-or-url" help:"Page IDs or URLs (all on one site)."`
	DryRun bool     `name:"dry-run" help:"Preview without archiving."`
}

func (c *PageArchiveCmd) Run(cli *CLI) error {
	return runArchiveOp(cli, c.Refs, archiveOp{
		verb:       "archive",
		doneField:  "archived",
		doneStatus: "archived",
		dryRun:     c.DryRun,
		precheck:   archivePrecheck,
		submit: func(ctx context.Context, client confluence.Client, pageID string) (string, error) {
			return client.ArchivePage(ctx, pageID)
		},
	})
}

// archivePrecheck decides whether a page may be archived. The v1 endpoint
// archives only the named page and silently reparents its children to the
// archived page's parent, so a page with any direct child (of any content
// type) is refused. The check is not atomic: a child created between it and
// the POST is still reparented.
func archivePrecheck(ctx context.Context, cli *CLI, client confluence.Client, pd *confluence.PageDetail) (noop bool, oErr *output.Error) {
	switch pd.Status {
	case "archived":
		return true, nil
	case "trashed":
		return false, &output.Error{
			Err:    "page_trashed",
			Detail: fmt.Sprintf("page %s is in the trash", pd.ID),
			Hint:   "A trashed page cannot be archived. Restore it from the space trash in the Confluence UI first.",
			Code:   output.ExitGeneral,
		}
	}
	children, _, err := client.ListDirectChildren(ctx, confluence.ContentPage, pd.ID, "", 1)
	if err != nil {
		return false, cli.ClassifyError(err)
	}
	if len(children) > 0 {
		return false, &output.Error{
			Err:    "has_children",
			Detail: fmt.Sprintf("page %s has children; archiving it would move them up to its parent", pd.ID),
			Hint:   fmt.Sprintf("Archive or move the children first. List them with 'confluence page children %s'.", pd.ID),
			Code:   output.ExitGeneral,
		}
	}
	return false, nil
}

// archiveOp describes one long-task page operation (archive or unarchive).
type archiveOp struct {
	verb       string // "archive"; names the <verb>_failed and <verb>_pending codes
	doneField  string // row field reporting whether this run changed the page
	doneStatus string // the page's status once the operation has taken effect
	dryRun     bool
	// precheck runs after the page is fetched. noop means the page is already
	// in the target state; a non-nil error refuses the page.
	precheck func(ctx context.Context, cli *CLI, client confluence.Client, pd *confluence.PageDetail) (noop bool, oErr *output.Error)
	// submit starts the operation and returns the long task id.
	submit func(ctx context.Context, client confluence.Client, pageID string) (taskID string, err error)
}

// runArchiveOp applies op to each ref in turn, one request per page, and waits
// for each long task. Per-page failures go inline. If the deadline expires
// after a submit succeeded, the page's outcome is unknown: that page gets a
// <verb>_pending row and the remaining refs are not attempted.
func runArchiveOp(cli *CLI, refs []string, op archiveOp) error {
	pairs, client, err := resolvePageRefs(cli, refs)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	errCount := 0
	exitCode := output.ExitGeneral
	fail := func(pr pageRef, oErr *output.Error) error {
		oErr.Input = pr.input
		errCount++
		return p.PrintItem(oErr.AsItem())
	}

refs:
	for _, pr := range pairs {
		pd, oErr := getPageOrExplain(ctx, cli, client, pr)
		if oErr != nil {
			if err := fail(pr, oErr); err != nil {
				return err
			}
			continue
		}
		row := map[string]any{"input": pr.input, "id": pr.pageID, "title": pd.Title}

		noop, oErr := op.precheck(ctx, cli, client, pd)
		switch {
		case oErr != nil:
			if err := fail(pr, oErr); err != nil {
				return err
			}
			continue
		case noop:
			row[op.doneField] = false
			row["status"] = pd.Status
		case op.dryRun:
			row["would_"+op.verb] = true
		default:
			oErr, pending := submitAndWait(ctx, cli, client, pr.pageID, op)
			if oErr != nil {
				if err := fail(pr, oErr); err != nil {
					return err
				}
				if pending {
					exitCode = oErr.Code
					break refs
				}
				continue
			}
			row[op.doneField] = true
			row["status"] = op.doneStatus
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
	}

	if err := p.PrintMeta(output.Meta{ErrorCount: errCount}); err != nil {
		return err
	}
	if errCount > 0 {
		return &output.ExitError{Code: exitCode}
	}
	return nil
}

// submitAndWait starts op on one page and waits for its long task. pending
// reports that the submit succeeded but the deadline expired before the task
// finished, so the outcome is unknown.
func submitAndWait(ctx context.Context, cli *CLI, client confluence.Client, pageID string, op archiveOp) (oErr *output.Error, pending bool) {
	taskID, err := op.submit(ctx, client, pageID)
	if err != nil {
		// The submit is not idempotent. When a retried request's first attempt
		// already committed, the retry is rejected ("already archived"), so
		// trust the page's state over the error.
		if cur, gerr := client.GetPage(ctx, pageID, confluence.BodyFormatStorage); gerr == nil && cur.Status == op.doneStatus {
			return nil, false
		}
		return cli.ClassifyError(err), false
	}

	task, err := awaitLongTask(ctx, client, taskID)
	if err != nil {
		oErr := cli.ClassifyError(err)
		oErr.Data = map[string]any{"id": pageID, "task_id": taskID}
		if ctx.Err() == nil {
			return oErr, false
		}
		oErr.Err = op.verb + "_pending"
		oErr.Detail = fmt.Sprintf("%s of page %s was submitted but did not finish before the timeout", op.verb, pageID)
		oErr.Hint = fmt.Sprintf("The task may still finish. Check with 'confluence page get %s' (status); re-running this command later is safe.", pageID)
		return oErr, true
	}

	// Fail closed: only an explicit success with no errors counts.
	if !task.Successful || len(task.Errors) > 0 {
		return &output.Error{
			Err:    op.verb + "_failed",
			Detail: fmt.Sprintf("%s task %s for page %s finished with status %q", op.verb, taskID, pageID, task.Status),
			Hint:   fmt.Sprintf("Check the page with 'confluence page get %s' (status).", pageID),
			Data: map[string]any{
				"id":       pageID,
				"task_id":  taskID,
				"status":   task.Status,
				"errors":   task.Errors,
				"messages": task.Messages,
			},
			Code: output.ExitGeneral,
		}, false
	}
	return nil, false
}

// awaitLongTask polls a long task until it reports finished or ctx ends. A
// task that is not finished is running, whatever its other fields say.
func awaitLongTask(ctx context.Context, client confluence.Client, taskID string) (*confluence.LongTask, error) {
	for {
		task, err := client.GetLongTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if task.Finished {
			return task, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(longTaskPollInterval):
		}
	}
}

// pageRef binds a caller input to the page id resolved from it. bare marks an
// id given without a URL, whose content type is therefore unknown.
type pageRef struct {
	input  string
	pageID string
	bare   bool
}

// resolvePageRefs parses bulk page arguments (ids or page URLs), enforces one
// site per invocation, and builds the client for that site. It runs before any
// mutation, so a mixed-site batch can never partially apply.
func resolvePageRefs(cli *CLI, refs []string) ([]pageRef, confluence.Client, error) {
	const hint = "Pass a numeric page id or a page URL (…/wiki/spaces/ENG/pages/123)."
	var pairs []pageRef
	var urlRefs []confluenceurl.Ref
	var siteURLArg string

	for _, arg := range refs {
		r, matched, perr := confluenceurl.Parse(arg)
		if matched && perr != nil {
			return nil, nil, &output.Error{Err: "invalid_input", Detail: perr.Error(), Hint: hint, Input: arg, Code: output.ExitGeneral}
		}
		if !matched {
			pairs = append(pairs, pageRef{input: arg, pageID: arg, bare: true})
			continue
		}
		if r.Kind != confluenceurl.KindPage || r.PageID == "" {
			return nil, nil, &output.Error{Err: "invalid_input", Detail: "URL does not identify a page", Hint: hint, Input: arg, Code: output.ExitGeneral}
		}
		urlRefs = append(urlRefs, r)
		siteURLArg = arg
		pairs = append(pairs, pageRef{input: arg, pageID: r.PageID})
	}

	siteHint := ""
	if len(urlRefs) > 0 {
		site, cerr := confluenceurl.CommonSite(urlRefs)
		if cerr != nil {
			return nil, nil, &output.Error{
				Err:    "invalid_input",
				Detail: "all pages must be on one site",
				Hint:   "Split into one invocation per site.",
				Code:   output.ExitGeneral,
			}
		}
		siteHint = site
		if cli.Site != "" {
			if err := requireSiteMatch(cli.Site, siteURLArg); err != nil {
				return nil, nil, err
			}
		}
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return nil, nil, err
	}
	return pairs, client, nil
}

// getPageOrExplain fetches a page. When a bare id is not a page it probes for
// a folder, so the caller gets not_a_page instead of a misleading
// page_not_found: the archive endpoints accept folder ids.
func getPageOrExplain(ctx context.Context, cli *CLI, client confluence.Client, pr pageRef) (*confluence.PageDetail, *output.Error) {
	pd, err := client.GetPage(ctx, pr.pageID, confluence.BodyFormatStorage)
	if err == nil {
		return pd, nil
	}
	oErr := cli.ClassifyError(err)
	if pr.bare && errors.Is(err, confluence.ErrPageNotFound) {
		switch _, ferr := client.GetFolder(ctx, pr.pageID); {
		case ferr == nil:
			oErr = &output.Error{
				Err:    "not_a_page",
				Detail: fmt.Sprintf("%s is a folder, not a page", pr.pageID),
				Hint:   "Only pages can be archived or unarchived with this command.",
				Code:   output.ExitGeneral,
			}
		case errors.Is(ferr, confluence.ErrFolderNotFound):
		default:
			oErr = cli.ClassifyError(ferr)
		}
	}
	return nil, oErr
}
