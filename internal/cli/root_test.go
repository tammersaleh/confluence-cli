package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/confluence"
	"github.com/tammersaleh/confluence-sync/internal/output"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantErr  string
		wantCode int
	}{
		{"unauthorized", confluence.ErrUnauthorized, "unauthorized", output.ExitAuth},
		{"forbidden", confluence.ErrForbidden, "forbidden", output.ExitAuth},
		{"space not found", confluence.ErrSpaceNotFound, "space_not_found", output.ExitGeneral},
		{"api error", confluence.ErrAPIError, "api_error", output.ExitGeneral},
		{
			name:     "wrapped space not found still maps",
			err:      fmt.Errorf("getting space: %w", confluence.ErrSpaceNotFound),
			wantErr:  "space_not_found",
			wantCode: output.ExitGeneral,
		},
		{
			name:     "deadline exceeded delegates to httpx",
			err:      context.DeadlineExceeded,
			wantErr:  "timeout",
			wantCode: output.ExitNetwork,
		},
		{
			name:     "plain error is general",
			err:      errors.New("boom"),
			wantErr:  "boom",
			wantCode: output.ExitGeneral,
		},
	}

	c := &CLI{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.ClassifyError(tt.err)
			if got.Err != tt.wantErr {
				t.Errorf("Err = %q, want %q", got.Err, tt.wantErr)
			}
			if got.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", got.Code, tt.wantCode)
			}
		})
	}
}

func TestParsedFields(t *testing.T) {
	c := &CLI{Fields: " a, b ,,c "}
	got := c.ParsedFields()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("ParsedFields() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ParsedFields()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
