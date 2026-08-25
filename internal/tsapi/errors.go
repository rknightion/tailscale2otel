package tsapi

import (
	"errors"
	"fmt"

	"github.com/rknightion/tailscale2otel/v4/internal/redact"
)

// StatusError is returned by the JSON helpers when the Tailscale API responds
// with a non-2xx HTTP status. Callers branch on the HTTP status code through
// [StatusCode] (or errors.As) so that "this optional feature is not enabled"
// can be told apart from "this credential was rejected".
type StatusError struct {
	Method string
	URL    string
	Code   int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("tsapi: %s %s: status %d", e.Method, redact.URLOrigin(e.URL), e.Code)
}

// StatusCode returns the HTTP status carried by err, if any error in its chain
// is a *StatusError.
//
// This is the ONLY sanctioned way to read a status code out of an API error
// (#420). Every collector used to hand-roll its own errors.As predicate, and
// they disagreed — flowlogs read 403 as "premium feature off" while logstream
// read any 4xx, a genuine 401 included, as "not configured". Routing every
// caller through one accessor (and, above it, apistate.Classify) is what stops
// those readings from drifting apart again.
//
// Matching on the error TEXT is never acceptable. Body may retain up to 16KB
// for explicitly sensitive inspection, but Error deliberately omits it because
// an upstream can reflect request credentials.
func StatusCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if se, ok := errors.AsType[*StatusError](err); ok {
		return se.Code, true
	}
	return 0, false
}
