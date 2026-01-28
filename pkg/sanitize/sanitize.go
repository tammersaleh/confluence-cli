package sanitize

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	nonAlphanumeric = regexp.MustCompile(`[^a-z0-9-]+`)
	multipleHyphens = regexp.MustCompile(`-+`)
)

// Filename converts a page title to a filesystem-safe filename.
// Rules:
//   - Lowercase
//   - Spaces → hyphens
//   - Remove characters not in [a-z0-9-]
//   - Collapse multiple hyphens
//   - Trim leading/trailing hyphens
//   - Returns "page" if result would be empty
func Filename(title string) string {
	s := strings.ToLower(title)
	s = strings.ReplaceAll(s, " ", "-")
	s = nonAlphanumeric.ReplaceAllString(s, "")
	s = multipleHyphens.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if s == "" {
		return "page"
	}
	return s
}

// FilenameWithCollision returns a unique filename by appending -2, -3, etc.
// if the sanitized name already exists in the provided set.
func FilenameWithCollision(title string, existing map[string]bool) string {
	base := Filename(title)
	if !existing[base] {
		return base
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !existing[candidate] {
			return candidate
		}
	}
}
