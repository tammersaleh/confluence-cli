# confluence-sync

One-way sync of a Confluence space to local Markdown files. See `README.md` for usage.

## Architecture

```
cmd/confluence-sync/main.go   CLI entry point (Cobra). Wires dependencies, maps errors to exit codes.
internal/
  confluence/
    client.go                  HTTP client for Confluence Cloud API v2. Retry w/ exponential backoff + jitter.
    types.go                   API response types: Space, Page, PageContent, Attachment.
    url.go                     Parses Confluence space URLs into baseURL + spaceKey.
  converter/
    converter.go               Converts Confluence storage format (XHTML) to GFM Markdown.
  sync/
    sync.go                    Orchestrates the sync: fetches pages, builds tree, walks it writing files.
    tree.go                    Converts flat page list to parent-child tree. Orphans become roots.
  filesystem/
    fs.go                      FileSystem interface (MkdirAll, WriteFile, ReadFile, RemoveAll).
    memory.go                  In-memory implementation for tests.
pkg/sanitize/
  sanitize.go                  Converts page titles to filesystem-safe names. Handles collisions.
```

### Data flow

1. `main.go` creates a `confluence.Client` and `filesystem.OS`, injects them into `sync.Syncer`.
2. `Syncer.Sync()` calls `client.GetSpace()` then `client.GetPages()` (paginated, fetches all pages).
3. For each page, it calls `client.GetPageContent()` and `getPageParentID()` separately - the list endpoint doesn't reliably return parent IDs.
4. `tree.BuildTree()` assembles pages into a hierarchy using parent IDs. Nodes sorted by ID for deterministic output.
5. `syncNode()` recursively walks the tree:
   - Converts content via `converter.ConvertWithOptions()`
   - Writes Markdown files (with YAML frontmatter including a `version` field)
   - Downloads attachments to `_attachments/` directories
6. On subsequent runs, version in frontmatter is compared to skip unchanged pages.

### Key interfaces

Both are in `internal/` and injected into `Syncer`:

- `confluence.Client` - API operations (GetSpace, GetPages, GetPageContent, GetAttachments, DownloadAttachment)
- `filesystem.FileSystem` - file I/O (MkdirAll, WriteFile, ReadFile, RemoveAll)

### Non-obvious behaviors

- Pages with children OR attachments become directories with `index.md`. Leaf pages without attachments are plain `.md` files.
- Broken attachments are logged as warnings and skipped - they don't abort the sync.
- Filename collisions (after sanitization) get `-2`, `-3`, etc. suffixes. Collision tracking is per-namespace (shared for roots, per-parent for non-roots).
- The converter handles Confluence-specific XHTML elements like `ac:structured-macro` (code blocks), `ac:image`, `ri:attachment`, and rewrites attachment URLs to local `_attachments/` paths.
- The converter uses Go's `xml.Decoder` with `AutoClose` for self-closing HTML elements - not an HTML parser. Malformed HTML will behave differently than you'd expect from a browser.

## Testing

```bash
mise run test          # unit tests
mise run test:race     # with race detector
mise run lint          # golangci-lint
```

Tests use:

- `httptest.Server` with custom handlers for API client tests
- `filesystem.Memory` (in-memory FS) for sync tests
- A `MockClient` implementing `confluence.Client` for sync tests without HTTP
- Tree determinism is verified by running `BuildTree` 100 times
- Integration tests (`mise run test:int`) use a build tag, not a separate binary or docker-compose

## Dependencies

Only direct dependency beyond stdlib is `github.com/spf13/cobra`.

## Go version

Requires Go 1.25.5 (see `go.mod`).
