package tsapi

import (
	"errors"
	"fmt"
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
	return fmt.Sprintf("tsapi: %s %s: status %d: %s", e.Method, e.URL, e.Code, e.Body)
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
// Matching on the error TEXT is never acceptable: a StatusError's Body carries
// up to 16KB of upstream response, so a substring test for "403" or
// "forbidden" can match a proxy port (10.0.0.1:8403) or a 5xx error page that
// merely says "Forbidden".
func StatusCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if se, ok := errors.AsType[*StatusError](err); ok {
		return se.Code, true
	}
	return 0, false
}
