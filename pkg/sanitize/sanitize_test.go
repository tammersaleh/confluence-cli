package sanitize

import (
	"testing"
)

func TestFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "lowercase",
			input: "Hello World",
			want:  "hello-world",
		},
		{
			name:  "already lowercase",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "spaces to hyphens",
			input: "my page title",
			want:  "my-page-title",
		},
		{
			name:  "multiple spaces",
			input: "hello   world",
			want:  "hello-world",
		},
		{
			name:  "special characters removed",
			input: "API Design & Docs!",
			want:  "api-design-docs",
		},
		{
			name:  "numbers preserved",
			input: "Version 2.0",
			want:  "version-20",
		},
		{
			name:  "hyphens preserved",
			input: "already-hyphenated",
			want:  "already-hyphenated",
		},
		{
			name:  "collapse multiple hyphens",
			input: "foo---bar",
			want:  "foo-bar",
		},
		{
			name:  "trim leading hyphens",
			input: "---hello",
			want:  "hello",
		},
		{
			name:  "trim trailing hyphens",
			input: "hello---",
			want:  "hello",
		},
		{
			name:  "complex title",
			input: "  API Design: Best Practices (2024)  ",
			want:  "api-design-best-practices-2024",
		},
		{
			name:  "unicode removed",
			input: "Café Design",
			want:  "caf-design",
		},
		{
			name:  "empty after sanitization",
			input: "!!!",
			want:  "page",
		},
		{
			name:  "empty string",
			input: "",
			want:  "page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Filename(tt.input)
			if got != tt.want {
				t.Errorf("Filename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFilenameWithCollision(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		existing map[string]bool
		want     string
	}{
		{
			name:     "no collision",
			input:    "my-page",
			existing: map[string]bool{},
			want:     "my-page",
		},
		{
			name:     "first collision",
			input:    "my-page",
			existing: map[string]bool{"my-page": true},
			want:     "my-page-2",
		},
		{
			name:     "second collision",
			input:    "my-page",
			existing: map[string]bool{"my-page": true, "my-page-2": true},
			want:     "my-page-3",
		},
		{
			name:     "multiple collisions",
			input:    "test",
			existing: map[string]bool{"test": true, "test-2": true, "test-3": true},
			want:     "test-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilenameWithCollision(tt.input, tt.existing)
			if got != tt.want {
				t.Errorf("FilenameWithCollision(%q, ...) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
