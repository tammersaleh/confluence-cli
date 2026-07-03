---
name: confluence-cli
description: "Agent-first Confluence CLI: sync spaces to Markdown, read pages (page list/get/children/ancestors/tree), inspect spaces (space info/list), read/download attachments, CQL search, list comments and labels, and look up users, with the write surface arriving in later releases"
argument-hint: ""
allowed-tools:
  - Bash(confluence *)
---

# Confluence CLI

Agent-first CLI for Confluence. JSONL output (one JSON object per line). Every
command ends with a `_meta` trailer: `{"_meta":{"has_more":false}}`.

Today the `version`, `space sync`, `space info`, `space list`, `auth`,
`page list`, `page get`, `page children`, `page ancestors`, `page tree`,
`attachment list`, `attachment download`, `search`, `comment list`,
`label list`, `user current`, and `user info` commands ship. The write surface
below is planned but not yet implemented - do not invoke it.

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

### space info

Fetch metadata for one or more spaces. Each argument is a space key, a numeric
space id, or a space/page URL; all must be on one site. Each row echoes its
`input`. Unknown keys or unreadable spaces appear inline on stdout and bump
`_meta.error_count`.

```bash
confluence space info ENG
confluence space info ENG DESIGN 98765
```

```jsonl
{"input":"ENG","id":"98765","key":"ENG","name":"Engineering","type":"global","status":"current","homepage_id":"123400"}
{"_meta":{"has_more":false,"error_count":0}}
```

### space list

List spaces on the site (from `--site` or the single stored default; no URL arg).
Page with `--limit`/`--cursor`, or drain with `--all`. Rows carry `id`, `key`,
`name`, `type`, `status`, `homepage_id`.

```bash
confluence space list
confluence space list --all
```

```jsonl
{"id":"98765","key":"ENG","name":"Engineering","type":"global","status":"current","homepage_id":"123400"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
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

### page children

Direct children of a page (numeric id or page URL). Page with
`--limit`/`--cursor`, or drain with `--all`. Rows carry `id`, `title`, `type`.

```bash
confluence page children 123400
confluence page children 123400 --all
```

```jsonl
{"id":"123456","title":"API Design","type":"page"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page ancestors

Ancestor chain of a page, root-most first (numeric id or page URL). Not
paginated. Rows carry `id` and `type`; the endpoint omits `title`.

```bash
confluence page ancestors 123456
```

```jsonl
{"id":"123400","type":"page"}
{"id":"123410","type":"page"}
{"_meta":{"has_more":false}}
```

### page tree

Space page hierarchy in depth-first order. `--space` is a bare key or a
space/page URL. Rows carry `id`, `title`, `type`, `depth` (0 for roots), and
`parent_id` when the page has a parent within the space.

Ordering is by page ID, not Confluence display order - the v2 API doesn't expose
display order in the crawl, so siblings sort by ID for deterministic output.

```bash
confluence page tree --space ENG
```

```jsonl
{"id":"123400","title":"Architecture","type":"page","depth":0}
{"id":"123456","title":"API Design","type":"page","depth":1,"parent_id":"123400"}
{"_meta":{"has_more":false}}
```

### attachment list

List a page's attachments. The argument is a numeric page id or page URL. Rows
carry `id`, `title`, `media_type`, `download_url` (absolute), and `page_id`.

```bash
confluence attachment list 123456
```

```jsonl
{"id":"att987","title":"diagram.png","media_type":"image/png","download_url":"https://acme.atlassian.net/wiki/download/attachments/123456/diagram.png","page_id":"123456"}
{"_meta":{"has_more":false}}
```

### attachment download

Download an attachment by id (not a URL) to a file. Without `--out`, the file is
written to the attachment's filename in the current directory.

```bash
confluence attachment download att987
confluence attachment download att987 --out ./diagram.png
```

```jsonl
{"id":"att987","title":"diagram.png","media_type":"image/png","path":"./diagram.png","bytes":48213}
{"_meta":{"has_more":false}}
```

### search

CQL search. The positional is the CQL query. Site-wide (from `--site` or the
single stored default). Page with `--limit`/`--cursor`, or drain with `--all`.
Rows carry `id`, `title`, `type`, `space_key`, `excerpt`, `url`.

```bash
confluence search 'type = page AND text ~ "runbook"'
confluence search 'label = on-call' --all
```

```jsonl
{"id":"123456","title":"Incident Runbook","type":"page","space_key":"ENG","excerpt":"steps to follow during an incident","url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### comment list

A page's comments (numeric id or page URL). Footer and inline are fully drained,
so there is no cursor. `--footer`/`--inline` narrow to one kind. Rows carry `id`,
`kind` (`footer`/`inline`), `body`, `author_id`, `created_at`, `web_url`. A
missing page is a fatal `page_not_found`.

```bash
confluence comment list 123456
confluence comment list 123456 --inline
```

```jsonl
{"id":"c1","kind":"footer","body":"<p>Looks good to me.</p>","author_id":"a1","created_at":"2024-06-20T14:45:00.000Z","created_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c1"}
{"_meta":{"has_more":false}}
```

### label list

A page's labels (numeric id or page URL). Fully drained, so there is no cursor.
Rows carry `id`, `name`, `prefix`.

```bash
confluence label list 123456
```

```jsonl
{"id":"l1","name":"runbook","prefix":"global"}
{"_meta":{"has_more":false}}
```

### user current

The authenticated user. A single row with `account_id`, `display_name`, `email`.

```bash
confluence user current
```

```jsonl
{"account_id":"a1","display_name":"Ada Lovelace","email":"ada@acme.com"}
{"_meta":{"has_more":false}}
```

### user info

Look up users by account id. Each row echoes its `input` and carries
`account_id`, `display_name`, `email`. Unknown ids appear inline on stdout and
bump `_meta.error_count`.

```bash
confluence user info a1
confluence user info a1 a2 a3
```

```jsonl
{"input":"a1","account_id":"a1","display_name":"Ada Lovelace","email":"ada@acme.com"}
{"input":"a2","error":"user_not_found","detail":"No user with account id 'a2'","hint":"confluence user current"}
{"_meta":{"has_more":false,"error_count":1}}
```

## Planned (not yet available)

Designed but not implemented. Do not run these yet:

- `confluence attachment upload` - upload files to a page.
- `confluence comment add` - add a footer or inline comment.
- `confluence label add|remove` - add or remove labels.
- `confluence page create|update|delete` - authoring (Markdown-in, with
  optimistic concurrency on update).
