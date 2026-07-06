package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		expected int
	}{
		{"success", ExitSuccess, 0},
		{"general", ExitGeneral, 1},
		{"auth", ExitAuth, 2},
		{"rate limit", ExitRateLimit, 3},
		{"network", ExitNetwork, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.expected {
				t.Errorf("got %d, want %d", tt.code, tt.expected)
			}
		})
	}
}

func TestErrorInterface(t *testing.T) {
	err := &Error{Err: "page_not_found", Detail: "No such page"}
	if err.Error() != "page_not_found" {
		t.Errorf("got %q, want %q", err.Error(), "page_not_found")
	}
}

func TestExitErrorInterface(t *testing.T) {
	e := &ExitError{Code: ExitRateLimit}
	if e.Error() != "exit" {
		t.Errorf("got %q, want %q", e.Error(), "exit")
	}
}

func TestPrintItem_CompactJSON(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	type item struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := p.PrintItem(item{ID: "123", Title: "Home"}); err != nil {
		t.Fatal(err)
	}

	want := `{"id":"123","title":"Home"}` + "\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestPrintItem_NonObjectWrittenAsIs(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}}

	if err := p.PrintItem([]int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	want := "[1,2,3]\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestPrintItem_EnrichmentRunsAfterFiltering(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{
		Out:    &out,
		Err:    &bytes.Buffer{},
		Fields: []string{"id"},
		EnrichFunc: func(m map[string]any) {
			if _, ok := m["id"]; ok {
				m["id_name"] = "resolved"
			}
			// This must not leak: "title" was already filtered out.
			if _, ok := m["title"]; ok {
				t.Error("EnrichFunc saw filtered-out field 'title'")
			}
		},
	}

	msg := map[string]any{"id": "123", "title": "Home"}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["id_name"] != "resolved" {
		t.Errorf("expected enrichment field id_name, got %v", got["id_name"])
	}
	if _, ok := got["title"]; ok {
		t.Error("'title' should be filtered out")
	}
}

