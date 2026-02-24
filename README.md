# confluence-sync

A CLI tool that syncs Confluence space pages to local Markdown files.

## Installation

```bash
go install github.com/tammersaleh/confluence-sync/cmd/confluence-sync@latest
```

## Usage

```
confluence-sync <space-url> <output-dir> [flags]

Arguments:
  space-url             Confluence space URL (e.g., https://acme.atlassian.net/wiki/spaces/ENG)
  output-dir            Output directory for synced files

Flags:
      --prune           Remove stale local files after sync
      --dry-run         Show what would be synced without writing files
  -q, --quiet           Suppress progress output
  -h, --help            Help
```

### Configuration

Authentication via environment variables:

| Variable | Description |
|----------|-------------|
| `ATLASSIAN_API_KEY` | API token |
| `ATLASSIAN_API_EMAIL` | Account email |

### Example

```bash
export ATLASSIAN_API_KEY=your-api-token
export ATLASSIAN_API_EMAIL=user@acme.com
confluence-sync https://acme.atlassian.net/wiki/spaces/ENG ./docs
```

Accepts URLs in these formats:

- `https://acme.atlassian.net/wiki/spaces/ENG`
- `https://acme.atlassian.net/wiki/spaces/ENG/overview`
- `https://acme.atlassian.net/wiki/spaces/ENG/pages/...`

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Configuration error |
| 2 | Authentication failure |
| 3 | API error |
| 4 | Filesystem error |

## Behavior

Pages are converted from Confluence storage format to Markdown. The page hierarchy is preserved as a directory structure.

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

### Frontmatter

Each Markdown file includes YAML frontmatter with Confluence metadata:

```yaml
---
confluence_page_id: "123456"
title: "Page Title"
author_id: "user-account-id"
confluence_url: "https://acme.atlassian.net/wiki/spaces/ENG/pages/123456"
version: 5
created_at: "2024-01-15T10:30:00.000Z"
modified_at: "2024-06-20T14:45:00.000Z"
---
```

### Attachments

Attachments are stored in `_attachments/` alongside their parent page's content. Markdown references are rewritten to point to local paths:

```markdown
![diagram](_attachments/diagram.png)
```

A leaf page with attachments is promoted to a directory:

```
api-design/
├── index.md
└── _attachments/
    └── openapi.yaml
```

### Incremental Sync

On subsequent runs, the `version` field in each file's frontmatter is compared against the Confluence API. Unchanged pages are skipped.

With `--prune`, stale local files (pages deleted or renamed in Confluence) are removed after sync. Attachments of version-skipped pages are left alone since their attachment list wasn't re-fetched.

### Filename Sanitization

Page titles are converted to filesystem-safe names:

- Lowercase
- Spaces → hyphens
- Remove characters not in `[a-z0-9-]`
- Collapse multiple hyphens
- Trim leading/trailing hyphens

Collisions are resolved by appending `-2`, `-3`, etc.

## Development

```bash
mise run test          # Run tests
mise run test:race     # With race detector
mise run test:cover    # With coverage report
mise run lint          # Run golangci-lint
mise run build         # Build binary
```
