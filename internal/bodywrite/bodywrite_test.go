package bodywrite

import (
	"os"
	"strings"
	"testing"
)

func TestReadFromReader(t *testing.T) {
	present, body, err := Read(strings.NewReader("<p>hi</p>"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Errorf("present = false, want true")
	}
	if body != "<p>hi</p>" {
		t.Errorf("body = %q, want %q", body, "<p>hi</p>")
	}
}

func TestReadEmptyReader(t *testing.T) {
	present, body, err := Read(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Errorf("present = false, want true (empty piped input is present)")
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

// os.Pipe yields *os.File that is NOT a character device, so Read must treat it
// as present. The TTY (char-device) path is covered by the ModeCharDevice check
// in Read; it cannot easily be simulated with a real *os.File in a test.
func TestReadFromPipeFile(t *testing.T) {
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer rp.Close()

	go func() {
		wp.WriteString("piped body")
		wp.Close()
	}()

	present, body, err := Read(rp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !present {
		t.Errorf("present = false, want true for a pipe file")
	}
	if body != "piped body" {
		t.Errorf("body = %q, want %q", body, "piped body")
	}
}

// os.DevNull opened as *os.File is not a character device on the test path we
// exercise; confirm it reads as present with an empty body.
func TestReadFromDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer f.Close()

	present, body, err := Read(f)
	// /dev/null is a character device on unix, so present must be false.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if present {
		t.Errorf("present = true, want false for /dev/null (char device)")
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestPrepareStorageValid(t *testing.T) {
	in := "<p>hi <strong>x</strong></p>"
	rep, val, err := Prepare("storage", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep != "storage" {
		t.Errorf("rep = %q, want storage", rep)
	}
	if val != in {
		t.Errorf("val = %q, want %q", val, in)
	}
}

func TestPrepareStorageSelfClosing(t *testing.T) {
	in := "<p>line<br/>break</p>"
	rep, val, err := Prepare("storage", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep != "storage" || val != in {
		t.Errorf("got (%q, %q), want (storage, %q)", rep, val, in)
	}
}

func TestPrepareStorageEmpty(t *testing.T) {
	rep, val, err := Prepare("storage", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep != "storage" || val != "" {
		t.Errorf("got (%q, %q), want (storage, \"\")", rep, val)
	}
}

func TestPrepareStorageMalformed(t *testing.T) {
	_, _, err := Prepare("storage", "<p>unclosed")
	if err == nil {
		t.Fatal("expected error for malformed storage body")
	}
}

func TestPrepareStorageFullDocument(t *testing.T) {
	_, _, err := Prepare("storage", "<html><body><p>hi</p></body></html>")
	if err == nil {
		t.Fatal("expected error for full HTML document")
	}
}

func TestPrepareStorageXMLDeclaration(t *testing.T) {
	_, _, err := Prepare("storage", "<?xml version=\"1.0\"?><p>hi</p>")
	if err == nil {
		t.Fatal("expected error for XML declaration")
	}
}

func TestPrepareADFValid(t *testing.T) {
	in := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}`
	rep, val, err := Prepare("adf", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep != "atlas_doc_format" {
		t.Errorf("rep = %q, want atlas_doc_format", rep)
	}
	if strings.ContainsAny(val, " \n\t") {
		t.Errorf("value not compact: %q", val)
	}
	if !strings.Contains(val, `"type":"doc"`) {
		t.Errorf("value missing doc type: %q", val)
	}
}

func TestPrepareADFAlias(t *testing.T) {
	in := `{"type":"doc","version":1}`
	rep, _, err := Prepare("atlas_doc_format", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep != "atlas_doc_format" {
		t.Errorf("rep = %q, want atlas_doc_format", rep)
	}
}

func TestPrepareADFTopLevelArray(t *testing.T) {
	_, _, err := Prepare("adf", `[{"type":"doc","version":1}]`)
	if err == nil {
		t.Fatal("expected error for top-level array")
	}
}

func TestPrepareADFMissingType(t *testing.T) {
	_, _, err := Prepare("adf", `{"version":1}`)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestPrepareADFMissingVersion(t *testing.T) {
	_, _, err := Prepare("adf", `{"type":"doc"}`)
	if err == nil {
		t.Fatal("expected error for missing version")
	}
}

func TestPrepareADFNonNumericVersion(t *testing.T) {
	_, _, err := Prepare("adf", `{"type":"doc","version":"1"}`)
	if err == nil {
		t.Fatal("expected error for non-numeric version")
	}
}

func TestPrepareADFTextNodeMissingText(t *testing.T) {
	in := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text"}]}]}`
	_, _, err := Prepare("adf", in)
	if err == nil {
		t.Fatal("expected error for text node without text")
	}
}

func TestPrepareADFInvalidJSON(t *testing.T) {
	_, _, err := Prepare("adf", `{not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPrepareADFBadMarks(t *testing.T) {
	in := `{"type":"doc","version":1,"content":[{"type":"text","text":"x","marks":[{"foo":"bar"}]}]}`
	_, _, err := Prepare("adf", in)
	if err == nil {
		t.Fatal("expected error for mark missing type")
	}
}

func TestPrepareUnknownFormat(t *testing.T) {
	for _, f := range []string{"markdown", "view", "xml"} {
		if _, _, err := Prepare(f, "x"); err == nil {
			t.Errorf("format %q: expected error", f)
		}
	}
}
