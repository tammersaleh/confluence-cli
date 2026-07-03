package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/tammersaleh/confluence-sync/internal/confluenceurl"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type AttachmentCmd struct {
	List     AttachmentListCmd     `cmd:"" help:"List a page's attachments."`
	Download AttachmentDownloadCmd `cmd:"" help:"Download an attachment to a file."`
}

type AttachmentListCmd struct {
	Page string `arg:"" name:"page" help:"Page ID or URL."`
}

func (c *AttachmentListCmd) Run(cli *CLI) error {
	siteHint, pageID, err := c.resolvePage(cli)
	if err != nil {
		return err
	}

	client, rc, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	atts, err := client.GetAttachments(ctx, pageID)
	if err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	for _, a := range atts {
		row := map[string]any{
			"id":           a.ID,
			"title":        a.Title,
			"media_type":   a.MediaType,
			"download_url": absolutizeAttachmentURL(rc.Site, a.DownloadURL),
			"page_id":      pageID,
		}
		if err := p.PrintItem(row); err != nil {
			return err
		}
	}
	return p.PrintMeta(output.Meta{})
}

// resolvePage derives the site hint and page id from the Page arg, which is
// either a bare page id or a Confluence page URL.
func (c *AttachmentListCmd) resolvePage(cli *CLI) (siteHint, pageID string, err error) {
	ref, matched, perr := confluenceurl.Parse(c.Page)
	if matched && perr != nil {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: perr.Error(),
			Hint:   "Pass a numeric page id or a page URL (…/wiki/spaces/ENG/pages/123).",
			Input:  c.Page,
			Code:   output.ExitGeneral,
		}
	}

	if !matched {
		// Bare id: site comes from --site or the single stored default.
		return "", c.Page, nil
	}

	if ref.Kind != confluenceurl.KindPage || ref.PageID == "" {
		return "", "", &output.Error{
			Err:    "invalid_input",
			Detail: "URL does not identify a page",
			Hint:   "Pass a numeric page id or a page URL (…/wiki/spaces/ENG/pages/123).",
			Input:  c.Page,
			Code:   output.ExitGeneral,
		}
	}

	if cli.Site != "" {
		if err := requireSiteMatch(cli.Site, c.Page); err != nil {
			return "", "", err
		}
	}

	return ref.BaseURL, ref.PageID, nil
}

type AttachmentDownloadCmd struct {
	ID  string `arg:"" name:"id" help:"Attachment ID."`
	Out string `short:"o" help:"Output file path (default: the attachment filename in the current dir)."`
}

func (c *AttachmentDownloadCmd) Run(cli *CLI) error {
	// An attachment ID carries no site. A URL arg is rejected: attachment web
	// URLs aren't parseable into an attachment id, so require the bare id.
	if _, matched, _ := confluenceurl.Parse(c.ID); matched {
		return &output.Error{
			Err:    "invalid_input",
			Detail: "expected an attachment id, not a URL",
			Hint:   "Pass the attachment ID (list a page's attachments with 'confluence attachment list <page-id|url>').",
			Input:  c.ID,
			Code:   output.ExitGeneral,
		}
	}

	client, _, err := cli.NewClientForSite("")
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	att, err := client.GetAttachmentByID(ctx, c.ID)
	if err != nil {
		return cli.ClassifyError(err)
	}

	body, err := client.DownloadAttachment(ctx, *att)
	if err != nil {
		return cli.ClassifyError(err)
	}
	defer func() { _ = body.Close() }()

	path := c.Out
	if path == "" {
		path = filepath.Base(att.Title)
	}

	f, err := os.Create(path)
	if err != nil {
		return &output.Error{
			Err:    "write_error",
			Detail: err.Error(),
			Hint:   "Check the output path is writable.",
			Input:  path,
			Code:   output.ExitGeneral,
		}
	}
	n, err := io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return &output.Error{
			Err:    "write_error",
			Detail: err.Error(),
			Hint:   "Check the output path is writable.",
			Input:  path,
			Code:   output.ExitGeneral,
		}
	}

	p := cli.NewPrinter()
	row := map[string]any{
		"id":         att.ID,
		"title":      att.Title,
		"media_type": att.MediaType,
		"path":       path,
		"bytes":      n,
	}
	if err := p.PrintItem(row); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}
