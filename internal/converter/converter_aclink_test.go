package converter

import (
	"strings"
	"testing"
)

func mustConvert(t *testing.T, input string, opts Options) string {
	t.Helper()
	got, err := ConvertWithOptions(input, opts)
	if err != nil {
		t.Fatalf("ConvertWithOptions() error: %v", err)
	}
	return got
}

// Regression: HTMLAutoClose matched <ac:link> by its local name "link", so the
// real </ac:link> became a decoder error that parse swallowed, truncating the
// body after the first link.
func TestConvert_ACLink(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "page link with body, content after survives",
			input: `<p>a <ac:link><ri:page ri:content-title="X" /><ac:link-body>Label</ac:link-body></ac:link></p><h2>B</h2><p>after</p>`,
			want:  "a [Label](X)\n\n## B\nafter\n",
		},
		{
			name:  "page link without body uses title",
			input: `<p><ac:link><ri:page ri:content-title="Target Page" /></ac:link></p>`,
			want:  "[Target Page](<Target Page>)\n",
		},
		{
			name:  "plain text link body",
			input: `<p><ac:link><ri:page ri:content-title="X" /><ac:plain-text-link-body><![CDATA[Plain]]></ac:plain-text-link-body></ac:link></p>`,
			want:  "[Plain](X)\n",
		},
		{
			name:  "page link in heading",
			input: `<h2><ac:link><ri:page ri:content-title="Compute Infrastructure" /><ac:link-body>Compute Infrastructure</ac:link-body></ac:link></h2>`,
			want:  "## [Compute Infrastructure](<Compute Infrastructure>)\n",
		},
		{
			name:  "user mention",
			input: `<p><ac:link><ri:user ri:account-id="712020:96cc" /></ac:link> owns it</p>`,
			want:  "@712020:96cc owns it\n",
		},
		{
			name:  "user mention with no account id",
			input: `<p><ac:link><ri:user /></ac:link></p>`,
			want:  "@unknown-user\n",
		},
		{
			name:  "attachment link",
			input: `<p><ac:link><ri:attachment ri:filename="spec.pdf" /><ac:link-body>the spec</ac:link-body></ac:link></p>`,
			want:  "[the spec](spec.pdf)\n",
		},
		{
			name:  "attachment on another page keeps the attachment as target",
			input: `<p><ac:link><ri:attachment ri:filename="spec.pdf"><ri:page ri:content-title="Other Page" /></ri:attachment><ac:link-body>spec</ac:link-body></ac:link></p>`,
			want:  "[spec](spec.pdf)\n",
		},
		{
			name:  "url link",
			input: `<p><ac:link><ri:url ri:value="https://example.com" /><ac:link-body>Example</ac:link-body></ac:link></p>`,
			want:  "[Example](https://example.com)\n",
		},
		{
			name:  "link with no resource emits label",
			input: `<p><ac:link ac:anchor="sec"><ac:link-body>Section</ac:link-body></ac:link></p>`,
			want:  "Section\n",
		},
		{
			name:  "page title with parentheses is wrapped",
			input: `<p><ac:link><ri:page ri:content-title="Plan(v2)" /></ac:link></p>`,
			want:  "[Plan(v2)](<Plan(v2)>)\n",
		},
		{
			name:  "nested formatting inside link body",
			input: `<p><ac:link><ri:page ri:content-title="X" /><ac:link-body><strong>Bold</strong> label</ac:link-body></ac:link></p>`,
			want:  "[Bold label](X)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustConvert(t, tt.input, Options{})
			if got != tt.want {
				t.Errorf("Convert() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestConvert_ACLinkAttachmentResolved(t *testing.T) {
	input := `<p><ac:link><ri:attachment ri:filename="spec.pdf" /><ac:link-body>spec</ac:link-body></ac:link></p>`

	local := mustConvert(t, input, Options{AttachmentPath: "_attachments"})
	if local != "[spec](_attachments/spec.pdf)\n" {
		t.Errorf("local attachment link =\n%q", local)
	}

	resolver := func(filename string) (string, bool) {
		if filename == "spec.pdf" {
			return "https://acme.atlassian.net/wiki/download/attachments/1/spec.pdf", true
		}
		return "", false
	}
	remote := mustConvert(t, input, Options{AttachmentURL: resolver})
	if remote != "[spec](https://acme.atlassian.net/wiki/download/attachments/1/spec.pdf)\n" {
		t.Errorf("remote attachment link =\n%q", remote)
	}
}

// Regression: an unknown structured macro leaked its parameter text as body.
func TestConvert_UnknownMacro(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "parameter text never leaks",
			input: `<h2>A</h2><p><ac:structured-macro ac:name="excerpt-include" ac:schema-version="1"><ac:parameter ac:name="nopanel">true</ac:parameter></ac:structured-macro></p><h2>B</h2><p>after</p>`,
			want:  "## A\n[macro: excerpt-include]\n\n## B\nafter\n",
		},
		{
			name:  "ri:page title inside macro is included",
			input: `<p><ac:structured-macro ac:name="excerpt-include"><ac:parameter ac:name=""><ri:page ri:content-title="Compute Infrastructure" /></ac:parameter><ac:parameter ac:name="nopanel">true</ac:parameter></ac:structured-macro></p>`,
			want:  "[macro: excerpt-include Compute Infrastructure]\n",
		},
		{
			name:  "macro outside a paragraph",
			input: `<ac:structured-macro ac:name="toc"><ac:parameter ac:name="maxLevel">3</ac:parameter></ac:structured-macro><p>after</p>`,
			want:  "[macro: toc]\n\nafter\n",
		},
		{
			name:  "nameless macro",
			input: `<ac:structured-macro></ac:structured-macro><p>after</p>`,
			want:  "[macro: unknown]\n\nafter\n",
		},
		{
			name:  "status macro emits its title",
			input: `<p><ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">Green</ac:parameter><ac:parameter ac:name="title">On Track</ac:parameter></ac:structured-macro></p>`,
			want:  "[status: On Track]\n",
		},
		{
			name:  "status macro without title falls back to placeholder",
			input: `<p><ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">Green</ac:parameter></ac:structured-macro></p>`,
			want:  "[macro: status]\n",
		},
		{
			name:  "title parameter of other macros stays suppressed",
			input: `<p><ac:structured-macro ac:name="jira"><ac:parameter ac:name="title">Leak</ac:parameter></ac:structured-macro></p>`,
			want:  "[macro: jira]\n",
		},
		{
			name:  "rich text body is converted, parameters dropped",
			input: `<ac:structured-macro ac:name="info"><ac:parameter ac:name="title">Note</ac:parameter><ac:rich-text-body><p>Body <strong>text</strong></p></ac:rich-text-body></ac:structured-macro><p>after</p>`,
			want:  "Body **text**\n\nafter\n",
		},
		{
			name:  "nested macro inside rich text body",
			input: `<ac:structured-macro ac:name="expand"><ac:rich-text-body><p>a</p><ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">X-1</ac:parameter></ac:structured-macro></ac:rich-text-body></ac:structured-macro>`,
			want:  "a\n\n[macro: jira]\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustConvert(t, tt.input, Options{})
			if got != tt.want {
				t.Errorf("Convert() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

// The shape of the live "FE Tech Lead Assignments" page: a linked heading
// followed by an excerpt-include macro, repeated.
func TestConvert_LinkedHeadingsWithMacros(t *testing.T) {
	section := func(title string) string {
		return `<h2><ac:link><ri:page ri:content-title="` + title + `" /><ac:link-body>` + title + `</ac:link-body></ac:link></h2>` +
			`<p><ac:structured-macro ac:name="excerpt-include" ac:schema-version="1">` +
			`<ac:parameter ac:name=""><ri:page ri:content-title="` + title + `" /></ac:parameter>` +
			`<ac:parameter ac:name="nopanel">true</ac:parameter>` +
			`<ac:parameter ac:name="class">tech-leads</ac:parameter>` +
			`</ac:structured-macro></p>`
	}
	input := section("One") + section("Two") + section("Three")

	got := mustConvert(t, input, Options{})

	if n := strings.Count("\n"+got, "\n## "); n != 3 {
		t.Errorf("heading count = %d, want 3:\n%s", n, got)
	}
	for _, title := range []string{"One", "Two", "Three"} {
		if !strings.Contains(got, "## ["+title+"]("+title+")") {
			t.Errorf("missing linked heading for %s:\n%s", title, got)
		}
		if !strings.Contains(got, "[macro: excerpt-include "+title+"]") {
			t.Errorf("missing macro placeholder for %s:\n%s", title, got)
		}
	}
	for _, leak := range []string{"true", "tech-leads"} {
		if strings.Contains(got, leak) {
			t.Errorf("parameter text %q leaked:\n%s", leak, got)
		}
	}
}

// The shape of a stock program-status template: the first data cell holds a
// user mention. Every later row and heading must survive.
func TestConvert_TableWithUserMentionInFirstCell(t *testing.T) {
	input := `<table>
		<tr><th>Role</th><th>Who</th></tr>
		<tr><td>Driver</td><td><p><ac:link><ri:user ri:account-id="712020:abc" /></ac:link> </p></td></tr>
		<tr><td>Approver</td><td><p><ac:link><ri:user ri:account-id="712020:def" /></ac:link></p></td></tr>
		<tr><td>Status</td><td>Green</td></tr>
	</table>
	<h2>Objective</h2>
	<p>Ship it.</p>
	<h2>Timeline</h2>
	<table>
		<tr><th>Milestone</th><th>Date</th></tr>
		<tr><td>M1</td><td>Jan</td></tr>
		<tr><td>M2</td><td>Feb</td></tr>
	</table>`

	got := mustConvert(t, input, Options{})

	wantRows := []string{
		"| Role | Who |",
		"| Driver | @712020:abc |",
		"| Approver | @712020:def |",
		"| Status | Green |",
		"| Milestone | Date |",
		"| M1 | Jan |",
		"| M2 | Feb |",
	}
	for _, row := range wantRows {
		if !strings.Contains(got, row) {
			t.Errorf("missing row %q:\n%s", row, got)
		}
	}
	for _, h := range []string{"## Objective", "## Timeline"} {
		if !strings.Contains(got, h) {
			t.Errorf("missing heading %q:\n%s", h, got)
		}
	}
}

// Void elements still work in both unclosed HTML form and self-closed form.
func TestConvert_VoidElements(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "unclosed br", input: "<p>a<br>b</p>", want: "a\nb\n"},
		{name: "self-closed br", input: "<p>a<br/>b</p>", want: "a\nb\n"},
		{name: "unclosed hr", input: "<p>a</p><hr><p>b</p>", want: "a\n\n---\nb\n"},
		{name: "unclosed img is ignored", input: `<p>a<img src="x.png">b</p>`, want: "ab\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustConvert(t, tt.input, Options{})
			if got != tt.want {
				t.Errorf("Convert() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

// A decoder error must surface, never truncate silently. The decoder runs
// non-strict, so an unclosed element is repaired at the wrapper's end tag; a
// stray end tag with no opener is unrepairable and must error from every
// subtree handler, not just the top-level loop.
func TestConvert_MalformedReturnsError(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "mismatched end tag", input: "<p>a</div><p>b</p>"},
		{name: "stray end tag", input: "<p>a</p></span>"},
		{name: "stray end tag inside table cell", input: "<table><tr><td>a</span></td></tr></table><h2>after</h2>"},
		{name: "stray end tag inside link", input: `<p><ac:link><ri:page ri:content-title="X" /></span></ac:link></p><h2>after</h2>`},
		{name: "stray end tag inside macro parameter", input: `<ac:structured-macro ac:name="toc"><ac:parameter ac:name="x">1</span></ac:parameter></ac:structured-macro><h2>after</h2>`},
		{name: "stray end tag inside image", input: `<ac:image></span></ac:image><h2>after</h2>`},
		{name: "stray end tag inside task", input: `<ac:task-list><ac:task></span></ac:task></ac:task-list><h2>after</h2>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Convert(tt.input)
			if err == nil {
				t.Fatalf("Convert(%q) returned nil error", tt.input)
			}
		})
	}
}
