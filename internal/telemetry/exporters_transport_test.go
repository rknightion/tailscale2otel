package telemetry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// TestTransport_CompressionExplicitGzip proves an explicit
// Transport.Compression="gzip" always wins: the request the exporter sends
// carries Content-Encoding: gzip regardless of any env var.
func TestTransport_CompressionExplicitGzip(t *testing.T) {
	var gotEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	runOneExport(t, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		Transport:      telemetry.TransportOptions{Compression: "gzip"},
	})

	if gotEncoding != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", gotEncoding)
	}
}

// TestTransport_CompressionExplicitNoneOverridesEnv proves an explicit "none"
// wins over an OTEL_EXPORTER_OTLP_METRICS_COMPRESSION=gzip env var — the
// "explicitly-set Options value ALWAYS wins" rule from #360/#480.
func TestTransport_CompressionExplicitNoneOverridesEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_COMPRESSION", "gzip")

	var gotEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	runOneExport(t, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		Transport:      telemetry.TransportOptions{Compression: "none"},
	})

	if gotEncoding != "" {
		t.Fatalf("Content-Encoding = %q, want empty (explicit none must override env gzip)", gotEncoding)
	}
}

// TestTransport_CompressionUnsetFallsBackToEnv proves that leaving
// Transport.Compression unset ("") is NOT the same as forcing "none" — the SDK's
// own OTEL_EXPORTER_OTLP_METRICS_COMPRESSION resolution still applies.
func TestTransport_CompressionUnsetFallsBackToEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_COMPRESSION", "gzip")

	var gotEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	runOneExport(t, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		// Transport.Compression left zero-value ("") on purpose.
	})

	if gotEncoding != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (unset field must fall back to env var)", gotEncoding)
	}
}

// TestTransport_InvalidCompressionRejected proves an unrecognized compression
// value is a loud config error, not a silent no-op.
func TestTransport_InvalidCompressionRejected(t *testing.T) {
	ctx := context.Background()
	_, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       "http://127.0.0.1:0",
		MetricInterval: time.Hour,
		Transport:      telemetry.TransportOptions{Compression: "brotli"},
	})
	if err == nil {
		t.Fatal("NewProvider succeeded with an invalid compression value, want error")
	}
}

// TestTransport_MaxRequestSizeRejectsOversizedExport proves MaxRequestSize is a
// client-side rejection guard: an export bigger than the configured ceiling
// never reaches the server at all.
func TestTransport_MaxRequestSizeRejectsOversizedExport(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		Transport:      telemetry.TransportOptions{MaxRequestSize: 16},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.Shutdown(shutdownCtx)
	}()

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		p.Emitter().Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{"id": id})
	}
	// ForceFlush is expected to report an error: the serialized request exceeds
	// the 16-byte ceiling, so the exporter must refuse to send it.
	if err := p.ForceFlush(ctx); err == nil {
		t.Fatal("ForceFlush succeeded despite MaxRequestSize being exceeded, want error")
	}

	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("server received %d requests, want 0 (oversized request must be rejected client-side, never sent)", requests)
	}
}

// TestTransport_TimeoutBoundsExportAttempt proves an explicit Transport.Timeout
// aborts an export against a slow backend well before the SDK's own 10s
// default would.
func TestTransport_TimeoutBoundsExportAttempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		Transport: telemetry.TransportOptions{
			Timeout: 50 * time.Millisecond,
			// Disable retry so the timeout, not a retry loop, bounds this test.
			Retry: &telemetry.RetryPolicy{Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.Shutdown(shutdownCtx)
	}()

	p.Emitter().Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{})

	start := time.Now()
	err = p.ForceFlush(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("ForceFlush succeeded against a slow backend, want a timeout error")
	}
	if elapsed > time.Second {
		t.Fatalf("ForceFlush took %s, want well under the server's 2s response time (Transport.Timeout=50ms should have aborted it)", elapsed)
	}
}

// TestTransport_RetryDisabledFailsFast proves Retry.Enabled=false disables the
// exporter's built-in retry loop rather than only tuning its backoff.
func TestTransport_RetryDisabledFailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		Transport: telemetry.TransportOptions{
			Retry: &telemetry.RetryPolicy{Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.Shutdown(shutdownCtx)
	}()

	p.Emitter().Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{})

	start := time.Now()
	err = p.ForceFlush(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("ForceFlush succeeded against a 503 backend, want error")
	}
	// The SDK default retry policy waits 5s before its first retry; failing in
	// well under that proves retry was actually disabled, not just re-tuned.
	if elapsed > 2*time.Second {
		t.Fatalf("ForceFlush took %s, want well under the default 5s initial retry backoff", elapsed)
	}
}

// TestTransport_GRPCReconnectionPeriodAccepted proves the gRPC-only
// reconnection-period option is accepted without error and does not affect the
// HTTP path (dead field there).
func TestTransport_GRPCReconnectionPeriodAccepted(t *testing.T) {
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "grpc",
		Endpoint:       "127.0.0.1:0",
		MetricInterval: time.Hour,
		Transport:      telemetry.TransportOptions{GRPCReconnectionPeriod: time.Minute},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.Shutdown(shutdownCtx)
}

// runOneExport builds a Provider from opts, emits one counter, force-flushes,
// and shuts down. It fails the test if NewProvider or ForceFlush errors.
func runOneExport(t *testing.T, opts telemetry.Options) {
	t.Helper()
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, opts)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.Shutdown(shutdownCtx)
	}()
	p.Emitter().Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{})
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
}
