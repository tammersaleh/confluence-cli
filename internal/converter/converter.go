package converter

import (
	"bytes"
	"path"
	"regexp"
	"strconv"
	"strings"

	"encoding/xml"
)

type Options struct {
	AttachmentPath string
}

func Convert(storage string) string {
	return ConvertWithOptions(storage, Options{})
}

func ConvertWithOptions(storage string, opts Options) string {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		return ""
	}

	// Wrap in root element for valid XML
	wrapped := "<root>" + storage + "</root>"

	decoder := xml.NewDecoder(strings.NewReader(wrapped))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity

	var buf bytes.Buffer
	c := &converter{
		buf:            &buf,
		opts:           opts,
		listDepth:      0,
		orderedCounter: make([]int, 10),
	}

	c.parse(decoder)

	result := buf.String()
	// Clean up multiple newlines
	result = regexp.MustCompile(`\n{3,}`).ReplaceAllString(result, "\n\n")
	result = strings.TrimRight(result, "\n")
	if result != "" {
		result += "\n"
	}
	return result
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
	tableHeaders   []string
}

func (c *converter) parse(decoder *xml.Decoder) {
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			c.handleStart(t, decoder)
		case xml.EndElement:
			c.handleEnd(t)
		case xml.CharData:
			c.handleCharData(t)
		}
	}
}

func xmlName(name xml.Name) string {
	if name.Space != "" {
		return name.Space + ":" + name.Local
	}
	return name.Local
}

func (c *converter) handleStart(elem xml.StartElement, decoder *xml.Decoder) {
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
		c.handleLink(elem, decoder)
		return
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
		c.collectTableCell(decoder, "th")
		return
	case "td":
		c.collectTableCell(decoder, "td")
		return
	case "ac:structured-macro":
		macroName := getAttr(elem.Attr, "ac:name")
		if macroName == "code" {
			c.handleCodeMacro(decoder)
			return
		}
	case "ac:image":
		c.handleImage(decoder)
		return
	case "ac:task-list":
		// Task list container
	case "ac:task":
		c.handleTask(decoder)
		return
	}
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
	case "table":
		c.tableHeaders = nil
	}
}

func (c *converter) handleCharData(data xml.CharData) {
	text := string(data)

	// Normalize whitespace but preserve meaningful spaces
	if c.inParagraph || c.listDepth > 0 {
		// Replace newlines and tabs with spaces, collapse multiple spaces
		text = regexp.MustCompile(`[\n\t]+`).ReplaceAllString(text, " ")
		if text != "" {
			c.buf.WriteString(text)
		}
	} else if strings.TrimSpace(text) != "" {
		c.buf.WriteString(strings.TrimSpace(text))
	}
}

func (c *converter) handleLink(elem xml.StartElement, decoder *xml.Decoder) {
	href := getAttr(elem.Attr, "href")
	text := c.collectInlineText(decoder, "a")

	// Rewrite attachment URLs
	if c.opts.AttachmentPath != "" && isAttachmentURL(href) {
		filename := path.Base(href)
		href = c.opts.AttachmentPath + "/" + filename
	}

	c.buf.WriteString("[" + text + "](" + href + ")")
}

func (c *converter) handleImage(decoder *xml.Decoder) {
	var filename string
	depth := 1

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			name := xmlName(t.Name)
			if name == "ri:attachment" {
				filename = getAttr(t.Attr, "ri:filename")
			}
		case xml.EndElement:
			depth--
			name := xmlName(t.Name)
			if name == "ac:image" || depth == 0 {
				imgPath := filename
				if c.opts.AttachmentPath != "" && filename != "" {
					imgPath = c.opts.AttachmentPath + "/" + filename
				}
				if filename != "" {
					c.buf.WriteString("![" + filename + "](" + imgPath + ")\n")
				}
				return
			}
		}
	}
}

func (c *converter) handleCodeMacro(decoder *xml.Decoder) {
	var lang string
	var code string
	depth := 1

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			name := xmlName(t.Name)
			if name == "ac:parameter" && getAttr(t.Attr, "ac:name") == "language" {
				lang = c.collectInlineText(decoder, "ac:parameter")
				depth--
			} else if name == "ac:plain-text-body" {
				code = c.collectCDATA(decoder)
				depth--
			}
		case xml.EndElement:
			depth--
			name := xmlName(t.Name)
			if name == "ac:structured-macro" || depth == 0 {
				c.buf.WriteString("```" + lang + "\n")
				c.buf.WriteString(strings.TrimSpace(code))
				c.buf.WriteString("\n```\n")
				return
			}
		}
	}
}

func (c *converter) handleTask(decoder *xml.Decoder) {
	var status string
	var body string
	depth := 1

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			name := xmlName(t.Name)
			if name == "ac:task-status" {
				status = c.collectInlineText(decoder, "ac:task-status")
				depth--
			} else if name == "ac:task-body" {
				body = c.collectInlineText(decoder, "ac:task-body")
				depth--
			}
		case xml.EndElement:
			depth--
			name := xmlName(t.Name)
			if name == "ac:task" || depth == 0 {
				checkbox := "[ ]"
				if status == "complete" {
					checkbox = "[x]"
				}
				c.buf.WriteString("- " + checkbox + " " + strings.TrimSpace(body) + "\n")
				return
			}
		}
	}
}

func (c *converter) collectTableCell(decoder *xml.Decoder, tagName string) {
	text := c.collectInlineText(decoder, tagName)
	c.tableRowCells = append(c.tableRowCells, strings.TrimSpace(text))
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
		c.tableHeaders = c.tableRowCells
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

func (c *converter) collectInlineText(decoder *xml.Decoder, endTag string) string {
	var text strings.Builder
	depth := 1

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
			name := xmlName(t.Name)
			if depth == 0 || name == endTag {
				return text.String()
			}
		case xml.CharData:
			text.Write(t)
		}
	}
	return text.String()
}

func (c *converter) collectCDATA(decoder *xml.Decoder) string {
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.CharData:
			return string(t)
		case xml.EndElement:
			return ""
		}
	}
	return ""
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
