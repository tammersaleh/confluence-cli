// Package converter turns Confluence storage format (XHTML) into GFM Markdown.
package converter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"

	"encoding/xml"
)

type Options struct {
	AttachmentPath string

	// AttachmentURL, when non-nil, maps an attachment filename to the URL used in
	// the rendered Markdown for images and attachment links. When nil, the existing
	// local `_attachments/<filename>` rewriting is used (sync behavior, unchanged).
	// When it returns !ok (unknown attachment), the bare filename is used.
	AttachmentURL func(filename string) (url string, ok bool)
}

// autoClose lists the HTML void elements the converter accepts in unclosed
// form (<br>, not just <br/>). It deliberately replaces xml.HTMLAutoClose: that
// list matches on local name, so its "link" entry auto-closed <ac:link> and
// made the real </ac:link> a decoder error that truncated the output.
var autoClose = []string{"br", "hr", "img"}

var multiNewline = regexp.MustCompile(`\n{3,}`)
var inlineWhitespace = regexp.MustCompile(`[\n\t]+`)
var cellWhitespace = regexp.MustCompile(`\s*\n\s*`)

// Convert renders a storage-format body as Markdown. It returns an error when
// the body is not well-formed XML; the output is never silently truncated.
func Convert(storage string) (string, error) {
	return ConvertWithOptions(storage, Options{})
}

// ConvertWithOptions is Convert with attachment resolution options.
func ConvertWithOptions(storage string, opts Options) (string, error) {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		return "", nil
	}

	// Wrap in root element for valid XML
	wrapped := "<root>" + storage + "</root>"

	decoder := xml.NewDecoder(strings.NewReader(wrapped))
	decoder.Strict = false
	decoder.AutoClose = autoClose
	decoder.Entity = xml.HTMLEntity

	var buf bytes.Buffer
	c := newConverter(&buf, opts)

	if err := c.parse(decoder); err != nil {
		return "", err
	}

	result := buf.String()
	// Clean up multiple newlines
	result = multiNewline.ReplaceAllString(result, "\n\n")
	result = strings.TrimRight(result, "\n")
	if result != "" {
		result += "\n"
	}
	return result, nil
}

type converter struct {
	buf            *bytes.Buffer
	opts           Options
	listDepth      int
	orderedCounter []int
	inOrderedList  []bool
	inParagraph    bool
	inBlockquote   bool
	inTableHeader  bool
	tableRowCells  []string
}

func newConverter(buf *bytes.Buffer, opts Options) *converter {
	return &converter{
		buf:            buf,
		opts:           opts,
		orderedCounter: make([]int, 10),
	}
}

// parse drives the decoder to EOF, dispatching every token.
func (c *converter) parse(decoder *xml.Decoder) error {
	for {
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return wrapTokenErr(err)
		}
		if err := c.handleToken(tok, decoder); err != nil {
			return err
		}
	}
}

// parseUntilEnd dispatches tokens until the end tag that closes the element
// whose start tag was just consumed. Elements consumed whole by handleStart do
// not affect the depth count since their end tags never reach this loop.
func (c *converter) parseUntilEnd(decoder *xml.Decoder) error {
	depth := 1
	for {
		tok, err := nextToken(decoder)
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			consumed, err := c.handleStart(t, decoder)
			if err != nil {
				return err
			}
			if !consumed {
				depth++
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				return nil
			}
			c.handleEnd(t)
		case xml.CharData:
			c.handleCharData(t)
		}
	}
}

func (c *converter) handleToken(tok xml.Token, decoder *xml.Decoder) error {
	switch t := tok.(type) {
	case xml.StartElement:
		_, err := c.handleStart(t, decoder)
		return err
	case xml.EndElement:
		c.handleEnd(t)
	case xml.CharData:
		c.handleCharData(t)
	}
	return nil
}

// nextToken reads a token inside an open element. EOF here means the element
// was never closed, which is an error rather than a clean end.
func nextToken(decoder *xml.Decoder) (xml.Token, error) {
	tok, err := decoder.Token()
	if errors.Is(err, io.EOF) {
		return nil, wrapTokenErr(errors.New("unexpected EOF inside element"))
	}
	if err != nil {
		return nil, wrapTokenErr(err)
	}
	return tok, nil
}

func wrapTokenErr(err error) error {
	return fmt.Errorf("storage body is not well-formed XML: %w", err)
}

func xmlName(name xml.Name) string {
	if name.Space != "" {
		return name.Space + ":" + name.Local
	}
	return name.Local
}

