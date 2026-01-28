package confluence

import (
	"testing"
)

func TestParseSpaceURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantBaseURL string
		wantSpaceKey string
		wantErr     bool
	}{
		{
			name:        "basic space URL",
			url:         "https://acme.atlassian.net/wiki/spaces/ENG",
			wantBaseURL: "https://acme.atlassian.net",
			wantSpaceKey: "ENG",
		},
		{
			name:        "space URL with overview",
			url:         "https://acme.atlassian.net/wiki/spaces/ENG/overview",
			wantBaseURL: "https://acme.atlassian.net",
			wantSpaceKey: "ENG",
		},
		{
			name:        "space URL with pages path",
			url:         "https://acme.atlassian.net/wiki/spaces/ENG/pages/123456/Some+Page",
			wantBaseURL: "https://acme.atlassian.net",
			wantSpaceKey: "ENG",
		},
		{
			name:        "lowercase space key",
			url:         "https://company.atlassian.net/wiki/spaces/docs",
			wantBaseURL: "https://company.atlassian.net",
			wantSpaceKey: "docs",
		},
		{
			name:        "space key with numbers",
			url:         "https://corp.atlassian.net/wiki/spaces/TEAM123/overview",
			wantBaseURL: "https://corp.atlassian.net",
			wantSpaceKey: "TEAM123",
		},
		{
			name:    "missing space key",
			url:     "https://acme.atlassian.net/wiki/spaces/",
			wantErr: true,
		},
		{
			name:    "missing spaces path",
			url:     "https://acme.atlassian.net/wiki/",
			wantErr: true,
		},
		{
			name:    "not a confluence URL",
			url:     "https://acme.atlassian.net/jira/",
			wantErr: true,
		},
		{
			name:    "invalid URL",
			url:     "not-a-url",
			wantErr: true,
		},
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL, spaceKey, err := ParseSpaceURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseSpaceURL(%q) expected error, got nil", tt.url)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseSpaceURL(%q) unexpected error: %v", tt.url, err)
				return
			}
			if baseURL != tt.wantBaseURL {
				t.Errorf("ParseSpaceURL(%q) baseURL = %q, want %q", tt.url, baseURL, tt.wantBaseURL)
			}
			if spaceKey != tt.wantSpaceKey {
				t.Errorf("ParseSpaceURL(%q) spaceKey = %q, want %q", tt.url, spaceKey, tt.wantSpaceKey)
			}
		})
	}
}
