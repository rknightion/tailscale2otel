package hsapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
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
	var (
		mu    sync.Mutex
		infos []RequestInfo
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"nodes":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Options{
		URL: srv.URL, APIKey: "x", Timeout: 5 * time.Millisecond, RateLimit: 25,
		OnRequest: func(_ context.Context, info RequestInfo) {
			mu.Lock()
			infos = append(infos, info)
			mu.Unlock()
		},
	})
	if _, err := c.Nodes(context.Background()); err != nil {
		t.Fatalf("first Nodes: %v", err)
	}
	start := time.Now()
	if _, err := c.Nodes(context.Background()); err != nil {
		t.Fatalf("second Nodes: %v; limiter wait must not consume the 5ms attempt timeout", err)
	}
	elapsed := time.Since(start)
	mu.Lock()
	defer mu.Unlock()
	if len(infos) != 2 {
		t.Fatalf("OnRequest calls = %d, want 2", len(infos))
	}
	got := infos[1]
	if got.WaitDuration < 30*time.Millisecond {
		t.Fatalf("WaitDuration = %v, want about 40ms limiter wait", got.WaitDuration)
	}
	if got.Duration >= elapsed-10*time.Millisecond {
		t.Fatalf("Duration = %v, appears to include limiter wait in total elapsed %v", got.Duration, elapsed)
	}
}
