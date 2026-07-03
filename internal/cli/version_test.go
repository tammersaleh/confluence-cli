package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCmd(t *testing.T) {
	var out, errBuf bytes.Buffer
	c := &CLI{}
	c.SetOutput(&out, &errBuf)

	cmd := &VersionCmd{}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 stdout lines, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"version"`) {
		t.Errorf("first line missing version: %q", lines[0])
	}
	if !strings.Contains(lines[1], `"_meta"`) || !strings.Contains(lines[1], `"has_more":false`) {
		t.Errorf("second line not the meta trailer: %q", lines[1])
	}
}

func TestVersionCmd_Quiet(t *testing.T) {
	var out, errBuf bytes.Buffer
	c := &CLI{Quiet: true}
	c.SetOutput(&out, &errBuf)

	cmd := &VersionCmd{}
	if err := cmd.Run(c); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("quiet mode wrote stdout: %q", out.String())
	}
}
