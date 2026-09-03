package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// TestSignals_ZeroValueIsByteIdenticalToCommon proves that a zero-value
// Options.Signals builds every exporter exactly as it did before #361 existed:
// all three signals land on the one common endpoint.
func TestSignals_ZeroValueIsByteIdenticalToCommon(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		TracingEnabled: true,
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
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["/v1/metrics"] == 0 {
		t.Errorf("no /v1/metrics hit recorded, want at least 1")
	}
}

// TestSignals_MetricsOverrideIndependentEndpoint proves otlp.metrics can point
// at a different destination than the common (log/trace) endpoint.
func TestSignals_MetricsOverrideIndependentEndpoint(t *testing.T) {
	var mu sync.Mutex
	var metricsHits, commonHits int
	metricsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		metricsHits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer metricsSrv.Close()
	commonSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		commonHits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer commonSrv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       commonSrv.URL,
		MetricInterval: time.Hour,
		Signals: telemetry.SignalOptions{
			Metrics: &telemetry.SignalOverride{Endpoint: metricsSrv.URL},
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
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if metricsHits == 0 {
		t.Errorf("metrics override endpoint got %d hits, want at least 1", metricsHits)
	}
	if commonHits != 0 {
		t.Errorf("common endpoint got %d hits, want 0 (metrics must use its own override)", commonHits)
	}
}

// TestSignals_CredentialsNeverCrossSignals proves a header set only on the
// traces override never reaches the metric exporter's requests.
func TestSignals_CredentialsNeverCrossSignals(t *testing.T) {
	var mu sync.Mutex
	var metricsAuth, tracesAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		switch r.URL.Path {
		case "/v1/metrics":
			metricsAuth = r.Header.Get("Authorization")
		case "/v1/traces":
			tracesAuth = r.Header.Get("Authorization")
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		TracingEnabled: true,
		Signals: telemetry.SignalOptions{
			Traces: &telemetry.SignalOverride{
				Headers: map[string]string{"Authorization": "Bearer traces-only-secret"},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	p.Emitter().Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{})
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	_, span := p.Tracer().Start(ctx, "test-span")
	span.End()
	// ForceFlush deliberately excludes traces (see provider.go); Shutdown is
	// what flushes the span pipeline, so it has to run before the assertions
	// rather than in a deferred cleanup.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if metricsAuth == "Bearer traces-only-secret" {
		t.Fatal("traces-only Authorization header leaked into the metrics exporter's request")
	}
	if metricsAuth != "" {
		t.Errorf("metrics Authorization header = %q, want empty (no common header configured)", metricsAuth)
	}
	if tracesAuth != "Bearer traces-only-secret" {
		t.Errorf("traces Authorization header = %q, want the traces-only override to reach the traces exporter itself", tracesAuth)
	}
}

// TestSignals_LogsDisabledSuppressesExport proves Enabled=false on the logs
// override stops log records from ever reaching the backend, without
// disrupting metric export.
func TestSignals_LogsDisabledSuppressesExport(t *testing.T) {
	var mu sync.Mutex
	var logHits, metricHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		switch r.URL.Path {
		case "/v1/logs":
			logHits++
		case "/v1/metrics":
			metricHits++
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	disabled := false
	ctx := context.Background()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{
		ServiceName:    "tailscale2otel",
		Protocol:       "http",
		Endpoint:       srv.URL,
		MetricInterval: time.Hour,
		Signals: telemetry.SignalOptions{
			Logs: &telemetry.SignalOverride{Enabled: &disabled},
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
	p.Emitter().LogEvent(telemetry.Event{Body: "hello"})
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if logHits != 0 {
		t.Errorf("log endpoint got %d hits, want 0 (logs disabled via Signals.Logs.Enabled)", logHits)
	}
	if metricHits == 0 {
		t.Error("metric endpoint got 0 hits, want at least 1 (disabling logs must not disrupt metrics)")
	}
}

// TestResolveEffectiveSettings_ReflectsPerSignalOverridesAndNeverLeaksSecrets
// proves the status-page accessor reports the actually-resolved per-signal
// settings (not just the common block) and never carries Headers, so a
// credential can never reach it even by accident.
func TestResolveEffectiveSettings_ReflectsPerSignalOverridesAndNeverLeaksSecrets(t *testing.T) {
	disabled := false
	retryPolicy := &telemetry.RetryPolicy{Enabled: true, InitialInterval: 2 * time.Second}
	opts := telemetry.Options{
		Protocol: "http",
		Endpoint: "https://common.example/otlp",
		Headers:  map[string]string{"Authorization": "Bearer common-secret"},
		Transport: telemetry.TransportOptions{
			Compression: "gzip",
			Retry:       retryPolicy,
		},
		Signals: telemetry.SignalOptions{
			Metrics: &telemetry.SignalOverride{
				Endpoint: "https://metrics.example/otlp",
				Headers:  map[string]string{"Authorization": "Bearer metrics-secret"},
			},
			Logs: &telemetry.SignalOverride{Enabled: &disabled},
		},
	}

	eff := telemetry.ResolveEffectiveSettings(opts)

	if eff.Metrics.Endpoint != "https://metrics.example/otlp" {
		t.Errorf("Metrics.Endpoint = %q, want the metrics override endpoint", eff.Metrics.Endpoint)
	}
	if !eff.Metrics.Enabled {
		t.Error("Metrics.Enabled = false, want true (no override)")
	}
	if eff.Metrics.Transport.Compression != "gzip" {
		t.Errorf("Metrics.Transport.Compression = %q, want gzip (inherited from common Transport)", eff.Metrics.Transport.Compression)
	}
	if eff.Metrics.Transport.RetryEnabled == nil || !*eff.Metrics.Transport.RetryEnabled {
		t.Error("Metrics.Transport.RetryEnabled, want true (inherited retry policy)")
	}

	if eff.Logs.Enabled {
		t.Error("Logs.Enabled = true, want false (explicit override)")
	}
	if eff.Logs.Endpoint != "https://common.example/otlp" {
		t.Errorf("Logs.Endpoint = %q, want the inherited common endpoint", eff.Logs.Endpoint)
	}

	if eff.Traces.Endpoint != "https://common.example/otlp" {
		t.Errorf("Traces.Endpoint = %q, want the inherited common endpoint", eff.Traces.Endpoint)
	}

	// The load-bearing assertion: nothing in EffectiveSignalSettings or
	// EffectiveTransportSettings can even HOLD a header/credential value —
	// enforced by reflection so a future field addition can't quietly
	// reintroduce one without this test failing.
	assertNoHeaderLikeField(t, "Metrics", eff.Metrics)
	assertNoHeaderLikeField(t, "Logs", eff.Logs)
	assertNoHeaderLikeField(t, "Traces", eff.Traces)
}

func assertNoHeaderLikeField(t *testing.T, label string, s telemetry.EffectiveSignalSettings) {
	t.Helper()
	v := reflect.ValueOf(s)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if strings.Contains(strings.ToLower(name), "header") {
			t.Errorf("%s.%s: EffectiveSignalSettings must never carry a Headers-shaped field", label, name)
		}
	}
}
