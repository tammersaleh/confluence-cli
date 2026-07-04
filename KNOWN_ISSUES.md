# Known issues

Real caveats to know before relying on this tool. See `SPEC.md` for the full contract.

## Live-API verification pending

Only the original `space sync` has been exercised against a live Confluence Cloud instance. Every other command - `auth`, `page get/list/children/ancestors/tree/create/update/delete`, `space list/info`, `attachment list/download/upload`, `search`, `comment list/add` (including inline), `label list/add/remove`, and `user current/info` - is unit-tested with `httptest` against Atlassian's documented request/response shapes but has NOT been run against a real instance.

Endpoint paths, field names, and (especially) write payloads are implemented from the Atlassian REST docs and may need field tweaks against a real instance. The payloads most likely to need adjustment:

- page create/update body shape
- the v1 content-label endpoints
- the multipart attachment upload (`PUT /wiki/rest/api/content/{id}/child/attachment`, `X-Atlassian-Token: nocheck`)
- the inline-comment `inlineCommentProperties` selection shape
- the v1 CQL search response

Run a first live smoke test (auth login, page get, page create in a scratch space, comment add, attachment upload) before relying on writes in production.

## Pagination drain loops have no repeated-cursor guard

The drain loops (`space list`, `page list`/`children`, `comment list`, `label list`, `search --all`) stop when the API returns an empty next cursor. They have no guard against a misbehaving API that returns the same non-empty cursor forever, which would loop. Low risk with Confluence; a repeated-cursor guard in the client is possible hardening.

## Markdown write bodies are not code macros

With `--body-format markdown`, fenced code renders as a plain `<pre><code>` preformatted block, not a Confluence code macro. Raw HTML in markdown is escaped, not passed through.

## page tree ordering

`page tree` orders siblings by ID (deterministic), not Confluence display order.
