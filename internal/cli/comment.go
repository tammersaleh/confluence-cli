package cli

import (
	"context"
	"strings"

	"github.com/tammersaleh/confluence-cli/internal/bodywrite"
	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

type CommentCmd struct {
	List CommentListCmd `cmd:"" help:"List a page's comments."`
	Add  CommentAddCmd  `cmd:"" help:"Add a footer or inline comment to a page."`
}

// CommentListCmd lists a page's comments. By default it fetches both footer and
// inline comments; --footer or --inline narrow to a single kind. Each kind is
// fully drained (comment counts per page are bounded), so no cursor is emitted.
type CommentListCmd struct {
	Page    string `arg:"" name:"id-or-url" help:"Page ID or URL."`
	Footer  bool   `help:"Only footer comments."`
	Inline  bool   `help:"Only inline comments."`
	Replies bool   `help:"Also fetch replies (recursively), each with a parent_id linking to its parent."`
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

	printComment := func(cm confluence.Comment, parentID string) error {
		row := map[string]any{
			"id":         cm.ID,
			"kind":       cm.Kind,
			"body":       cm.Body,
			"author_id":  cm.AuthorID,
			"created_at": cm.CreatedAt,
			"web_url":    cm.WebURL,
		}
		if parentID != "" {
			row["parent_id"] = parentID
		}
		return p.PrintItem(row)
	}

	// drainReplies recursively emits the reply tree under a comment. Each reply
	// carries a parent_id pointing at its immediate parent. Comment threads are
	// bounded, so every level is fully drained (no cursor surfaced).
	var drainReplies func(commentID, kind string) error
	drainReplies = func(commentID, kind string) error {
		cursor := ""
		for {
			replies, next, err := client.GetCommentChildren(ctx, commentID, kind, cursor, 0)
			if err != nil {
				return cli.ClassifyError(err)
			}
			for _, rc := range replies {
				if err := printComment(rc, commentID); err != nil {
					return err
				}
				if err := drainReplies(rc.ID, rc.Kind); err != nil {
					return err
				}
			}
			if next == "" {
				return nil
			}
			cursor = next
		}
	}

	drain := func(fetch func(ctx context.Context, pageID, cursor string, limit int) ([]confluence.Comment, string, error)) error {
		cursor := ""
		for {
			comments, next, err := fetch(ctx, pageID, cursor, 0)
			if err != nil {
				return cli.ClassifyError(err)
			}
			for _, cm := range comments {
				if err := printComment(cm, ""); err != nil {
					return err
				}
				if c.Replies {
					if err := drainReplies(cm.ID, cm.Kind); err != nil {
						return err
					}
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

// CommentAddCmd adds a comment to a page. The body is read from stdin and
// validated for the requested --body-format. By default it adds a footer
// comment; --inline anchors an inline comment to on-page text selected by
// --selection-text (with optional --match-index/--match-count).
type CommentAddCmd struct {
	Page          string `arg:"" name:"id-or-url" help:"Page ID or URL."`
	BodyFormat    string `help:"storage, adf, or markdown (piped body)."`
	Inline        bool   `help:"Add an inline comment anchored to on-page text (requires --selection-text)."`
	SelectionText string `name:"selection-text" help:"Text on the page to anchor an inline comment to (required with --inline)."`
	MatchIndex    int    `name:"match-index" default:"0" help:"Which occurrence of the selection text (0-based)."`
	MatchCount    int    `name:"match-count" default:"1" help:"Total occurrences of the selection text in the page."`
}

func (c *CommentAddCmd) Run(cli *CLI) error {
	siteHint, pageID, err := resolvePageRef(cli, c.Page)
	if err != nil {
		return err
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
			Hint:   "Pass --body-format storage, adf, or markdown.",
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

	if c.Inline {
		if c.SelectionText == "" {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "--inline requires --selection-text",
				Hint:   "Pass --selection-text with the exact on-page text to anchor to.",
				Code:   output.ExitGeneral,
			}
		}
		if c.MatchCount < 1 {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "--match-count must be >= 1",
				Hint:   "Set --match-count to the number of times the selection text appears on the page.",
				Code:   output.ExitGeneral,
			}
		}
		if c.MatchIndex < 0 || c.MatchIndex >= c.MatchCount {
			return &output.Error{
				Err:    "invalid_input",
				Detail: "--match-index must be >= 0 and < --match-count",
				Hint:   "Pass a 0-based --match-index within the number of occurrences.",
				Code:   output.ExitGeneral,
			}
		}
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	body := confluence.WriteBody{
		Representation: confluence.APIBodyFormat(repr),
		Value:          val,
	}

	p := cli.NewPrinter()

	if c.Inline {
		cm, err := client.AddInlineComment(ctx, pageID, body, confluence.InlineCommentSelection{
			Text:       c.SelectionText,
			MatchCount: c.MatchCount,
			MatchIndex: c.MatchIndex,
		})
		if err != nil {
			return cli.ClassifyError(err)
		}
		row := map[string]any{
			"id":             cm.ID,
			"page_id":        pageID,
			"kind":           "inline",
			"body_format":    repr,
			"web_url":        cm.WebURL,
			"selection_text": c.SelectionText,
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
		return p.PrintMeta(output.Meta{})
	}

	cm, err := client.AddFooterComment(ctx, pageID, body)
	if err != nil {
		return cli.ClassifyError(err)
	}

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
