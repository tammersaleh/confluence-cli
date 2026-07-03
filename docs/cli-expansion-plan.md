# confluence-sync → agent-first Confluence CLI

Status: decisions confirmed, awaiting session restart to begin Phase 0.

Expand `confluence-sync` from a single-purpose sync tool into a full Confluence
CLI whose primary user is LLM agents, mirroring the sibling project
`slack-cli` in everything except business logic: output contract, command
philosophy, error model, auth handling, GitHub automation, Homebrew tap
distribution, agent-skill distribution, and development process.

The reference is `~/src/github.com/tammersaleh/slack-cli`. Read its `SPEC.md`,
`CLAUDE.md`, `cmd/root.go`, `internal/output`, `internal/auth`,
`internal/resolve`, `.goreleaser.yml`, `.github/workflows/*`,
`skills/slack-cli/SKILL.md` before implementing any phase.

## Gating decisions

Three decisions shape everything below. All confirmed by the owner.

1. Binary name: **`confluence`** (repo + Go module stay `confluence-sync`,
   mirroring slack→slack-cli). Commands read `confluence page get`,
   `confluence space sync`. **Confirmed.**
2. Old invocation: **clean break**. `confluence-sync <url> <dir>` is removed;
   the sync becomes `confluence space sync <url> <dir>`. New CLI adopts
   slack-cli house exit codes. **Confirmed.**
3. Write scope: **writes are first-class, co-equal with reads.** This is the
   key departure from my initial read-focused proposal. Authoring structured
   Confluence content (storage-format XHTML / ADF) is genuinely hard — the
   direct analogue of slack-cli's Block Kit draft feature, which earned a large
   share of that project's design effort and its most detailed SKILL.md
   section. Spend comparable attention on getting the write interface right.
   **Confirmed.**

   This does not mean careless writes. Carry the slack-draft safety instincts:
   optimistic concurrency (`--if-version` / require current version), file/stdin
   body input rather than a sprawl of mutating flags, and rich local validation
   of the content shape (the way `validateBlockShapes` rejects bad Block Kit
   before it reaches Slack). But writing is a primary goal, not a deferred
   Phase 5.

## Why mirror the contract, not the file names (Codex correction)

slack-cli's `internal/api` is thin transport glue because the real domain
client is `slack-go/slack`. In confluence-sync, `internal/confluence` *is* the
domain client and already holds real business logic (retry/backoff, parent
resolution, URL normalization). Do **not** rename it into `internal/api`. Mirror
slack-cli's *boundaries*: keep a Confluence domain package, and add a small
shared support layer around it.

## Target package shape

```text
cmd/
  confluence/main.go        # Kong entry: parse, run, map errors→exit codes
internal/
  cli/                      # CLI struct, global flags, Context(), NewPrinter/NewClient
  output/                   # Printer, Meta, Error, exit codes  (port from slack-cli)
  auth/                     # credentials store, env overrides, site selection
  httpx/                    # retry policy, tracing, Paginate[T], error classification
  confluence/               # domain client + types + endpoint methods (KEEP)
  confluenceurl/            # tri-state URL parser: site / space / page / attachment
  resolve/                  # minimal: space key↔id, authorId→name enrichment
  converter/                # storage XHTML → Markdown  (KEEP, extend for `page get`)
  sync/                     # existing engine, adapted to emit typed events (KEEP)
pkg/
  sanitize/                 # KEEP
skills/
  confluence-cli/SKILL.md   # static, installed via `skills` npm tool
SPEC.md
```

Note the current package uses Cobra; `cmd/` moves to **Kong** (owner's standing
preference for the house style). The migration is small because today's
entrypoint is a thin single-command wrapper.

## Output & error contract (identical to slack-cli)

- JSONL data rows to stdout, one JSON object per line.
- Every command ends with a `_meta` trailer: `{"_meta":{"has_more":false}}`.
- Info/bulk commands echo an `input` field per row; per-item errors appear
  inline on stdout and bump `_meta.error_count`.
- Fatal errors to stderr as one JSON object: `error`, `detail`, `hint`,
  `input`, `endpoint`. Every `hint` names a concrete recovery command.
- `--fields` top-level filter, `--quiet`, `--timeout`, `--trace` global flags.
- Timestamp enrichment: `*_ts`/`created_at`/`modified_at` gain an `*_iso`
  sibling (Confluence already returns ISO 8601, so this is mostly passthrough;
  keep the mechanism for consistency).