func TestPrintMeta(t *testing.T) {
	tests := []struct {
		name string
		meta Meta
		want string
	}{
		{"has more with cursor", Meta{HasMore: true, NextCursor: "abc123"}, `{"_meta":{"has_more":true,"next_cursor":"abc123"}}`},
		{"no more", Meta{HasMore: false}, `{"_meta":{"has_more":false}}`},
		{"with error count", Meta{HasMore: false, ErrorCount: 2}, `{"_meta":{"has_more":false,"error_count":2}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			p := &Printer{Out: &out, Err: &bytes.Buffer{}}
			if err := p.PrintMeta(tt.meta); err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.want+"\n" {
				t.Errorf("got %q, want %q", out.String(), tt.want+"\n")
			}
		})
	}
}

func TestPrintError(t *testing.T) {
	var out, errOut bytes.Buffer
	p := &Printer{Out: &out, Err: &errOut}

	e := &Error{
		Err:    "page_not_found",
		Detail: "No page matching 'nope'",
		Hint:   "Run 'confluence page list ...' to list pages",
		Code:   ExitGeneral,
	}
	if err := p.PrintError(e); err != nil {
		t.Fatal(err)
	}

	if out.Len() != 0 {
		t.Errorf("unexpected stdout output: %s", out.String())
	}

	var got map[string]any
	if err := json.Unmarshal(errOut.Bytes(), &got); err != nil {
		t.Fatalf("invalid error JSON: %v", err)
	}
	if got["error"] != "page_not_found" {
		t.Errorf("got error=%q, want %q", got["error"], "page_not_found")
	}
	if _, ok := got["code"]; ok {
		t.Error("code field should not be in JSON output")
	}
}

func TestPrintError_OmitsEmpty(t *testing.T) {
	var errOut bytes.Buffer
	p := &Printer{Out: &bytes.Buffer{}, Err: &errOut}

	e := &Error{Err: "unknown_error"}
	if err := p.PrintError(e); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(errOut.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["detail"]; ok {
		t.Error("empty detail should be omitted")
	}
	if _, ok := got["hint"]; ok {
		t.Error("empty hint should be omitted")
	}
}

func TestQuiet_SuppressesPrintItem(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Quiet: true}

	if err := p.PrintItem(map[string]string{"id": "123"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("quiet mode should suppress PrintItem output, got: %s", out.String())
	}
}

func TestQuiet_SuppressesPrintMeta(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Quiet: true}

	if err := p.PrintMeta(Meta{HasMore: false}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("quiet mode should suppress PrintMeta output, got: %s", out.String())
	}
}

func TestQuiet_DoesNotSuppressPrintError(t *testing.T) {
	var errOut bytes.Buffer
	p := &Printer{Out: &bytes.Buffer{}, Err: &errOut, Quiet: true}

	e := &Error{Err: "something_broke"}
	if err := p.PrintError(e); err != nil {
		t.Fatal(err)
	}
	if errOut.Len() == 0 {
		t.Error("quiet mode should NOT suppress PrintError")
	}
}

func TestFieldFiltering(t *testing.T) {
	tests := []struct {
		name    string
		fields  []string
		in      map[string]any
		wantIn  []string // keys expected present
		wantOut []string // keys expected absent
	}{
		{
			name:    "whitelist keeps only listed",
			fields:  []string{"id", "title"},
			in:      map[string]any{"id": "1", "title": "Home", "version": 3, "space": "DEV"},
			wantIn:  []string{"id", "title"},
			wantOut: []string{"version", "space"},
		},
		{
			name:    "input always kept",
			fields:  []string{"id"},
			in:      map[string]any{"input": "Home", "id": "1", "title": "Home"},
			wantIn:  []string{"input", "id"},
			wantOut: []string{"title"},
		},
		{
			name:    "empty fields passthrough",
			fields:  nil,
			in:      map[string]any{"id": "1", "title": "Home"},
			wantIn:  []string{"id", "title"},
			wantOut: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			p := &Printer{Out: &out, Err: &bytes.Buffer{}, Fields: tt.fields}
			if err := p.PrintItem(tt.in); err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			for _, k := range tt.wantIn {
				if _, ok := got[k]; !ok {
					t.Errorf("expected key %q present", k)
				}
			}
			for _, k := range tt.wantOut {
				if _, ok := got[k]; ok {
					t.Errorf("key %q should be filtered out", k)
				}
			}
		})
	}
}

func TestError_AsItem(t *testing.T) {
	e := &Error{
		Err:    "page_not_found",
		Detail: "No page matching 'nope'",
		Hint:   "list pages first",
		Input:  "nope",
	}
	m := e.AsItem()
	if m["error"] != "page_not_found" {
		t.Errorf("error=%v want page_not_found", m["error"])
	}
	if m["input"] != "nope" {
		t.Errorf("input=%v want 'nope' (pulled from Error.Input)", m["input"])
	}
	if _, ok := m["detail"]; !ok {
		t.Error("detail should be present when Detail is non-empty")
	}
	if _, ok := m["hint"]; !ok {
		t.Error("hint should be present when Hint is non-empty")
	}
}

func TestError_AsItem_OmitsEmpty(t *testing.T) {
	e := &Error{Err: "some_error"}
	m := e.AsItem()
	if m["error"] != "some_error" {
		t.Errorf("error=%v want some_error", m["error"])
	}
	if _, ok := m["detail"]; ok {
		t.Error("detail should be omitted when Detail is empty")
	}
	if _, ok := m["hint"]; ok {
		t.Error("hint should be omitted when Hint is empty")
	}
	if _, ok := m["input"]; ok {
		t.Error("input should be omitted when Input is empty")
	}
}

// Field filtering runs before enrichment, so asking for `ts` still yields `ts_iso`.
func TestFieldFiltering_PreservesTimestampEnrichment(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out, Err: &bytes.Buffer{}, Fields: []string{"ts"}}

	msg := map[string]any{
		"ts":   "1705312200.123456",
		"text": "hello",
	}
	if err := p.PrintItem(msg); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := got["ts"]; !ok {
		t.Error("expected 'ts' field")
	}
	if _, ok := got["ts_iso"]; !ok {
		t.Error("expected 'ts_iso' field to survive field filtering")
	}
	if _, ok := got["text"]; ok {
		t.Error("'text' should be filtered out")
	}
}
