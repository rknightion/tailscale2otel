package hsapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestClientRetriesTooManyRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"nodes":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Options{
		URL:         srv.URL,
		APIKey:      "x",
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
	})
	if _, err := c.Nodes(context.Background()); err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestClientRetriesTransientStatusesAndReportsAttempts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []int
		attempts int
		wantErr  bool
	}{
		{name: "server error retries", statuses: []int{http.StatusBadGateway, http.StatusOK}, attempts: 2},
		{name: "client error is final", statuses: []int{http.StatusUnauthorized, http.StatusOK}, attempts: 1, wantErr: true},
		{name: "zero max attempts is one request", statuses: []int{http.StatusServiceUnavailable, http.StatusOK}, attempts: 1, wantErr: true},
		{name: "one max attempt is one request", statuses: []int{http.StatusServiceUnavailable, http.StatusOK}, attempts: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var info RequestInfo
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1))
				w.WriteHeader(tc.statuses[n-1])
				if tc.statuses[n-1] == http.StatusOK {
					_, _ = w.Write([]byte(`{"nodes":[]}`))
				}
			}))
			defer srv.Close()

			maxAttempts := 2
			if tc.name == "zero max attempts is one request" {
				maxAttempts = 0
			}
			if tc.name == "one max attempt is one request" {
				maxAttempts = 1
			}
			c := NewClient(Options{
				URL: srv.URL, APIKey: "x", MaxAttempts: maxAttempts,
				BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
				OnRequest: func(_ context.Context, got RequestInfo) { info = got },
			})
			_, err := c.Nodes(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Nodes error = %v, want error=%v", err, tc.wantErr)
			}
			if got := int(calls.Load()); got != tc.attempts {
				t.Fatalf("requests = %d, want %d", got, tc.attempts)
			}
			if info.Attempts != tc.attempts {
				t.Fatalf("RequestInfo.Attempts = %d, want %d", info.Attempts, tc.attempts)
			}
		})
	}
}

func TestClientRetryAfterIsClampedToMaxDelay(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"nodes":[]}`))
	}))
	defer srv.Close()

	const maxDelay = 30 * time.Millisecond
	c := NewClient(Options{URL: srv.URL, APIKey: "x", MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: maxDelay})
	start := time.Now()
	if _, err := c.Nodes(context.Background()); err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if elapsed := time.Since(start); elapsed < maxDelay-5*time.Millisecond {
		t.Fatalf("elapsed = %v, want Retry-After clamped sleep of about %v", elapsed, maxDelay)
	}
}

func TestClientCanceledContextAbortsRetryBackoff(t *testing.T) {
	first := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-first:
		default:
			close(first)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "x", MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Nodes(ctx)
		done <- err
	}()
	<-first
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Nodes error = %v, want context.Canceled", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Nodes did not abort retry backoff after context cancellation")
	}
}

func TestClientRateLimitWaitIsOutsideAttemptTimeoutAndRequestDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			attemptTimeout  = 5 * time.Millisecond
			requestDuration = 2 * time.Millisecond
			limiterInterval = 40 * time.Millisecond // 25 requests/second, burst one.
		)
		var infos []RequestInfo
		c := NewClient(Options{
			URL: "https://headscale.example", APIKey: "x", Timeout: attemptTimeout, RateLimit: 25,
			OnRequest: func(_ context.Context, info RequestInfo) { infos = append(infos, info) },
		})
		transport := c.http.Transport.(*retryTransport)
		limiter := transport.limiter
		var waitCtx context.Context
		var waits, requests int
		transport.limiter = limiterWaitFunc(func(ctx context.Context) error {
			waits++
			waitCtx = ctx
			if deadline, ok := ctx.Deadline(); ok {
				t.Errorf("limiter context has deadline %v; wait must use the parent context", deadline)
			}
			return limiter.Wait(ctx)
		})
		// Replace only the network edge: Nodes still traverses getJSON, the real
		// rate limiter, retryTransport, JSON decoding and OnRequest reporting.
		transport.base = limiterRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests++
			ctx := req.Context()
			if ctx == waitCtx || ctx.Value(retryStateKey{}) != waitCtx.Value(retryStateKey{}) {
				t.Error("attempt context must be a child of the limiter's request context")
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) != attemptTimeout {
				t.Errorf("attempt timeout remaining = %v (deadline present=%v), want %v after limiter wait", time.Until(deadline), ok, attemptTimeout)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(requestDuration):
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"nodes":[]}`)), Request: req,
			}, nil
		})
		for i := range 2 {
			start := time.Now()
			if _, err := c.Nodes(context.Background()); err != nil {
				t.Fatalf("Nodes call %d: %v; limiter wait must not consume the attempt timeout", i+1, err)
			}
			if len(infos) != i+1 {
				t.Fatalf("OnRequest calls = %d, want %d", len(infos), i+1)
			}
			wantWait := time.Duration(0)
			if i == 1 {
				wantWait = limiterInterval - requestDuration
			}
			got := infos[i]
			if got.WaitDuration != wantWait {
				t.Errorf("call %d WaitDuration = %v, want %v", i+1, got.WaitDuration, wantWait)
			}
			if got.Duration != requestDuration {
				t.Errorf("call %d Duration = %v, want %v excluding limiter wait", i+1, got.Duration, requestDuration)
			}
			if elapsed := time.Since(start); elapsed != wantWait+requestDuration {
				t.Errorf("call %d elapsed = %v, want wait + request = %v", i+1, elapsed, wantWait+requestDuration)
			}
			if got.Attempts != 1 || got.Status != http.StatusOK || got.Err != "" {
				t.Errorf("call %d outcome = %+v, want one successful attempt", i+1, got)
			}
		}
		if waits != 2 || requests != 2 {
			t.Errorf("limiter waits = %d, transport requests = %d, want 2 each", waits, requests)
		}
	})
}

type limiterWaitFunc func(context.Context) error

func (f limiterWaitFunc) Wait(ctx context.Context) error { return f(ctx) }

type limiterRoundTripFunc func(*http.Request) (*http.Response, error)

func (f limiterRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
