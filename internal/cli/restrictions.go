package cli

import (
	"context"

	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

// PageRestrictionsCmd groups the page-level view/edit restriction commands.
type PageRestrictionsCmd struct {
	Get   PageRestrictionsGetCmd   `cmd:"" help:"Show a page's direct view/edit restrictions."`
	Set   PageRestrictionsSetCmd   `cmd:"" help:"Replace a page's view/edit restrictions (declarative: both operations are rewritten)."`
	Clear PageRestrictionsClearCmd `cmd:"" help:"Remove every direct restriction from a page."`
}

// restrictionRow renders one operation as a JSONL row. Lists are always arrays.
func restrictionRow(pageID string, op confluence.OperationRestriction) map[string]any {
	users := make([]map[string]any, 0, len(op.Users))
	for _, u := range op.Users {
		users = append(users, map[string]any{"account_id": u.AccountID, "display_name": u.DisplayName})
	}
	groups := make([]map[string]any, 0, len(op.Groups))
	for _, g := range op.Groups {
		groups = append(groups, map[string]any{"id": g.ID, "name": g.Name})
	}
	return map[string]any{
		"page_id":   pageID,
		"operation": op.Operation,
		"users":     users,
		"groups":    groups,
	}
}

// printRestrictions fetches the page's restrictions and emits one row per
// operation followed by the trailer.
func printRestrictions(ctx context.Context, cli *CLI, client confluence.Client, pageID string) error {
	ops, err := client.GetRestrictions(ctx, pageID)
	if err != nil {
		return cli.ClassifyError(err)
	}
	p := cli.NewPrinter()
	for _, op := range ops {
		if err := p.PrintItem(restrictionRow(pageID, op)); err != nil {
			return err
		}
	}
	return p.PrintMeta(output.Meta{})
}

// PageRestrictionsGetCmd shows a page's direct restrictions: one row for read,
// one for update. Inherited (ancestor) restrictions are not included.
type PageRestrictionsGetCmd struct {
	Page string `arg:"" name:"id-or-url" help:"Page ID or URL."`
}

func (c *PageRestrictionsGetCmd) Run(cli *CLI) error {
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
	return printRestrictions(ctx, cli, client, pageID)
}

// PageRestrictionsSetCmd replaces the page's restrictions with exactly the
// principals given. Both operations are always sent, so an operation with no
// flags is cleared. Output is the final state, read back after the write.
type PageRestrictionsSetCmd struct {
	Page         string   `arg:"" name:"id-or-url" help:"Page ID or URL."`
	ReadUsers    []string `name:"read-user" help:"Account ID allowed to view (repeatable)."`
	ReadGroups   []string `name:"read-group" help:"Group name allowed to view (repeatable)."`
	UpdateUsers  []string `name:"update-user" help:"Account ID allowed to edit (repeatable)."`
	UpdateGroups []string `name:"update-group" help:"Group name allowed to edit (repeatable)."`
	OnlyMe       bool     `name:"only-me" help:"Restrict both view and edit to the authenticated user; conflicts with the other principal flags."`
}

func (c *PageRestrictionsSetCmd) Run(cli *CLI) error {
	siteHint, pageID, err := resolvePageRef(cli, c.Page)
	if err != nil {
		return err
	}

	hasPrincipals := len(c.ReadUsers)+len(c.ReadGroups)+len(c.UpdateUsers)+len(c.UpdateGroups) > 0
	switch {
	case c.OnlyMe && hasPrincipals:
		return &output.Error{
			Err:    "invalid_input",
			Detail: "--only-me cannot be combined with --read-user/--read-group/--update-user/--update-group",
			Hint:   "Pass --only-me alone, or name the principals explicitly.",
			Input:  c.Page,
			Code:   output.ExitGeneral,
		}
	case !c.OnlyMe && !hasPrincipals:
		return &output.Error{
			Err:    "invalid_input",
			Detail: "no restrictions given",
			Hint:   "Pass --only-me or at least one of --read-user/--read-group/--update-user/--update-group. To remove all restrictions use 'confluence page restrictions clear <id>'.",
			Input:  c.Page,
			Code:   output.ExitGeneral,
		}
	}

	client, _, err := cli.NewClientForSite(siteHint)
	if err != nil {
		return err
	}
	ctx, cancel := cli.Context()
	defer cancel()

	var ops []confluence.OperationRestriction
	if c.OnlyMe {
		me, err := client.GetCurrentUser(ctx)
		if err != nil {
			return cli.ClassifyError(err)
		}
		ops = onlyMeRestrictions(me.AccountID)
	} else {
		ops = []confluence.OperationRestriction{
			{Operation: confluence.RestrictionRead, Users: toRestrictionUsers(c.ReadUsers), Groups: toRestrictionGroups(c.ReadGroups)},
			{Operation: confluence.RestrictionUpdate, Users: toRestrictionUsers(c.UpdateUsers), Groups: toRestrictionGroups(c.UpdateGroups)},
		}
	}

	if err := client.SetRestrictions(ctx, pageID, ops); err != nil {
		return cli.ClassifyError(err)
	}
	return printRestrictions(ctx, cli, client, pageID)
}

// onlyMeRestrictions restricts both view and edit to a single account.
func onlyMeRestrictions(accountID string) []confluence.OperationRestriction {
	me := []confluence.RestrictionUser{{AccountID: accountID}}
	return []confluence.OperationRestriction{
		{Operation: confluence.RestrictionRead, Users: me},
		{Operation: confluence.RestrictionUpdate, Users: me},
	}
}

func toRestrictionUsers(ids []string) []confluence.RestrictionUser {
	out := make([]confluence.RestrictionUser, 0, len(ids))
	for _, id := range ids {
		out = append(out, confluence.RestrictionUser{AccountID: id})
	}
	return out
}

func toRestrictionGroups(names []string) []confluence.RestrictionGroup {
	out := make([]confluence.RestrictionGroup, 0, len(names))
	for _, n := range names {
		out = append(out, confluence.RestrictionGroup{Name: n})
	}
	return out
}

// PageRestrictionsClearCmd removes all direct restrictions from a page.
type PageRestrictionsClearCmd struct {
	Page string `arg:"" name:"id-or-url" help:"Page ID or URL."`
}

func (c *PageRestrictionsClearCmd) Run(cli *CLI) error {
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

	if err := client.ClearRestrictions(ctx, pageID); err != nil {
		return cli.ClassifyError(err)
	}
	p := cli.NewPrinter()
	if err := p.PrintItem(map[string]any{"page_id": pageID, "cleared": true}); err != nil {
		return err
	}
	return p.PrintMeta(output.Meta{})
}
