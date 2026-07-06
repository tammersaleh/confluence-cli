# confluence-cli

Agent-first Confluence CLI. The binary is `confluence`; the repository and Go
module are `confluence-cli` (mirroring `slack-cli`, whose binary is `slack`). Output is JSONL,
one JSON object per line; commands are non-interactive and scriptable. Built on
Kong. Auth is an API token + email over HTTP Basic.

`SPEC.md` is the source of truth for the full command surface and output
contract. The read and write surface ships today: `version`, `space sync`,
`space info`, `space list`, `auth`, `page list`, `page get`, `page children`,
`page ancestors`, `page tree`, `page create`, `page update`, `page delete`,
`attachment list`, `attachment download`, `attachment upload`, `search`,
`comment list`, `comment add`, `label list`, `label add`, `label remove`,
`user current`, and `user info`. The full command surface is implemented. See
`README.md` for user-facing usage and `skills/confluence-cli/SKILL.md` for the
agent-facing skill.

## Architecture

```
cmd/confluence/main.go   Kong entry point. Parses, runs, maps errors to exit codes.
internal/
  cli/
    root.go                    CLI struct + global flags, Context(), NewPrinter, ResolveCredentials, ClassifyError.
    version.go                 `version` command.
    space.go                   `space sync` (wraps the sync engine), `space info`, and `space list` commands.
    auth.go                    `auth login|status|logout` commands.
    page.go                    `page list/get/children/ancestors/tree` (reads) + `page create/update/delete` (writes). Derives site/space from --space or URL args; --body-format incl. derived markdown on read. `page tree` builds the hierarchy in-command, sorted by ID (not display order). `page update` preflights GetPage to enforce --if-version (version_conflict) and preserve title/body when not supplied.
    attachment.go              `attachment list`, `attachment download`, `attachment upload` commands.
    search.go                  `search <cql>` command. CQL positional, site-wide, paginated (--limit/--cursor/--all).
    comment.go                 `comment list` + `comment add` (footer, or inline via --inline --selection-text with --match-index/--match-count).
    label.go                   `label list`, `label add`, `label remove`. Add/remove are per-label (independent, inline errors).
    user.go                    `user current` and `user info <accountId>...` commands.
  output/
    output.go                  Printer (JSONL rows + _meta trailer), Meta, Error, ExitError, exit codes, field filtering.
    errors.go                  Error.AsItem for inline per-item errors.
    json.go                    Timestamp enrichment (*_iso siblings).
  auth/
    credentials.go             Credentials store keyed by canonical site URL. Env precedence + merge.
  httpx/
    errors.go                  RateLimitError + generic ClassifyError (domain-agnostic).
    trace.go                   Tracer interface + JSON-lines tracer; the confluence client emits per-request events.
  confluence/
    client.go                  HTTP client for Confluence Cloud API v2. Retry w/ exponential backoff + jitter. Sentinels (ErrUnauthorized, ErrSpaceNotFound, ...) mapped to structured errors in internal/cli.
    types.go                   API response types: Space, Page, PageContent, Attachment.
    url.go                     Parses Confluence space URLs into baseURL + spaceKey.
  confluenceurl/
    confluenceurl.go           Parses page/space URLs into a Ref (Kind, BaseURL, SpaceKey, PageID). CommonSite enforces one-site-per-invocation for page get.
  bodywrite/
    bodywrite.go               Write-command body input: Read (stdin, TTY vs pipe) + Prepare (validate storage XHTML or ADF locally, or convert markdown to storage via goldmark, return representation+value). invalid bodies fail before the API sees them. Markdown fenced code becomes a plain preformatted block, not a code macro; raw HTML is escaped.
  converter/
    converter.go               Converts Confluence storage format (XHTML) to GFM Markdown.
  sync/
    sync.go                    Orchestrates the sync: fetches pages, builds tree, walks it writing files.
    tree.go                    Converts flat page list to parent-child tree. Orphans become roots.
  filesystem/
    fs.go                      FileSystem interface (MkdirAll, WriteFile, ReadFile, RemoveAll, ReadDir).
    memory.go                  In-memory implementation for tests.
pkg/sanitize/
  sanitize.go                  Converts page titles to filesystem-safe names. Handles collisions.
```

