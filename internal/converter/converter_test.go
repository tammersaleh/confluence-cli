package converter

import (
	"strings"
	"testing"
)

func TestConvert(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain paragraph",
			input: "<p>Hello world</p>",
			want:  "Hello world\n",
		},
		{
			name:  "multiple paragraphs",
			input: "<p>First</p><p>Second</p>",
			want:  "First\n\nSecond\n",
		},
		{
			name:  "h1",
			input: "<h1>Title</h1>",
			want:  "# Title\n",
		},
		{
			name:  "h2",
			input: "<h2>Section</h2>",
			want:  "## Section\n",
		},
		{
			name:  "h3",
			input: "<h3>Subsection</h3>",
			want:  "### Subsection\n",
		},
		{
			name:  "h4",
			input: "<h4>Minor</h4>",
			want:  "#### Minor\n",
		},
		{
			name:  "h5",
			input: "<h5>Detail</h5>",
			want:  "##### Detail\n",
		},
		{
			name:  "h6",
			input: "<h6>Fine</h6>",
			want:  "###### Fine\n",
		},
		{
			name:  "bold with strong",
			input: "<p>This is <strong>bold</strong> text</p>",
			want:  "This is **bold** text\n",
		},
		{
			name:  "bold with b tag",
			input: "<p>This is <b>bold</b> text</p>",
			want:  "This is **bold** text\n",
		},
		{
			name:  "italic with em",
			input: "<p>This is <em>italic</em> text</p>",
			want:  "This is *italic* text\n",
		},
		{
			name:  "italic with i tag",
			input: "<p>This is <i>italic</i> text</p>",
			want:  "This is *italic* text\n",
		},
		{
			name:  "unordered list",
			input: "<ul><li>First</li><li>Second</li><li>Third</li></ul>",
			want:  "- First\n- Second\n- Third\n",
		},
		{
			name:  "ordered list",
			input: "<ol><li>First</li><li>Second</li><li>Third</li></ol>",
			want:  "1. First\n2. Second\n3. Third\n",
		},
		{
			name:  "nested list",
			input: "<ul><li>Item<ul><li>Nested</li></ul></li></ul>",
			want:  "- Item\n  - Nested\n",
		},
		{
			name:  "code block with pre",
			input: `<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[func main() {}]]></ac:plain-text-body></ac:structured-macro>`,
			want:  "```\nfunc main() {}\n```\n",
		},
		{
			name:  "code block with language",
			input: `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:plain-text-body><![CDATA[func main() {}]]></ac:plain-text-body></ac:structured-macro>`,
			want:  "```go\nfunc main() {}\n```\n",
		},
		{
			name:  "inline code",
			input: "<p>Use <code>fmt.Println</code> to print</p>",
			want:  "Use `fmt.Println` to print\n",
		},
		{
			name:  "link",
			input: `<p>Visit <a href="https://example.com">Example</a></p>`,
			want:  "Visit [Example](https://example.com)\n",
		},
		{
			name:  "image",
			input: `<ac:image><ri:attachment ri:filename="diagram.png"/></ac:image>`,
			want:  "![diagram.png](diagram.png)\n",
		},
		{
			name: "simple table",
			input: `<table>
				<tr><th>Name</th><th>Value</th></tr>
				<tr><td>Foo</td><td>1</td></tr>
				<tr><td>Bar</td><td>2</td></tr>
			</table>`,
			want: "| Name | Value |\n| --- | --- |\n| Foo | 1 |\n| Bar | 2 |\n",
		},
		{
			name:  "line break",
			input: "<p>Line one<br/>Line two</p>",
			want:  "Line one\nLine two\n",
		},
		{
			name:  "horizontal rule",
			input: "<hr/>",
			want:  "---\n",
		},
		{
			name:  "blockquote",
			input: "<blockquote><p>Quoted text</p></blockquote>",
			want:  "> Quoted text\n",
		},
		{
			name:  "strikethrough",
			input: "<p>This is <s>deleted</s> text</p>",
			want:  "This is ~~deleted~~ text\n",
		},
		{
			name:  "strikethrough with del",
			input: "<p>This is <del>deleted</del> text</p>",
			want:  "This is ~~deleted~~ text\n",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \n\t  ",
			want:  "",
		},
		{
			name:  "task list",
			input: `<ac:task-list><ac:task><ac:task-status>incomplete</ac:task-status><ac:task-body>Todo item</ac:task-body></ac:task><ac:task><ac:task-status>complete</ac:task-status><ac:task-body>Done item</ac:task-body></ac:task></ac:task-list>`,
			want:  "- [ ] Todo item\n- [x] Done item\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Convert(tt.input)
			if got != tt.want {
				t.Errorf("Convert() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestConvertWithAttachmentRewrite(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		attachmentPath string
		want           string
	}{
		{
			name:           "image with attachment path",
			input:          `<ac:image><ri:attachment ri:filename="diagram.png"/></ac:image>`,
			attachmentPath: "_attachments",
			want:           "![diagram.png](_attachments/diagram.png)\n",
		},
		{
			name:           "confluence download link",
			input:          `<p><a href="/wiki/download/attachments/123/doc.pdf">Download</a></p>`,
			attachmentPath: "_attachments",
			want:           "[Download](_attachments/doc.pdf)\n",
		},
		{
			name:           "preserve external links",
			input:          `<p><a href="https://example.com/file.pdf">External</a></p>`,
			attachmentPath: "_attachments",
			want:           "[External](https://example.com/file.pdf)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertWithOptions(tt.input, Options{AttachmentPath: tt.attachmentPath})
			if got != tt.want {
				t.Errorf("ConvertWithOptions() =\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestConvert_ComplexDocument(t *testing.T) {
	input := `
	<h1>Project Overview</h1>
	<p>This is a <strong>critical</strong> project with <em>important</em> details.</p>
	<h2>Features</h2>
	<ul>
		<li>Feature one</li>
		<li>Feature two</li>
	</ul>
	<h2>Code Example</h2>
	<ac:structured-macro ac:name="code">
		<ac:parameter ac:name="language">go</ac:parameter>
		<ac:plain-text-body><![CDATA[package main

func main() {
    println("hello")
}]]></ac:plain-text-body>
	</ac:structured-macro>
	<p>See <a href="https://golang.org">Go website</a> for more.</p>
	`

	got := Convert(input)

	// Check key elements are present
	checks := []string{
		"# Project Overview",
		"**critical**",
		"*important*",
		"## Features",
		"- Feature one",
		"- Feature two",
		"## Code Example",
		"```go",
		`println("hello")`,
		"```",
		"[Go website](https://golang.org)",
	}

	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("output missing %q\nGot:\n%s", check, got)
		}
	}
}
