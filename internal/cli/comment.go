package cli

import (
	"context"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type CommentCmd struct {
	List CommentListCmd `cmd:"" help:"List a page's comments."`
}

// CommentListCmd lists a page's comments. By default it fetches both footer and
// inline comments; --footer or --inline narrow to a single kind. Each kind is
// fully drained (comment counts per page are bounded), so no cursor is emitted.
type CommentListCmd struct {
	Page   string `arg:"" name:"id-or-url" help:"Page ID or URL."`
	Footer bool   `help:"Only footer comments."`
	Inline bool   `help:"Only inline comments."`
}

func (c *CommentListCmd) Run(cli *CLI) error {
	siteHint, pageID, err := resolvePageRef(cli, c.Page)
	if err != nil {
		return err
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	// Default (neither flag) fetches both; each flag opts a kind in.
	wantFooter := c.Footer || (!c.Footer && !c.Inline)
	wantInline := c.Inline || (!c.Footer && !c.Inline)

	p := cli.NewPrinter()

	drain := func(fetch func(ctx context.Context, pageID, cursor string, limit int) ([]confluence.Comment, string, error)) error {
		cursor := ""
		for {
			comments, next, err := fetch(ctx, pageID, cursor, 0)
			if err != nil {
				return cli.ClassifyError(err)
			}
			for _, cm := range comments {
				row := map[string]any{
					"id":         cm.ID,
					"kind":       cm.Kind,
					"body":       cm.Body,
					"author_id":  cm.AuthorID,
					"created_at": cm.CreatedAt,
					"web_url":    cm.WebURL,
				}
				if err := p.PrintItem(row); err != nil {
					return err
				}
			}
			if next == "" {
				return nil
			}
			cursor = next
		}
	}

	if wantFooter {
		if err := drain(client.GetFooterComments); err != nil {
			return err
		}
	}
	if wantInline {
		if err := drain(client.GetInlineComments); err != nil {
			return err
		}
	}

	return p.PrintMeta(output.Meta{})
}