- Exit codes: `0` success, `1` general/partial, `2` auth, `3` rate-limited,
  `4` network.

This replaces today's text-to-stdout / `os.Exit`-from-handlers model and the
`4 = filesystem` code (breaking; see decision 2).

## Auth model

API token + email (Basic auth), not OAuth 3LO — static creds fit agent/CI use
and match the current tool's evolutionary path. 3LO's app registration + refresh
lifecycle is too heavy for v1.

- Credentials at `~/.config/confluence-cli/credentials.json`, keyed by **site
  base URL** (store `cloud_id` when known, but URL is the primary selector since
  agents naturally hold URLs).
- `confluence auth login --site <url> --email <e>` (token via prompt/stdin, not
  argv), `auth status`, `auth logout`.
- Env precedence: flags > `CONFLUENCE_SITE`/`CONFLUENCE_EMAIL`/
  `CONFLUENCE_API_TOKEN` > `ATLASSIAN_SITE`/`ATLASSIAN_API_EMAIL`/
  `ATLASSIAN_API_KEY` (compat aliases) > stored credentials.
- Site selection: derive site from a URL argument when present; else `--site`
  or the single configured default; error if a URL-derived site disagrees with
  `--site`.

## Command surface (phased)

```text
confluence auth login|status|logout
confluence page get <id|url>...        # --body-format storage|adf|view|markdown
confluence page list --space <key|url> # --limit --cursor --all
confluence page children <id|url>
confluence page ancestors <id|url>
confluence page tree --space <key|url> # only if ordering semantics are acceptable
confluence space list
confluence space info <key|id|url>...
confluence space sync <space-url> <output-dir>  # existing engine
confluence attachment list <page>
confluence attachment download <id|url>
confluence search <cql>                # --text convenience later
confluence comment list <page>         # footer + inline
confluence label list <page>
confluence user current
confluence user info <accountId>...
confluence version

# Writes (first-class — see "Write interface design")
confluence page create --space <key|url> --title <t> [--parent <id|url>] [< body]
confluence page update <id|url> --if-version <n> [--title <t>] [< body]
confluence page delete <id|url>...     # guarded
confluence comment add <page> [--inline] [< body]
confluence label add|remove <page> <label>...
confluence attachment upload <page> <file>...
```

## Reuse vs rewrite

Keep: `converter`, `sync`, `filesystem`, `pkg/sanitize`, and the Confluence
domain methods. Rewrite: entrypoint (Cobra→Kong), error classification, output.

Two domain-layer corrections required before broadening commands:

- **Split list/get from sync-crawl.** Today `GetPages()` fetches all pages, then
  does an N+1 `GET /pages/{id}` pass for parent info, then resolves synthetic
  folder/database parents. That's a good *space-crawl* primitive but a bad
  backing method for thin `page list`/`children`/`ancestors`. Add plain
  list/get methods; keep the crawl for `space sync`.
- **Resource-specific not-found.** `doRequest()` maps every 404 to
  `ErrSpaceNotFound`. Split into `space_not_found` / `page_not_found` /
  `attachment_not_found` so per-item errors, hints, and exit codes are correct.

`page get --body-format markdown` is a **derived** representation, not a raw API
format. The existing converter rewrites attachment links to `_attachments/…`,
which is sync-specific. For `page get`, decide: leave attachment links as remote
URLs, and emit `body_format: "markdown"` + `source_body_format: "storage"` in
the row. Do not treat markdown as just another enum next to storage/adf/view.

`space sync` gets a typed reporter instead of `Logger.Printf`:
`PageWritten/PageSkipped/AttachmentDownloaded/AttachmentSkipped/PathPruned`.
The command maps events to JSONL on stdout; human progress + trace to stderr;
recoverable per-item failures (e.g. broken attachment) become inline error rows
with `_meta.error_count` and exit 1.

## Write interface design

Writing is a headline feature, and it is the hardest part. Confluence's native
body formats are storage-format XHTML (a bespoke macro-laden XML dialect) and
ADF (Atlassian Document Format, a nested JSON node tree). Neither is pleasant to
author by hand, and both have shape rules that fail confusingly server-side —
exactly the situation slack-cli faced with Block Kit drafts.

Mirror the slack-draft playbook:

- **Structured JSON/text on stdin, not a flag matrix.** Body comes from stdin or
  a file, not dozens of mutating flags. Decide the canonical input format
  (candidates: ADF JSON, storage XHTML, or GFM Markdown that we convert →
  storage/ADF). Markdown-in is the agent-friendly default and reuses/extends the
  existing converter in reverse; ADF/storage passthrough is the escape hatch for
  full fidelity.