// handleStart handles a start tag. consumed reports whether the handler read
// through the element's matching end tag itself.
func (c *converter) handleStart(elem xml.StartElement, decoder *xml.Decoder) (consumed bool, err error) {
	name := xmlName(elem.Name)

	switch name {
	case "root":
		// Ignore wrapper
	case "p":
		c.inParagraph = true
		if c.inBlockquote {
			c.buf.WriteString("> ")
		}
	case "h1":
		c.buf.WriteString("# ")
	case "h2":
		c.buf.WriteString("## ")
	case "h3":
		c.buf.WriteString("### ")
	case "h4":
		c.buf.WriteString("#### ")
	case "h5":
		c.buf.WriteString("##### ")
	case "h6":
		c.buf.WriteString("###### ")
	case "strong", "b":
		c.buf.WriteString("**")
	case "em", "i":
		c.buf.WriteString("*")
	case "s", "del":
		c.buf.WriteString("~~")
	case "code":
		c.buf.WriteString("`")
	case "ul":
		if c.listDepth > 0 {
			c.buf.WriteString("\n") // Newline before nested list
		}
		c.inOrderedList = append(c.inOrderedList, false)
		c.listDepth++
	case "ol":
		if c.listDepth > 0 {
			c.buf.WriteString("\n") // Newline before nested list
		}
		c.inOrderedList = append(c.inOrderedList, true)
		if c.listDepth < len(c.orderedCounter) {
			c.orderedCounter[c.listDepth] = 0
		}
		c.listDepth++
	case "li":
		c.writeListPrefix()
	case "a":
		return true, c.handleLink(elem, decoder)
	case "br":
		c.buf.WriteString("\n")
	case "hr":
		c.buf.WriteString("---\n")
	case "blockquote":
		c.inBlockquote = true
	case "table":
		// Start of table
	case "tr":
		c.tableRowCells = nil
	case "th":
		c.inTableHeader = true
		return true, c.collectTableCell(decoder)
	case "td":
		return true, c.collectTableCell(decoder)
	case "ac:structured-macro":
		return true, c.handleMacro(elem, decoder)
	case "ac:link":
		return true, c.handleACLink(decoder)
	case "ac:image":
		return true, c.handleImage(decoder)
	case "ac:task-list":
		// Task list container
	case "ac:task":
		return true, c.handleTask(decoder)
	}
	return false, nil
}

func (c *converter) handleEnd(elem xml.EndElement) {
	name := xmlName(elem.Name)

	switch name {
	case "p":
		c.inParagraph = false
		if c.inBlockquote {
			c.buf.WriteString("\n")
		} else {
			c.buf.WriteString("\n\n")
		}
	case "h1", "h2", "h3", "h4", "h5", "h6":
		c.buf.WriteString("\n")
	case "strong", "b":
		c.buf.WriteString("**")
	case "em", "i":
		c.buf.WriteString("*")
	case "s", "del":
		c.buf.WriteString("~~")
	case "code":
		c.buf.WriteString("`")
	case "ul", "ol":
		c.listDepth--
		if len(c.inOrderedList) > 0 {
			c.inOrderedList = c.inOrderedList[:len(c.inOrderedList)-1]
		}
	case "li":
		c.buf.WriteString("\n")
	case "blockquote":
		c.inBlockquote = false
	case "tr":
		c.writeTableRow()
	}
}

func (c *converter) handleCharData(data xml.CharData) {
	text := string(data)

	// Normalize whitespace but preserve meaningful spaces
	if c.inParagraph || c.listDepth > 0 {
		// Replace newlines and tabs with spaces, collapse multiple spaces
		text = inlineWhitespace.ReplaceAllString(text, " ")
		if text != "" {
			c.buf.WriteString(text)
		}
	} else if strings.TrimSpace(text) != "" {
		c.buf.WriteString(strings.TrimSpace(text))
	}
}

// resolveAttachment maps an attachment filename to the href used in Markdown:
// the AttachmentURL hook when set (bare filename when it reports !ok), else the
// local AttachmentPath, else the bare filename.
func (c *converter) resolveAttachment(filename string) string {
	if c.opts.AttachmentURL != nil {
		if u, ok := c.opts.AttachmentURL(filename); ok {
			return u
		}
		return filename
	}
	if c.opts.AttachmentPath != "" {
		return c.opts.AttachmentPath + "/" + filename
	}
	return filename
}

