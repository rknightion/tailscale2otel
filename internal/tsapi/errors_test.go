package tsapi_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
)

// TestStatusCode is the guard for the one shared status-code accessor (#420).
// Before it existed, every collector reached into *StatusError.Code through its
// own errors.As, and the predicates disagreed about what each code meant.
func TestStatusCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantOK   bool
	}{
		{"nil error", nil, 0, false},
		{"bare StatusError", &tsapi.StatusError{Code: 403}, 403, true},
		{"wrapped StatusError", fmt.Errorf("fetch: %w", &tsapi.StatusError{Code: 401}), 401, true},
		{
			"doubly wrapped",
			fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", &tsapi.StatusError{Code: 404})),
			404, true,
		},
		{"untyped error", errors.New("boom"), 0, false},
		{"context cancellation", context.Canceled, 0, false},
		// A plain error whose TEXT mentions a status must not be read as one:
		// the flow-log error text embeds up to 16KB of response body.
		{"text mentioning 403", errors.New("dial 10.0.0.1:8403: forbidden"), 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, ok := tsapi.StatusCode(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("StatusCode(%v) ok = %v, want %v", tc.err, ok, tc.wantOK)
			}
			if code != tc.wantCode {
				t.Errorf("StatusCode(%v) code = %d, want %d", tc.err, code, tc.wantCode)
			}
		})
	}
}
