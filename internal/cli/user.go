package cli

import (
	"github.com/tammersaleh/confluence-sync/internal/output"
)

type UserCmd struct {
	Current UserCurrentCmd `cmd:"" help:"Show the authenticated user."`
	Info    UserInfoCmd    `cmd:"" help:"Look up users by accountId."`
}

// UserCurrentCmd shows the authenticated user. The site comes from --site or the
// single stored default.
type UserCurrentCmd struct{}

func (c *UserCurrentCmd) Run(cli *CLI) error {
	client, _, err := cli.NewClientForSite("")
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	u, err := client.GetCurrentUser(ctx)
	if err != nil {
		return cli.ClassifyError(err)
	}

	p := cli.NewPrinter()
	if err := p.PrintItem(map[string]any{
		"account_id":   u.AccountID,
		"display_name": u.DisplayName,
		"email":        u.Email,
	}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}

// UserInfoCmd looks up one or more users by accountId. Per-item failures are
// emitted inline and the command exits non-zero if any lookup failed.
type UserInfoCmd struct {
	IDs []string `arg:"" name:"accountId" help:"Account IDs to look up."`
}

func (c *UserInfoCmd) Run(cli *CLI) error {
	client, _, err := cli.NewClientForSite("")
	if err != nil {
		return err
	}

	ctx, cancel := cli.Context()
	defer cancel()

	p := cli.NewPrinter()
	errCount := 0
	for _, id := range c.IDs {
		u, err := client.GetUser(ctx, id)
		if err != nil {
			oErr := cli.ClassifyError(err)
			oErr.Input = id
			if err := p.PrintItem(oErr.AsItem()); err != nil {
				return err
			}
			errCount++
			continue
		}
		row := map[string]any{
			"input":        id,
			"account_id":   u.AccountID,
			"display_name": u.DisplayName,
			"email":        u.Email,
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
