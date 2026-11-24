package cli

import (
	"errors"

	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/confluenceurl"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

type FolderCmd struct {
	Get    FolderGetCmd    `cmd:"" help:"Get one or more folders."`
	Create FolderCreateCmd `cmd:"" help:"Create a folder."`
	Move   FolderMoveCmd   `cmd:"" help:"Move a folder under a new parent page or folder."`
	Delete FolderDeleteCmd `cmd:"" help:"Delete folders (moves to trash)."`
}

// containerRef is a resolved page-or-folder reference. Kind is ContentPage or
// ContentFolder when the input was a typed URL, and "" for a bare id, whose
// type is only knowable by asking the API.
type containerRef struct {
	siteHint string
	id       string
	kind     confluence.ContentKind
}

// resolveContainerRef derives the site hint, id, and (when the URL says so)
// content kind from a ref that may name a page or a folder. A bare id leaves
// kind empty. Only the commands that genuinely accept either kind (page
// children, page move --parent, --parent flags) use this; everything else
// stays on resolvePageRef so a folder URL cannot slip into a page-only write.
func resolveContainerRef(cli *CLI, ref string) (containerRef, error) {
	r, matched, perr := confluenceurl.Parse(ref)
	if matched && perr != nil {
		return containerRef{}, &output.Error{
			Err:    "invalid_input",
			Detail: perr.Error(),
			Hint:   "Pass a numeric id, a page URL (…/wiki/spaces/ENG/pages/123), or a folder URL (…/wiki/spaces/ENG/folder/123).",
			Input:  ref,
			Code:   output.ExitGeneral,
		}
	}
	if !matched {
		return containerRef{id: ref}, nil
	}
	var cr containerRef
	switch {
	case r.Kind == confluenceurl.KindPage && r.PageID != "":
		cr = containerRef{siteHint: r.BaseURL, id: r.PageID, kind: confluence.ContentPage}
	case r.Kind == confluenceurl.KindFolder && r.FolderID != "":
		cr = containerRef{siteHint: r.BaseURL, id: r.FolderID, kind: confluence.ContentFolder}
	default:
		return containerRef{}, &output.Error{
			Err:    "invalid_input",
			Detail: "URL does not identify a page or folder",
			Hint:   "Pass a numeric id, a page URL (…/wiki/spaces/ENG/pages/123), or a folder URL (…/wiki/spaces/ENG/folder/123).",
			Input:  ref,
			Code:   output.ExitGeneral,
		}
	}
	if cli.Site != "" {
		if err := requireSiteMatch(cli.Site, ref); err != nil {
			return containerRef{}, err
		}
	}
	return cr, nil
}

// resolveFolderRef derives the site hint and folder id from a bare folder id or
// a folder URL. A page URL is rejected.
func resolveFolderRef(cli *CLI, ref string) (siteHint, folderID string, err error) {
	r, matched, perr := confluenceurl.Parse(ref)
	if matched && perr != nil {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: perr.Error(),
			Hint:   "Pass a numeric folder id or a folder URL (…/wiki/spaces/ENG/folder/123).",
			Input:  ref,
			Code:   output.ExitGeneral,
		}
	}
	if !matched {
		return "", ref, nil
	}
	if r.Kind != confluenceurl.KindFolder || r.FolderID == "" {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: "URL does not identify a folder",
			Hint:   "Pass a numeric folder id or a folder URL (…/wiki/spaces/ENG/folder/123). For a page, use the page commands ('confluence page get', 'confluence page move').",
			Input:  ref,
			Code:   output.ExitGeneral,
		}
	}
	if cli.Site != "" {
		if err := requireSiteMatch(cli.Site, ref); err != nil {
			return "", "", err
		}
	}
	return r.BaseURL, r.FolderID, nil
}

