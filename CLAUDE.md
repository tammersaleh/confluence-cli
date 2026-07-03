# confluence-sync

Agent-first Confluence CLI. The binary is `confluence`; the repository and Go
module stay `confluence-sync` (mirroring `slack`→`slack-cli`). Output is JSONL,
one JSON object per line; commands are non-interactive and scriptable. Built on
Kong. Auth is an API token + email over HTTP Basic.

`SPEC.md` is the source of truth for the full command surface and output
contract. The `version`, `space sync`, and `auth` commands ship today; the rest
is designed but not implemented. See `README.md` for user-facing usage and
`skills/confluence-cli/SKILL.md` for the agent-facing skill.

## Architecture

```
cmd/confluence/main.go   Kong entry point. Parses, runs, maps errors to exit codes.
internal/
  cli/
    root.go                    CLI struct + global flags, Context(), NewPrinter, ResolveCredentials, ClassifyError.
    version.go                 `version` command.
    space.go                   `space sync` command (wraps the sync engine).
    auth.go                    `auth login|status|logout` commands.
  output/
    output.go                  Printer (JSONL rows + _meta trailer), Meta, Error, ExitError, exit codes, field filtering.
    errors.go                  Error.AsItem for inline per-item errors.
    json.go                    Timestamp enrichment (*_iso siblings).
  auth/
    credentials.go             Credentials store keyed by canonical site URL. Env precedence + merge.
  httpx/
    errors.go                  RateLimitError + generic ClassifyError (domain-agnostic).
    trace.go                   Tracer interface + JSON-lines tracer wired via context. (Paginate[T] arrives with its first caller in Phase 1.)
  confluence/
    client.go                  HTTP client for Confluence Cloud API v2. Retry w/ exponential backoff + jitter. Sentinels (ErrUnauthorized, ErrSpaceNotFound, ...) mapped to structured errors in internal/cli.
    types.go                   API response types: Space, Page, PageContent, Attachment.
    url.go                     Parses Confluence space URLs into baseURL + spaceKey.
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

The `version`, `space sync`, and `auth` commands ship today. All honor `--quiet` and `--timeout`.

- `confluence version` - emits the build version, then the trailer.
- `confluence space sync <space-url> <output-dir>` - one-way space crawl to local Markdown. Flags: `--prune`, `--dry-run`, `--quiet`, `--timeout`, `--trace`. Progress goes to stderr; stdout carries a single summary object then the trailer.
- `confluence auth login --site <url> --email <email>` - read the API token from stdin, validate it, and store it under the canonical site URL. Token never comes from argv.
- `confluence auth status` - list configured sites and any active env override, without printing secrets.
- `confluence auth logout [<site>]` - remove stored credentials for a site. The `<site>` positional is optional when exactly one site is configured.

The rest of the surface (`page`, `attachment`, `search`, `comment`, `label`,
`user`, and the write commands) is designed but not yet implemented. See
`SPEC.md`.

### Global flags

`--site`, `--fields`, `--quiet`, `--timeout`, `--trace`. `Context()` on the CLI
threads `--timeout` and `--trace` into a context every command's `Run` uses;
`--trace` attaches a JSON-lines tracer to the context. The domain client does
not emit request events yet; that wiring lands in Phase 1 (see the plan's
review follow-ups).

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

## Dependencies

Only direct dependency beyond stdlib is `github.com/alecthomas/kong` (Kong, not
Cobra - the migration replaced the old single-command Cobra tool).

## Go version

Requires Go 1.25 (go.mod declares 1.25.5).
