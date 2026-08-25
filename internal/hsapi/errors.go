package hsapi

import (
	"errors"
	"fmt"
)

// StatusError is a non-2xx response from the Headscale API, carrying the code
// so callers can classify it instead of matching on message text.
//
// This mirrors tsapi.StatusError deliberately. Headscale errors used to be
// bare fmt.Errorf strings, which meant nothing above this package could tell a
// revoked API key (401) from a decode failure — so `-preflight` reported every
// Headscale credential problem as a collector failure (exit 4, "the credential
// works, a collector doesn't") when the actual fix was to rotate the key
// (exit 3). The two exit codes exist to point at different next actions, so
// getting the classification wrong is worse than not classifying at all.
//
// Body is truncated by the caller and retained only for explicitly sensitive
// inspection. It is never included in Error because a peer can reflect request
// credentials into its response.
type StatusError struct {
	Path string
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("headscale GET %s: status %d", e.Path, e.Code)
}

// StatusCode returns the HTTP status carried by err, if any error in its chain
// is a *StatusError. This is the only sanctioned way to read a Headscale
// status out of an error; see tsapi.StatusCode's comment for what happens when
// each caller hand-rolls its own predicate instead.
func StatusCode(err error) (int, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code, true
	}
	return 0, false
}