### Data flow (space sync)

1. `space.go` parses the space URL, resolves credentials, builds a `confluence.Client` and `filesystem.OS`, injects them into `sync.Syncer`.
2. `Syncer.Sync()` calls `client.GetSpace()` then `client.GetPages()` (paginated, fetches all pages).
3. For each page, it calls `client.GetPageContent()` and `getPageParent()` separately - the list endpoint doesn't reliably return parent IDs or types.
4. `GetPages()` resolves non-page parents (databases, folders) by fetching them via `GetContentParent()` and adding them as synthetic nodes. Chains are followed until all parents resolve to known nodes or root.
5. `tree.BuildTree()` assembles pages into a hierarchy using parent IDs. Nodes sorted by ID for deterministic output.
6. `syncNode()` recursively walks the tree:
   - Database/folder nodes (`Type == "database"` or `"folder"`) create directories only - no markdown, no content fetch
   - Page nodes convert content via `converter.ConvertWithOptions()`
   - Writes Markdown files (with YAML frontmatter including a `version` field)
   - Downloads attachments to `_attachments/` directories
7. On subsequent runs, version in frontmatter is compared to skip unchanged pages.
8. With `--prune`, a manifest of expected paths is built during the walk. After syncing, `pruneStaleFiles` walks the output directory and removes anything not in the manifest. `_attachments/` dirs of version-skipped pages are protected (not descended into) since their attachment list wasn't re-fetched.

### Key interfaces

Both are in `internal/` and injected into `Syncer`:

- `confluence.Client` - API operations (GetSpace, GetPages, GetPageContent, GetAttachments, DownloadAttachment, GetContentParent)
- `filesystem.FileSystem` - file I/O (MkdirAll, WriteFile, ReadFile, RemoveAll, ReadDir)

### Non-obvious behaviors

