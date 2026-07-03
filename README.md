# confluence-sync

An agent-first Confluence CLI. The binary is `confluence`; the repository and Go
module stay `confluence-sync`. Output is JSONL (one JSON object per line);
commands are non-interactive and scriptable, built for LLM agents and CI.

The `version`, `space sync`, and `auth` commands ship today. A wider read/write
surface (pages, attachments, search, comments, labels, users, and authoring) is
designed but not yet implemented. See `SPEC.md` for the full contract and planned
commands, and `skills/confluence-cli/SKILL.md` for the agent skill.

## Installation

```bash
brew install --cask tammersaleh/tap/confluence-cli
```

Or with Go:

```bash
go install github.com/tammersaleh/confluence-sync/cmd/confluence@latest
```

## Auth

API token + email over HTTP Basic. Set environment variables:

| Variable | Description |
|----------|-------------|
| `CONFLUENCE_SITE` | Site base URL (e.g. `https://acme.atlassian.net`) |
| `CONFLUENCE_EMAIL` | Atlassian account email |
| `CONFLUENCE_API_TOKEN` | API token |

`ATLASSIAN_SITE` / `ATLASSIAN_API_EMAIL` / `ATLASSIAN_API_KEY` work as
compatibility aliases.

```bash
export CONFLUENCE_EMAIL=user@acme.com
export CONFLUENCE_API_TOKEN=your-api-token
```

Or store credentials at `~/.config/confluence-cli/credentials.json` with
`confluence auth login`. The token is read from stdin, validated against the
site, and then saved under the canonical site URL:

```bash
printf '%s' "$TOKEN" | confluence auth login --site https://acme.atlassian.net --email you@example.com
```

## Commands

The available commands honor `--quiet` and `--timeout`. Every command ends with
a `_meta` trailer line.

### version

```bash
confluence version
```

```jsonl
{"version":"0.1.0"}
{"_meta":{"has_more":false}}
```

### auth

Manage stored credentials keyed by canonical site URL.

```bash
printf '%s' "$TOKEN" | confluence auth login --site https://acme.atlassian.net --email you@example.com
confluence auth status
confluence auth logout https://acme.atlassian.net
```

`auth login` reads the token from stdin (never argv), validates it, then saves
it. `auth status` lists configured sites and any active env override without
printing secrets. `auth logout` removes a site; the `<site>` positional is
optional when only one is configured.

### space sync

One-way sync of a Confluence space to local Markdown files. This is a full-space
crawl. Human-readable progress goes to stderr; stdout carries a single summary
object then the trailer.

```bash
confluence space sync <space-url> <output-dir> [--prune] [--dry-run]
```

```bash
confluence space sync https://acme.atlassian.net/wiki/spaces/ENG ./eng-docs
```

```jsonl
{"synced":true,"space":"ENG","output_dir":"./eng-docs","dry_run":false}
{"_meta":{"has_more":false}}
```

Flags:

- `--prune` - after syncing, remove files in the output directory no longer part of the space. `_attachments/` dirs of version-skipped pages are protected.
- `--dry-run` - report what would happen without writing.
- `--quiet` - suppress all stdout (summary and trailer). Rely on the exit code; fatal errors still print to stderr.
- `--trace` - attach a JSON-lines diagnostics tracer to stderr. Per-request event emission lands in a later release.

Accepts space URLs in these formats:

- `https://acme.atlassian.net/wiki/spaces/ENG`
- `https://acme.atlassian.net/wiki/spaces/ENG/overview`
- `https://acme.atlassian.net/wiki/spaces/ENG/pages/...`

## Sync behavior

Pages are converted from Confluence storage format to Markdown. The page
hierarchy is preserved as a directory structure.

### Directory structure

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

Pages with children (or attachments) get their own directory with content in
`index.md`. Leaf pages without attachments are single `.md` files.

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

Attachments are stored in `_attachments/` alongside their parent page's content,
and Markdown references are rewritten to local paths:

```markdown
![diagram](_attachments/diagram.png)
```

A leaf page with attachments is promoted to a directory with `index.md` plus
`_attachments/`.

### Incremental sync

On subsequent runs, the `version` field in each file's frontmatter is compared
against the Confluence API and unchanged pages are skipped. With `--prune`, stale
local files (pages deleted or renamed in Confluence) are removed after sync;
attachments of version-skipped pages are left alone since their attachment list
wasn't re-fetched.

### Filename sanitization

Page titles become filesystem-safe names: lowercased, spaces to hyphens,
characters outside `[a-z0-9-]` removed, repeated hyphens collapsed, leading and
trailing hyphens trimmed. Collisions get `-2`, `-3`, etc. suffixes.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error (including partial failure) |
| 2 | Authentication error |
| 3 | Rate limited |
| 4 | Network error |

## Development

```bash
mise run check         # test + lint + build (use this by default)
mise run test          # unit tests only
mise run test:race     # with race detector
mise run lint          # golangci-lint
mise run build         # build the confluence binary
```

Requires Go 1.25. See `CLAUDE.md` for architecture and the release workflow.
