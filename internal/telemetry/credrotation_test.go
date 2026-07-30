package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// #362's whole point is that a rotated credential reaches the wire without a
// restart. The OTLP exporters bake headers in at construction, so "the reloader
// returns the new token" proves nothing on its own — only a real export observed
// by a real server does. These tests drive that end to end.

// headerRecorder captures the Authorization header of every request.
type headerRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (h *headerRecorder) add(v string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen = append(h.seen, v)
}

func (h *headerRecorder) all() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

func TestDynamicHeaders_RotatedTokenReachesTheWire(t *testing.T) {
	rec := &headerRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// The token the DynamicHeaders func returns changes between exports, standing
	// in for a rotated secret file. Guarded because the exporter's transport may
	// read it from another goroutine.
	var mu sync.Mutex
	token := "Bearer first"
	opts := telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour, // never fire on the timer; ForceFlush drives exports
		DynamicHeaders: func() map[string]string {
			mu.Lock()
			defer mu.Unlock()
			return map[string]string{"Authorization": token}
		},
	}

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, opts)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(sctx)
	}()

	p.Emitter().Counter("tailscale.credrotation.test", "1", "", 1, nil)
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("first ForceFlush: %v", err)
	}

	mu.Lock()
	token = "Bearer rotated"
	mu.Unlock()

	p.Emitter().Counter("tailscale.credrotation.test", "1", "", 1, nil)
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("second ForceFlush: %v", err)
	}

	got := rec.all()
	if len(got) < 2 {
		t.Fatalf("server saw %d requests, want at least 2: %v", len(got), got)
	}
	if got[0] != "Bearer first" {
		t.Errorf("first request Authorization = %q, want %q", got[0], "Bearer first")
	}
	// The last request must carry the ROTATED value. If the exporter had captured
	// the header map at construction — the pre-#362 behavior — every request would
	// still say "Bearer first" and the process would keep authenticating with a
	// revoked credential until restarted.
	last := got[len(got)-1]
	if last != "Bearer rotated" {
		t.Errorf("last request Authorization = %q, want %q — the rotated credential never reached the wire",
			last, "Bearer rotated")
	}
}

// A nil DynamicHeaders must leave the static path exactly as it was, so a
// deployment that configures no watched file is unaffected.
func TestDynamicHeaders_NilKeepsStaticHeaders(t *testing.T) {
	rec := &headerRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		Headers:        map[string]string{"Authorization": "Bearer static"},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(sctx)
	}()

	p.Emitter().Counter("tailscale.credrotation.static", "1", "", 1, nil)
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	got := rec.all()
	if len(got) == 0 {
		t.Fatal("server received no request")
	}
	if got[0] != "Bearer static" {
		t.Errorf("Authorization = %q, want %q", got[0], "Bearer static")
	}
}
