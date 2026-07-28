package hsapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A Headscale API key that is wrong or revoked is a credential problem, and
// callers have to be able to SAY so — `-preflight` exits 3 ("fix the
// credential") rather than 4 ("the credential works, a collector doesn't")
// precisely on this signal (#311). Before this, getJSON wrapped every non-200
// in a bare fmt.Errorf, so a Headscale 401 was indistinguishable from a
// decoding failure and every Headscale deployment silently got the wrong exit
// code. Matching on the message text is not an option — tsapi.StatusCode's own
// doc comment explains why hand-rolled predicates drift apart.
func TestStatusCode_ReadsTheHeadscaleStatus(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"message":"nope"}`))
			}))
			defer srv.Close()

			c := NewClient(Options{URL: srv.URL, APIKey: "bad", Timeout: 5 * time.Second})
			_, err := c.Nodes(context.Background())
			if err == nil {
				t.Fatalf("status %d should be an error", code)
			}
			if got, ok := StatusCode(err); !ok || got != code {
				t.Errorf("StatusCode(%v) = (%d,%v), want (%d,true)", err, got, ok, code)
			}
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("error chain carries no *StatusError: %v", err)
			}
			if se.Code != code || se.Path == "" {
				t.Errorf("StatusError = %+v, want Code=%d and a non-empty Path", se, code)
			}
		})
	}
}

// A non-status error must not be mistaken for one, or an unreachable server
// would read as some arbitrary HTTP code.
func TestStatusCode_IgnoresNonStatusErrors(t *testing.T) {
	if got, ok := StatusCode(errors.New("connection refused")); ok {
		t.Errorf("StatusCode(plain error) = (%d,true), want ok=false", got)
	}
}
