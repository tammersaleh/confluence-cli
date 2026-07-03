# confluence CLI Specification

An agent-first Confluence CLI built in Go. Output is JSONL, one JSON object per line; commands are non-interactive and scriptable. The binary is `confluence`; the repository and Go module stay `confluence-sync` (mirroring `slack`→`slack-cli`). This document describes the full intended surface. Only two commands ship today (see "Commands available now"); everything under "Planned commands" is designed but not yet implemented.

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

Two commands ship today. Both honor `--quiet` and `--timeout`.

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

## Planned commands (not yet implemented)

Everything below is designed but not built. Do not invoke these yet; they are documented so the surface is settled before implementation. They land across phases (auth and `page` first, then attachments/space, then the wider read surface, then writes).

### Auth

- `confluence auth login --site <url> --email <e>` - store credentials (token via prompt/stdin, never argv). Not yet available.
- `confluence auth status` - report the active site and credential source. Not yet available.
- `confluence auth logout` - remove stored credentials for a site. Not yet available.

### Page (read)

- `confluence page get <id|url>...` - fetch pages; `--body-format storage|adf|view|markdown` (markdown is a derived representation). Not yet available.
- `confluence page list --space <key|url>` - list pages in a space; `--limit --cursor --all`. Not yet available.
- `confluence page children <id|url>` - direct children of a page. Not yet available.
- `confluence page ancestors <id|url>` - ancestor chain of a page. Not yet available.
- `confluence page tree --space <key|url>` - hierarchical page tree, pending acceptable ordering semantics. Not yet available.

### Space (read)

- `confluence space list` - list accessible spaces. Not yet available.
- `confluence space info <key|id|url>...` - metadata for one or more spaces. Not yet available.

### Attachment

- `confluence attachment list <page>` - list a page's attachments. Not yet available.
- `confluence attachment download <id|url>` - download an attachment. Not yet available.
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
