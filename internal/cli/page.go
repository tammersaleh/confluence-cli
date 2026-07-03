package cli

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/confluenceurl"
	"github.com/tammersaleh/confluence-sync/internal/converter"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type PageCmd struct {
	List      PageListCmd      `cmd:"" help:"List pages in a space."`
	Get       PageGetCmd       `cmd:"" help:"Get one or more pages."`
	Children  PageChildrenCmd  `cmd:"" help:"List a page's direct children."`
	Ancestors PageAncestorsCmd `cmd:"" help:"List a page's ancestors (root-most first)."`
	Tree      PageTreeCmd      `cmd:"" help:"Print a space's page hierarchy (ordered by ID, not display order)."`
}

// resolvePageRef derives the site hint and page id from a single ref, which is
// either a bare page id or a Confluence page URL. A non-page URL is invalid.
func resolvePageRef(cli *CLI, ref string) (siteHint, pageID string, err error) {
	r, matched, perr := confluenceurl.Parse(ref)
	if matched && perr != nil {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: perr.Error(),
			Hint:   "Pass a numeric page id or a page URL (…/wiki/spaces/ENG/pages/123).",
			Input:  ref,
			Code:   output.ExitGeneral,
		}
	}
	if !matched {
		// Bare id: no site info.
		return "", ref, nil
	}
	if r.Kind != confluenceurl.KindPage || r.PageID == "" {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: "URL does not identify a page",
			Hint:   "Pass a numeric page id or a page URL (…/wiki/spaces/ENG/pages/123).",
			Input:  ref,
			Code:   output.ExitGeneral,
		}
	}
	if cli.Site != "" {
		if err := requireSiteMatch(cli.Site, ref); err != nil {
			return "", "", err
		}
	}
	return r.BaseURL, r.PageID, nil
}

type PageListCmd struct {
	Space  string `required:"" help:"Space key or URL."`
	Limit  int    `default:"25" help:"Page size requested from the API."`
	Cursor string `help:"Opaque pagination cursor from a prior _meta.next_cursor."`
	All    bool   `help:"Fetch every page (loop until the cursor is exhausted)."`
}

