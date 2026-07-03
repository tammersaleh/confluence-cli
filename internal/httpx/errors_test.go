package httpx

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	"github.com/tammersaleh/confluence-sync/internal/output"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantErr      string
		wantCode     int
		wantEndpoint string
	}{
		{
			name:         "rate limit exhausted",
			err:          &RateLimitError{Endpoint: "/pages", Retries: 5, Err: errors.New("429")},
			wantErr:      "rate_limited",
			wantCode:     output.ExitRateLimit,
			wantEndpoint: "/pages",
		},
		{
			name:     "raw deadline exceeded",
			err:      context.DeadlineExceeded,
			wantErr:  "timeout",
			wantCode: output.ExitNetwork,
		},
		{
			name:     "deadline exceeded wrapped in url.Error",
			err:      &url.Error{Op: "Get", URL: "http://x", Err: context.DeadlineExceeded},
			wantErr:  "timeout",
			wantCode: output.ExitNetwork,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			wantErr:  "canceled",
			wantCode: output.ExitGeneral,
		},
		{
			name:     "net.OpError",
			err:      &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			wantErr:  "network_error",
			wantCode: output.ExitNetwork,
		},
		{
			name:     "plain url.Error",
			err:      &url.Error{Op: "Get", URL: "http://x", Err: errors.New("x")},
			wantErr:  "network_error",
			wantCode: output.ExitNetwork,
		},
		{
			name:     "generic error",
			err:      errors.New("boom"),
			wantErr:  "boom",
			wantCode: output.ExitGeneral,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyError(tt.err)
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

func TestRateLimitError(t *testing.T) {
	inner := errors.New("429 Too Many Requests")
	e := &RateLimitError{Endpoint: "/pages", Retries: 3, Err: inner}

	if got, want := e.Error(), "rate limited after 3 retries on /pages"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(e, inner) {
		t.Error("Unwrap should expose the inner error")
	}
}
