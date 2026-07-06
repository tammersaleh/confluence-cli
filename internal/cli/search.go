package cli

import (
	"github.com/tammersaleh/confluence-cli/internal/output"
)

// SearchCmd runs a CQL search. The site comes from --site or the single stored
// default (there is no URL arg to derive it from).
type SearchCmd struct {
	CQL    string `arg:"" name:"cql" help:"Confluence Query Language expression."`
	Limit  int    `default:"25" help:"Page size requested from the API."`
	Cursor string `help:"Opaque pagination cursor from a prior _meta.next_cursor."`
	All    bool   `help:"Fetch every page (loop until the cursor is exhausted)."`
}

func (c *SearchCmd) Run(cli *CLI) error {
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
		results, n, err := client.Search(ctx, c.CQL, cursor, c.Limit)
		if err != nil {
			return cli.ClassifyError(err)
		}
		next = n

		for _, r := range results {
			row := map[string]any{
				"id":        r.ID,
				"title":     r.Title,
				"type":      r.Type,
				"space_key": r.SpaceKey,
				"excerpt":   r.Excerpt,
				"url":       r.URL,
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