- **Local validation before the API sees it.** The analogue of
  `validateBlockShapes`: reject malformed node trees / unsupported macros up
  front with an `invalid_body` error that names the problem and the fix, so the
  agent corrects without a server round-trip.
- **Optimistic concurrency.** `page update` requires `--if-version <n>` (or
  fetches-then-checks); a version mismatch is a distinct, recoverable error.
- **A SKILL.md section as detailed as slack's draft docs.** Document the body
  format, the shapes that look right but break, and copy-pasteable minimal
  examples. This is where much of the value lands for agents.
- **Reversibility / safety.** `page delete` guarded; consider a preview/dry-run
  that renders the resulting body and target metadata before `--apply`.

Open format decision to settle in the write phase: author in Markdown (convert
on the way in) vs. require ADF/storage. Leaning Markdown-first with ADF/storage
passthrough.

## Phasing

### Phase 0 — product-contract scaffold
Kong skeleton; global flags (`--site`, `--fields`, `--quiet`, `--timeout`,
`--trace`); port `internal/output`; `internal/auth`; `httpx` (retry/trace/
paginate/classify); `version` command; `SPEC.md`; `.mise.toml` parity
(build/test/lint/check); pre-push hook; release-please config + manifest;
`.goreleaser.yml` (cask → tammersaleh/homebrew-tap); CI + release workflows;
skill skeleton; LICENSE; CHANGELOG.

### Phase 1 — prove one resource (`page`) end-to-end
`auth login/status/logout`; `page get <id|url>...`; `page list --space`;
URL parser + site selection; structured errors; `_meta`; trace/timeout;
Homebrew cask + skill actually shipping. Acceptance: install from tap, run
`confluence page get <url>` and `page list --space <key> --all`, verify JSONL +
`_meta` + error hints + exit codes end-to-end.

### Phase 2 — preserve existing value
`space sync` (adapted reporter), `attachment list`, `attachment download`,
`space info`.

### Phase 3 — expand read surface near existing domain
`page children`, `page ancestors`, `space list`, `page tree` (only with
acceptable ordering semantics).

### Phase 4 — net-new read surface
`search` (CQL), `comment list`, `label list`, `user current`/`user info`,
resolve/enrichment for authorId→name.

### Phase 5 — writes (first-class, headline feature)
`page create`/`page update` with the body-authoring interface above
(Markdown-in default + ADF/storage passthrough, local validation,
`--if-version`), then `comment add`, `label add|remove`, `attachment upload`,
guarded `page delete`. Ships with a SKILL.md write section as detailed as
slack-cli's draft docs. This is the most design-heavy phase — settle the input
format and validation model before writing command code.

## Automation & distribution checklist (port from slack-cli)

- `.goreleaser.yml`: build `confluence` for darwin/linux amd64/arm64, CGO off,
  ldflags version stamp; `homebrew_casks` → `tammersaleh/homebrew-tap`,
  `directory: Casks`, quarantine-strip post-install hook.
- `.github/workflows/ci.yml`: mise build/test/lint, `go mod tidy` check,
  goreleaser check + snapshot.
- `.github/workflows/release.yml`: release-please (RELEASE_PAT, auto-merge PR)
  → goreleaser on tag.
- `release-please-config.json` + `.release-please-manifest.json` (release-type
  go, include-component-in-tag false).
- `.githooks/pre-push` → `mise run check`; `.mise.toml` `setup-hooks` task.
- `skills/confluence-cli/SKILL.md` with `allowed-tools: Bash(confluence *)`,
  installed via `skills add tammersaleh/confluence-sync -g`.
- Rewrite `README.md` and `CLAUDE.md` for the new scope, workflow, and release
  discipline (conventional commits drive releases).

## Open questions

- Confluence has no aggressive rate-limit culture like Slack; keep the retry/
  backoff but the `3 = rate-limited` path is rarely hit. Fine to keep for
  symmetry.
- ADF vs storage: default `page get` body format? Proposal: `storage` (matches
  sync + converter); offer `adf`, `view`, `markdown`.
- Does `page tree` earn its place, given the sync tree builder sorts by ID for
  determinism rather than Confluence's display order?
- Write input format: Markdown-in (convert → storage/ADF) vs. require raw
  ADF/storage. Leaning Markdown-first + passthrough (see Write interface design).