func (c *converter) handleLink(elem xml.StartElement, decoder *xml.Decoder) error {
	href := getAttr(elem.Attr, "href")
	text, err := c.collectInlineText(decoder)
	if err != nil {
		return err
	}

	if isAttachmentURL(href) {
		href = c.resolveAttachment(path.Base(href))
	}

	c.buf.WriteString("[" + text + "](" + href + ")")
	return nil
}

// handleACLink renders <ac:link>: the label comes from ac:link-body or
// ac:plain-text-link-body, the target from the ri:* resource child.
//
//	ri:page       -> [label](content-title), label defaults to the title
//	ri:user       -> @<account-id> (no lookup)
//	ri:attachment -> [label](resolved attachment href)
//	ri:url        -> [label](value)
//	none          -> label text
func (c *converter) handleACLink(decoder *xml.Decoder) error {
	var label, kind, target string
	depth := 1

	for {
		tok, err := nextToken(decoder)
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			name := xmlName(t.Name)
			switch name {
			case "ac:link-body", "ac:plain-text-link-body":
				label, err = c.collectInlineText(decoder)
				if err != nil {
					return err
				}
				continue
			case "ri:page":
				kind, target = name, getAttr(t.Attr, "ri:content-title")
			case "ri:user":
				kind, target = name, getAttr(t.Attr, "ri:account-id")
			case "ri:attachment":
				kind, target = name, getAttr(t.Attr, "ri:filename")
			case "ri:url":
				kind, target = name, getAttr(t.Attr, "ri:value")
			}
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				c.writeACLink(kind, strings.TrimSpace(label), target)
				return nil
			}
		}
	}
}

func (c *converter) writeACLink(kind, label, target string) {
	switch kind {
	case "ri:user":
		if target == "" {
			target = "unknown-user"
		}
		c.buf.WriteString("@" + target)
	case "ri:attachment":
		if label == "" {
			label = target
		}
		c.buf.WriteString("[" + label + "](" + c.resolveAttachment(target) + ")")
	case "ri:page", "ri:url":
		if label == "" {
			label = target
		}
		if label == "" {
			return
		}
		c.buf.WriteString("[" + label + "](" + target + ")")
	default:
		c.buf.WriteString(label)
	}
}

func (c *converter) handleImage(decoder *xml.Decoder) error {
	var filename string
	depth := 1

	for {
		tok, err := nextToken(decoder)
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if xmlName(t.Name) == "ri:attachment" {
				filename = getAttr(t.Attr, "ri:filename")
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				if filename != "" {
					c.buf.WriteString("![" + filename + "](" + c.resolveAttachment(filename) + ")\n")
				}
				return nil
			}
		}
	}
}

// handleMacro renders <ac:structured-macro>. A macro with an ac:rich-text-body
// has that body converted in place and its parameters dropped. A macro with an
// ac:plain-text-body (the code macro and its relatives) becomes a fenced code
// block, using the "language" parameter as the info string. Any other macro
// becomes a placeholder `[macro: <name>]`, with the title of an ri:page found
// among its parameters appended when present. Parameter text is never emitted
// as body text.
func (c *converter) handleMacro(elem xml.StartElement, decoder *xml.Decoder) error {
	name := getAttr(elem.Attr, "ac:name")
	if name == "" {
		name = "unknown"
	}

	var lang, code, pageTitle string
	hasCode, hasRichBody := false, false
	depth := 1

	for {
		tok, err := nextToken(decoder)
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch xmlName(t.Name) {
			case "ac:parameter":
				text, title, err := c.collectParameter(decoder)
				if err != nil {
					return err
				}
				if getAttr(t.Attr, "ac:name") == "language" {
					lang = strings.TrimSpace(text)
				}
				if pageTitle == "" {
					pageTitle = title
				}
			case "ac:plain-text-body":
				hasCode = true
				code, err = c.collectInlineText(decoder)
				if err != nil {
					return err
				}
			case "ac:rich-text-body":
				hasRichBody = true
				if err := c.parseUntilEnd(decoder); err != nil {
					return err
				}
			default:
				depth++
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				switch {
				case hasRichBody:
				case hasCode || name == "code":
					c.buf.WriteString("```" + lang + "\n")
					c.buf.WriteString(strings.TrimSpace(code))
					c.buf.WriteString("\n```\n")
				default:
					c.buf.WriteString("[macro: " + name)
					if pageTitle != "" {
						c.buf.WriteString(" " + pageTitle)
					}
					c.buf.WriteString("]")
					if !c.inParagraph {
						c.buf.WriteString("\n\n")
					}
				}
				return nil
			}
		}
	}
}

