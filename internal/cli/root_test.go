package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tammersaleh/confluence-cli/internal/confluence"
	"github.com/tammersaleh/confluence-cli/internal/httpx"
	"github.com/tammersaleh/confluence-cli/internal/output"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantErr      string
		wantCode     int
		wantEndpoint string
	}{
		{name: "unauthorized", err: confluence.ErrUnauthorized, wantErr: "unauthorized", wantCode: output.ExitAuth},
		{name: "forbidden", err: confluence.ErrForbidden, wantErr: "forbidden", wantCode: output.ExitAuth},
		{name: "space not found", err: confluence.ErrSpaceNotFound, wantErr: "space_not_found", wantCode: output.ExitGeneral},
		{name: "page not found", err: confluence.ErrPageNotFound, wantErr: "page_not_found", wantCode: output.ExitGeneral},
		{name: "api error", err: confluence.ErrAPIError, wantErr: "api_error", wantCode: output.ExitGeneral},
		{
			name:     "wrapped space not found still maps",
			err:      fmt.Errorf("getting space: %w", confluence.ErrSpaceNotFound),
			wantErr:  "space_not_found",
			wantCode: output.ExitGeneral,
		},
		{
			name:     "wrapped page not found still maps",
			err:      fmt.Errorf("getting page: %w", confluence.ErrPageNotFound),
			wantErr:  "page_not_found",
			wantCode: output.ExitGeneral,
		},
		{
			name:         "status error preserves endpoint",
			err:          &confluence.StatusError{StatusCode: 404, Endpoint: "/wiki/api/v2/pages/123", Err: confluence.ErrPageNotFound},
			wantErr:      "page_not_found",
			wantCode:     output.ExitGeneral,
			wantEndpoint: "/wiki/api/v2/pages/123",
		},
		{
			name:     "deadline exceeded delegates to httpx",
			err:      context.DeadlineExceeded,
			wantErr:  "timeout",
			wantCode: output.ExitNetwork,
		},
		{
			name:         "rate limit wins over wrapped api error",
			err:          &httpx.RateLimitError{Endpoint: "/x", Retries: 5, Err: confluence.ErrAPIError},
			wantErr:      "rate_limited",
			wantCode:     output.ExitRateLimit,
			wantEndpoint: "/x",
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
			if got.Endpoint != tt.wantEndpoint {
				t.Errorf("Endpoint = %q, want %q", got.Endpoint, tt.wantEndpoint)
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
