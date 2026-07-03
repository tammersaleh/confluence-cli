// Package confluenceurl parses Confluence Cloud web URLs into typed references.
// It is a pure parser: no network, no resolution, no CLI semantics. Callers
// decide what to do with a Ref based on its Kind, and use CommonSite to enforce
// that a single invocation targets one site.
package confluenceurl

import (
	"fmt"
	"net/url"
	"strings"
)

// Kind classifies what a Confluence URL points at.
type Kind int

const (
	KindSite Kind = iota
	KindSpace
	KindPage
)

func (k Kind) String() string {
	switch k {
	case KindSite:
		return "site"
	case KindSpace:
		return "space"
	case KindPage:
		return "page"
	default:
		return "unknown"
	}
}

// Ref is a parsed Confluence URL. Only the fields relevant to Kind are
// populated. BaseURL is always set for a successful parse.
type Ref struct {
	Kind     Kind
	BaseURL  string // canonical: scheme + lowercased host, no path (e.g. https://acme.atlassian.net)
	SpaceKey string // set for KindSpace and for KindPage when derivable from the URL
	PageID   string // set for KindPage
}

// Parse interprets raw as a Confluence URL.
//
// It is tri-state:
//   - matched=false, err=nil: raw is not a URL (a bare id/key). The caller
//     handles it as an id/key.
//   - matched=true, err!=nil: raw is URL-shaped but not a usable Confluence
//     URL. The caller should fast-fail.
//   - matched=true, err=nil: ref is populated.
func Parse(raw string) (ref Ref, matched bool, err error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		return Ref{}, false, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Ref{}, true, fmt.Errorf("not a valid URL: %w", err)
	}
	// Use u.Host (not u.Hostname()) so a non-default port is preserved in the
	// canonical BaseURL. Lowercasing is safe: hostnames are case-insensitive and
	// digits/colon in the port are unaffected.
	host := strings.ToLower(u.Host)
	if host == "" {
		return Ref{}, true, fmt.Errorf("URL has no host: %q", raw)
	}

	base := u.Scheme + "://" + host
	segs := splitPath(u.Path)

	// Drop an optional leading /wiki segment; some instances omit it.
	if len(segs) > 0 && segs[0] == "wiki" {
		segs = segs[1:]
	}

	// Site: bare host, /wiki, /wiki/ (all reduce to no remaining segments).
	if len(segs) == 0 {
		return Ref{Kind: KindSite, BaseURL: base}, true, nil
	}

	switch segs[0] {
	case "spaces":
		return parseSpaces(base, segs)
	case "pages":
		return parsePagesViewpage(base, segs, u.Query())
	default:
		return Ref{}, true, fmt.Errorf("unrecognized Confluence URL path %q", u.Path)
	}
}

// parseSpaces handles /spaces/KEY and /spaces/KEY/pages/ID[/Title].
//
// /spaces/KEY/pages/ID is a page. Any other trailing content after the space
// key (e.g. /overview, /blog/..., a single extra segment) is treated as a
// space URL with the suffix ignored, matching the Cloud UI's space URLs.
func parseSpaces(base string, segs []string) (Ref, bool, error) {
	if len(segs) < 2 || segs[1] == "" {
		return Ref{}, true, fmt.Errorf("space URL missing space key")
	}
	spaceKey := segs[1]

	// /spaces/KEY/pages/ID[/Title] -> page.
	if len(segs) > 2 && segs[2] == "pages" {
		if len(segs) < 4 {
			return Ref{}, true, fmt.Errorf("page URL missing page id")
		}
		id := segs[3]
		if !isDigits(id) {
			return Ref{}, true, fmt.Errorf("not a numeric page id: %q", id)
		}
		return Ref{Kind: KindPage, BaseURL: base, SpaceKey: spaceKey, PageID: id}, true, nil
	}

	// /spaces/KEY and anything else (/overview, /blog/..., etc.) -> space.
	return Ref{Kind: KindSpace, BaseURL: base, SpaceKey: spaceKey}, true, nil
}

// parsePagesViewpage handles /pages/viewpage.action?pageId=ID.
func parsePagesViewpage(base string, segs []string, q url.Values) (Ref, bool, error) {
	if len(segs) < 2 || segs[1] != "viewpage.action" {
		return Ref{}, true, fmt.Errorf("unrecognized pages URL path %q", "/"+strings.Join(segs, "/"))
	}
	id := q.Get("pageId")
	if id == "" {
		return Ref{}, true, fmt.Errorf("viewpage URL missing pageId")
	}
	if !isDigits(id) {
		return Ref{}, true, fmt.Errorf("not a numeric page id: %q", id)
	}
	return Ref{Kind: KindPage, BaseURL: base, PageID: id}, true, nil
}

// CommonSite returns the shared canonical BaseURL across refs, or an error if
// they span different sites or refs is empty.
func CommonSite(refs []Ref) (string, error) {
	if len(refs) == 0 {
		return "", fmt.Errorf("no refs")
	}
	base := refs[0].BaseURL
	for _, r := range refs[1:] {
		if r.BaseURL != base {
			return "", fmt.Errorf("refs span multiple sites: %q and %q", base, r.BaseURL)
		}
	}
	return base, nil
}

// splitPath returns the non-empty, unescaped path segments.
func splitPath(p string) []string {
	var out []string
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			continue
		}
		if unescaped, err := url.PathUnescape(seg); err == nil {
			seg = unescaped
		}
		out = append(out, seg)
	}
	return out
}

// isDigits reports whether s is non-empty and all ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
