# confluence CLI Specification

An agent-first Confluence CLI built in Go. Output is JSONL, one JSON object per line; commands are non-interactive and scriptable. The binary is `confluence`; the repository and Go module are `confluence-cli` (mirroring `slack-cli`, whose binary is `slack`). This document describes the full intended surface. The `version`, `space sync`, `space info`, `space list`, `auth`, `page list`, `page get`, `page children`, `page descendants`, `page ancestors`, `page tree`, `page create`, `page update`, `page move`, `page delete`, `page convert-to-live`, `page restrictions get`, `page restrictions set`, `page restrictions clear`, `folder get`, `folder create`, `folder delete`, `attachment list`, `attachment download`, `attachment upload`, `search`, `comment list`, `comment add`, `label list`, `label add`, `label remove`, `user current`, and `user info` commands ship today (see "Commands available now"). The full surface from the plan is implemented, including Markdown body input for writes and inline comments.

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
- `data` - an error-specific structured object, present only when an error carries structured context (e.g. the blocking comments for `unresolved_inline_comments`). Always an object; not subject to `--fields`.

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
- `--trace` - emit JSON-lines diagnostics to stderr: one event per API request (endpoint, attempt, status, latency) plus retry waits.

## Auth model

API token + email over HTTP Basic auth. No OAuth 3LO; static credentials fit agent and CI use.

Credentials live at `~/.config/confluence-cli/credentials.json`, keyed by canonical site base URL (the `cloud_id` is stored when known, but the URL is the primary selector since agents naturally hold URLs).

Precedence, highest first:

1. Flags (`--site`, and the credential set via `auth login`).
2. `CONFLUENCE_SITE` / `CONFLUENCE_EMAIL` / `CONFLUENCE_API_TOKEN`.
3. `ATLASSIAN_SITE` / `ATLASSIAN_EMAIL` / `ATLASSIAN_TOKEN` (compatibility aliases).
4. Stored credentials.

Site selection derives the target from a URL argument when the command takes one; else `--site`; else the single configured default. A URL-derived site that disagrees with an explicit `--site` is an error.

## Commands available now

The `version`, `space sync`, `space info`, `space list`, `auth`, `page list`, `page get`, `page children`, `page descendants`, `page ancestors`, `page tree`, `page create`, `page update`, `page move`, `page delete`, `page convert-to-live`, `page restrictions get`, `page restrictions set`, `page restrictions clear`, `folder get`, `folder create`, `folder delete`, `attachment list`, `attachment download`, `attachment upload`, `search`, `comment list`, `comment add`, `label list`, `label add`, `label remove`, `user current`, and `user info` commands ship today. All honor `--quiet` and `--timeout`.

### Body input for writes

Write commands that carry page or comment content (`page create`, `page update`, `comment add`) take the body on stdin, never as a flag. `--body-format` names the representation of what you pipe:

`--format` is accepted as an undocumented alias of `--body-format` on every command that takes it (`page get`, `page create`, `page update`, `comment add`). `--body-format` is the canonical name; use it in scripts and docs.

- `storage` - Confluence storage format, a well-formed XHTML fragment (e.g. `<p>Hello</p>`).
- `adf` (alias `atlas_doc_format`) - Atlassian Document Format, a JSON document whose root is `{"type":"doc","version":1,"content":[...]}`.
- `markdown` (alias `md`) - GFM Markdown, converted locally to storage XHTML via goldmark before the API sees it. Fenced code blocks become a plain preformatted block, not a Confluence code macro. Raw HTML embedded in the Markdown is escaped, not passed through.

The body is validated locally before the API sees it; a malformed fragment or ADF doc fails with `invalid_body` and nothing is written.

