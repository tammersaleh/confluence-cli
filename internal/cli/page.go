package cli

import (
	"github.com/tammersaleh/confluence-sync/internal/confluenceurl"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type PageCmd struct {
	List PageListCmd `cmd:"" help:"List pages in a space."`
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
