package cli

import (
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type LabelCmd struct {
	List LabelListCmd `cmd:"" help:"List a page's labels."`
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
