package tsapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestOAuthTokenFetchIsBounded pins #84: a stalled OAuth token refresh must not
// hang forever. oauth2.Transport fetches the token ignoring the request context,
// so only the token-fetch client's own transport timeouts can bound it — the
// request must return an error quickly rather than blocking indefinitely.
func TestOAuthTokenFetchIsBounded(t *testing.T) {
	var tokenHits int32
	unblock := make(chan struct{}) // released at teardown so the stalled handler can exit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			atomic.AddInt32(&tokenHits, 1)
			// Never send response headers; the client's ResponseHeaderTimeout must be
			// what ends the request. Hold until teardown so srv.Close() doesn't block.
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	defer close(unblock) // LIFO: runs before srv.Close(), unblocking the stalled handler

	c, err := buildHTTPClient(Options{
		OAuthClientID:     "id",
		OAuthClientSecret: "secret",
		BaseURL:           srv.URL,
		Timeout:           150 * time.Millisecond,
		MaxAttempts:       1, // keep the test fast/deterministic (no retries)
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
		resp, reqErr := c.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- reqErr
	}()

	select {
	case reqErr := <-done:
		if reqErr == nil {
			t.Fatal("expected an error from the stalled token fetch, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request hung well past the 150ms token-fetch bound (#84 regression)")
	}
	if atomic.LoadInt32(&tokenHits) == 0 {
		t.Fatal("token endpoint was never hit; test did not exercise the refresh path")
	}
}

// TestOAuthTokenFetchBodyStallIsBounded pins #200: the #84 fix bounds
// connection setup, TLS, and response headers, but a token endpoint that
// sends valid headers and then stalls mid-body was still able to hang the
// refresh forever, because ResponseHeaderTimeout only covers the arrival of
// headers, not the body read that follows. The fetch must time out even
// though headers (and a partial body) were already received.
func TestOAuthTokenFetchBodyStallIsBounded(t *testing.T) {
	var tokenHits int32
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			atomic.AddInt32(&tokenHits, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_toke`)) // flush headers + a partial body
			w.(http.Flusher).Flush()
			// Stall mid-body: never write the rest, never end the response.
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	defer close(unblock)

	c, err := buildHTTPClient(Options{
		OAuthClientID:     "id",
		OAuthClientSecret: "secret",
		BaseURL:           srv.URL,
		Timeout:           150 * time.Millisecond,
		MaxAttempts:       1,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
		resp, reqErr := c.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- reqErr
	}()

	select {
	case reqErr := <-done:
		if reqErr == nil {
			t.Fatal("expected an error from the body-stalled token fetch, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request hung well past the 150ms token-fetch bound (#200 regression: body read unbounded)")
	}
	if atomic.LoadInt32(&tokenHits) == 0 {
		t.Fatal("token endpoint was never hit; test did not exercise the refresh path")
	}
}
