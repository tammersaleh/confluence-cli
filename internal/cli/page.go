package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/tammersaleh/confluence-cli/internal/bodywrite"
	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/confluenceurl"
	"github.com/tammersaleh/confluence-cli/internal/converter"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

type PageCmd struct {
	List          PageListCmd          `cmd:"" help:"List pages in a space."`
	Get           PageGetCmd           `cmd:"" help:"Get one or more pages."`
	Children      PageChildrenCmd      `cmd:"" help:"List a page's direct children."`
	Descendants   PageDescendantsCmd   `cmd:"" help:"List all descendants of a page (every level)."`
	Ancestors     PageAncestorsCmd     `cmd:"" help:"List a page's ancestors (root-most first)."`
	Tree          PageTreeCmd          `cmd:"" help:"Print a space's page hierarchy (ordered by ID, not display order)."`
	Create        PageCreateCmd        `cmd:"" help:"Create a page."`
	Update        PageUpdateCmd        `cmd:"" help:"Update a page (optimistic concurrency)."`
	Move          PageMoveCmd          `cmd:"" help:"Reparent a page within its space (optimistic concurrency)."`
	Delete        PageDeleteCmd        `cmd:"" help:"Delete pages (moves to trash)."`
	ConvertToLive PageConvertToLiveCmd `cmd:"" name:"convert-to-live" help:"Convert pages to live docs (undocumented endpoint; no supported undo)."`
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

type PageDescendantsCmd struct {
	Ref    string `arg:"" name:"id-or-url" help:"Page ID or URL."`
	Limit  int    `default:"25" help:"Page size requested from the API."`
	Cursor string `help:"Opaque pagination cursor from a prior _meta.next_cursor."`
	All    bool   `help:"Fetch every page (loop until the cursor is exhausted)."`
}

func (c *PageDescendantsCmd) Run(cli *CLI) error {
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
		descs, n, err := client.GetDescendants(ctx, pageID, cursor, c.Limit)
		if err != nil {
			return cli.ClassifyError(err)
		}
		next = n

		for _, d := range descs {
			row := map[string]any{
				"id":    d.ID,
				"title": d.Title,
				"type":  d.Type,
				"depth": d.Depth,
			}
			if d.ParentID != "" {
				row["parent_id"] = d.ParentID
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
	Refs           []string `arg:"" name:"id-or-url" help:"Page IDs or URLs (all on one site)."`
	BodyFormat     string   `default:"storage" help:"storage | atlas_doc_format (alias: adf) | view | markdown (alias: md, derived from storage)."`
	ResolveAuthors bool     `name:"resolve-authors" help:"Resolve author_id to an author_name sibling (best-effort, one cached user lookup per unique author)."`
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

	var authors *authorResolver
	if c.ResolveAuthors {
		authors = newAuthorResolver(client)
	}

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
		if pd.Subtype != "" {
			row["subtype"] = pd.Subtype
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
		authors.enrich(ctx, row, pd.AuthorID)
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

// resolveWriteBody reads a piped body from stdin and prepares it for a write.
// A body is only "present" when non-whitespace content was piped: an absent
// (interactive TTY) stdin and an empty/whitespace-only pipe both yield a nil
// body, so an empty page needs no --body-format. bodyFormat is required whenever
// real content is present.
func resolveWriteBody(cli *CLI, bodyFormat string) (*confluence.WriteBody, error) {
	present, raw, err := bodywrite.Read(cli.Stdin())
	if err != nil {
		return nil, &output.Error{
			Err:    "general",
			Detail: err.Error(),
			Code:   output.ExitGeneral,
		}
	}
	if !present || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if bodyFormat == "" {
		return nil, &output.Error{
			Err:    "invalid_input",
			Detail: "a body was piped but --body-format is not set",
			Hint:   "Pass --body-format storage, adf, or markdown.",
			Code:   output.ExitGeneral,
		}
	}
	repr, val, err := bodywrite.Prepare(bodyFormat, raw)
	if err != nil {
		return nil, &output.Error{
			Err:    "invalid_body",
			Detail: err.Error(),
			Hint:   "Body must be valid storage XHTML, an ADF doc, or Markdown. See the skill.",
			Code:   output.ExitGeneral,
		}
	}
	return &confluence.WriteBody{Representation: confluence.APIBodyFormat(repr), Value: val}, nil
}

type PageCreateCmd struct {
	Space      string `required:"" help:"Space key or URL."`
	Title      string `required:"" help:"Page title."`
	Parent     string `help:"Parent page ID or URL."`
	BodyFormat string `help:"storage, adf, or markdown (required when piping a body)."`
	Live       bool   `help:"Create a live doc (subtype=live) instead of a regular page."`
}

func (c *PageCreateCmd) Run(cli *CLI) error {
	siteHint, spaceKey, err := (&PageListCmd{Space: c.Space}).resolveSpace(cli)
	if err != nil {
		return err
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	// Resolve --parent to a bare page id. A parent URL must live on the same
	// site as the space.
	parentID := ""
	if c.Parent != "" {
		pSite, pID, perr := resolvePageRef(cli, c.Parent)
		if perr != nil {
			return perr
		}
		if pSite != "" && siteHint != "" && pSite != siteHint {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "parent is on a different site than --space",
				Hint:   "Pass a parent page on the same site as the space.",
				Input:  c.Parent,
				Code:   output.ExitGeneral,
			}
		}
		parentID = pID
	}

	body, err := resolveWriteBody(cli, c.BodyFormat)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	space, err := client.GetSpace(ctx, spaceKey)
	if err != nil {
		return cli.ClassifyError(err)
	}

	subtype := ""
	if c.Live {
		subtype = confluence.PageSubtypeLive
	}

	rec, err := client.CreatePage(ctx, confluence.CreatePageParams{
		SpaceID:  space.ID,
		Title:    c.Title,
		ParentID: parentID,
		Body:     body,
		Subtype:  subtype,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	row := map[string]any{
		"id":         rec.ID,
		"title":      rec.Title,
		"space_id":   rec.SpaceID,
		"version":    rec.Version,
		"author_id":  rec.AuthorID,
		"created_at": rec.CreatedAt,
		"web_url":    rec.WebURL,
	}
	if rec.ParentID != "" {
		row["parent_id"] = rec.ParentID
	}
	// Subtype comes from the server response, not the --live flag: only report
	// "live" when Confluence confirmed it.
	if rec.Subtype != "" {
		row["subtype"] = rec.Subtype
	}
	if err := p.PrintItem(row); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

type PageUpdateCmd struct {
	Ref                           string `arg:"" name:"id-or-url" help:"Page ID or URL."`
	IfVersion                     int    `name:"if-version" required:"" help:"Expected current version; the update is rejected if it differs."`
	Title                         string `help:"New title (keeps current if omitted)."`
	BodyFormat                    string `help:"storage, adf, or markdown (required when piping a body)."`
	AllowUnresolvedInlineComments bool   `name:"allow-unresolved-inline-comments" help:"Proceed even if the page has unresolved inline comments whose anchors this write may destroy. Skips the inline-comment inspection entirely; does not bypass --if-version or other checks."`
}

func (c *PageUpdateCmd) Run(cli *CLI) error {
	siteHint, pageID, err := resolvePageRef(cli, c.Ref)
	if err != nil {
		return err
	}

	body, err := resolveWriteBody(cli, c.BodyFormat)
	if err != nil {
		return err
	}

	// Reject a no-op invocation up front (before auth/network): with neither a
	// new title nor a body there is nothing to change, and a blind re-send would
	// bump the version for no reason.
	if body == nil && c.Title == "" {
		return &output.Error{
			Err:    "invalid_input",
			Detail: "nothing to update: pass --title and/or pipe a body",
			Hint:   "Set --title, or pipe a body with --body-format storage|adf|markdown.",
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

	// Preflight: fetch as ADF so a no-body update preserves the body as ADF (not
	// a lossy storage round-trip), and so we preserve title/status/version.
	cur, err := client.GetPage(ctx, pageID, confluence.BodyFormatAtlasDoc)
	if err != nil {
		return cli.ClassifyError(err)
	}
	if cur.Version != c.IfVersion {
		return &output.Error{
			Err:    "version_conflict",
			Detail: fmt.Sprintf("expected current version %d, got %d", c.IfVersion, cur.Version),
			Hint:   fmt.Sprintf("Fetch the latest with 'confluence page get %s' and retry with --if-version %d.", pageID, cur.Version),
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}

	newTitle := c.Title
	if newTitle == "" {
		newTitle = cur.Title
	}

	p := cli.NewPrinter()

	// A title-only update whose title matches the current title is a provable
	// no-op: emit success without a PUT so the version does not move.
	if body == nil && newTitle == cur.Title {
		row := map[string]any{
			"id":               cur.ID,
			"title":            cur.Title,
			"version":          cur.Version,
			"previous_version": c.IfVersion,
			"updated":          false,
			"web_url":          cur.WebURL,
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
		return p.PrintMeta(output.Meta{})
	}

	// Choose the body to send: the caller's when piped, else the current ADF
	// preserved unchanged. Fail closed if the ADF body is missing (never legal
	// for a real page) rather than clobbering the page with an empty body.
	wb := confluence.WriteBody{Representation: confluence.BodyFormatAtlasDoc, Value: cur.Body}
	if body != nil {
		wb = *body
	} else if strings.TrimSpace(cur.Body) == "" {
		return &output.Error{
			Err:    "api_error",
			Detail: "page returned no atlas_doc_format body to preserve",
			Hint:   "Retry; if it persists the page body could not be fetched as ADF for preservation.",
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}

	// Inline-comment guard: only a caller-supplied replacement body can destroy
	// anchors. A no-body update re-sends the exact current ADF, which a live
	// scratch-page test confirmed preserves inline-comment marks, so it is not
	// guarded. The guard is the last remote precondition before the PUT, to
	// shrink the window in which a comment is added after inspection. Skipped
	// with the explicit override (which skips the request entirely, so an
	// inspection outage does not block an overridden write).
	if body != nil && !c.AllowUnresolvedInlineComments {
		if err := guardInlineComments(ctx, cli, client, pageID, cur.WebURL, c.Ref); err != nil {
			return err
		}
	}

	rec, err := client.UpdatePage(ctx, confluence.UpdatePageParams{
		ID:      pageID,
		Title:   newTitle,
		Status:  cur.Status,
		Version: c.IfVersion + 1,
		Body:    wb,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	row := map[string]any{
		"id":               rec.ID,
		"title":            rec.Title,
		"version":          rec.Version,
		"previous_version": c.IfVersion,
		"updated":          true,
		"web_url":          rec.WebURL,
	}
	if err := p.PrintItem(row); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// guardInlineComments refuses a body-replacing write when the page has any
// inline comment that is not exactly "resolved" (open, reopened, dangling, or
// any unknown/future status all block). It fails closed: a failure to inspect
// the comments refuses the write. The caller skips this only via the explicit
// override.
func guardInlineComments(ctx context.Context, cli *CLI, client confluence.Client, pageID, pageURL, input string) error {
	comments, err := drainInlineComments(ctx, client, pageID)
	if err != nil {
		return cli.ClassifyError(err)
	}
	blockers := unresolvedBlockers(comments, pageURL)
	if len(blockers) == 0 {
		return nil
	}
	return &output.Error{
		Err:    "unresolved_inline_comments",
		Detail: fmt.Sprintf("refusing to replace the page body: %d inline comment(s) are not resolved and this write may destroy their anchors", len(blockers)),
		Hint:   "Resolve or re-anchor the comments in Confluence (see the web URLs), then retry. Pass --allow-unresolved-inline-comments to override.",
		Input:  input,
		Data: map[string]any{
			"page_id":                pageID,
			"blocking_comment_count": len(blockers),
			"blocking_comments":      blockers,
		},
		Code: output.ExitGeneral,
	}
}

// drainInlineComments fully drains a page's top-level inline comments
// (unfiltered), detecting a repeated cursor so a malformed API response cannot
// loop forever. The larger page size shrinks the time-of-check/time-of-use
// window before the write.
func drainInlineComments(ctx context.Context, client confluence.Client, pageID string) ([]confluence.Comment, error) {
	var all []confluence.Comment
	seen := make(map[string]bool)
	cursor := ""
	for {
		batch, next, err := client.GetInlineComments(ctx, pageID, cursor, 250, "")
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if next == "" {
			return all, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("%w: repeated inline-comment pagination cursor", confluence.ErrAPIError)
		}
		seen[next] = true
		cursor = next
	}
}

// unresolvedBlockers returns the inline comments that are not exactly
// "resolved", deduplicated by id and sorted by id for deterministic output.
func unresolvedBlockers(comments []confluence.Comment, pageURL string) []map[string]any {
	byID := make(map[string]confluence.Comment)
	for _, cm := range comments {
		if cm.ResolutionStatus == "resolved" {
			continue
		}
		byID[cm.ID] = cm
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		cm := byID[id]
		out = append(out, map[string]any{
			"id":                 cm.ID,
			"resolution_status":  cm.ResolutionStatus,
			"original_selection": cm.OriginalSelection,
			"inline_marker_ref":  cm.InlineMarkerRef,
			"web_url":            commentFocusURL(cm, pageURL),
		})
	}
	return out
}

// commentFocusURL returns the comment's own web URL, or a best-effort fallback
// that focuses the comment on the page URL. Returns "" when neither is known.
func commentFocusURL(cm confluence.Comment, pageURL string) string {
	if cm.WebURL != "" {
		return cm.WebURL
	}
	if pageURL == "" {
		return ""
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("focusedCommentId", cm.ID)
	u.RawQuery = q.Encode()
	return u.String()
}

type PageMoveCmd struct {
	Ref       string `arg:"" name:"id-or-url" help:"Source page ID or URL."`
	Parent    string `required:"" help:"Destination parent page ID or URL (must be in the same space)."`
	IfVersion int    `name:"if-version" required:"" help:"Expected current version of the source page; the move is rejected if it differs."`
}

func (c *PageMoveCmd) Run(cli *CLI) error {
	if c.IfVersion <= 0 {
		return &output.Error{
			Err:    "invalid_input",
			Detail: "--if-version must be a positive version number",
			Hint:   "Fetch the source page with 'confluence page get <id>' and pass its version.",
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}

	// Resolve both refs symmetrically. resolvePageRef enforces --site matching for
	// URL refs; here we also reject two URLs that point at different sites.
	srcSite, srcID, err := resolvePageRef(cli, c.Ref)
	if err != nil {
		return err
	}
	dstSite, dstID, err := resolvePageRef(cli, c.Parent)
	if err != nil {
		return err
	}
	if srcSite != "" && dstSite != "" && srcSite != dstSite {
		return &output.Error{
			Err:    "invalid_input",
			Detail: "source and destination are on different sites",
			Hint:   "Both refs must be on one site; a cross-site move is not possible.",
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}
	siteHint := srcSite
	if siteHint == "" {
		siteHint = dstSite
	}

	// Self-parenting is invalid regardless of any remote state.
	if dstID == srcID {
		return &output.Error{
			Err:    "invalid_move",
			Detail: "a page cannot be its own parent",
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

	// Fetch the source as ADF so the reparent PUT preserves the body exactly.
	src, err := client.GetPage(ctx, srcID, confluence.BodyFormatAtlasDoc)
	if err != nil {
		return cli.ClassifyError(err)
	}
	if src.Version != c.IfVersion {
		return &output.Error{
			Err:    "version_conflict",
			Detail: fmt.Sprintf("expected current version %d, got %d", c.IfVersion, src.Version),
			Hint:   fmt.Sprintf("Fetch the latest with 'confluence page get %s' and retry with --if-version %d.", srcID, src.Version),
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}
	if strings.TrimSpace(src.Body) == "" {
		return &output.Error{
			Err:    "api_error",
			Detail: "source page returned no atlas_doc_format body to preserve",
			Hint:   "Retry; if it persists the page body could not be fetched as ADF for preservation.",
			Input:  c.Ref,
			Code:   output.ExitGeneral,
		}
	}

	// Fetch the destination parent to compare spaces and confirm it exists.
	dst, err := client.GetPage(ctx, dstID, confluence.BodyFormatStorage)
	if err != nil {
		return cli.ClassifyError(err)
	}
	if src.SpaceID != dst.SpaceID {
		return &output.Error{
			Err:    "cross_space_move_unsupported",
			Detail: fmt.Sprintf("source page is in space %s but destination parent is in space %s; the Confluence v2 API only reparents within one space", src.SpaceID, dst.SpaceID),
			Hint:   "Open the source page in Confluence and use the Move action to move it across spaces.",
			Input:  c.Ref,
			Data: map[string]any{
				"source_page_id":       srcID,
				"source_space_id":      src.SpaceID,
				"source_web_url":       src.WebURL,
				"destination_page_id":  dstID,
				"destination_space_id": dst.SpaceID,
				"destination_web_url":  dst.WebURL,
			},
			Code: output.ExitGeneral,
		}
	}

	p := cli.NewPrinter()

	// Already under the destination parent: idempotent success, no PUT (the
	// version check above still guards against a stale retry).
	if src.ParentID == dstID {
		row := map[string]any{
			"id":               src.ID,
			"title":            src.Title,
			"space_id":         src.SpaceID,
			"parent_id":        dstID,
			"previous_version": c.IfVersion,
			"version":          src.Version,
			"moved":            false,
			"web_url":          src.WebURL,
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
		return p.PrintMeta(output.Meta{})
	}

	// Reject a cycle: the destination must not be a descendant of the source.
	// Fail closed if ancestry cannot be inspected.
	ancestors, err := client.GetAncestors(ctx, dstID)
	if err != nil {
		return cli.ClassifyError(err)
	}
	for _, a := range ancestors {
		if a.ID == srcID {
			return &output.Error{
				Err:    "invalid_move",
				Detail: "destination parent is a descendant of the source page (move would create a cycle)",
				Hint:   "Pick a destination that is not under the page being moved.",
				Input:  c.Ref,
				Code:   output.ExitGeneral,
			}
		}
	}

	// No inline-comment guard: a move re-sends the source's exact ADF and changes
	// only parentId. A live scratch-page test confirmed this preserves inline
	// comment anchors (marker ref and resolution status), so a move cannot
	// destroy them.
	rec, err := client.UpdatePage(ctx, confluence.UpdatePageParams{
		ID:       srcID,
		Title:    src.Title,
		Status:   src.Status,
		Version:  c.IfVersion + 1,
		Body:     confluence.WriteBody{Representation: confluence.BodyFormatAtlasDoc, Value: src.Body},
		ParentID: &dstID,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	row := map[string]any{
		"id":                 rec.ID,
		"title":              rec.Title,
		"space_id":           src.SpaceID,
		"previous_parent_id": src.ParentID,
		"parent_id":          dstID,
		"previous_version":   c.IfVersion,
		"version":            rec.Version,
		"moved":              true,
		"web_url":            rec.WebURL,
	}
	if err := p.PrintItem(row); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

type PageDeleteCmd struct {
	Refs   []string `arg:"" name:"id-or-url" help:"Page IDs or URLs (all on one site)."`
	Yes    bool     `help:"Confirm deletion (required unless --dry-run)."`
	DryRun bool     `name:"dry-run" help:"Preview without deleting."`
}

func (c *PageDeleteCmd) Run(cli *CLI) error {
	// pair binds each caller input to the page id resolved from it.
	type pair struct {
		input  string
		pageID string
	}
	var pairs []pair
	var urlRefs []confluenceurl.Ref
	var siteURLArg string

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
	for _, pr := range pairs {
		if c.DryRun {
			cur, err := client.GetPage(ctx, pr.pageID, confluence.BodyFormatStorage)
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
				"input":        pr.input,
				"id":           pr.pageID,
				"title":        cur.Title,
				"version":      cur.Version,
				"would_delete": true,
				"delete_mode":  "trash",
			}
			if err := p.PrintItem(row); err != nil {
				return err
			}
			continue
		}

		if err := client.DeletePage(ctx, pr.pageID); err != nil {
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
			"id":          pr.pageID,
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

type PageConvertToLiveCmd struct {
	Refs []string `arg:"" name:"id-or-url" help:"Page IDs or URLs (all on one site)."`
}

// convertToLiveWarning is emitted once to stderr (unless --quiet) before any
// mutation, so callers know the command depends on an unsupported endpoint.
const convertToLiveWarning = "warning: convert-to-live uses an undocumented Atlassian endpoint " +
	"and has no supported API undo; it may change or be blocked without notice.\n"

func (c *PageConvertToLiveCmd) Run(cli *CLI) error {
	// pair binds each caller input to the page id resolved from it.
	type pair struct {
		input  string
		pageID string
	}
	var pairs []pair
	var urlRefs []confluenceurl.Ref
	var siteURLArg string

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

	// Resolve one site for the whole batch before any mutation, so a mixed-site
	// batch can never partially convert one tenant before failing.
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

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	// Warn once, after validation and before the first mutation.
	if !cli.Quiet {
		_, _ = io.WriteString(cli.stderr(), convertToLiveWarning)
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	errCount := 0
	for _, pr := range pairs {
		if err := client.ConvertPageToLive(ctx, pr.pageID); err != nil {
			oErr := cli.ClassifyError(err)
			oErr.Input = pr.input
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}
		// converted:true means the mutation returned success. The command does
		// not read the page back, so it does not report subtype here; verify with
		// 'confluence page get <id>' if needed.
		row := map[string]any{
			"input":     pr.input,
			"id":        pr.pageID,
			"converted": true,
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
