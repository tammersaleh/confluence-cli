// Package bodywrite handles body input for write commands: reading a body from
// stdin (distinguishing an interactive TTY from piped input) and validating a
// raw body for the Confluence storage or ADF formats.
package bodywrite

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

// Read consumes a write command's body from stdin. It distinguishes an absent
// body (interactive TTY - do NOT block) from a present-but-empty body (piped
// empty input). When r is an *os.File that is a character device (TTY),
// present is false and nothing is read. Otherwise it reads all of r.
func Read(r io.Reader) (present bool, body string, err error) {
	if f, ok := r.(*os.File); ok {
		info, _ := f.Stat()
		if info != nil && info.Mode()&os.ModeCharDevice != 0 {
			return false, "", nil
		}
	}

	b, err := io.ReadAll(r)
	if err != nil {
		return false, "", err
	}
	return true, string(b), nil
}

// Prepare validates a raw body for the given write format and returns the
// Confluence body representation + value to send. format accepts "storage",
// "adf", or "atlas_doc_format" (adf is the documented alias).
//
//	storage -> (representation="storage", value=raw), validated as a
//	           well-formed XHTML fragment.
//	adf     -> (representation="atlas_doc_format", value=compact JSON string),
//	           validated as an ADF doc and re-marshaled to compact JSON.
func Prepare(format, raw string) (representation, value string, err error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "storage":
		if err := validateStorageFragment(raw); err != nil {
			return "", "", err
		}
		return "storage", raw, nil
	case "adf", "atlas_doc_format":
		compact, err := validateADF(raw)
		if err != nil {
			return "", "", err
		}
		return "atlas_doc_format", compact, nil
	default:
		return "", "", fmt.Errorf("unknown body format %q (use storage or adf)", format)
	}
}

// validateStorageFragment checks that s is a well-formed XHTML fragment. An
// empty (or whitespace-only) body is allowed. Non-empty input is wrapped in a
// synthetic root element and decoded with encoding/xml, mirroring the
// converter's decoder setup so self-closing tags do not error.
func validateStorageFragment(s string) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}

	if strings.HasPrefix(trimmed, "<?xml") {
		return fmt.Errorf("storage body must be an XHTML fragment, not a full XML document (remove the <?xml ...?> declaration)")
	}
	if lower := strings.ToLower(trimmed); strings.HasPrefix(lower, "<!doctype") ||
		strings.HasPrefix(lower, "<html") || strings.HasPrefix(lower, "<body") {
		return fmt.Errorf("storage body must be an XHTML fragment, not a full HTML document (omit the <html>/<body> wrapper)")
	}

	// Strict parsing catches mismatched/unclosed tags. AutoClose (mirroring the
	// converter) lets HTML void elements like <br> close themselves, and
	// HTMLEntity tolerates named entities such as &nbsp;.
	decoder := xml.NewDecoder(strings.NewReader("<root>" + s + "</root>"))
	decoder.Strict = true
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity

	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("storage body is not well-formed XML: %w", err)
		}
	}
}

// validateADF validates s as an Atlassian Document Format document and returns
// its compact JSON re-marshaling.
func validateADF(s string) (string, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(s), &root); err != nil {
		return "", fmt.Errorf("ADF body is not valid JSON: %w", err)
	}

	if t, ok := root["type"].(string); !ok || t != "doc" {
		return "", fmt.Errorf("ADF root must be an object with type=doc and numeric version")
	}
	if _, ok := root["version"].(float64); !ok {
		return "", fmt.Errorf("ADF root must be an object with type=doc and numeric version")
	}
	if c, ok := root["content"]; ok {
		arr, ok := c.([]any)
		if !ok {
			return "", fmt.Errorf("ADF content must be an array")
		}
		if err := validateADFNodes(arr); err != nil {
			return "", err
		}
	}

	compact, err := json.Marshal(root)
	if err != nil {
		return "", fmt.Errorf("failed to re-marshal ADF: %w", err)
	}
	return string(compact), nil
}

func validateADFNodes(nodes []any) error {
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			return fmt.Errorf("ADF node must be an object")
		}
		typ, ok := node["type"].(string)
		if !ok {
			return fmt.Errorf("ADF node missing string \"type\"")
		}
		if typ == "text" {
			if _, ok := node["text"].(string); !ok {
				return fmt.Errorf("ADF text node missing string \"text\"")
			}
		}
		if m, ok := node["marks"]; ok {
			marks, ok := m.([]any)
			if !ok {
				return fmt.Errorf("ADF marks must be an array")
			}
			for _, mk := range marks {
				mark, ok := mk.(map[string]any)
				if !ok {
					return fmt.Errorf("ADF mark must be an object")
				}
				if _, ok := mark["type"].(string); !ok {
					return fmt.Errorf("ADF mark missing string \"type\"")
				}
			}
		}
		if c, ok := node["content"]; ok {
			arr, ok := c.([]any)
			if !ok {
				return fmt.Errorf("ADF content must be an array")
			}
			if err := validateADFNodes(arr); err != nil {
				return err
			}
		}
	}
	return nil
}
