package cli

import (
	"context"
	"strings"

	"github.com/tammersaleh/confluence-sync/internal/bodywrite"
	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type CommentCmd struct {
	List CommentListCmd `cmd:"" help:"List a page's comments."`
	Add  CommentAddCmd  `cmd:"" help:"Add a footer comment to a page."`
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

// CommentAddCmd adds a footer comment to a page. The body is read from stdin and
// validated for the requested --body-format. Inline comments are not yet
// supported.
type CommentAddCmd struct {
	Page       string `arg:"" name:"id-or-url" help:"Page ID or URL."`
	BodyFormat string `help:"storage or adf."`
	Inline     bool   `help:"(not yet supported)"`
}

func (c *CommentAddCmd) Run(cli *CLI) error {
	siteHint, pageID, err := resolvePageRef(cli, c.Page)
	if err != nil {
		return err
	}

	if c.Inline {
		return &output.Error{
			Err:    "not_implemented",
			Detail: "inline comments are not yet supported",
			Hint:   "Omit --inline to add a footer comment.",
			Code:   output.ExitGeneral,
		}
	}

	present, raw, err := bodywrite.Read(cli.Stdin())
	if err != nil {
		return &output.Error{
			Err:    "read_error",
			Detail: err.Error(),
			Hint:   "Pipe the comment body on stdin.",
			Code:   output.ExitGeneral,
		}
	}
	if !present || strings.TrimSpace(raw) == "" {
		return &output.Error{
			Err:    "missing_body",
			Detail: "no comment body on stdin",
			Hint:   "Pipe the comment body on stdin, e.g. printf '<p>hi</p>' | confluence comment add <page> --body-format storage",
			Code:   output.ExitGeneral,
		}
	}
	if c.BodyFormat == "" {
		return &output.Error{
			Err:    "invalid_input",
			Detail: "--body-format is required",
			Hint:   "Pass --body-format storage or adf.",
			Code:   output.ExitGeneral,
		}
	}

	repr, val, err := bodywrite.Prepare(c.BodyFormat, raw)
	if err != nil {
		return &output.Error{
			Err:    "invalid_body",
			Detail: err.Error(),
			Code:   output.ExitGeneral,
		}
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	cm, err := client.AddFooterComment(ctx, pageID, confluence.WriteBody{
		Representation: confluence.APIBodyFormat(repr),
		Value:          val,
	})
	if err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	row := map[string]any{
		"id":          cm.ID,
		"page_id":     pageID,
		"kind":        "footer",
		"body_format": repr,
		"web_url":     cm.WebURL,
	}
	if err := p.PrintItem(row); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}
