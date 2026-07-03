package cli

import (
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type LabelCmd struct {
	List   LabelListCmd   `cmd:"" help:"List a page's labels."`
	Add    LabelAddCmd    `cmd:"" help:"Add labels to a page."`
	Remove LabelRemoveCmd `cmd:"" help:"Remove labels from a page."`
}

// LabelListCmd lists a page's labels. Labels are fully drained (bounded per
// page), so no cursor is emitted.
type LabelListCmd struct {
	Page string `arg:"" name:"id-or-url" help:"Page ID or URL."`
}

func (c *LabelListCmd) Run(cli *CLI) error {
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

	p := cli.NewPrinter()
	cursor := ""
	for {
		labels, next, err := client.GetLabels(ctx, pageID, cursor, 0)
		if err != nil {
			return cli.ClassifyError(err)
		}
		for _, l := range labels {
			row := map[string]any{
				"id":     l.ID,
				"name":   l.Name,
				"prefix": l.Prefix,
			}
			if err := p.PrintItem(row); err != nil {
				return err
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	return p.PrintMeta(output.Meta{})
}

// LabelAddCmd adds one or more labels to a page. Each label is applied
// independently; a failure on one is reported inline and does not abort the rest.
type LabelAddCmd struct {
	Page   string   `arg:"" name:"id-or-url" help:"Page ID or URL."`
	Labels []string `arg:"" name:"label" help:"Label(s) to add."`
}

func (c *LabelAddCmd) Run(cli *CLI) error {
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

	p := cli.NewPrinter()
	errCount := 0
	for _, label := range c.Labels {
		l, err := client.AddLabel(ctx, pageID, label)
		if err != nil {
			oErr := cli.ClassifyError(err)
			oErr.Input = label
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}
		row := map[string]any{
			"page_id": pageID,
			"name":    l.Name,
			"prefix":  l.Prefix,
			"added":   true,
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

// LabelRemoveCmd removes one or more labels from a page. Like add, each removal
// is independent and a per-label failure is reported inline.
type LabelRemoveCmd struct {
	Page   string   `arg:"" name:"id-or-url" help:"Page ID or URL."`
	Labels []string `arg:"" name:"label" help:"Label(s) to remove."`
}

func (c *LabelRemoveCmd) Run(cli *CLI) error {
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

	p := cli.NewPrinter()
	errCount := 0
	for _, label := range c.Labels {
		if err := client.RemoveLabel(ctx, pageID, label); err != nil {
			oErr := cli.ClassifyError(err)
			oErr.Input = label
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}
		row := map[string]any{
			"page_id": pageID,
			"name":    label,
			"removed": true,
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
