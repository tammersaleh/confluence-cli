package cli

import (
	"context"

	"github.com/tammersaleh/confluence-cli/internal/confluence"
)

// authorResolver caches accountId -> displayName lookups within a single command
// invocation. Lookups are best-effort: a failed or empty GetUser leaves the name
// unset and never aborts the caller. Each unique account id is fetched at most
// once.
type authorResolver struct {
	client confluence.Client
	cache  map[string]string // accountID -> displayName; "" means resolved-but-empty
}

func newAuthorResolver(client confluence.Client) *authorResolver {
	return &authorResolver{client: client, cache: make(map[string]string)}
}

// name returns the display name for accountID, resolving and caching on first
// use. It returns "" when the id is empty or the lookup yields no usable name.
func (r *authorResolver) name(ctx context.Context, accountID string) string {
	if accountID == "" {
		return ""
	}
	if n, ok := r.cache[accountID]; ok {
		return n
	}
	u, err := r.client.GetUser(ctx, accountID)
	if err != nil || u == nil {
		r.cache[accountID] = ""
		return ""
	}
	r.cache[accountID] = u.DisplayName
	return u.DisplayName
}

// enrich adds an author_name sibling to row when authorID resolves to a
// non-empty display name. It is a no-op when r is nil, so callers can pass a nil
// resolver to disable enrichment.
func (r *authorResolver) enrich(ctx context.Context, row map[string]any, authorID string) {
	if r == nil {
		return
	}
	if name := r.name(ctx, authorID); name != "" {
		row["author_name"] = name
	}
}