- Pages with children OR attachments become directories with `index.md`. Leaf pages without attachments are plain `.md` files.
- Database and folder parents (from Confluence's v2 API `parentType` field) are resolved into directory-only nodes. They create subdirectories but produce no markdown files.
- Broken attachments are logged as warnings and skipped - they don't abort the sync.
- Filename collisions (after sanitization) get `-2`, `-3`, etc. suffixes. Collision tracking is per-namespace (shared for roots, per-parent for non-roots).
- The converter handles Confluence-specific XHTML elements like `ac:structured-macro` (code blocks), `ac:image`, `ri:attachment`, and rewrites attachment URLs to local `_attachments/` paths.
- The converter uses Go's `xml.Decoder` with `AutoClose` for self-closing HTML elements - not an HTML parser. Malformed HTML will behave differently than you'd expect from a browser.
- `--prune` protects `_attachments/` dirs of version-skipped pages. Since the attachment list wasn't re-fetched, stale attachments in those dirs won't be cleaned until the page version bumps.

## Output & error contract

Identical to the sibling `slack-cli`. Full detail in `SPEC.md`.

- Data rows: JSONL to stdout, one JSON object per line, `snake_case` fields.
- Every command ends with a `_meta` trailer, always present: `{"_meta":{"has_more":false}}`. List commands carry `next_cursor`; bulk commands carry `error_count`.
- Info/bulk commands echo an `input` field per row; recoverable per-item errors appear inline on stdout and bump `_meta.error_count` (via `Error.AsItem`).
- Fatal errors go to stderr as a single JSON object with `error` (stable code), `detail`, `hint` (a concrete recovery command), and where relevant `input` and `endpoint`.
- Timestamp enrichment: fields named `*_ts`, `created_at`, or `modified_at` gain an `*_iso` sibling (Confluence already returns ISO 8601, so mostly passthrough; kept for consistency).

### Exit codes

- `0` - success.
- `1` - general error, including partial failure in a bulk command.
- `2` - authentication error.
- `3` - rate limited, after exhausting retries.
- `4` - network error.

`ExitError` carries an exit code without printing to stderr, for partial-failure
commands whose per-item errors are already on stdout. This scheme REPLACED the
old Cobra tool's `1 = config / 2 = auth / 3 = api / 4 = filesystem` codes
(breaking change).

## Commands

The `version`, `space sync`, `space info`, `space list`, `auth`, `page list`, `page get`, `page children`, `page ancestors`, `page tree`, `page create`, `page update`, `page delete`, `attachment list`, `attachment download`, `attachment upload`, `search`, `comment list`, `comment add`, `label list`, `label add`, `label remove`, `user current`, and `user info` commands ship today. All honor `--quiet` and `--timeout`. Write commands that carry content (`page create`, `page update`, `comment add`) take the body on stdin with `--body-format storage|adf|markdown` (validated locally via `internal/bodywrite` before the API sees it; `markdown` is converted to storage XHTML via goldmark, where fenced code becomes a plain preformatted block rather than a code macro and raw HTML is escaped).

- `confluence version` - emits the build version, then the trailer.
- `confluence space sync <space-url> <output-dir>` - one-way space crawl to local Markdown. Flags: `--prune`, `--dry-run`, `--quiet`, `--timeout`, `--trace`. Progress goes to stderr; stdout carries a single summary object then the trailer.
- `confluence space info <key|id|url>...` - metadata for one or more spaces (one site per invocation). Rows echo `input` and carry `id`, `key`, `name`, `type`, `status`, `homepage_id`. Per-item errors go inline and bump `_meta.error_count`.
- `confluence space list` - list spaces on the site (from `--site` or the single stored default). Flags: `--limit`, `--cursor`, `--all`. Rows carry `id`, `key`, `name`, `type`, `status`, `homepage_id`.
- `confluence auth login --site <url> --email <email>` - read the API token from stdin, validate it, and store it under the canonical site URL. Token never comes from argv.
- `confluence auth status` - list configured sites and any active env override, without printing secrets.
- `confluence auth logout [<site>]` - remove stored credentials for a site. The `<site>` positional is optional when exactly one site is configured.
- `confluence page list --space <key|url>` - list pages in a space. Flags: `--limit`, `--cursor`, `--all`. Rows carry `id`, `title`, `type`, `space_key`, and `parent_id`/`parent_type` when present.
- `confluence page get <id|url>...` - fetch pages by id or URL (one site per invocation). `--body-format storage|atlas_doc_format (adf)|view|markdown (md)`, default `storage`. ADF `body` is a nested object; `markdown` is derived from storage (attachments resolved to remote URLs) and adds `source_body_format`. Per-item errors go inline and bump `_meta.error_count`.
- `confluence page children <id|url>` - direct children of a page. Flags: `--limit`, `--cursor`, `--all`. Rows carry `id`, `title`, `type`.
- `confluence page ancestors <id|url>` - ancestor chain, root-most first. Not paginated. Rows carry `id`, `type` (the endpoint omits `title`).
- `confluence page tree --space <key|url>` - space page hierarchy in DFS order. Rows carry `id`, `title`, `type`, `depth`, and `parent_id` when present. Siblings sorted by ID (deterministic, not Confluence display order).
- `confluence page create --space <key|url> --title <t> [--parent <id|url>] [--body-format storage|adf|markdown]` - create a page; body on stdin (`--body-format` required only for a non-empty body; empty stdin = empty page). Row: `id`, `title`, `space_id`, `parent_id?`, `version`, `author_id`, `created_at`, `web_url`. Body not echoed.
- `confluence page update <id|url> --if-version <n> [--title <t>] [--body-format storage|adf|markdown]` - update under optimistic concurrency; body on stdin. A live-version mismatch fails with `version_conflict` and writes nothing. Title-only preserves body; body-only preserves title. Row: `id`, `title`, `version`, `previous_version`, `web_url`.
- `confluence page delete <id|url>...` - move pages to trash (not purge). `--yes` required for a real delete; without it (and without `--dry-run`) fails with `confirmation_required`. `--dry-run` previews. Rows echo `input`; per-item errors bump `_meta.error_count`.
- `confluence attachment list <page id|url>` - list a page's attachments. Rows carry `id`, `title`, `media_type`, `download_url` (absolute), `page_id`.
- `confluence attachment download <id> [-o|--out <path>]` - download an attachment by id (not a URL) to a file; defaults to the attachment filename in the current dir. Emits `id`, `title`, `media_type`, `path`, `bytes`.
- `confluence attachment upload <page id|url> <file>...` - upload files as attachments (per-file, inline errors). A filename collision creates a new version. Rows echo `input` and carry `page_id`, `attachment_id`, `title`, `media_type`, `uploaded:true`.
- `confluence search <cql>` - CQL search. The CQL is the positional arg; site-wide (from `--site` or the single stored default). Flags: `--limit`, `--cursor`, `--all`. Rows carry `id`, `title`, `type`, `space_key`, `excerpt`, `url`.
- `confluence comment list <page id|url>` - a page's comments. Drains footer + inline (no cursor); `--footer`/`--inline` narrow to one kind. Rows carry `id`, `kind` (`footer`/`inline`), `body`, `author_id`, `created_at`, `web_url`. A missing page is a fatal `page_not_found`.
- `confluence comment add <page id|url> --body-format storage|adf|markdown` - add a comment; body on stdin (required). Footer by default; `--inline --selection-text <text> [--match-index N] [--match-count N]` anchors an inline comment to on-page text. Row: `id`, `page_id`, `kind` (`footer`/`inline`), `body_format`, `web_url`; inline rows also carry `selection_text`.
- `confluence label list <page id|url>` - a page's labels, fully drained. Rows carry `id`, `name`, `prefix`.
- `confluence label add <page id|url> <label>...` - add labels (per-label, inline errors). Rows carry `page_id`, `name`, `prefix`, `added:true`.
- `confluence label remove <page id|url> <label>...` - remove labels (per-label, inline errors). Rows carry `page_id`, `name`, `removed:true`.
- `confluence user current` - the authenticated user. A single row with `account_id`, `display_name`, `email`.
- `confluence user info <accountId>...` - look up users by account id. Rows echo `input` and carry `account_id`, `display_name`, `email`. Unknown ids go inline as `user_not_found` and bump `_meta.error_count`.

The full command surface is implemented. See `SPEC.md`.

### Global flags

`--site`, `--fields`, `--quiet`, `--timeout`, `--trace`. `Context()` on the CLI
threads `--timeout` and `--trace` into a context every command's `Run` uses;
`--trace` attaches a JSON-lines tracer to the context; the confluence client
emits a `request` event per attempt (endpoint, status, latency) and a
`retry_wait` event on backoff.

## Auth model

API token + email over HTTP Basic. No OAuth 3LO; static credentials fit agent
and CI use.

Credentials live at `~/.config/confluence-cli/credentials.json`, keyed by
canonical site base URL. Precedence, highest first:

1. Flags (`--site`, and the credential set via `auth login`).
2. `CONFLUENCE_SITE` / `CONFLUENCE_EMAIL` / `CONFLUENCE_API_TOKEN`.
3. `ATLASSIAN_SITE` / `ATLASSIAN_API_EMAIL` / `ATLASSIAN_API_KEY` (compat aliases).
4. Stored credentials.

Site selection derives the target from a URL argument when present; else
`--site`; else the single configured default. Use `confluence auth login` to
store credentials, or set env vars.

## Testing

```bash
mise run check         # test + lint + build (use this by default)
mise run test          # unit tests only
mise run lint          # golangci-lint v2
mise run test:race     # with race detector
mise run test:int      # integration tests (build tag, not a separate binary)
mise run test:cover    # coverage report
```

`mise run check` is intentionally stricter than test+lint: it also builds the
renamed `confluence` binary. Always run it after changes.

golangci-lint is v2 (v1 predates Go 1.25 and emits false positives). `errcheck`
is disabled for `_test.go` files only (fixture/httptest writes where a failure
surfaces as a test failure anyway); production code is linted at full strength.
See `.golangci.yml`.

Tests use:

- `httptest.Server` with custom handlers for API client tests
- `filesystem.Memory` (in-memory FS) for `space sync` tests
- A `MockClient` implementing `confluence.Client` for sync tests without HTTP
- Tree determinism is verified by running `BuildTree` 100 times

## Workflow / release discipline

Same model as the sibling `slack-cli`. Every change - feature, fix, refactor -
follows it:

- Red-green-refactor: write failing tests first, implement, then clean up.
- Run `mise run check` after every change; it must pass before committing.
- MANDATORY code review before push (code changes only; docs-only Markdown edits are exempt). Spawn a `feature-dev:code-reviewer` on the pending diff, address every important finding, re-run to confirm clean. Never push code without a clean review pass.
- Keep commits small and conventional. Commit type IS the release trigger: `feat:`/`fix:` cut a release; `chore:`/`docs:`/`refactor:`/`test:`/`perf:`/`style:` cut nothing. `feat!:` or a `BREAKING CHANGE:` footer for anything that breaks a caller (removed flags, changed output shape or exit codes).
- Releases are automated: release-please watches main, opens an auto-merging release PR when a version-bumping commit lands, tags it, and GoReleaser builds binaries and pushes the Homebrew artifact.
- The artifact is a Homebrew CASK, not a formula: it lives at `Casks/confluence-cli.rb` in `tammersaleh/homebrew-tap`. Install/upgrade with `brew install --cask tammersaleh/tap/confluence-cli` / `brew upgrade --cask tammersaleh/tap/confluence-cli`.
- A push is not the finish line. After a release-cutting push, wait for the tag, upgrade the installed cask, and verify `confluence version` against the real artifact before calling a change done.
- The pre-push hook runs `mise run check`; never bypass with `--no-verify`.

## Notes for future sessions

Learnings from the build worth keeping.

### Pushing

The SSH remote key is fingerprint/touch-gated, so a non-interactive `git push origin` over SSH fails. Push over HTTPS using the gh credential helper instead: run `gh auth setup-git` once, then `git push https://github.com/tammersaleh/confluence-cli.git main:main`.

Exception: changes under `.github/workflows/` need a token with the `workflow` scope. The repo's gh and `RELEASE_PAT` tokens carry only `repo`, so workflow-file changes must be pushed over SSH (with the fingerprint) or with a workflow-scoped token.

### Release flow

Pushing to main triggers release-please, which opens a release PR that auto-merges on green CI, tags, and lets GoReleaser publish the cask to `tammersaleh/homebrew-tap`. Because release-please's merge advances remote main, fetch and rebase over HTTPS before each subsequent push:

```bash
git fetch https://github.com/tammersaleh/confluence-cli.git main && git rebase FETCH_HEAD
```

### Verifying the gate

Check `mise run check`'s exit code directly. Piping it to `grep` masks a nonzero exit - a lint failure slipped through that way once. Run it and gate on the code:

```bash
mise run check > /tmp/c.txt 2>&1; echo $?
```

### Linting

golangci-lint is pinned to v2. v1.64.x predates Go 1.25 and emits false-positive SA5011. gopls "modernize" hints (`interface{}`->`any`, `min`/`max`, `SplitSeq`) are NOT part of the golangci-lint gate; don't chase them.

## Dependencies

Only direct dependency beyond stdlib is `github.com/alecthomas/kong` (Kong, not
Cobra - the migration replaced the old single-command Cobra tool).

## Go version

Requires Go 1.25 (go.mod declares 1.25.5).
