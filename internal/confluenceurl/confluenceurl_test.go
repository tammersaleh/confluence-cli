package confluenceurl

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		matched bool
		wantErr bool
		want    Ref
	}{
		// Not URLs: bare id/key/empty -> matched=false, no error.
		{name: "bare page id", raw: "123456", matched: false},
		{name: "bare space key", raw: "ENG", matched: false},
		{name: "empty string", raw: "", matched: false},
		{name: "whitespace trimmed to empty", raw: "   ", matched: false},
		{name: "bare id with surrounding space", raw: "  123456  ", matched: false},

		// Site.
		{name: "site bare host", raw: "https://acme.atlassian.net", matched: true,
			want: Ref{Kind: KindSite, BaseURL: "https://acme.atlassian.net"}},
		{name: "site with wiki", raw: "https://acme.atlassian.net/wiki", matched: true,
			want: Ref{Kind: KindSite, BaseURL: "https://acme.atlassian.net"}},
		{name: "site with wiki trailing slash", raw: "https://acme.atlassian.net/wiki/", matched: true,
			want: Ref{Kind: KindSite, BaseURL: "https://acme.atlassian.net"}},

		// Space.
		{name: "space with wiki", raw: "https://acme.atlassian.net/wiki/spaces/ENG", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG"}},
		{name: "space trailing slash", raw: "https://acme.atlassian.net/wiki/spaces/ENG/", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG"}},
		{name: "space without wiki", raw: "https://acme.atlassian.net/spaces/ENG", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG"}},
		{name: "space host lowercased", raw: "https://ACME.atlassian.net/wiki/spaces/ENG", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG"}},
		{name: "space keeps port", raw: "https://confluence.example.com:8090/wiki/spaces/OPS", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://confluence.example.com:8090", SpaceKey: "OPS"}},
		{name: "space overview suffix", raw: "https://acme.atlassian.net/wiki/spaces/ENG/overview", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG"}},
		{name: "space blog suffix", raw: "https://acme.atlassian.net/wiki/spaces/ENG/blog/2024/x", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG"}},

		// Page via /spaces/KEY/pages/ID.
		{name: "page", raw: "https://acme.atlassian.net/wiki/spaces/ENG/pages/12345", matched: true,
			want: Ref{Kind: KindPage, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG", PageID: "12345"}},
		{name: "page with title suffix", raw: "https://acme.atlassian.net/wiki/spaces/ENG/pages/12345/Some-Title", matched: true,
			want: Ref{Kind: KindPage, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG", PageID: "12345"}},
		{name: "page trailing slash", raw: "https://acme.atlassian.net/wiki/spaces/ENG/pages/12345/", matched: true,
			want: Ref{Kind: KindPage, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG", PageID: "12345"}},
		{name: "page without wiki", raw: "https://acme.atlassian.net/spaces/ENG/pages/12345", matched: true,
			want: Ref{Kind: KindPage, BaseURL: "https://acme.atlassian.net", SpaceKey: "ENG", PageID: "12345"}},
		{name: "page keeps port", raw: "https://confluence.example.com:8090/wiki/spaces/ENG/pages/12345", matched: true,
			want: Ref{Kind: KindPage, BaseURL: "https://confluence.example.com:8090", SpaceKey: "ENG", PageID: "12345"}},
		{name: "page non-numeric id", raw: "https://acme.atlassian.net/wiki/spaces/ENG/pages/abc", matched: true, wantErr: true},

		// Page via viewpage.action.
		{name: "viewpage with wiki", raw: "https://acme.atlassian.net/wiki/pages/viewpage.action?pageId=12345", matched: true,
			want: Ref{Kind: KindPage, BaseURL: "https://acme.atlassian.net", PageID: "12345"}},
		{name: "viewpage without wiki", raw: "https://acme.atlassian.net/pages/viewpage.action?pageId=12345", matched: true,
			want: Ref{Kind: KindPage, BaseURL: "https://acme.atlassian.net", PageID: "12345"}},
		{name: "viewpage non-numeric", raw: "https://acme.atlassian.net/pages/viewpage.action?pageId=abc", matched: true, wantErr: true},
		{name: "viewpage missing pageId", raw: "https://acme.atlassian.net/pages/viewpage.action", matched: true, wantErr: true},

		// URL-shaped but not Confluence.
		{name: "unrecognized path", raw: "https://acme.atlassian.net/wiki/foo/bar", matched: true, wantErr: true},
		{name: "no host", raw: "https:///wiki/spaces/ENG", matched: true, wantErr: true},

		// Non-atlassian host still accepted (host not hardcoded).
		{name: "custom host space", raw: "https://confluence.internal.example.com/wiki/spaces/OPS", matched: true,
			want: Ref{Kind: KindSpace, BaseURL: "https://confluence.internal.example.com", SpaceKey: "OPS"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, matched, err := Parse(tt.raw)
			if matched != tt.matched {
				t.Fatalf("matched = %v, want %v (err=%v)", matched, tt.matched, err)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ref %+v", ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.matched {
				return
			}
			if ref != tt.want {
				t.Fatalf("ref = %+v, want %+v", ref, tt.want)
			}
		})
	}
}

func TestCommonSite(t *testing.T) {
	base := "https://acme.atlassian.net"
	other := "https://beta.atlassian.net"

	tests := []struct {
		name    string
		refs    []Ref
		want    string
		wantErr bool
	}{
		{name: "empty", refs: nil, wantErr: true},
		{name: "single", refs: []Ref{{BaseURL: base}}, want: base},
		{name: "all same", refs: []Ref{{BaseURL: base}, {BaseURL: base}, {BaseURL: base}}, want: base},
		{name: "mixed", refs: []Ref{{BaseURL: base}, {BaseURL: other}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CommonSite(tt.refs)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
