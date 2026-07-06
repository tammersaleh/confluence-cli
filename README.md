# confluence-cli

An agent-first Confluence CLI. The binary is `confluence`; the repository and Go
module are `confluence-cli`. Output is JSONL (one JSON object per line);
commands are non-interactive and scriptable, built for LLM agents and CI.

The read and write surface ships today: `version`, `space sync`, `space info`,
`space list`, `auth`, `page list`, `page get`, `page children`, `page descendants`, `page ancestors`,
`page tree`, `page create`, `page update`, `page delete`, `attachment list`,
`attachment download`, `attachment upload`, `search`, `comment list`,
`comment add`, `label list`, `label add`, `label remove`, `user current`, and
`user info`. The full command surface is implemented. See `SPEC.md` for the full
contract and `skills/confluence-cli/SKILL.md` for the agent skill.

## Installation

```bash
brew install --cask tammersaleh/tap/confluence-cli
```

Or with Go:

```bash
go install github.com/tammersaleh/confluence-cli/cmd/confluence@latest
```

## Auth

API token + email over HTTP Basic. Set environment variables:

| Variable | Description |
|----------|-------------|
| `CONFLUENCE_SITE` | Site base URL (e.g. `https://acme.atlassian.net`) |
| `CONFLUENCE_EMAIL` | Atlassian account email |
| `CONFLUENCE_API_TOKEN` | API token |

`ATLASSIAN_SITE` / `ATLASSIAN_API_EMAIL` / `ATLASSIAN_API_KEY` work as
compatibility aliases.

```bash
export CONFLUENCE_EMAIL=user@acme.com
export CONFLUENCE_API_TOKEN=your-api-token
```

Or store credentials at `~/.config/confluence-cli/credentials.json` with
`confluence auth login`. The token is read from stdin, validated against the
site, and then saved under the canonical site URL:

```bash
printf '%s' "$TOKEN" | confluence auth login --site https://acme.atlassian.net --email you@example.com
```

## Commands

The available commands honor `--quiet` and `--timeout`. Every command ends with
a `_meta` trailer line.

Write commands that carry content (`page create`, `page update`, `comment add`)
take the body on stdin, never as a flag. `--body-format` names what you pipe:
`storage` (a well-formed XHTML fragment), `adf`/`atlas_doc_format` (an ADF JSON
doc rooted at `{"type":"doc","version":1,"content":[...]}`), or `markdown`/`md`
(GFM, converted to storage XHTML via goldmark). The body is validated locally
before the API sees it. Markdown fenced code becomes a plain preformatted block,
not a Confluence code macro, and raw HTML embedded in the Markdown is escaped.

### version

```bash
confluence version
```

```jsonl
{"version":"0.1.0"}
{"_meta":{"has_more":false}}
```

### auth

Manage stored credentials keyed by canonical site URL.

```bash
printf '%s' "$TOKEN" | confluence auth login --site https://acme.atlassian.net --email you@example.com
confluence auth status
confluence auth logout https://acme.atlassian.net
```

`auth login` reads the token from stdin (never argv), validates it, then saves
it. `auth status` lists configured sites and any active env override without
printing secrets. `auth logout` removes a site; the `<site>` positional is
optional when only one is configured.

### space sync

One-way sync of a Confluence space to local Markdown files. This is a full-space
crawl. Human-readable progress goes to stderr; stdout carries a single summary
object then the trailer.

```bash
confluence space sync <space-url> <output-dir> [--prune] [--dry-run]
```

```bash
confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs
```

```jsonl
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":false}
{"_meta":{"has_more":false}}
```

Flags:

- `--prune` - after syncing, remove files in the output directory no longer part of the space. `_attachments/` dirs of version-skipped pages are protected.
- `--dry-run` - report what would happen without writing.
- `--quiet` - suppress all stdout (summary and trailer). Rely on the exit code; fatal errors still print to stderr.
- `--trace` - emit JSON-lines diagnostics to stderr: one event per API request (endpoint, attempt, status, latency) plus retry waits.

Accepts space URLs in these formats:

- `https://acme.atlassian.net/wiki/spaces/ENG`
- `https://acme.atlassian.net/wiki/spaces/ENG/overview`
- `https://acme.atlassian.net/wiki/spaces/ENG/pages/...`

### space info

Fetch metadata for one or more spaces. Each argument is a space key, numeric
space id, or space/page URL; all must be on one site. Each row echoes its
`input`.

```bash
confluence space info ENG
confluence space info ENG DESIGN 98765
```