Feeding one command's output into another needs care: every command's stdout is JSONL with a `_meta` trailer line, so `page get <id> | jq -r .body` emits the body followed by a `null` line (jq's `-r` on the trailer's missing `.body`). Extract a single field with `jq -rs '.[0].body'` or `head -1 | jq -r .body`. As a guard, a body whose last line is exactly `null`, `true`, or `false` fails with `invalid_input` and a hint naming the fix. For `storage` the preceding content must also end with `>` (the artifact follows the last closing tag; `<p>null</p>` is content and passes). `markdown` has no tag to anchor on, so any final line that is exactly one of those words is refused, including a body that is only `null` (an empty page piped through `jq -r`). An `adf` body with the same trailing line fails JSON parsing (`invalid_body`).

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
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":false,"conversion_errors":0}
{"_meta":{"has_more":false}}
```

```jsonl
$ confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs --dry-run
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":true,"conversion_errors":0}
{"_meta":{"has_more":false}}
```

A page whose storage body the converter cannot parse (see "Markdown conversion" under `page get`) is logged to stderr as a warning and skipped: its existing Markdown file, if any, is left in place (and protected from `--prune`), and its children still sync. `conversion_errors` counts those pages; when it is nonzero the trailer carries `error_count` with the same value and the command exits 1, like any bulk command with per-item failures.

```jsonl
$ confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":false,"conversion_errors":1}
{"_meta":{"has_more":false,"error_count":1}}
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
confluence page get <id|url>... [--body-format storage|atlas_doc_format|view|markdown] [--resolve-authors]
```

Fetch one or more pages by numeric id or page URL. All arguments must resolve to a single site (one site per invocation); mixing sites is an error. Each row echoes the `input` that produced it.

`--resolve-authors` adds an `author_name` sibling next to `author_id`, resolved via a best-effort user lookup (one cached call per unique author; a failed lookup omits `author_name` rather than failing the row).

`--body-format` selects the body representation (default `storage`):

- `storage` - Confluence storage format (XHTML), returned as a string.
- `atlas_doc_format` (alias `adf`) - ADF; the `body` field is a nested JSON object, not a string.
- `view` - rendered view HTML, as a string.
- `markdown` (alias `md`) - derived by converting the storage body to GFM, with attachment references resolved to absolute remote URLs. Costs one extra API call per page to list attachments. Adds `source_body_format:"storage"` and sets `body_format:"markdown"`. A storage body the converter cannot parse is an inline `markdown_conversion_failed` error (bumps `_meta.error_count`, exit 1); the body is never silently truncated. See "Markdown conversion" below.

#### Markdown conversion

The same converter serves `page get --body-format markdown` and `space sync`. Confluence-specific elements render as:

- `<ac:link>` - the label is the `ac:link-body` or `ac:plain-text-link-body` text. `ri:page` becomes `[label](Title)` (label defaults to the title; the target is the page title, not a URL). A title containing spaces or parentheses is wrapped so the link stays valid CommonMark: `[label](<Page Title>)`. `ri:user` becomes `@<account-id>` with no lookup (`@unknown-user` when the id is missing). `ri:attachment` becomes `[label](href)` with the href resolved like images (remote URL for `page get`, `_attachments/<file>` for sync). `ri:url` becomes `[label](value)`. A link with no resource (an anchor) emits the label as plain text.
- `<ac:structured-macro>` - a macro with an `ac:rich-text-body` (info, note, panel, expand, ...) has that body converted in place and its parameters dropped. A macro with an `ac:plain-text-body` (code, noformat, ...) becomes a fenced code block using the `language` parameter as the info string. The `status` macro becomes `[status: <title>]` (its lozenge text; `[macro: status]` when the title is missing). Any other macro becomes the placeholder `[macro: <name>]`, or `[macro: <name> <Page Title>]` when an `ri:page` appears among its parameters (excerpt-include, include). No other macro parameter text appears in the output.
- Table cells keep inline formatting, links, and mentions, flattened to one line.

The decoder is non-strict: `<br>`, `<hr>`, and `<img>` may be unclosed, and an element left open is closed at the end of the body. A body that still fails to parse (a stray end tag, a bad entity) is an error; the converter never returns a truncated body.

A `subtype` field appears only when the page is a live doc (value `live`); regular pages omit it. Use this to tell live docs apart and to verify a `page convert-to-live`.

Each row carries `status` (`current`, `draft`, ...). `parent_id` and `parent_type` (`page`, `folder`, `database`) appear when the page has a parent; a space root omits both. Right after a move the parent fields can lag the write by a few minutes server-side; `page children` on the destination reflects the move first.

`page get` reads pages only. A bare id that turns out to be a folder (its own v2 resource) fails inline with `not_a_page` and a hint pointing at `folder get`; a folder URL is rejected locally with `invalid_input`. A bare id that is neither stays `page_not_found`. The folder check costs one extra request and only runs after a page lookup 404s.

Per-item errors (bad id, no permission) appear inline on stdout and bump `_meta.error_count`.

Output is JSONL: a single-id fetch still emits two lines, the row and the `_meta` trailer. To extract one field for a pipeline, use `jq -rs '.[0].body'` or `head -1 | jq -r .body`; bare `jq -r .body` appends a `null` line from the trailer (see "Body input for writes").

```jsonl
$ confluence page get 123456
{"input":"123456","id":"123456","title":"API Design","space_id":"98765","status":"current","parent_id":"123400","parent_type":"page","version":5,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","modified_at":"2024-06-20T14:45:00.000Z","modified_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456","body":"<p>See the design.</p>","body_format":"storage"}
{"_meta":{"has_more":false,"error_count":0}}
```

Markdown, with a failed lookup interleaved:

```jsonl
$ confluence page get 123456 99999 --body-format markdown
{"input":"123456","id":"123456","title":"API Design","space_id":"98765","status":"current","parent_id":"123400","parent_type":"page","version":5,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","modified_at":"2024-06-20T14:45:00.000Z","modified_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456","body":"# API Design\n\nSee the design.","body_format":"markdown","source_body_format":"storage"}
{"input":"99999","error":"page_not_found","detail":"No page with id '99999'","hint":"confluence page list --space ENG"}
{"_meta":{"has_more":false,"error_count":1}}
```

### page children

```text
confluence page children <id|url> [--limit <n>] [--cursor <c>] [--all]
```

List the direct children of a page or a folder: every content type under it (pages, folders, databases, whiteboards, embeds), each tagged with its `type`. The argument is a numeric id, a page URL, or a folder URL (`…/wiki/spaces/KEY/folder/123`); a URL selects the site and fixes the kind. A bare id is looked up as a page first and, only if that returns `page_not_found`, as a folder (one extra request, paid once per invocation); an id that is neither is `page_not_found`. One object per child, then the trailer.

This uses the v2 `direct-children` endpoint. The older `children` endpoint (pages only, no `type`) is deprecated and omitted folders entirely; filter on `type` if you want pages only. Cursors are endpoint-specific, so a `next_cursor` saved from a release before this change will not resume.

- `--limit` - page size requested from the API (default 25).
- `--cursor` - opaque cursor from a prior `_meta.next_cursor`, to fetch the next page.
- `--all` - loop until the cursor is exhausted, emitting every child. The trailer omits `next_cursor` since nothing remains.

Each row carries `id`, `title`, and `type` (`page`, `folder`, `database`, `whiteboard`, `embed`, as the API reports it).

```jsonl
$ confluence page children 123400
{"id":"123456","title":"API Design","type":"page"}
{"id":"777000","title":"Pillars","type":"folder"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

```jsonl
$ confluence page children 777000
{"id":"123457","title":"Database Schema","type":"page"}
{"_meta":{"has_more":false}}
```

### page descendants

```text
confluence page descendants <id|url> [--limit <n>] [--cursor <c>] [--all]
```

List every descendant of a page (all levels below it, not just direct children). The argument is a numeric page id or a page URL; a URL selects the site. One object per descendant, then the trailer.

- `--limit` - page size requested from the API (default 25).
- `--cursor` - opaque cursor from a prior `_meta.next_cursor`, to fetch the next page.
- `--all` - loop until the cursor is exhausted, emitting every descendant. The trailer omits `next_cursor` since nothing remains.

Each row carries `id`, `title`, `type`, and `depth` (1 for a direct child); `parent_id` appears when the API returns it.

```jsonl
$ confluence page descendants 123400
{"id":"123456","title":"API Design","type":"page","depth":1,"parent_id":"123400"}
{"id":"123999","title":"Auth flow","type":"page","depth":2,"parent_id":"123456"}
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
confluence page create --space <key|url> --title <t> [--parent <id|url>] [--body-format storage|adf|markdown] [--live] [--restrict-to-me] [< body]
```

Create a page. `--space` takes a bare space key or a space/page URL; a URL selects the site. `--parent` nests the new page under an existing page or folder, by id or URL (same site as the space); the API accepts either as the parent, and a parent URL selects the site when `--space` is a bare key. The body is read from stdin; `--body-format` is required only when a non-empty body is piped. With no stdin (or an empty/whitespace body), the page is created empty and `--body-format` may be omitted. `--live` creates a live doc (subtype `live`) instead of a regular page. `--restrict-to-me` restricts view and edit to the authenticated user immediately after creation (see `page restrictions set --only-me`); the caller is resolved before the page is created, so a failed lookup creates nothing. If the restriction write itself fails, the page exists unrestricted and the error's `data` carries its `id` and `web_url`.

The row carries `id`, `title`, `space_id`, `version`, `author_id`, `created_at`, and `web_url`; `parent_id` appears when the page has a parent, `subtype` appears when the server returns one (for example `live`), and `restricted:true` appears with `--restrict-to-me`. The body is not echoed back.

```jsonl
$ printf '<p>Hello</p>' | confluence page create --space ENG --title "API Design" --body-format storage
{"id":"123456","title":"API Design","space_id":"98765","version":1,"author_id":"a1","created_at":"2024-03-01T00:00:00.000Z","created_at_iso":"2024-03-01T00:00:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
```

### page update

```text
confluence page update <id|url> --if-version <n> [--title <t>] [--body-format storage|adf|markdown] [--allow-unresolved-inline-comments] [< body]
```

Update a page under optimistic concurrency. `--if-version` is the version you expect to be current; if the live version differs - either at the preflight read or as a PUT-time 409 from a concurrent edit - the command fails with a recoverable `version_conflict` and writes nothing. On success the page is written at version `n+1`.

Passing neither `--title` nor a body is rejected with `invalid_input`. Omitting `--title` preserves the current title; omitting the piped body preserves the current body as its exact ADF (not a lossy storage round-trip) and preserves the current status. A title-only update whose title equals the current title is a no-op: the row carries `updated:false`, the version does not change, and no write is issued. The row carries `id`, `title`, `version`, `previous_version`, `updated`, and `web_url`.

```jsonl
$ printf '<p>Revised.</p>' | confluence page update 123456 --if-version 5 --body-format storage
{"id":"123456","title":"API Design","version":6,"previous_version":5,"updated":true,"web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
```

A version mismatch is a fatal error on stderr:

```json
{"error":"version_conflict","detail":"expected current version 5, got 7","hint":"Fetch the latest with 'confluence page get 123456' and retry with --if-version 7","input":"123456"}
```

#### Inline-comment guard

A `page update` that pipes a NEW body destroys the anchors of open inline comments (they are markers inside the body). Before such a write, `page update` drains the page's inline comments and refuses with `unresolved_inline_comments` if any is not exactly `resolved` (`open`, `reopened`, `dangling`, and any unknown status all block). The refusal writes nothing, does not bump the version, and carries the blocking comments in the error's `data` object. The check fails closed: if the comments cannot be inspected, the write is refused. `--allow-unresolved-inline-comments` skips the inspection entirely and proceeds; it does not bypass `--if-version` or any other check. A title-only `page update` and a same-space `page move` re-send the page's exact ADF (verified against a live page to preserve anchors), and a cross-space `page move` never touches the body, so they are not guarded.

```json
{"error":"unresolved_inline_comments","detail":"refusing to replace the page body: 1 inline comment(s) are not resolved and this write may destroy their anchors","hint":"Resolve or re-anchor the comments in Confluence (see the web URLs), then retry. Pass --allow-unresolved-inline-comments to override.","input":"123456","data":{"page_id":"123456","blocking_comment_count":1,"blocking_comments":[{"id":"c9","resolution_status":"open","original_selection":"the retry budget","inline_marker_ref":"m1","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c9"}]}}
```

### page move

```text
confluence page move <id|url> --parent <id|url> --if-version <n>
```

Move a page under a different parent, in the same space or in another space, preserving the page ID, history, comments, and attachments. The destination may be a page or a folder (bare id, page URL, or folder URL); a bare id is tried as a page first and as a folder only when the page lookup returns `page_not_found`. Both refs must resolve to one site. `--if-version` guards the source page's version as in `page update`.

Self-parenting and a destination that is a descendant of the source (a cycle) are refused with `invalid_move`. If the page is already under the destination parent, the row carries `moved:false` and no write is issued.

A same-space move is a v2 update that re-sends the page's exact ADF and changes only the parent. It bumps the version, and a concurrent edit fails at write time with `version_conflict`. It does not disturb inline-comment anchors and is not subject to the inline-comment guard.

A cross-space move (destination parent in another space) or a move into a folder (any space) uses the v1 move endpoint, the only public API for either (the v2 update endpoint refuses with "Only DRAFT pages can be moved between spaces" and only takes a page as `parentId`). It does not touch the body and does not bump the version, so `version` equals `previous_version` and `--if-version` is enforced only by the preflight read, not at write time. Attachments and comments move with the page. Whether page restrictions survive a cross-space move has not been verified; check them in Confluence afterwards if they matter. The page's URL changes to the destination space.

The row carries `id`, `title`, `previous_space_id`, `space_id`, `previous_parent_id`, `parent_id`, `parent_type` (`page` or `folder`), `previous_version`, `version`, `moved`, and `web_url`. `web_url` is the pre-move URL for a cross-space move (the response does not include the new one); `page get` returns the current URL.

```jsonl
$ confluence page move 123456 --parent 200000 --if-version 5
{"id":"123456","title":"API Design","previous_space_id":"98765","space_id":"98765","previous_parent_id":"100000","parent_id":"200000","parent_type":"page","previous_version":5,"version":6,"moved":true,"web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
```

Cross-space (version unchanged):

```jsonl
$ confluence page move 123456 --parent 300000 --if-version 5
{"id":"123456","title":"API Design","previous_space_id":"98765","space_id":"55555","previous_parent_id":"100000","parent_id":"300000","parent_type":"page","previous_version":5,"version":5,"moved":true,"web_url":"https://acme.atlassian.net/wiki/spaces/~user/pages/123456"}
{"_meta":{"has_more":false}}
```

Into a folder (v1 move, version unchanged):

```jsonl
$ confluence page move 123456 --parent 777000 --if-version 5
{"id":"123456","title":"API Design","previous_space_id":"98765","space_id":"98765","previous_parent_id":"100000","parent_id":"777000","parent_type":"folder","previous_version":5,"version":5,"moved":true,"web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"_meta":{"has_more":false}}
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

### page convert-to-live

```text
confluence page convert-to-live <id|url>...
```

Convert one or more existing pages to live docs. All arguments must resolve to a single site.

There is no public Confluence API for this conversion. The command calls Confluence's undocumented internal `/cgraphql` endpoint (operation `convertPageToLiveEditAction`). This dependency is unsupported: Atlassian may change or remove it without notice, it is reported to be blocked from Atlassian's automation IP ranges (it works with API-token auth from a developer machine), and there is no supported API to convert a live doc back to a page. The command writes one warning to stderr before the first conversion; `--quiet` suppresses it.

Each row echoes the `input` that produced it and carries `id` and `converted:true`. `converted:true` means the mutation reported success; the command does not read the page back, so it does not report `subtype`. Verify with `confluence page get <id>` and check the `subtype` field. Per-item failures appear inline as `live_convert_failed` and bump `_meta.error_count`; transport and non-2xx failures (401, 403, 404) keep their normal error codes.

```jsonl
$ confluence page convert-to-live 123456
warning: convert-to-live uses an undocumented Atlassian endpoint and has no supported API undo; it may change or be blocked without notice.
{"input":"123456","id":"123456","converted":true}
{"_meta":{"has_more":false,"error_count":0}}
```

(The warning line above is written to stderr, not stdout.)

### page restrictions get

```text
confluence page restrictions get <page id|url>
```

Show a page's direct view/edit restrictions. Emits exactly two rows, `read` (who may view) then `update` (who may edit), each with `page_id`, `operation`, `users` (`account_id`, `display_name`) and `groups` (`id`, `name`). Empty lists mean the operation is unrestricted and are emitted as `[]`. Inherited restrictions from ancestors are not included. Nested user/group pagination is drained, so there is no cursor.

Uses the v1 endpoint `/wiki/rest/api/content/{id}/restriction`. The v2 API splits restrictions into a latent `permissions` list plus a `restriction-state` flag, identifies groups by id only, and returns no display names, so v1 is used throughout.

```jsonl
$ confluence page restrictions get 123456
{"page_id":"123456","operation":"read","users":[{"account_id":"a1","display_name":"Ada Lovelace"}],"groups":[{"id":"g-1","name":"eng-leads"}]}
{"page_id":"123456","operation":"update","users":[{"account_id":"a1","display_name":"Ada Lovelace"}],"groups":[]}
{"_meta":{"has_more":false}}
```

### page restrictions set

```text
confluence page restrictions set <page id|url> [--read-user <accountId>]... [--read-group <name>]... [--update-user <accountId>]... [--update-group <name>]... [--only-me]
```

Replace a page's restrictions with exactly the principals given. The command is declarative: both operations are always written, so an operation with no flags is cleared (for example `--update-user a1` alone makes the page editable only by `a1` and viewable by anyone with space access). Users are account ids, groups are group names. `--only-me` resolves the authenticated user and restricts both view and edit to that account; it cannot be combined with the other principal flags. No flags at all is `invalid_input` (use `page restrictions clear` to remove restrictions). Output is the final state, read back after the write, in the same two rows as `page restrictions get`.

Confluence refuses a write that would remove the caller's own view or edit access (including when the caller is only covered by a group); this surfaces as `self_lockout`. Other server rejections (unknown account id, unknown group name) surface as `api_error` with the server message in `detail`.

```jsonl
$ confluence page restrictions set 123456 --only-me
{"page_id":"123456","operation":"read","users":[{"account_id":"a1","display_name":"Ada Lovelace"}],"groups":[]}
{"page_id":"123456","operation":"update","users":[{"account_id":"a1","display_name":"Ada Lovelace"}],"groups":[]}
{"_meta":{"has_more":false}}
```

### page restrictions clear

```text
confluence page restrictions clear <page id|url>
```

Remove every direct restriction from a page. Emits `page_id` and `cleared:true`.

```jsonl
$ confluence page restrictions clear 123456
{"page_id":"123456","cleared":true}
{"_meta":{"has_more":false}}
```

### folder get

```text
confluence folder get <id|url>...
```

Fetch one or more folders by numeric id or folder URL (`…/wiki/spaces/KEY/folder/123`). All arguments must resolve to one site. Each row echoes `input` and carries `id`, `type:"folder"`, `title`, `space_id`, `status`, `version`, `author_id`, `created_at`, `modified_at`, and `web_url`; `parent_id` and `parent_type` (`page` or `folder`) appear when the folder has a parent. Folders have no body. A trashed folder still resolves, with `status:"trashed"` and no parent. A page URL is rejected locally with `invalid_input`. Per-item errors (`folder_not_found`, no permission) appear inline and bump `_meta.error_count`.

```jsonl
$ confluence folder get 777000
{"input":"777000","id":"777000","type":"folder","title":"Pillars","space_id":"98765","status":"current","parent_id":"123400","parent_type":"page","version":1,"author_id":"a1","created_at":"2026-09-28T20:14:30.759Z","created_at_iso":"2026-09-28T20:14:30Z","modified_at":"2026-09-28T20:14:30.759Z","modified_at_iso":"2026-09-28T20:14:30Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/folder/777000"}
{"_meta":{"has_more":false,"error_count":0}}
```

### folder create

```text
confluence folder create --space <key|url> --title <t> [--parent <id|url>]
```

Create a folder. `--space` takes a bare space key or a space/page URL; a URL selects the site. `--parent` nests the folder under an existing page or folder (same site as the space); omit it for the space root. No body. The row has the `folder get` shape without `input`.

```jsonl
$ confluence folder create --space ENG --title Pillars --parent 123400
{"id":"777000","type":"folder","title":"Pillars","space_id":"98765","status":"current","parent_id":"123400","parent_type":"page","version":1,"author_id":"a1","created_at":"2026-09-28T20:14:30.759Z","created_at_iso":"2026-09-28T20:14:30Z","modified_at":"2026-09-28T20:14:30.759Z","modified_at_iso":"2026-09-28T20:14:30Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/folder/777000"}
{"_meta":{"has_more":false}}
```

### folder delete

```text
confluence folder delete <id|url>... [--yes] [--dry-run]
```

Delete one or more folders. Delete moves folders to the trash; it is not a permanent purge. Same contract as `page delete`: a real delete requires `--yes`; without it (and without `--dry-run`) the command refuses with `confirmation_required` and deletes nothing. `--dry-run` previews. Rows echo `input`; deleted rows carry `id`, `deleted:true`, `delete_mode:"trash"`, dry-run rows carry `id`, `title`, `version`, `would_delete:true`, `delete_mode:"trash"`. Per-item failures appear inline and bump `_meta.error_count`. What happens to a folder's children on delete is not verified; empty the folder first.

```jsonl
$ confluence folder delete 777000 --yes
{"input":"777000","id":"777000","deleted":true,"delete_mode":"trash"}
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

Each row carries `id`, `title`, `type`, `space_key`, `excerpt`, and `url`. `space_key` is the space's key (the value CQL `space = ...` and `--space` accept). `url` is returned verbatim and uses the space alias, which can differ from the key.

```jsonl
$ confluence search 'type = page AND text ~ "runbook"'
{"id":"123456","title":"Incident Runbook","type":"page","space_key":"ENG","excerpt":"steps to follow during an incident","url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"}
{"id":"123457","title":"On-call Runbook","type":"page","space_key":"OPS","excerpt":"paging and escalation runbook","url":"https://acme.atlassian.net/wiki/spaces/OPS/pages/123457"}
{"_meta":{"has_more":true,"next_cursor":"eyJpZCI6..."}}
```

### comment list

```text
confluence comment list <page id|url> [--footer] [--inline] [--resolution-status <s>] [--replies] [--resolve-authors]
```

List a page's comments. The argument is a numeric page id or a page URL. Footer and inline comments are fully drained, so there is no cursor and the trailer never carries `next_cursor`. `--footer` and `--inline` narrow the output to one kind; without either, both are emitted. A missing page is a fatal `page_not_found`.

`--resolution-status open|reopened|resolved|dangling` filters inline comments by resolution status (server-side). It implies `--inline` and conflicts with `--footer` (an invalid combination or an unknown value fails with `invalid_input` before any network call).

`--resolve-authors` adds an `author_name` sibling next to `author_id`, resolved via a best-effort user lookup (one cached call per unique author; a failed lookup omits `author_name`).

Each row carries `id`, `kind` (`footer` or `inline`), `body`, `author_id`, `created_at`, and `web_url`. Inline rows also carry `resolution_status`, `original_selection` (the anchored text), and `inline_marker_ref`; footer rows omit these three.

With `--replies`, each comment's reply thread is drained recursively (comment threads are bounded) and emitted after its parent. Reply rows carry an additional `parent_id` pointing at their immediate parent comment; top-level comments omit it.

```jsonl
$ confluence comment list 123456
{"id":"c1","kind":"footer","body":"<p>Looks good to me.</p>","author_id":"a1","created_at":"2024-06-20T14:45:00.000Z","created_at_iso":"2024-06-20T14:45:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c1"}
{"id":"c2","kind":"inline","body":"<p>Clarify this section.</p>","author_id":"a2","created_at":"2024-06-21T09:00:00.000Z","created_at_iso":"2024-06-21T09:00:00Z","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c2"}
{"_meta":{"has_more":false}}
```

```jsonl
$ confluence comment list 123456 --footer --replies
{"id":"c1","kind":"footer","body":"<p>Looks good to me.</p>","author_id":"a1","created_at":"2024-06-20T14:45:00.000Z","created_at_iso":"2024-06-20T14:45:00Z","web_url":"..."}
{"id":"r1","kind":"footer","parent_id":"c1","body":"<p>Agreed.</p>","author_id":"a3","created_at":"2024-06-20T15:00:00.000Z","created_at_iso":"2024-06-20T15:00:00Z","web_url":"..."}
{"_meta":{"has_more":false}}
```

### comment add

```text
confluence comment add <page id|url> --body-format storage|adf|markdown [< body]
confluence comment add <page id|url> --inline --selection-text <text> [--match-index N] [--match-count N] --body-format storage|adf|markdown [< body]
```

Add a comment to a page. The body is read from stdin (required) and `--body-format` names its representation (`storage`, `adf`, or `markdown`). Without `--inline` the comment is a footer comment; the row carries `id`, `page_id`, `kind:"footer"`, `body_format` (the API representation: `storage` or `atlas_doc_format`), and `web_url`.

```jsonl
$ printf '<p>Looks good to me.</p>' | confluence comment add 123456 --body-format storage
{"id":"c1","page_id":"123456","kind":"footer","body_format":"storage","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c1"}
{"_meta":{"has_more":false}}
```

`--inline` anchors the comment to on-page text. `--selection-text` is the exact page text to attach to. When that text occurs more than once, `--match-index N` (1-based) selects which occurrence; `--match-count N` asserts the expected number of occurrences and fails if the live count differs. Inline rows carry `kind:"inline"` and `selection_text`.

```jsonl
$ printf '<p>Clarify this.</p>' | confluence comment add 123456 --inline --selection-text "the retry budget" --body-format storage
{"id":"c2","page_id":"123456","kind":"inline","selection_text":"the retry budget","body_format":"storage","web_url":"https://acme.atlassian.net/wiki/spaces/ENG/pages/123456?focusedCommentId=c2"}
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

## Implementation status

The full surface described above is implemented, including Markdown body input for writes (`--body-format markdown`) and inline comments (`comment add --inline`).
