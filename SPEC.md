# confluence CLI Specification

An agent-first Confluence CLI built in Go. Output is JSONL, one JSON object per line; commands are non-interactive and scriptable. The binary is `confluence`; the repository and Go module stay `confluence-sync` (mirroring `slack`→`slack-cli`). This document describes the full intended surface. The `version`, `space sync`, `space info`, `auth`, `page list`, `page get`, `attachment list`, and `attachment download` commands ship today (see "Commands available now"); everything under "Planned commands" is designed but not yet implemented.

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

The `version`, `space sync`, `space info`, `auth`, `page list`, `page get`, `attachment list`, and `attachment download` commands ship today. All honor `--quiet` and `--timeout`.

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

## Planned commands (not yet implemented)

Everything below is designed but not built. Do not invoke these yet; they are documented so the surface is settled before implementation. They land across phases (`page` first, then attachments/space, then the wider read surface, then writes).

### Page (read)

- `confluence page children <id|url>` - direct children of a page. Not yet available.
- `confluence page ancestors <id|url>` - ancestor chain of a page. Not yet available.
- `confluence page tree --space <key|url>` - hierarchical page tree, pending acceptable ordering semantics. Not yet available.

### Space (read)

- `confluence space list` - list accessible spaces. Not yet available.

### Attachment

- `confluence attachment upload <page> <file>...` - upload files to a page. Not yet available.

### Search, comments, labels, users (read)

- `confluence search <cql>` - CQL search, with a `--text` convenience later. Not yet available.
- `confluence comment list <page>` - footer and inline comments. Not yet available.
- `confluence label list <page>` - labels on a page. Not yet available.
- `confluence user current` - the authenticated user. Not yet available.
- `confluence user info <accountId>...` - look up users by account id. Not yet available.

### Writes

Writing is a headline feature and the hardest part. Body content comes from stdin or a file (Markdown-in as the agent-friendly default, converted to storage/ADF; ADF/storage passthrough as the fidelity escape hatch), with local shape validation before the API sees it and optimistic concurrency on updates. None of these are available yet.

- `confluence page create --space <key|url> --title <t> [--parent <id|url>] [< body]` - create a page. Not yet available.
- `confluence page update <id|url> --if-version <n> [--title <t>] [< body]` - update a page; version mismatch is a distinct recoverable error. Not yet available.
- `confluence page delete <id|url>...` - delete pages (guarded). Not yet available.
- `confluence comment add <page> [--inline] [< body]` - add a footer or inline comment. Not yet available.
- `confluence label add|remove <page> <label>...` - add or remove labels. Not yet available.
