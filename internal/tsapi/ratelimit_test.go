package tsapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// okRoundTripper always returns 200 immediately.
type okRoundTripper struct {
	mu    sync.Mutex
	calls int
}

func (o *okRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	o.mu.Lock()
	o.calls++
	o.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestWrapRateLimit_ZeroIsPassThrough(t *testing.T) {
	base := &okRoundTripper{}
	rt := wrapRateLimit(base, 0)
	if rt != http.RoundTripper(base) {
		t.Fatalf("wrapRateLimit(_, 0) must return base unchanged")
	}
}

func TestRateLimit_SlowsRapidRequests(t *testing.T) {
	base := &okRoundTripper{}
	// 100 req/sec => ~10ms minimum spacing between tokens. Bucket starts full
	// with 1 token, so N requests take at least (N-1)*10ms.
	rt := wrapRateLimit(base, 100)

	const n = 5
	start := time.Now()
	for i := 0; i < n; i++ {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/api/v2/tailnet/t/devices", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("RoundTrip %d: %v", i, err)
		}
		_ = resp.Body.Close()
	}
	elapsed := time.Since(start)
	wantMin := time.Duration(n-1) * 10 * time.Millisecond
	// allow a little slack below the theoretical minimum for scheduling jitter.
	if elapsed < wantMin-2*time.Millisecond {
		t.Fatalf("elapsed = %v, want at least ~%v", elapsed, wantMin)
	}
}

func TestLimiter_WaitReturnsCtxErrOnCancel(t *testing.T) {
	// 1 token, then 1 token/sec. Drain the initial token, then a second Wait
	// blocks; canceling the context must make it return the ctx error.
	l := newLimiter(1)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	err := l.Wait(ctx)
	if err == nil {
		t.Fatalf("Wait returned nil, want ctx error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait err = %v, want context.Canceled", err)
	}
}

func TestRateLimit_ConcurrentRoundTripsAreRaceFree(t *testing.T) {
	base := &okRoundTripper{}
	rt := wrapRateLimit(base, 1000) // generous rate; we care about the race detector.

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/api/v2/tailnet/t/devices", nil)
				resp, err := rt.RoundTrip(req)
				if err != nil {
					t.Errorf("RoundTrip: %v", err)
					return
				}
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	base.mu.Lock()
	got := base.calls
	base.mu.Unlock()
	if got != 16*5 {
		t.Fatalf("base calls = %d, want %d", got, 16*5)
	}
}

func TestLimiter_WaitRespectsDeadline(t *testing.T) {
	l := newLimiter(1)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait err = %v, want DeadlineExceeded", err)
	}
}
