# MCP parity work - status

Live tracker so a restarted session can resume exactly. Update as work happens.

## Goal

Close the Confluence-side gaps between confluence-cli and the built-in Atlassian
MCP so the CLI (plus jira-cli for Jira) can replace the MCP. Jira is covered by
jira-cli. Unified cross-product search and site discovery are explicitly out of
scope (owner declined).

Three features to implement, each: domain method + CLI command + tests, then
`mise run check` green, then a code-review pass, then commit. Batch the releases
(one push at the end, or per feature) - pushing main auto-cuts a release.

Working directly in the main thread (sub-agents were dying on API "Not logged
in" instability). TDD, `mise run check` must pass (check its exit code directly,
never pipe to grep). golangci-lint v2 pin; ignore gopls modernize hints.

Current released version: v1.9.0 (repo/module renamed to confluence-cli).

## Tasks

### 1. page descendants  (STATUS: DONE - code+tests+docs green, committed)
MCP `getConfluencePageDescendants`. All descendants under a page, all levels.
- Domain: `GetDescendants(ctx, pageID, cursor, limit) ([]Descendant, next, err)`
  - GET /wiki/api/v2/pages/{id}/descendants ; item fields id,title,type,status,depth,childPosition[,parentId]
  - new `Descendant{ID,Title,Type,ParentID,Depth}` type
  - 404 -> ErrPageNotFound ; cursor via Link header then _links.next
  - add to Client interface + mock stub in internal/sync/sync_test.go
- CLI: `PageDescendantsCmd{Ref, Limit=25, Cursor, All}` wired into PageCmd
  - mirror PageChildrenCmd; row {id,title,type,depth,parent_id?}; _meta.next_cursor single-page, drained on --all
- Tests: client_test.go (items+depth+cursor, default limit 25, 404) ; page_test.go (rows, --all, URL-site, 404)
- Docs: SPEC/CLAUDE/README/SKILL add page descendants

### 2. comment replies  (STATUS: DONE - comment list --replies, recursive, committed)
MCP `getConfluenceCommentChildren`. Replies to a comment.
- Domain: `GetCommentChildren(ctx, commentID, cursor, limit) ([]Comment, next, err)`
  - GET /wiki/api/v2/footer-comments/{id}/children (and inline variant) ; reuse Comment type
- CLI: surface in `comment list` via `--replies` (drain each comment's children;
  emit reply rows with a parent_id linking to the top-level comment) OR a
  `comment children <id>` subcommand. Decide during impl; --replies preferred.
- Tests + docs.

### 3. author name enrichment  (STATUS: DONE - --resolve-authors opt-in, committed)
Resolved via opt-in `--resolve-authors` on page get + comment list, backed by a
per-invocation cached authorResolver (internal/cli/authors.go). Best-effort:
failed lookup omits author_name. Chose opt-in (not automatic) to avoid surprise
per-author API calls on every read.

## After the code
- Docs pass: DONE (all three in SPEC/README/SKILL/CLAUDE "available now").
- Code review: DONE (feature-dev:code-reviewer + Codex, both clean).
- Release: DONE. Shipped in v1.10.0 (PR #11 auto-merged, GoReleaser published the
  cask; `confluence version` -> 1.10.0 verified against the installed cask, all
  three new commands/flags present in the released binary).
- Then: authentication + live testing discussion with owner (CLI not yet authed;
  nothing past space sync is live-verified).