func (c *PageListCmd) Run(cli *CLI) error {
	siteHint, spaceKey, err := c.resolveSpace(cli)
	if err != nil {
		return err
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

	p := cli.NewPrinter()
	cursor := c.Cursor
	var next string
	for {
		pages, n, err := client.ListPages(ctx, space.ID, cursor, c.Limit)
		if err != nil {
			return cli.ClassifyError(err)
		}
		next = n

		for _, pg := range pages {
			row := map[string]any{
				"id":        pg.ID,
				"title":     pg.Title,
				"type":      pg.Type,
				"space_key": space.Key,
			}
			if pg.ParentID != "" {
				row["parent_id"] = pg.ParentID
			}
			if pg.ParentType != "" {
				row["parent_type"] = pg.ParentType
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

// resolveSpace derives the site hint and space key from --space, which is either
// a bare space key or a Confluence space/page URL.
func (c *PageListCmd) resolveSpace(cli *CLI) (siteHint, spaceKey string, err error) {
	ref, matched, perr := confluenceurl.Parse(c.Space)
	if matched && perr != nil {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: perr.Error(),
			Hint:   "Pass a space key (e.g. ENG) or a space URL (…/wiki/spaces/ENG).",
			Input:  c.Space,
			Code:   output.ExitGeneral,
		}
	}

	if !matched {
		// Bare key: site comes from --site or the single stored default.
		return "", c.Space, nil
	}

	// A URL. Only space and page URLs carry a space key.
	if ref.Kind != confluenceurl.KindSpace && ref.Kind != confluenceurl.KindPage || ref.SpaceKey == "" {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: "URL does not identify a space",
			Hint:   "Pass a space key (e.g. ENG) or a space URL (…/wiki/spaces/ENG).",
			Input:  c.Space,
			Code:   output.ExitGeneral,
		}
	}

	if cli.Site != "" {
		if err := requireSiteMatch(cli.Site, c.Space); err != nil {
			return "", "", err
		}
	}

	return ref.BaseURL, ref.SpaceKey, nil
}

type PageChildrenCmd struct {
	Ref    string `arg:"" name:"id-or-url" help:"Page ID or URL."`
	Limit  int    `default:"25" help:"Page size requested from the API."`
	Cursor string `help:"Opaque pagination cursor from a prior _meta.next_cursor."`
	All    bool   `help:"Fetch every page (loop until the cursor is exhausted)."`
}

func (c *PageChildrenCmd) Run(cli *CLI) error {
	siteHint, pageID, err := resolvePageRef(cli, c.Ref)
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
	cursor := c.Cursor
	var next string
	for {
		children, n, err := client.ListChildren(ctx, pageID, cursor, c.Limit)
		if err != nil {
			return cli.ClassifyError(err)
		}
		next = n

		for _, pg := range children {
			row := map[string]any{
				"id":    pg.ID,
				"title": pg.Title,
				"type":  pg.Type,
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

type PageAncestorsCmd struct {
	Ref string `arg:"" name:"id-or-url" help:"Page ID or URL."`
}

func (c *PageAncestorsCmd) Run(cli *CLI) error {
	siteHint, pageID, err := resolvePageRef(cli, c.Ref)
	if err != nil {
		return err
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	ancestors, err := client.GetAncestors(ctx, pageID)
	if err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	// The ancestors endpoint returns limited fields (id, type); title is often
	// absent, so it's omitted here. Order is as returned (root-most first).
	for _, a := range ancestors {
		row := map[string]any{
			"id":   a.ID,
			"type": a.Type,
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
	}
	return p.PrintMeta(output.Meta{})
}

type PageTreeCmd struct {
	Space string `required:"" help:"Space key or URL."`
}

func (c *PageTreeCmd) Run(cli *CLI) error {
	siteHint, spaceKey, err := (&PageListCmd{Space: c.Space}).resolveSpace(cli)
	if err != nil {
		return err
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

	pages, err := client.GetPages(ctx, space.ID)
	if err != nil {
		return cli.ClassifyError(err)
	}

	// Build the tree in the command (no dependency on internal/sync). Ordering
	// is by ID (deterministic), not Confluence display order, which the v2 API
	// doesn't expose in the crawl.
	idSet := make(map[string]bool, len(pages))
	childrenByParent := make(map[string][]confluence.Page, len(pages))
	for _, pg := range pages {
		idSet[pg.ID] = true
	}
	var roots []confluence.Page
	for _, pg := range pages {
		if pg.ParentID == "" || !idSet[pg.ParentID] {
			roots = append(roots, pg)
			continue
		}
		childrenByParent[pg.ParentID] = append(childrenByParent[pg.ParentID], pg)
	}
	sortByID := func(ps []confluence.Page) {
		sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	}
	sortByID(roots)
	for k := range childrenByParent {
		sortByID(childrenByParent[k])
	}

	p := cli.NewPrinter()

	var walk func(pg confluence.Page, depth int) error
	walk = func(pg confluence.Page, depth int) error {
		row := map[string]any{
			"id":    pg.ID,
			"title": pg.Title,
			"type":  pg.Type,
			"depth": depth,
		}
		if pg.ParentID != "" {
			row["parent_id"] = pg.ParentID
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
		for _, child := range childrenByParent[pg.ID] {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	for _, root := range roots {
		if err := walk(root, 0); err != nil {
			return err
		}
	}
	return p.PrintMeta(output.Meta{})
}

type PageGetCmd struct {
	Refs       []string `arg:"" name:"id-or-url" help:"Page IDs or URLs (all on one site)."`
	BodyFormat string   `default:"storage" help:"storage | atlas_doc_format (alias: adf) | view | markdown (alias: md, derived from storage)."`
}

func (c *PageGetCmd) Run(cli *CLI) error {
	format, derivedMarkdown, err := normalizeBodyFormat(c.BodyFormat)
	if err != nil {
		return err
	}

	// pair binds each caller input to the page id resolved from it.
	type pair struct {
		input  string
		pageID string
	}
	var pairs []pair
	var urlRefs []confluenceurl.Ref
	var siteURLArg string // a raw URL arg, used for --site match reporting

	for _, arg := range c.Refs {
		r, matched, perr := confluenceurl.Parse(arg)
		if matched && perr != nil {
			return &output.Error{
				Err:    "invalid_input",
				Detail: perr.Error(),
				Hint:   "Pass a numeric page id or a page URL (…/wiki/spaces/ENG/pages/123).",
				Input:  arg,
				Code:   output.ExitGeneral,
			}
		}
		if !matched {
			// Bare id: no site info.
			pairs = append(pairs, pair{input: arg, pageID: arg})
			continue
		}
		if r.Kind != confluenceurl.KindPage || r.PageID == "" {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "URL does not identify a page",
				Hint:   "Pass a numeric page id or a page URL (…/wiki/spaces/ENG/pages/123).",
				Input:  arg,
				Code:   output.ExitGeneral,
			}
		}
		urlRefs = append(urlRefs, r)
		siteURLArg = arg
		pairs = append(pairs, pair{input: arg, pageID: r.PageID})
	}

	siteHint := ""
	if len(urlRefs) > 0 {
		site, cerr := confluenceurl.CommonSite(urlRefs)
		if cerr != nil {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "all pages must be on one site",
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

	client, rc, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	errCount := 0
	for _, pr := range pairs {
		pd, err := client.GetPage(ctx, pr.pageID, format)
		if err != nil {
			oErr := cli.ClassifyError(err)
			oErr.Input = pr.input
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}

		row := map[string]any{
			"input":       pr.input,
			"id":          pd.ID,
			"title":       pd.Title,
			"space_id":    pd.SpaceID,
			"version":     pd.Version,
			"author_id":   pd.AuthorID,
			"created_at":  pd.CreatedAt,
			"modified_at": pd.ModifiedAt,
			"web_url":     pd.WebURL,
			"body_format": string(pd.BodyFormat),
		}
		switch {
		case derivedMarkdown:
			// markdown is derived from the storage body. We need the page's
			// attachments to resolve image/link targets to absolute remote URLs.
			// This costs one extra API call per page. A failure to list
			// attachments is treated as a per-item error (simplest robust path):
			// we can't render faithful links without it.
			atts, aerr := client.GetAttachments(ctx, pr.pageID)
			if aerr != nil {
				oErr := cli.ClassifyError(aerr)
				oErr.Input = pr.input
				if err := p.PrintItem(oErr.AsItem()); err != nil {
					return err
				}
				errCount++
				continue
			}
			md := converter.ConvertWithOptions(pd.Body, converter.Options{
				AttachmentURL: attachmentResolver(rc.Site, atts),
			})
			row["body"] = md
			row["body_format"] = "markdown"
			row["source_body_format"] = string(confluence.BodyFormatStorage)
		case format == confluence.BodyFormatAtlasDoc:
			// ADF is a JSON document; emit it as a nested object, not a string.
			row["body"] = json.RawMessage(pd.Body)
		default:
			row["body"] = pd.Body
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

// normalizeBodyFormat lower-cases the flag, maps the "adf"/"md" aliases, and
// rejects anything unsupported. markdown is not a Confluence API format; it is a
// derived representation the CLI produces by converting the storage body. In
// that case the returned api format is storage and derivedMarkdown is true.
func normalizeBodyFormat(v string) (api confluence.APIBodyFormat, derivedMarkdown bool, err error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "storage":
		return confluence.BodyFormatStorage, false, nil
	case "atlas_doc_format", "adf":
		return confluence.BodyFormatAtlasDoc, false, nil
	case "view":
		return confluence.BodyFormatView, false, nil
	case "markdown", "md":
		return confluence.BodyFormatStorage, true, nil
	default:
		return "", false, &output.Error{
			Err:    "invalid_input",
			Detail: "unknown --body-format",
			Hint:   "Use storage, atlas_doc_format (adf), view, or markdown (md).",
			Code:   output.ExitGeneral,
		}
	}
}

// absolutizeAttachmentURL turns a Confluence attachment download link into an
// absolute URL the same way WebURL is absolutized: pass through if already
// absolute, otherwise ensure a leading /wiki and prefix the site base URL.
func absolutizeAttachmentURL(baseURL, downloadURL string) string {
	if downloadURL == "" {
		return ""
	}
	if strings.HasPrefix(downloadURL, "http") {
		return downloadURL
	}
	if !strings.HasPrefix(downloadURL, "/wiki") {
		downloadURL = "/wiki" + downloadURL
	}
	return baseURL + downloadURL
}

// attachmentResolver builds a converter.AttachmentURL hook mapping an attachment
// filename to its absolute remote download URL.
func attachmentResolver(baseURL string, atts []confluence.Attachment) func(string) (string, bool) {
	m := make(map[string]string, len(atts))
	for _, a := range atts {
		m[a.Title] = absolutizeAttachmentURL(baseURL, a.DownloadURL)
	}
	return func(name string) (string, bool) {
		u, ok := m[name]
		return u, ok
	}
}
