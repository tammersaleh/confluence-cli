package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONLinesTracer(t *testing.T) {
	var buf bytes.Buffer
	tr := NewJSONLinesTracer(&buf)

	tr.Event("request", map[string]any{"method": "GET", "url": "/pages"})
	tr.Event("response", map[string]any{"status": 200})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	tests := []struct {
		line     string
		wantKind string
		wantKey  string
		wantVal  any
	}{
		{lines[0], "request", "method", "GET"},
		{lines[1], "response", "status", float64(200)},
	}
	for _, tt := range tests {
		var m map[string]any
		if err := json.Unmarshal([]byte(tt.line), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", tt.line, err)
		}
		if m["kind"] != tt.wantKind {
			t.Errorf("kind = %v, want %v", m["kind"], tt.wantKind)
		}
		if _, ok := m["ts"].(string); !ok {
			t.Errorf("ts missing or not a string: %v", m["ts"])
		}
		if m[tt.wantKey] != tt.wantVal {
			t.Errorf("%s = %v, want %v", tt.wantKey, m[tt.wantKey], tt.wantVal)
		}
	}
}

func TestJSONLinesTracer_DoesNotMutateCallerMap(t *testing.T) {
	var buf bytes.Buffer
	tr := NewJSONLinesTracer(&buf)

	attrs := map[string]any{"a": 1}
	tr.Event("x", attrs)
	if _, ok := attrs["ts"]; ok {
		t.Error("caller's map was mutated with ts")
	}
	if _, ok := attrs["kind"]; ok {
		t.Error("caller's map was mutated with kind")
	}
}

func TestTracerFrom_NoopWhenAbsent(t *testing.T) {
	tr := TracerFrom(context.Background())
	if tr == nil {
		t.Fatal("TracerFrom returned nil")
	}
	// Must not panic.
	tr.Event("noop", map[string]any{"k": "v"})
}

func TestWithTracer_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := NewJSONLinesTracer(&buf)
	ctx := WithTracer(context.Background(), want)
	if got := TracerFrom(ctx); got != want {
		t.Errorf("TracerFrom = %v, want %v", got, want)
	}
}

func TestWithTracer_NilReturnsContextUnchanged(t *testing.T) {
	ctx := context.Background()
	if got := WithTracer(ctx, nil); got != ctx {
		t.Error("WithTracer(nil) should return the context unchanged")
	}
}