// resolveFolderRefs resolves a list of folder refs that must share one site.
func resolveFolderRefs(cli *CLI, refs []string) (siteHint string, ids []string, err error) {
	for _, ref := range refs {
		site, id, rerr := resolveFolderRef(cli, ref)
		if rerr != nil {
			return "", nil, rerr
		}
		if site != "" {
			if siteHint != "" && siteHint != site {
				return "", nil, &output.Error{
					Err:    "invalid_input",
					Detail: "all folders must be on one site",
					Hint:   "Split into one invocation per site.",
					Code:   output.ExitGeneral,
				}
			}
			siteHint = site
		}
		ids = append(ids, id)
	}
	return siteHint, ids, nil
}

// folderRow is the output shape shared by folder get and folder create.
func folderRow(f *confluence.Folder) map[string]any {
	row := map[string]any{
		"id":          f.ID,
		"type":        "folder",
		"title":       f.Title,
		"space_id":    f.SpaceID,
		"status":      f.Status,
		"version":     f.Version,
		"author_id":   f.AuthorID,
		"created_at":  f.CreatedAt,
		"modified_at": f.ModifiedAt,
		"web_url":     f.WebURL,
	}
	if f.ParentID != "" {
		row["parent_id"] = f.ParentID
	}
	if f.ParentType != "" {
		row["parent_type"] = f.ParentType
	}
	return row
}

type FolderGetCmd struct {
	Refs []string `arg:"" name:"id-or-url" help:"Folder IDs or URLs (all on one site)."`
}

