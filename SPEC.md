# confluence CLI Specification

An agent-first Confluence CLI built in Go. Output is JSONL, one JSON object per line; commands are non-interactive and scriptable. The binary is `confluence`; the repository and Go module stay `confluence-sync` (mirroring `slack`→`slack-cli`). This document describes the full intended surface. The `version`, `space sync`, `space info`, `space list`, `auth`, `page list`, `page get`, `page children`, `page ancestors`, `page tree`, `page create`, `page update`, `page delete`, `attachment list`, `attachment download`, `attachment upload`, `search`, `comment list`, `comment add`, `label list`, `label add`, `label remove`, `user current`, and `user info` commands ship today (see "Commands available now"). Only Markdown body input for writes and inline comments remain unimplemented (see "Planned").

## Design principles

- **Agent-first**: JSONL output, no interactive prompts, deterministic and scriptable.
- **Thin wrapper**: one API page per call by default. No hidden pagination loops; the caller controls data volume. (`space sync` is the deliberate exception - it is a full-space crawl.)
- **Resource-verb pattern**: `confluence <resource> <verb> [flags]`, consistent with `gh`, `kubectl`, `aws`.
- **Writes are first-class**: the eventual surface treats authoring pages, comments, labels, and attachments as co-equal with reads, not a deferred afterthought.
- **Composable**: pipes, `jq`, shell scripts. Stdout carries data, stderr carries diagnostics.

## Output contract

Every command writes JSONL to stdout, one self-contained JSON object per line. Fields use `snake_case`, matching the Confluence API.

Every command ends with a `_meta` trailer, always present even when there are no results:

```jsonl
{"_meta":{"has_more":false}}
```

List commands emit one object per item, then the trailer. When more pages exist, the trailer carries the cursor:

