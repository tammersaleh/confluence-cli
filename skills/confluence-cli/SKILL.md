---
name: confluence-cli
description: "Agent-first Confluence CLI: sync spaces to Markdown, with a read/write command surface arriving in later releases"
argument-hint: ""
allowed-tools:
  - Bash(confluence *)
---

# Confluence CLI

Agent-first CLI for Confluence. JSONL output (one JSON object per line). Every
command ends with a `_meta` trailer: `{"_meta":{"has_more":false}}`.

Today the `version`, `space sync`, and `auth` commands ship. The wider read/write
surface below is planned but not yet implemented - do not invoke it.

## Prerequisites

Requires the `confluence` binary on PATH. If `command not found`, install it:
`brew install tammersaleh/tap/confluence-cli` (macOS) or
`go install github.com/tammersaleh/confluence-sync/cmd/confluence@latest`.

Skill install/update: `skills add tammersaleh/confluence-sync -g` /
`skills update`.

## Auth

Authenticate with an API token + email over environment variables:

- `CONFLUENCE_SITE` - site base URL (e.g. `https://acme.atlassian.net/wiki`).
- `CONFLUENCE_EMAIL` - Atlassian account email.
- `CONFLUENCE_API_TOKEN` - API token.

`ATLASSIAN_SITE` / `ATLASSIAN_API_EMAIL` / `ATLASSIAN_API_KEY` work as
compatibility aliases. Or store credentials with `confluence auth login` (see
below); the token is piped on stdin, validated, then saved.

## Commands available now

All honor `--quiet` and `--timeout`.

### auth

Store credentials keyed by canonical site URL. The token is read from stdin,
never argv.

```bash
printf '%s' "$TOKEN" | confluence auth login --site https://acme.atlassian.net --email you@example.com
```

```jsonl
{"status":"logged_in","site":"https://acme.atlassian.net","account_id":"a1"}
{"_meta":{"has_more":false}}
```

`auth status` lists configured sites and any active env override without printing
secrets. `auth logout [<site>]` removes a site; the positional is optional when
only one is configured.

```bash
confluence auth status
confluence auth logout https://acme.atlassian.net
```

### version

```bash
confluence version
```

```jsonl
{"version":"0.1.0"}
{"_meta":{"has_more":false}}
```

### space sync

```bash
confluence space sync <space-url> <output-dir> [--prune] [--dry-run]
```

One-way sync of a Confluence space to local Markdown. Progress goes to stderr;
stdout carries a summary object then the trailer.

- `--prune` - remove local files no longer in the space.
- `--dry-run` - report without writing.
- `--quiet` - suppress all stdout (summary and trailer); rely on the exit code. Progress and warnings still go to stderr.

```bash
confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs
```

```jsonl
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":false}
{"_meta":{"has_more":false}}
```

Preview before writing:

```bash
confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs --dry-run
```

## Planned (not yet available)

Designed but not implemented. Do not run these yet:

- `confluence page get|list|children|ancestors|tree` - read pages and hierarchy.
- `confluence space list|info` - list and inspect spaces.
- `confluence attachment list|download|upload` - attachments.
- `confluence search <cql>` - CQL search.
- `confluence comment list|add` - footer and inline comments.
- `confluence label list|add|remove` - labels.
- `confluence user current|info` - user lookup.
- `confluence page create|update|delete` - authoring (Markdown-in, with
  optimistic concurrency on update).
