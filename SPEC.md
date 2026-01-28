# confluence-sync

A CLI tool that syncs Confluence space pages to local Markdown files.

## Behavior

The tool fetches all pages from a specified Confluence space and writes them as Markdown files to a local directory. The Confluence page hierarchy is preserved as a directory structure.

### Sync Rules

- Pages are converted from Confluence storage format to Markdown
- Page titles become filenames (sanitized for filesystem compatibility)
- Child pages become subdirectories containing an `index.md` for the parent content
- Attachments are downloaded and stored alongside their parent page
- Databases and other content types are ignored
- Sync is one-way: Confluence → local filesystem

### Directory Structure

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

Pages with children get their own directory with content in `index.md`. Leaf pages are single `.md` files.

### Attachments

Attachments are stored in an `_attachments/` directory alongside their parent page's content:

- For pages with children (directory-based): `pagedir/_attachments/`
- For leaf pages: converted to directory structure with `index.md` and `_attachments/`

A leaf page `api-design.md` with attachments becomes:

```
api-design/
├── index.md
└── _attachments/
    └── openapi.yaml
```

Markdown image and link references are rewritten to point to the local attachment paths. Original Confluence URLs like:

```
![diagram](/wiki/download/attachments/123/diagram.png)
```

Become:

```
![diagram](_attachments/diagram.png)
```

Attachment filenames are preserved as-is (not sanitized) since they're already filesystem-safe from Confluence.

### Filename Sanitization

Page titles are converted to filesystem-safe names:

- Lowercase
- Spaces → hyphens
- Remove characters not in `[a-z0-9-]`
- Collapse multiple hyphens
- Trim leading/trailing hyphens

Collisions within a directory are resolved by appending `-2`, `-3`, etc.

## Configuration

Authentication is configured via environment variables:

| Variable | Description |
|----------|-------------|
| `ATLASSIAN_API_KEY` | API token |
| `ATLASSIAN_API_EMAIL` | Account email |

## Usage

```
confluence-sync <space-url> [flags]

Arguments:
  space-url             Confluence space URL (e.g., https://acme.atlassian.net/wiki/spaces/ENG)

Flags:
  -o, --output string   Output directory (default: ./output)
      --clean           Delete output directory before sync
      --dry-run         Show what would be synced without writing files
  -v, --verbose         Verbose output
  -h, --help            Help
```

The base URL and space key are extracted from the space URL. Accepts URLs in these formats:

- `https://acme.atlassian.net/wiki/spaces/ENG`
- `https://acme.atlassian.net/wiki/spaces/ENG/overview`
- `https://acme.atlassian.net/wiki/spaces/ENG/pages/...`

Example:

```
export ATLASSIAN_API_KEY=your-api-token
export ATLASSIAN_API_EMAIL=user@acme.com
confluence-sync https://acme.atlassian.net/wiki/spaces/ENG -o ./docs
```

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Configuration error |
| 2 | Authentication failure |
| 3 | API error |
| 4 | Filesystem error |

---

# Development

## Project Structure

```
.
├── cmd/
│   └── confluence-sync/
│       └── main.go
├── internal/
│   ├── confluence/      # API client
│   ├── converter/       # Confluence → Markdown conversion
│   ├── sync/            # Orchestration logic
│   └── filesystem/      # File operations
├── pkg/
│   └── sanitize/        # Filename sanitization (exported)
├── testdata/            # Test fixtures
├── go.mod
├── go.sum
└── mise.toml
```

## Testing Strategy

All external dependencies are abstracted behind interfaces and mocked in tests.

### Interfaces

```go
// confluence/client.go
type Client interface {
    GetSpace(ctx context.Context, spaceKey string) (*Space, error)
    GetPages(ctx context.Context, spaceID string) ([]Page, error)
    GetPageContent(ctx context.Context, pageID string) (*PageContent, error)
    GetAttachments(ctx context.Context, pageID string) ([]Attachment, error)
    DownloadAttachment(ctx context.Context, attachment Attachment) (io.ReadCloser, error)
}

// filesystem/fs.go
type FileSystem interface {
    MkdirAll(path string, perm os.FileMode) error
    WriteFile(path string, data []byte, perm os.FileMode) error
    RemoveAll(path string) error
}
```

### Test Categories

**Unit tests** (`*_test.go` alongside source):

- Test individual functions in isolation
- Use mock implementations of interfaces
- No network calls, no filesystem access

**Integration tests** (`internal/*/integration_test.go`):

- Use build tag `//go:build integration`
- Run against real Confluence (for local development only)
- Not part of CI

### Mock Generation

Use `go generate` with `mockgen`:

```go
//go:generate mockgen -destination=mock_client.go -package=confluence . Client
```

### Test Fixtures

Store Confluence API response fixtures in `testdata/`:

```
testdata/
├── confluence/
│   ├── space.json
│   ├── pages.json
│   ├── page_content.json
│   └── attachments.json
└── expected/
    └── simple_hierarchy/
        ├── index.md
        ├── _attachments/
        │   └── diagram.png
        └── child.md
```

### Running Tests

```bash
mise run test          # Unit tests only
mise run test:race     # With race detector
mise run test:cover    # With coverage report
mise run test:int      # Integration tests (requires credentials)
```

## Go Practices

### Error Handling

Wrap errors with context using `fmt.Errorf`:

```go
if err != nil {
    return fmt.Errorf("fetching page %s: %w", pageID, err)
}
```

Define sentinel errors for expected failure modes:

