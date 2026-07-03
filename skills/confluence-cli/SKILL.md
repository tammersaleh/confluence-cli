---
name: confluence-cli
description: "Agent-first Confluence CLI: sync spaces to Markdown and read pages (page list/get), with the wider read/write surface arriving in later releases"
argument-hint: ""
allowed-tools:
  - Bash(confluence *)
---

# Confluence CLI

Agent-first CLI for Confluence. JSONL output (one JSON object per line). Every
command ends with a `_meta` trailer: `{"_meta":{"has_more":false}}`.

Today the `version`, `space sync`, `auth`, `page list`, and `page get` commands
ship. The wider read/write surface below is planned but not yet implemented - do
not invoke it.

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

### page list

List pages in a space. `--space` is a bare key or a space/page URL. Page through
with `--limit`/`--cursor`, or fetch everything with `--all`.

```bash
confluence page list --space ENG
confluence page list --space ENG --limit 50 --cursor eyJpZCI6...
confluence page list --space ENG --all
```

```jsonl
{"id":"123456","title":"API Design","type":"page","space_key":"ENG","parent_id":"123400"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page get

Fetch pages by numeric id or page URL. All arguments must be on one site (one
site per invocation). Each row echoes its `input`.

`--body-format` defaults to `storage`; also `atlas_doc_format` (alias `adf`),
`view`, and `markdown` (alias `md`). ADF `body` is a nested JSON object;
`markdown` is derived from the storage body with attachments resolved to remote
URLs and adds `source_body_format`. Bad ids or unreadable pages appear inline on
stdout and bump `_meta.error_count`.

```bash
confluence page get 123456
confluence page get 123456 789012 --body-format markdown
confluence page get https://acme.atlassian.net/wiki/spaces/ENG/pages/123456 --body-format adf
```

```jsonl
{"input":"123456","id":"123456","title":"API Design","space_id":"98765","version":5,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","modified_at":"2024-06-20T14:45:00.000Z","modified_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456","body":"# API Design\n\nSee the design.","body_format":"markdown","source_body_format":"storage"}
{"_meta":{"has_more":false,"error_count":0}}
```

## Planned (not yet available)

Designed but not implemented. Do not run these yet:

- `confluence page children|ancestors|tree` - read page hierarchy.
- `confluence space list|info` - list and inspect spaces.
- `confluence attachment list|download|upload` - attachments.
- `confluence search <cql>` - CQL search.
- `confluence comment list|add` - footer and inline comments.
- `confluence label list|add|remove` - labels.
- `confluence user current|info` - user lookup.
- `confluence page create|update|delete` - authoring (Markdown-in, with
  optimistic concurrency on update).