```jsonl
{"input":"ENG","id":"98765","key":"ENG","name":"Engineering","type":"global","status":"current","homepage_id":"123400"}
{"_meta":{"has_more":false,"error_count":0}}
```

Unknown keys or spaces you can't read appear inline on stdout and bump
`_meta.error_count`.

### space list

List spaces accessible to the authenticated user. This is site-wide: the site
comes from `--site` or the single stored default. Page through with
`--limit`/`--cursor`, or fetch everything with `--all`. Rows carry `id`, `key`,
`name`, `type`, `status`, and `homepage_id`.

```bash
confluence space list
confluence space list --all
```

```jsonl
{"id":"98765","key":"ENG","name":"Engineering","type":"global","status":"current","homepage_id":"123400"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page list

List pages in a space. `--space` takes a bare key or a space/page URL. Use
`--limit`, `--cursor`, and `--all` to control paging.

```bash
confluence page list --space ENG
confluence page list --space ENG --all
```

```jsonl
{"id":"123456","title":"API Design","type":"page","space_key":"ENG","parent_id":"123400"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page get

Fetch pages by numeric id or page URL; all arguments must be on one site. Each
row echoes its `input`. `--body-format` (default `storage`) also accepts
`atlas_doc_format` (alias `adf`), `view`, and `markdown` (alias `md`). ADF `body`
is a nested JSON object; `markdown` is derived from storage with attachment
references resolved to remote URLs and adds `source_body_format`.
`--resolve-authors` adds an `author_name` sibling (best-effort, cached user
lookups).

```bash
confluence page get 123456
confluence page get 123456 --body-format markdown
confluence page get 123456 --resolve-authors
```

```jsonl
{"input":"123456","id":"123456","title":"API Design","space_id":"98765","version":5,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","modified_at":"2024-06-20T14:45:00.000Z","modified_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456","body":"<p>See the design.</p>","body_format":"storage"}
{"_meta":{"has_more":false,"error_count":0}}
```

Bad ids or pages you can't read appear inline on stdout and bump
`_meta.error_count`.

### page children

List a page's direct children. The argument is a numeric page id or a page URL.
Page through with `--limit`/`--cursor`, or fetch everything with `--all`. Rows
carry `id`, `title`, and `type`.

```bash
confluence page children 123400
confluence page children 123400 --all
```

```jsonl
{"id":"123456","title":"API Design","type":"page"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page descendants

List every descendant of a page (all levels, not just direct children). The
argument is a numeric page id or a page URL. Page through with `--limit`/`--cursor`,
or fetch everything with `--all`. Rows carry `id`, `title`, `type`, and `depth`
(1 for a direct child); `parent_id` appears when the API returns it.

```bash
confluence page descendants 123400
confluence page descendants 123400 --all
```

```jsonl
{"id":"123456","title":"API Design","type":"page","depth":1,"parent_id":"123400"}
{"id":"123999","title":"Auth flow","type":"page","depth":2,"parent_id":"123456"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page ancestors

List a page's ancestor chain, root-most first. The argument is a numeric page id
or a page URL. Not paginated. Rows carry `id` and `type`; the Confluence
ancestors endpoint omits `title`.

```bash
confluence page ancestors 123456
```

```jsonl
{"id":"123400","type":"page"}
{"id":"123410","type":"page"}
{"_meta":{"has_more":false}}
```

### page tree

Print a space's page hierarchy in depth-first order. `--space` takes a bare space
key or a space/page URL. Rows carry `id`, `title`, `type`, and `depth` (0 for
roots); `parent_id` appears when the page has a parent within the space.

Ordering is by page ID, not Confluence display order. The v2 API doesn't expose
display order in the crawl, so siblings are sorted by ID for deterministic
output. Don't expect it to match the order in the Confluence UI.

```bash
confluence page tree --space ENG
```

```jsonl
{"id":"123400","title":"Architecture","type":"page","depth":0}
{"id":"123456","title":"API Design","type":"page","depth":1,"parent_id":"123400"}
{"_meta":{"has_more":false}}
```

### page create

Create a page. `--space` takes a bare key or a space/page URL; `--parent` nests
the page under an existing page on the same site. The body is read from stdin;
`--body-format` is required only when a non-empty body is piped. With no stdin
(or empty input), the page is created empty. The row carries `id`, `title`,
`space_id`, `version`, `author_id`, `created_at`, and `web_url`; `parent_id`
appears when the page has a parent. The body is not echoed.

```bash
printf '<p>Hello</p>' | confluence page create --space ENG --title "API Design" --body-format storage
```

```jsonl
{"id":"123456","title":"API Design","space_id":"98765","version":1,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
```

### page update

Update a page under optimistic concurrency. `--if-version` is the version you
expect to be current; a mismatch fails with a recoverable `version_conflict` and
writes nothing. On success the page is written at version `n+1`. Omitting
`--title` preserves the title; omitting the body preserves the body. The row
carries `id`, `title`, `version`, `previous_version`, and `web_url`.

```bash
printf '<p>Revised.</p>' | confluence page update 123456 --if-version 5 --body-format storage
```

```jsonl
{"id":"123456","title":"API Design","version":6,"previous_version":5,"web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
```

### page delete

Delete one or more pages. Delete moves pages to the trash, not a permanent
purge. A real delete requires `--yes`; without it (and without `--dry-run`) the
command refuses with `confirmation_required`. `--dry-run` previews. All
arguments must be on one site. Deleted rows carry `input`, `id`, `deleted:true`,
`delete_mode:"trash"`; dry-run rows add `title`, `version`, `would_delete:true`.

```bash
confluence page delete 123456 --dry-run
confluence page delete 123456 --yes
```

```jsonl
{"input":"123456","id":"123456","deleted":true,"delete_mode":"trash"}
{"_meta":{"has_more":false,"error_count":0}}
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

### attachment upload

Upload one or more local files as attachments to a page. Each file is uploaded
independently; a failure on one is reported inline and does not abort the rest.
Re-uploading a file whose name collides with an existing attachment creates a new
version rather than a duplicate. Each row echoes the `input` file and carries
`page_id`, `attachment_id`, `title`, `media_type`, and `uploaded:true`.

```bash
confluence attachment upload 123456 ./diagram.png
```

```jsonl
{"input":"./diagram.png","page_id":"123456","attachment_id":"att987","title":"diagram.png","media_type":"image/png","uploaded":true}
{"_meta":{"has_more":false,"error_count":0}}
```

### search

CQL search. The positional argument is the CQL query. Site-wide: the site comes
from `--site` or the single stored default. Page with `--limit`/`--cursor`, or
drain with `--all`. Rows carry `id`, `title`, `type`, `space_key`, `excerpt`, and
`url`.

```bash
confluence search 'type = page AND text ~ "runbook"'
confluence search 'label = on-call' --all
```

```jsonl
{"id":"123456","title":"Incident Runbook","type":"page","space_key":"ENG","excerpt":"steps to follow during an incident","url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### comment list

List a page's comments (numeric id or page URL). Footer and inline comments are
fully drained, so there is no cursor. `--footer` and `--inline` narrow to one
kind; without either, both are emitted. Rows carry `id`, `kind`
(`footer`/`inline`), `body`, `author_id`, `created_at`, and `web_url`. A missing
page is a fatal `page_not_found`.

`--replies` drains each comment's reply thread recursively and emits the replies
after their parent, each with a `parent_id` pointing at its immediate parent.
`--resolve-authors` adds an `author_name` sibling (best-effort, cached).

```bash
confluence comment list 123456
confluence comment list 123456 --inline
confluence comment list 123456 --replies
confluence comment list 123456 --resolve-authors
```

```jsonl
{"id":"c1","kind":"footer","body":"<p>Looks good to me.</p>","author_id":"a1","created_at":"2024-06-20T14:45:00.000Z","created_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c1"}
{"_meta":{"has_more":false}}
```

### comment add

Add a comment to a page. The body is read from stdin (required) and
`--body-format` names its representation (`storage`, `adf`, or `markdown`).
Without `--inline` the comment is a footer comment; the row carries `id`,
`page_id`, `kind:"footer"`, `body_format` (the API representation, `storage` or
`atlas_doc_format`), and `web_url`.

```bash
printf '<p>Looks good to me.</p>' | confluence comment add 123456 --body-format storage
```

```jsonl
{"id":"c1","page_id":"123456","kind":"footer","body_format":"storage","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c1"}
{"_meta":{"has_more":false}}
```

`--inline` anchors the comment to on-page text via `--selection-text <text>`.
When that text occurs more than once, `--match-index N` (1-based) selects which
occurrence and `--match-count N` asserts the expected number of occurrences.
Inline rows carry `kind:"inline"` and `selection_text`.

```bash
printf '<p>Clarify this.</p>' | confluence comment add 123456 --inline --selection-text "the retry budget" --body-format storage
```

```jsonl
{"id":"c2","page_id":"123456","kind":"inline","selection_text":"the retry budget","body_format":"storage","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c2"}
{"_meta":{"has_more":false}}
```

### label list

List a page's labels (numeric id or page URL). Fully drained, so there is no
cursor. Rows carry `id`, `name`, and `prefix`.

```bash
confluence label list 123456
```

```jsonl
{"id":"l1","name":"runbook","prefix":"global"}
{"_meta":{"has_more":false}}
```

### label add

Add one or more labels to a page. Each label is applied independently; a failure
on one is reported inline (with the label as `input`) and does not abort the
rest. Success rows carry `page_id`, `name`, `prefix`, and `added:true`.

```bash
confluence label add 123456 runbook on-call
```

```jsonl
{"page_id":"123456","name":"runbook","prefix":"global","added":true}
{"_meta":{"has_more":false,"error_count":0}}
```

### label remove

Remove one or more labels from a page. Each removal is independent; a per-label
failure is reported inline. Success rows carry `page_id`, `name`, and
`removed:true`.

```bash
confluence label remove 123456 runbook
```

```jsonl
{"page_id":"123456","name":"runbook","removed":true}
{"_meta":{"has_more":false,"error_count":0}}
```

### user current

The authenticated user. Emits a single row with `account_id`, `display_name`,
and `email`.

```bash
confluence user current
```

```jsonl
{"account_id":"a1","display_name":"Ada Lovelace","email":"ada@acme.com"}
{"_meta":{"has_more":false}}
```

### user info

Look up one or more users by account id. Each row echoes its `input` and carries
`account_id`, `display_name`, and `email`. Unknown account ids appear inline on
stdout and bump `_meta.error_count`.

```bash
confluence user info a1
confluence user info a1 a2 a3
```

```jsonl
{"input":"a1","account_id":"a1","display_name":"Ada Lovelace","email":"ada@acme.com"}
{"_meta":{"has_more":false,"error_count":0}}
```

## Sync behavior

Pages are converted from Confluence storage format to Markdown. The page
hierarchy is preserved as a directory structure.

### Directory structure

Given a Confluence space with this hierarchy:

```
Engineering
├── Architecture
│   ├── API Design
│   └── Database Schema
└── Runbooks
    └── Incident Response
```

The output becomes:

```
output/
├── index.md                          # "Engineering" page content
├── _attachments/                     # Attachments for "Engineering" page
│   └── diagram.png
├── architecture/
│   ├── index.md                      # "Architecture" page content
│   ├── api-design.md
│   └── database-schema.md
└── runbooks/
    ├── index.md                      # "Runbooks" page content
    ├── _attachments/                 # Attachments for "Runbooks" page
    │   └── checklist.pdf
    └── incident-response.md
```

Pages with children (or attachments) get their own directory with content in
`index.md`. Leaf pages without attachments are single `.md` files.

### Frontmatter

Each Markdown file includes YAML frontmatter with Confluence metadata:

```yaml
---
confluence_page_id: "123456"
title: "Page Title"
author_id: "user-account-id"
confluence_url: "https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"
version: 5
created_at: "2024-01-15T10:30:00.000Z"
modified_at: "2024-06-20T14:45:00.000Z"
---
```

### Attachments

Attachments are stored in `_attachments/` alongside their parent page's content,
and Markdown references are rewritten to local paths:

```markdown
![diagram](_attachments/diagram.png)
```

A leaf page with attachments is promoted to a directory with `index.md` plus
`_attachments/`.

### Incremental sync

On subsequent runs, the `version` field in each file's frontmatter is compared
against the Confluence API and unchanged pages are skipped. With `--prune`, stale
local files (pages deleted or renamed in Confluence) are removed after sync;
attachments of version-skipped pages are left alone since their attachment list
wasn't re-fetched.

### Filename sanitization

Page titles become filesystem-safe names: lowercased, spaces to hyphens,
characters outside `[a-z0-9-]` removed, repeated hyphens collapsed, leading and
trailing hyphens trimmed. Collisions get `-2`, `-3`, etc. suffixes.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error (including partial failure) |
| 2 | Authentication error |
| 3 | Rate limited |
| 4 | Network error |

## Development

```bash
mise run check         # test + lint + build (use this by default)
mise run test          # unit tests only
mise run test:race     # with race detector
mise run lint          # golangci-lint
mise run build         # build the confluence binary
```

Requires Go 1.25. See `CLAUDE.md` for architecture and the release workflow.