```jsonl
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

Info and bulk commands accept one or more arguments and echo an `input` field on each row, matching the argument that produced it. Per-item errors appear inline on stdout, interleaved with successful rows, and bump `_meta.error_count`:

```jsonl
{"input":"12345","id":"12345","title":"Runbook",...}
{"input":"99999","error":"page_not_found","detail":"No page with id '99999'","hint":"confluence page list --space ENG"}
{"_meta":{"has_more":false,"error_count":1}}
```

The `input` field is always present on info-command output regardless of `--fields`.

### Fatal errors

Errors that prevent the command from running (auth failure, network error, bad input) go to stderr as a single JSON object:

```json
{"error":"page_not_found","detail":"No page with id '99999'","hint":"confluence page list --space ENG","input":"99999","endpoint":"/wiki/api/v2/pages/99999"}
```

- `error` - stable code (e.g. `page_not_found`, `space_not_found`, `not_authed`).
- `detail` - human-readable context.
- `hint` - a concrete recovery command. Every hint names a command the caller can run next.
- `input` - the specific input that failed.
- `endpoint` - the Confluence API endpoint, present only on upstream API failures.

### Timestamp enrichment

Fields named `*_ts`, `created_at`, or `modified_at` gain an `*_iso` sibling in RFC 3339 form. Confluence already returns ISO 8601, so this is mostly passthrough, but the mechanism is kept for consistency with sibling tools.

```json
{"created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z"}
```

### Exit codes

- `0` - success.
- `1` - general error, including partial failure in a bulk command.
- `2` - authentication error (no credentials, expired, insufficient permission).
- `3` - rate limited, after exhausting retries.
- `4` - network error.

## Global flags

- `--site` - select the target site (base URL or configured alias). Env: `CONFLUENCE_SITE`.
- `--fields` - comma-separated list limiting output to those top-level fields. Applies to data rows only, not `_meta`. Nested objects are returned whole when their parent field is selected; use `jq` for deeper access.
- `--quiet` - suppress all stdout (data rows and the `_meta` trailer). The exit code conveys success/failure and fatal errors still print to stderr.
- `--timeout` - per-command deadline for API work.
- `--trace` - attach a JSON-lines diagnostics tracer to stderr. The plumbing is in place; per-request event emission (fetches, retries, latencies) arrives in a later release with the broadened client.

## Auth model

API token + email over HTTP Basic auth. No OAuth 3LO; static credentials fit agent and CI use.

Credentials live at `~/.config/confluence-cli/credentials.json`, keyed by canonical site base URL (the `cloud_id` is stored when known, but the URL is the primary selector since agents naturally hold URLs).

Precedence, highest first:

1. Flags (`--site`, and the credential set via `auth login`).
2. `CONFLUENCE_SITE` / `CONFLUENCE_EMAIL` / `CONFLUENCE_API_TOKEN`.
3. `ATLASSIAN_SITE` / `ATLASSIAN_API_EMAIL` / `ATLASSIAN_API_KEY` (compatibility aliases).
4. Stored credentials.

Site selection derives the target from a URL argument when the command takes one; else `--site`; else the single configured default. A URL-derived site that disagrees with an explicit `--site` is an error.

## Commands available now

The `version`, `space sync`, `space info`, `space list`, `auth`, `page list`, `page get`, `page children`, `page ancestors`, `page tree`, `page create`, `page update`, `page delete`, `attachment list`, `attachment download`, `attachment upload`, `search`, `comment list`, `comment add`, `label list`, `label add`, `label remove`, `user current`, and `user info` commands ship today. All honor `--quiet` and `--timeout`.

### Body input for writes

Write commands that carry page or comment content (`page create`, `page update`, `comment add`) take the body on stdin, never as a flag. `--body-format` names the representation of what you pipe:

- `storage` - Confluence storage format, a well-formed XHTML fragment (e.g. `<p>Hello</p>`).
- `adf` (alias `atlas_doc_format`) - Atlassian Document Format, a JSON document whose root is `{"type":"doc","version":1,"content":[...]}`.

The body is validated locally before the API sees it; a malformed fragment or ADF doc fails with `invalid_body` and nothing is written. Markdown body input is not yet supported.

### version

Emits the build version, then the trailer.

```jsonl
$ confluence version
{"version":"0.1.0"}
{"_meta":{"has_more":false}}
```

### space sync

```text
confluence space sync <space-url> <output-dir> [--prune] [--dry-run]
```

One-way sync of a Confluence space to local Markdown files under `<output-dir>`. This is a full-space crawl, not a thin per-page call.

- `--prune` - after syncing, remove files in the output directory that are no longer part of the space. `_attachments/` directories of version-skipped pages are protected.
- `--dry-run` - report what would happen without writing.

Human-readable progress goes to stderr. Stdout carries a single summary object, then the trailer:

```jsonl
$ confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":false}
{"_meta":{"has_more":false}}
```

```jsonl
$ confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs --dry-run
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":true}
{"_meta":{"has_more":false}}
```

### space info

```text
confluence space info <key|id|url>...
```

Fetch metadata for one or more spaces. Each argument is a space key (e.g. `ENG`), a numeric space id, or a space/page URL. All arguments must resolve to a single site (one site per invocation); mixing sites is an error. Each row echoes the `input` that produced it and carries `id`, `key`, `name`, `type`, `status`, and `homepage_id`.

Per-item errors (unknown key, no permission) appear inline on stdout and bump `_meta.error_count`.

```jsonl
$ confluence space info ENG 99999
{"input":"ENG","id":"98765","key":"ENG","name":"Engineering","type":"global","status":"current","homepage_id":"123400"}
{"input":"99999","error":"space_not_found","detail":"No space with id '99999'","hint":"confluence space info ENG"}
{"_meta":{"has_more":false,"error_count":1}}
```

### space list

```text
confluence space list [--limit <n>] [--cursor <c>] [--all]
```

List spaces accessible to the authenticated user. This is site-wide: the site comes from `--site` or the single stored default, since there is no URL argument to derive it from. One object per space, then the trailer.

- `--limit` - page size requested from the API (default 25).
- `--cursor` - opaque cursor from a prior `_meta.next_cursor`, to fetch the next page.
- `--all` - loop until the cursor is exhausted, emitting every space. The trailer omits `next_cursor` since nothing remains.

Each row carries `id`, `key`, `name`, `type`, `status`, and `homepage_id`.

```jsonl
$ confluence space list
{"id":"98765","key":"ENG","name":"Engineering","type":"global","status":"current","homepage_id":"123400"}
{"id":"98766","key":"DESIGN","name":"Design","type":"global","status":"current","homepage_id":"223400"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### auth

Manage stored credentials at `~/.config/confluence-cli/credentials.json`, keyed by canonical site base URL.

```text
confluence auth login --site <url> --email <email>
confluence auth status
confluence auth logout [<site>]
```

`auth login` reads the API token from stdin (never argv), validates it against the site, and only then saves it under the canonical site URL.

```jsonl
$ printf '%s' "$TOKEN" | confluence auth login --site https://acme.atlassian.net --email you@example.com
{"status":"logged_in","site":"https://acme.atlassian.net","account_id":"a1"}
{"_meta":{"has_more":false}}
```

`auth status` lists configured sites and any active env override, without printing secrets.

```jsonl
$ confluence auth status
{"site":"https://acme.atlassian.net","source":"stored","is_default":true}
{"_meta":{"has_more":false}}
```

`auth logout` removes the stored credentials for a site. The `<site>` positional is optional when exactly one site is configured.

```jsonl
$ confluence auth logout https://acme.atlassian.net
{"status":"logged_out","site":"https://acme.atlassian.net"}
{"_meta":{"has_more":false}}
```

### page list

```text
confluence page list --space <key|url> [--limit <n>] [--cursor <c>] [--all]
```

List pages in a space. `--space` takes a bare space key or a space/page URL; a URL selects the site. One object per page, then the trailer.

- `--limit` - page size requested from the API (default 25).
- `--cursor` - opaque cursor from a prior `_meta.next_cursor`, to fetch the next page.
- `--all` - loop until the cursor is exhausted, emitting every page. The trailer omits `next_cursor` since nothing remains.

Each row carries `id`, `title`, `type`, and `space_key`; `parent_id` and `parent_type` appear when the page has a parent.

```jsonl
$ confluence page list --space ENG
{"id":"123456","title":"API Design","type":"page","space_key":"ENG","parent_id":"123400"}
{"id":"123400","title":"Architecture","type":"page","space_key":"ENG"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page get

```text
confluence page get <id|url>... [--body-format storage|atlas_doc_format|view|markdown]
```

Fetch one or more pages by numeric id or page URL. All arguments must resolve to a single site (one site per invocation); mixing sites is an error. Each row echoes the `input` that produced it.

`--body-format` selects the body representation (default `storage`):

- `storage` - Confluence storage format (XHTML), returned as a string.
- `atlas_doc_format` (alias `adf`) - ADF; the `body` field is a nested JSON object, not a string.
- `view` - rendered view HTML, as a string.
- `markdown` (alias `md`) - derived by converting the storage body to GFM, with attachment references resolved to absolute remote URLs. Costs one extra API call per page to list attachments. Adds `source_body_format:"storage"` and sets `body_format:"markdown"`.

Per-item errors (bad id, no permission) appear inline on stdout and bump `_meta.error_count`.

```jsonl
$ confluence page get 123456
{"input":"123456","id":"123456","title":"API Design","space_id":"98765","version":5,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","modified_at":"2024-06-20T14:45:00.000Z","modified_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456","body":"<p>See the design.</p>","body_format":"storage"}
{"_meta":{"has_more":false,"error_count":0}}
```

Markdown, with a failed lookup interleaved:

```jsonl
$ confluence page get 123456 99999 --body-format markdown
{"input":"123456","id":"123456","title":"API Design","space_id":"98765","version":5,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","modified_at":"2024-06-20T14:45:00.000Z","modified_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456","body":"# API Design\n\nSee the design.","body_format":"markdown","source_body_format":"storage"}
{"input":"99999","error":"page_not_found","detail":"No page with id '99999'","hint":"confluence page list --space ENG"}
{"_meta":{"has_more":false,"error_count":1}}
```

### page children

```text
confluence page children <id|url> [--limit <n>] [--cursor <c>] [--all]
```

List a page's direct children. The argument is a numeric page id or a page URL; a URL selects the site. One object per child, then the trailer.

- `--limit` - page size requested from the API (default 25).
- `--cursor` - opaque cursor from a prior `_meta.next_cursor`, to fetch the next page.
- `--all` - loop until the cursor is exhausted, emitting every child. The trailer omits `next_cursor` since nothing remains.

Each row carries `id`, `title`, and `type`.

```jsonl
$ confluence page children 123400
{"id":"123456","title":"API Design","type":"page"}
{"id":"123457","title":"Database Schema","type":"page"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### page ancestors

```text
confluence page ancestors <id|url>
```

List a page's ancestor chain, root-most first. The argument is a numeric page id or a page URL. This endpoint is not paginated. Each row carries `id` and `type`; the Confluence ancestors endpoint returns limited fields, so `title` is omitted.

```jsonl
$ confluence page ancestors 123456
{"id":"123400","type":"page"}
{"id":"123410","type":"page"}
{"_meta":{"has_more":false}}
```

### page tree

```text
confluence page tree --space <key|url>
```

Print a space's page hierarchy in depth-first order. `--space` takes a bare space key or a space/page URL; a URL selects the site. One object per page, then the trailer.

Ordering is by page ID, not Confluence display order. The v2 API doesn't expose display order in the crawl, so siblings are sorted by ID for deterministic output; do not rely on this matching the order shown in the Confluence UI.

Each row carries `id`, `title`, `type`, and `depth` (0 for roots); `parent_id` appears when the page has a parent within the space.

```jsonl
$ confluence page tree --space ENG
{"id":"123400","title":"Architecture","type":"page","depth":0}
{"id":"123456","title":"API Design","type":"page","depth":1,"parent_id":"123400"}
{"id":"123457","title":"Database Schema","type":"page","depth":1,"parent_id":"123400"}
{"_meta":{"has_more":false}}
```

### page create

```text
confluence page create --space <key|url> --title <t> [--parent <id|url>] [--body-format storage|adf] [< body]
```

Create a page. `--space` takes a bare space key or a space/page URL; a URL selects the site. `--parent` nests the new page under an existing page (same site as the space). The body is read from stdin; `--body-format` is required only when a non-empty body is piped. With no stdin (or an empty/whitespace body), the page is created empty and `--body-format` may be omitted.

The row carries `id`, `title`, `space_id`, `version`, `author_id`, `created_at`, and `web_url`; `parent_id` appears when the page has a parent. The body is not echoed back.

```jsonl
$ printf '<p>Hello</p>' | confluence page create --space ENG --title "API Design" --body-format storage
{"id":"123456","title":"API Design","space_id":"98765","version":1,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
```

### page update

```text
confluence page update <id|url> --if-version <n> [--title <t>] [--body-format storage|adf] [< body]
```

Update a page under optimistic concurrency. `--if-version` is the version you expect to be current; if the live version differs, the command fails with a recoverable `version_conflict` and writes nothing. On success the page is written at version `n+1`.

Omitting `--title` preserves the current title; omitting the piped body preserves the current body. The row carries `id`, `title`, `version`, `previous_version`, and `web_url`.

```jsonl
$ printf '<p>Revised.</p>' | confluence page update 123456 --if-version 5 --body-format storage
{"id":"123456","title":"API Design","version":6,"previous_version":5,"web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
```

A version mismatch is a fatal error on stderr:

```json
{"error":"version_conflict","detail":"expected current version 5, got 7","hint":"Fetch the latest with 'confluence page get 123456' and retry with --if-version 7","input":"123456"}
```

### page delete

```text
confluence page delete <id|url>... [--yes] [--dry-run]
```

Delete one or more pages. Delete moves pages to the trash; it is not a permanent purge. All arguments must resolve to a single site. A real delete requires `--yes`; without it (and without `--dry-run`) the command refuses with `confirmation_required` and deletes nothing. `--dry-run` previews without deleting.

Each row echoes the `input` that produced it. Deleted rows carry `id`, `deleted:true`, and `delete_mode:"trash"`. Dry-run rows carry `id`, `title`, `version`, `would_delete:true`, and `delete_mode:"trash"`. Per-item failures appear inline and bump `_meta.error_count`.

```jsonl
$ confluence page delete 123456 --dry-run
{"input":"123456","id":"123456","title":"API Design","version":6,"would_delete":true,"delete_mode":"trash"}
{"_meta":{"has_more":false,"error_count":0}}
```

```jsonl
$ confluence page delete 123456 --yes
{"input":"123456","id":"123456","deleted":true,"delete_mode":"trash"}
{"_meta":{"has_more":false,"error_count":0}}
```

### attachment list

```text
confluence attachment list <page id|url>
```

List a page's attachments. The argument is a numeric page id or a page URL. Each row carries `id`, `title`, `media_type`, `download_url`, and `page_id`. `download_url` is absolute.

```jsonl
$ confluence attachment list 123456
{"id":"att987","title":"diagram.png","media_type":"image/png","download_url":"https://acme.atlassian.net/wiki/download/attachments/123456/diagram.png","page_id":"123456"}
{"_meta":{"has_more":false}}
```

### attachment download

```text
confluence attachment download <id> [-o|--out <path>]
```

Download an attachment to a file. The argument is an attachment id (not a URL). Without `--out`, the file is written to the attachment's filename in the current directory. The emitted row carries `id`, `title`, `media_type`, `path`, and `bytes`.

```jsonl
$ confluence attachment download att987 -o ./diagram.png
{"id":"att987","title":"diagram.png","media_type":"image/png","path":"./diagram.png","bytes":48213}
{"_meta":{"has_more":false}}
```

### attachment upload

```text
confluence attachment upload <page id|url> <file>...
```

Upload one or more local files as attachments to a page. Each file is uploaded independently; a failure on one (including a file that can't be opened) is reported inline and does not abort the rest. Re-uploading a file whose name collides with an existing attachment creates a new version of that attachment rather than a duplicate.

Each row echoes the `input` file and carries `page_id`, `attachment_id`, `title`, `media_type`, and `uploaded:true`. Per-item failures bump `_meta.error_count`.

```jsonl
$ confluence attachment upload 123456 ./diagram.png
{"input":"./diagram.png","page_id":"123456","attachment_id":"att987","title":"diagram.png","media_type":"image/png","uploaded":true}
{"_meta":{"has_more":false,"error_count":0}}
```

### search

```text
confluence search <cql> [--limit <n>] [--cursor <c>] [--all]
```

CQL search. The positional argument is the CQL query. This is site-wide: the site comes from `--site` or the single stored default, since there is no URL argument to derive it from. One object per result, then the trailer.

- `--limit` - page size requested from the API (default 25).
- `--cursor` - opaque cursor from a prior `_meta.next_cursor`, to fetch the next page.
- `--all` - loop until the cursor is exhausted, emitting every result. The trailer omits `next_cursor` since nothing remains.

Each row carries `id`, `title`, `type`, `space_key`, `excerpt`, and `url`.

```jsonl
$ confluence search 'type = page AND text ~ "runbook"'
{"id":"123456","title":"Incident Runbook","type":"page","space_key":"ENG","excerpt":"steps to follow during an incident","url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"id":"123457","title":"On-call Runbook","type":"page","space_key":"OPS","excerpt":"paging and escalation runbook","url":"https://acme.atlassian.net/wiki/spaces/OPS/pages/123457"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### comment list

```text
confluence comment list <page id|url> [--footer] [--inline]
```

List a page's comments. The argument is a numeric page id or a page URL. Footer and inline comments are fully drained, so there is no cursor and the trailer never carries `next_cursor`. `--footer` and `--inline` narrow the output to one kind; without either, both are emitted. A missing page is a fatal `page_not_found`.

Each row carries `id`, `kind` (`footer` or `inline`), `body`, `author_id`, `created_at`, and `web_url`.

```jsonl
$ confluence comment list 123456
{"id":"c1","kind":"footer","body":"<p>Looks good to me.</p>","author_id":"a1","created_at":"2024-06-20T14:45:00.000Z","created_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c1"}
{"id":"c2","kind":"inline","body":"<p>Clarify this section.</p>","author_id":"a2","created_at":"2024-06-21T09:00:00.000Z","created_at_iso":"2024-06-21T09:00:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c2"}
{"_meta":{"has_more":false}}
```

### comment add

```text
confluence comment add <page id|url> --body-format storage|adf [< body]
```

Add a footer comment to a page. The body is read from stdin (required) and `--body-format` names its representation. Only footer comments are supported; `--inline` is reserved but not yet implemented and returns `not_implemented`.

The row carries `id`, `page_id`, `kind:"footer"`, `body_format` (the API representation: `storage` or `atlas_doc_format`), and `web_url`.

```jsonl
$ printf '<p>Looks good to me.</p>' | confluence comment add 123456 --body-format storage
{"id":"c1","page_id":"123456","kind":"footer","body_format":"storage","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c1"}
{"_meta":{"has_more":false}}
```

### label list

```text
confluence label list <page id|url>
```

List a page's labels. The argument is a numeric page id or a page URL. Labels are fully drained, so there is no cursor. Each row carries `id`, `name`, and `prefix`.

```jsonl
$ confluence label list 123456
{"id":"l1","name":"runbook","prefix":"global"}
{"id":"l2","name":"on-call","prefix":"global"}
{"_meta":{"has_more":false}}
```

### label add

```text
confluence label add <page id|url> <label>...
```

Add one or more labels to a page. Each label is applied independently; a failure on one is reported inline (with the label as `input`) and does not abort the rest. Success rows carry `page_id`, `name`, `prefix`, and `added:true`. Per-item failures bump `_meta.error_count`.

```jsonl
$ confluence label add 123456 runbook on-call
{"page_id":"123456","name":"runbook","prefix":"global","added":true}
{"page_id":"123456","name":"on-call","prefix":"global","added":true}
{"_meta":{"has_more":false,"error_count":0}}
```

### label remove

```text
confluence label remove <page id|url> <label>...
```

Remove one or more labels from a page. Like add, each removal is independent and a per-label failure is reported inline (with the label as `input`). Success rows carry `page_id`, `name`, and `removed:true`.

```jsonl
$ confluence label remove 123456 runbook
{"page_id":"123456","name":"runbook","removed":true}
{"_meta":{"has_more":false,"error_count":0}}
```

### user current

```text
confluence user current
```

The authenticated user. Emits a single row with `account_id`, `display_name`, and `email`, then the trailer.

```jsonl
$ confluence user current
{"account_id":"a1","display_name":"Ada Lovelace","email":"ada@acme.com"}
{"_meta":{"has_more":false}}
```

### user info

```text
confluence user info <accountId>...
```

Look up one or more users by account id. Each row echoes the `input` that produced it and carries `account_id`, `display_name`, and `email`. Unknown account ids produce an inline `user_not_found` on stdout and bump `_meta.error_count`.

```jsonl
$ confluence user info a1 nope
{"input":"a1","account_id":"a1","display_name":"Ada Lovelace","email":"ada@acme.com"}
{"input":"nope","error":"user_not_found","detail":"No user with account id 'nope'","hint":"confluence user current"}
{"_meta":{"has_more":false,"error_count":1}}
```

## Planned (not yet implemented)

The write surface ships today; two refinements remain designed but not built.

- Markdown body input for writes. Today `page create`, `page update`, and `comment add` accept `storage` or `adf` on stdin. Markdown-in (converted to storage/ADF) is the intended agent-friendly default and is not yet available.
- Inline comments. `comment add` writes footer comments only; `--inline` is reserved and currently returns `not_implemented`.
