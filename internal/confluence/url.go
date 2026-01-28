package confluence

import (
	"errors"
	"net/url"
	"strings"
)

var ErrInvalidSpaceURL = errors.New("invalid Confluence space URL")

// ParseSpaceURL extracts the base URL and space key from a Confluence space URL.
// Accepts URLs in these formats:
//   - https://acme.atlassian.net/wiki/spaces/ENG
//   - https://acme.atlassian.net/wiki/spaces/ENG/overview
//   - https://acme.atlassian.net/wiki/spaces/ENG/pages/...
func ParseSpaceURL(rawURL string) (baseURL string, spaceKey string, err error) {
	if rawURL == "" {
		return "", "", ErrInvalidSpaceURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", ErrInvalidSpaceURL
	}

	if u.Scheme == "" || u.Host == "" {
		return "", "", ErrInvalidSpaceURL
	}

	// Path should be /wiki/spaces/SPACEKEY/...
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "wiki" || parts[1] != "spaces" {
		return "", "", ErrInvalidSpaceURL
	}

	spaceKey = parts[2]
	if spaceKey == "" {
		return "", "", ErrInvalidSpaceURL
	}

	baseURL = u.Scheme + "://" + u.Host
	return baseURL, spaceKey, nil
}
