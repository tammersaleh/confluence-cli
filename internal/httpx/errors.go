package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/tammersaleh/confluence-cli/internal/output"
)

// RateLimitError is returned by transport/pagination helpers when rate-limit
// retries are exhausted. Phase 1's Paginate[T] will produce it; defined here so
// ClassifyError can map it to the rate-limit exit code.
type RateLimitError struct {
	Endpoint string
	Retries  int
	Err      error
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited after %d retries on %s", e.Retries, e.Endpoint)
}

func (e *RateLimitError) Unwrap() error { return e.Err }

// ClassifyError maps a generic error to an *output.Error with the appropriate
// exit code. It is domain-agnostic: Confluence-specific sentinels are mapped by
// the CLI layer, which then delegates the residual error here.
//
// Order matters: the context sentinel checks (DeadlineExceeded, Canceled) run
// before the net checks so a context timeout wrapped in a *url.Error classifies
// as a timeout, not a generic network error.
func ClassifyError(err error) *output.Error {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return &output.Error{
			Err:      "rate_limited",
			Detail:   fmt.Sprintf("Rate limited after %d retries on %s", rl.Retries, rl.Endpoint),
			Endpoint: rl.Endpoint,
			Code:     output.ExitRateLimit,
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &output.Error{
			Err:    "timeout",
			Detail: err.Error(),
			Code:   output.ExitNetwork,
		}
	}

	if errors.Is(err, context.Canceled) {
		return &output.Error{
			Err:    "canceled",
			Detail: err.Error(),
			Code:   output.ExitGeneral,
		}
	}

	var urlErr *url.Error
	var netErr net.Error
	var opErr *net.OpError
	if errors.As(err, &urlErr) || errors.As(err, &netErr) || errors.As(err, &opErr) {
		return &output.Error{
			Err:    "network_error",
			Detail: err.Error(),
			Code:   output.ExitNetwork,
		}
	}

	return &output.Error{
		Err:  err.Error(),
		Code: output.ExitGeneral,
	}
}