func (c *FolderGetCmd) Run(cli *CLI) error {
	siteHint, ids, err := resolveFolderRefs(cli, c.Refs)
	if err != nil {
		return err
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	errCount := 0
	for i, id := range ids {
		f, err := client.GetFolder(ctx, id)
		if err != nil {
			oErr := cli.ClassifyError(err)
			oErr.Input = c.Refs[i]
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}
		row := folderRow(f)
		row["input"] = c.Refs[i]
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

type FolderCreateCmd struct {
	Space  string `required:"" help:"Space key or URL."`
	Title  string `required:"" help:"Folder title."`
	Parent string `help:"Parent page or folder ID or URL (omit for the space root)."`
}

func (c *FolderCreateCmd) Run(cli *CLI) error {
	siteHint, spaceKey, err := (&PageListCmd{Space: c.Space}).resolveSpace(cli)
	if err != nil {
		return err
	}

	parentID := ""
	if c.Parent != "" {
		pr, perr := resolveContainerRef(cli, c.Parent)
		if perr != nil {
			return perr
		}
		if pr.siteHint != "" && siteHint != "" && pr.siteHint != siteHint {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "parent is on a different site than --space",
				Hint:   "Pass a parent on the same site as the space.",
				Input:  c.Parent,
				Code:   output.ExitGeneral,
			}
		}
		if siteHint == "" {
			siteHint = pr.siteHint
		}
		parentID = pr.id
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	space, err := client.GetSpace(ctx, spaceKey)
	if err != nil {
		return cli.ClassifyError(err)
	}

	f, err := client.CreateFolder(ctx, confluence.CreateFolderParams{
		SpaceID:  space.ID,
		Title:    c.Title,
		ParentID: parentID,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(folderRow(f)); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

type FolderMoveCmd struct {
	Ref    string `arg:"" name:"id-or-url" help:"Source folder ID or URL."`
	Parent string `required:"" help:"Destination parent page or folder ID or URL. For the space root, pass the space homepage."`
}

func (c *FolderMoveCmd) Run(cli *CLI) error {
	srcSite, srcID, err := resolveFolderRef(cli, c.Ref)
	if err != nil {
		return err
	}
	dst, err := resolveContainerRef(cli, c.Parent)
	if err != nil {
		return err
	}
	siteHint, err := moveSite(srcSite, dst.siteHint, c.Ref)
	if err != nil {
		return err
	}
	dstID := dst.id

	// Self-parenting is invalid regardless of any remote state.
	if dstID == srcID {
		return &output.Error{
			Err:    "invalid_move",
			Detail: "a folder cannot be its own parent",
			Hint:   "Pass a different destination parent.",
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	// fail attributes an API failure to the ref it concerns.
	fail := func(err error, input string) error {
		oErr := cli.ClassifyError(err)
		oErr.Input = input
		return oErr
	}

	src, err := client.GetFolder(ctx, srcID)
	if err != nil {
		return fail(err, c.Ref)
	}
	dest, err := resolveMoveDestination(ctx, client, dst)
	if err != nil {
		return fail(err, c.Parent)
	}

	// A folder's version never changes on a move (verified live), so there is
	// no --if-version and both version fields carry the preflight value.
	row := map[string]any{
		"id":                 src.ID,
		"title":              src.Title,
		"previous_space_id":  src.SpaceID,
		"previous_parent_id": src.ParentID,
		"parent_id":          dstID,
		"parent_type":        string(dest.kind),
		"previous_version":   src.Version,
		"version":            src.Version,
		"web_url":            src.WebURL,
	}
	p := cli.NewPrinter()

	// Already under the destination parent: idempotent success, no write.
	if src.ParentID == dstID {
		row["space_id"] = src.SpaceID
		row["moved"] = false
		if err := p.PrintItem(row); err != nil {
			return err
		}
		return p.PrintMeta(output.Meta{})
	}

	cycle, err := destinationUnder(ctx, client, dest.kind, dstID, srcID)
	if err != nil {
		return fail(err, c.Parent)
	}
	if cycle {
		return &output.Error{
			Err:    "invalid_move",
			Detail: "destination parent is a descendant of the source folder (move would create a cycle)",
			Hint:   "Pick a destination that is not under the folder being moved.",
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}

	// The v1 move endpoint is the only API that moves a folder. The row's
	// parent and space come from the resolved destination, not a read-back.
	if err := client.MoveContent(ctx, confluence.ContentFolder, srcID, confluence.MoveAppend, dstID); err != nil {
		if errors.Is(err, confluence.ErrMoveTargetNotFound) {
			return fail(err, c.Parent)
		}
		return fail(err, c.Ref)
	}
	row["space_id"] = dest.spaceID
	row["moved"] = true
	if err := p.PrintItem(row); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

type FolderDeleteCmd struct {
	Refs   []string `arg:"" name:"id-or-url" help:"Folder IDs or URLs (all on one site)."`
	Yes    bool     `help:"Confirm deletion (required unless --dry-run)."`
	DryRun bool     `name:"dry-run" help:"Preview without deleting."`
}

func (c *FolderDeleteCmd) Run(cli *CLI) error {
	siteHint, ids, err := resolveFolderRefs(cli, c.Refs)
	if err != nil {
		return err
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	// Refuse a real delete without confirmation, before any mutation.
	if !c.DryRun && !c.Yes {
		return &output.Error{
			Err:    "confirmation_required",
			Detail: "refusing to delete without --yes",
			Hint:   "Re-run with --yes to delete, or --dry-run to preview.",
			Code:   output.ExitGeneral,
		}
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	errCount := 0
	for i, id := range ids {
		input := c.Refs[i]
		if c.DryRun {
			f, err := client.GetFolder(ctx, id)
			if err != nil {
				oErr := cli.ClassifyError(err)
				oErr.Input = input
				if err := p.PrintItem(oErr.AsItem()); err != nil {
					return err
				}
				errCount++
				continue
			}
			row := map[string]any{
				"input":        input,
				"id":           id,
				"title":        f.Title,
				"version":      f.Version,
				"would_delete": true,
				"delete_mode":  "trash",
			}
			if err := p.PrintItem(row); err != nil {
				return err
			}
			continue
		}

		if err := client.DeleteFolder(ctx, id); err != nil {
			oErr := cli.ClassifyError(err)
			oErr.Input = input
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}
		row := map[string]any{
			"input":       input,
			"id":          id,
			"deleted":     true,
			"delete_mode": "trash",
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