// collectParameter consumes an <ac:parameter> subtree, returning its character
// data and the content-title of the first ri:page inside it.
func (c *converter) collectParameter(decoder *xml.Decoder) (text, pageTitle string, err error) {
	var sb strings.Builder
	depth := 1

	for {
		tok, err := nextToken(decoder)
		if err != nil {
			return "", "", err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if xmlName(t.Name) == "ri:page" && pageTitle == "" {
				pageTitle = getAttr(t.Attr, "ri:content-title")
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				return sb.String(), pageTitle, nil
			}
		case xml.CharData:
			sb.Write(t)
		}
	}
}

func (c *converter) handleTask(decoder *xml.Decoder) error {
	var status, body string
	depth := 1

	for {
		tok, err := nextToken(decoder)
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch xmlName(t.Name) {
			case "ac:task-status":
				status, err = c.collectInlineText(decoder)
			case "ac:task-body":
				body, err = c.collectInlineText(decoder)
			default:
				depth++
			}
			if err != nil {
				return err
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				checkbox := "[ ]"
				if status == "complete" {
					checkbox = "[x]"
				}
				c.buf.WriteString("- " + checkbox + " " + strings.TrimSpace(body) + "\n")
				return nil
			}
		}
	}
}

// collectTableCell converts a <td>/<th> subtree with a nested converter so
// inline formatting, links, and mentions survive, then flattens the result to
// one line.
func (c *converter) collectTableCell(decoder *xml.Decoder) error {
	var cell bytes.Buffer
	sub := newConverter(&cell, c.opts)
	sub.inParagraph = true

	if err := sub.parseUntilEnd(decoder); err != nil {
		return err
	}

	text := cellWhitespace.ReplaceAllString(strings.TrimSpace(cell.String()), " ")
	c.tableRowCells = append(c.tableRowCells, text)
	return nil
}

func (c *converter) writeTableRow() {
	if len(c.tableRowCells) == 0 {
		return
	}

	c.buf.WriteString("| " + strings.Join(c.tableRowCells, " | ") + " |\n")

	// If this was a header row, write separator
	if c.inTableHeader {
		seps := make([]string, len(c.tableRowCells))
		for i := range seps {
			seps[i] = "---"
		}
		c.buf.WriteString("| " + strings.Join(seps, " | ") + " |\n")
		c.inTableHeader = false
	}

	c.tableRowCells = nil
}

func (c *converter) writeListPrefix() {
	indent := strings.Repeat("  ", c.listDepth-1)
	c.buf.WriteString(indent)

	if len(c.inOrderedList) > 0 && c.inOrderedList[len(c.inOrderedList)-1] {
		idx := c.listDepth - 1
		if idx < len(c.orderedCounter) {
			c.orderedCounter[idx]++
			c.buf.WriteString(strconv.Itoa(c.orderedCounter[idx]) + ". ")
		}
	} else {
		c.buf.WriteString("- ")
	}
}

// collectInlineText consumes the current element's subtree and returns its
// character data (including CDATA), ignoring markup.
func (c *converter) collectInlineText(decoder *xml.Decoder) (string, error) {
	var text strings.Builder
	depth := 1

	for {
		tok, err := nextToken(decoder)
		if err != nil {
			return "", err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				return text.String(), nil
			}
		case xml.CharData:
			text.Write(t)
		}
	}
}

func getAttr(attrs []xml.Attr, name string) string {
	// Handle both namespaced and non-namespaced attribute lookups
	parts := strings.SplitN(name, ":", 2)

	for _, a := range attrs {
		// Build the full attribute name
		fullName := a.Name.Local
		if a.Name.Space != "" {
			fullName = a.Name.Space + ":" + a.Name.Local
		}

		// Check for exact match
		if fullName == name {
			return a.Value
		}

		// Check for local name match (for non-namespaced queries)
		if len(parts) == 1 && a.Name.Local == name {
			return a.Value
		}

		// Check for namespace prefix match
		if len(parts) == 2 && a.Name.Local == parts[1] {
			return a.Value
		}
	}
	return ""
}

var attachmentURLPattern = regexp.MustCompile(`^/wiki/download/attachments/`)

func isAttachmentURL(url string) bool {
	return attachmentURLPattern.MatchString(url)
}
