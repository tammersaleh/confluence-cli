package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// parseCLI runs Kong over args exactly as cmd/confluence/main.go does and
// returns the populated CLI plus the parse context.
func parseCLI(t *testing.T, args ...string) (*CLI, *kong.Context) {
	t.Helper()
	var c CLI
	parser, err := kong.New(&c, kong.Name("confluence"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		t.Fatalf("Parse(%q): %v", args, err)
	}
	return &c, ctx
}

// --format is a hidden alias of --body-format on every command that has one.
func TestFormatAlias_ParsesLikeBodyFormat(t *testing.T) {
	cases := []struct {
		name string
		args []string
		get  func(*CLI) string
	}{
		{"page get", []string{"page", "get", "123"}, func(c *CLI) string { return c.Page.Get.BodyFormat }},
		{"page create", []string{"page", "create", "--space", "ENG", "--title", "T"}, func(c *CLI) string { return c.Page.Create.BodyFormat }},
		{"page update", []string{"page", "update", "123", "--if-version", "1"}, func(c *CLI) string { return c.Page.Update.BodyFormat }},
		{"comment add", []string{"comment", "add", "123"}, func(c *CLI) string { return c.Comment.Add.BodyFormat }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viaAlias, _ := parseCLI(t, append(tc.args, "--format", "markdown")...)
			viaCanon, _ := parseCLI(t, append(tc.args, "--body-format", "markdown")...)
			if got := tc.get(viaAlias); got != "markdown" {
				t.Errorf("--format: BodyFormat = %q, want markdown", got)
			}
			if tc.get(viaAlias) != tc.get(viaCanon) {
				t.Errorf("--format and --body-format disagree: %q vs %q", tc.get(viaAlias), tc.get(viaCanon))
			}
		})
	}
}

// The alias is accepted but not advertised: --help documents --body-format only.
func TestFormatAlias_HiddenFromHelp(t *testing.T) {
	for _, args := range [][]string{
		{"page", "get", "--help"},
		{"page", "create", "--help"},
		{"page", "update", "--help"},
		{"comment", "add", "--help"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			var c CLI
			var help bytes.Buffer
			parser, err := kong.New(&c, kong.Name("confluence"), kong.Writers(&help, &help), kong.Exit(func(int) {}))
			if err != nil {
				t.Fatalf("kong.New: %v", err)
			}
			_, _ = parser.Parse(args)
			if !strings.Contains(help.String(), "--body-format") {
				t.Fatalf("help should document --body-format:\n%s", help.String())
			}
			if strings.Contains(help.String(), "--format") {
				t.Errorf("help should not advertise --format:\n%s", help.String())
			}
		})
	}
}

// End to end: page get --format markdown produces the same rows as
// --body-format markdown.
func TestPageGet_FormatAliasEndToEnd(t *testing.T) {
	clearCredEnv(t)
	server := pageGetServer(t, nil)
	defer server.Close()

	t.Setenv("CONFLUENCE_SITE", server.URL)
	t.Setenv("CONFLUENCE_EMAIL", "test@example.com")
	t.Setenv("CONFLUENCE_API_TOKEN", "api-token")

	run := func(flag string) string {
		var out, errBuf bytes.Buffer
		c, _ := parseCLI(t, "page", "get", "123", flag, "markdown")
		c.SetOutput(&out, &errBuf)
		c.SetCredentialsPath(t.TempDir() + "/none.json")
		if err := c.Page.Get.Run(c); err != nil {
			t.Fatalf("Run() with %s error: %v", flag, err)
		}
		return out.String()
	}

	alias, canon := run("--format"), run("--body-format")
	if alias != canon {
		t.Errorf("output differs\n--format:      %s\n--body-format: %s", alias, canon)
	}
	if !strings.Contains(alias, `"body_format":"markdown"`) {
		t.Errorf("expected body_format markdown, got: %s", alias)
	}
}