```go
var (
    ErrSpaceNotFound = errors.New("space not found")
    ErrUnauthorized  = errors.New("unauthorized")
)
```

### Context

All operations that could block accept `context.Context` as first parameter:

```go
func (c *client) GetPages(ctx context.Context, spaceID string) ([]Page, error)
```

### Dependencies

Minimize external dependencies. Acceptable:

- `github.com/spf13/cobra` - CLI
- `go.uber.org/mock` - Test mocks
- Standard library for everything else

Markdown conversion: use standard library. Confluence storage format is XML-based; parse with `encoding/xml` and convert to Markdown manually.

### Code Style

- `gofmt` and `goimports`
- `golangci-lint` with default settings
- No package-level variables except errors and compile-time constants

## mise

mise manages the Go toolchain version and runs tasks.

```toml
# mise.toml
[tools]
go = "1.22"

[tasks.build]
run = "go build -o bin/confluence-sync ./cmd/confluence-sync"

[tasks.test]
run = "go test ./..."

[tasks."test:race"]
run = "go test -race ./..."

[tasks."test:cover"]
run = "go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out -o coverage.html"

[tasks."test:int"]
run = "go test -tags=integration ./..."

[tasks.lint]
run = "golangci-lint run"

[tasks.fmt]
run = "gofmt -w . && goimports -w ."

[tasks.generate]
run = "go generate ./..."

[tasks.clean]
run = "rm -rf bin/ coverage.out coverage.html"
```

---

# Implementation Plan

## Phase 1: Project Setup

- [ ] Initialize Go module (`go mod init`)
- [ ] Create `mise.toml` with Go version and tasks
- [ ] Create directory structure (`cmd/`, `internal/`, `pkg/`)
- [ ] Add `.gitignore`

## Phase 2: URL Parsing

- [ ] Write tests for extracting base URL from space URL
- [ ] Write tests for extracting space key from space URL
- [ ] Write tests for invalid URL handling
- [ ] Implement URL parser in `internal/confluence/url.go`

## Phase 3: Filename Sanitization

- [ ] Write tests for lowercase conversion
- [ ] Write tests for space-to-hyphen conversion
- [ ] Write tests for special character removal
- [ ] Write tests for hyphen collapsing
- [ ] Write tests for collision resolution (`-2`, `-3`, etc.)
- [ ] Implement sanitizer in `pkg/sanitize/sanitize.go`

## Phase 4: Confluence Client Types

- [ ] Define `Space` struct
- [ ] Define `Page` struct with parent/child relationships
- [ ] Define `PageContent` struct
- [ ] Define `Attachment` struct
- [ ] Define `Client` interface
- [ ] Define sentinel errors

## Phase 5: Confluence Client Implementation

- [ ] Write tests for `GetSpace` using mock HTTP server
- [ ] Implement `GetSpace`
- [ ] Write tests for `GetPages` with pagination
- [ ] Implement `GetPages`
- [ ] Write tests for `GetPageContent`
- [ ] Implement `GetPageContent`
- [ ] Write tests for `GetAttachments`
- [ ] Implement `GetAttachments`
- [ ] Write tests for `DownloadAttachment`
- [ ] Implement `DownloadAttachment`
- [ ] Write tests for authentication errors
- [ ] Implement auth error handling

## Phase 6: Filesystem Abstraction

- [ ] Define `FileSystem` interface
- [ ] Implement real filesystem adapter
- [ ] Implement in-memory filesystem for tests

## Phase 7: Markdown Converter

- [ ] Write tests for plain text paragraphs
- [ ] Write tests for headings (h1-h6)
- [ ] Write tests for bold/italic
- [ ] Write tests for bullet lists
- [ ] Write tests for numbered lists
- [ ] Write tests for code blocks
- [ ] Write tests for inline code
- [ ] Write tests for links
- [ ] Write tests for images
- [ ] Write tests for tables
- [ ] Implement converter in `internal/converter/converter.go`
- [ ] Write tests for attachment URL rewriting
- [ ] Implement attachment URL rewriting

## Phase 8: Sync Orchestration

- [ ] Write tests for building page tree from flat list
- [ ] Implement page tree builder
- [ ] Write tests for determining output paths (leaf vs directory)
- [ ] Implement output path resolver
- [ ] Write tests for single page sync (no children, no attachments)
- [ ] Implement single page sync
- [ ] Write tests for page with children
- [ ] Implement directory creation for pages with children
- [ ] Write tests for page with attachments
- [ ] Implement attachment downloading
- [ ] Write tests for leaf page promoted to directory (has attachments)
- [ ] Implement leaf-to-directory promotion
- [ ] Write tests for full space sync
- [ ] Implement full sync orchestration
- [ ] Write tests for `--clean` flag behavior
- [ ] Implement clean output directory
- [ ] Write tests for `--dry-run` flag behavior
- [ ] Implement dry-run mode

## Phase 9: CLI

- [ ] Create cobra root command
- [ ] Add space URL argument parsing
- [ ] Add `--output` flag
- [ ] Add `--clean` flag
- [ ] Add `--dry-run` flag
- [ ] Add `--verbose` flag
- [ ] Read `ATLASSIAN_API_KEY` from environment
- [ ] Read `ATLASSIAN_API_EMAIL` from environment
- [ ] Implement exit codes
- [ ] Wire up sync orchestration

## Phase 10: Polish

- [ ] Add progress output (page count, attachment count)
- [ ] Add verbose logging
- [ ] Run `golangci-lint`, fix issues
- [ ] Test manually against real Confluence space
- [ ] Write README with installation and usage
