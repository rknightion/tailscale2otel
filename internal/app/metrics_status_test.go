package app

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// resetMetricsScrapeHealth swaps the process-wide /metrics-serving health
// tracker for a fresh one and restores the original at test end, mirroring
// profilingHealthState's test seam (profilinghealth_test.go) so tests never
// see counts left over from an earlier test in the same package.
func resetMetricsScrapeHealth(t *testing.T) {
	t.Helper()
	prev := metricsScrapeHealthState
	metricsScrapeHealthState = newMetricsScrapeHealth()
	t.Cleanup(func() { metricsScrapeHealthState = prev })
}

// fixedStatusHandler always responds with code, bypassing promhttp entirely so
// the "shed" (503) and "error" (500) outcome branches can be driven directly
// without needing a real MaxRequestsInFlight/Timeout collision.
func fixedStatusHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
}

// TestMetricsScrapeInfo_TracksRegardlessOfSelfObs pins the #377 requirement
// that the status-page DTO is populated from live state even when self-obs is
// OFF (no procEmitter) — like profilingHealth and deliveryTracker, the admin
// page must work on a deployment that exports no self-telemetry at all.
func TestMetricsScrapeInfo_TracksRegardlessOfSelfObs(t *testing.T) {
	resetMetricsScrapeHealth(t)
	cfg := config.Default()
	cfg.Prometheus.Enabled = true
	a := &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)} // procEmitter nil: self-obs off

	drive := func(code int) {
		h := a.instrumentScrape(fixedStatusHandler(code))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	}
	drive(http.StatusOK)
	drive(http.StatusServiceUnavailable)
	drive(http.StatusInternalServerError)

	info := a.metricsScrapeInfo()
	if !info.Enabled {
		t.Error("Enabled = false, want true (prometheus.enabled)")
	}
	if info.ScrapesTotal != 3 {
		t.Errorf("ScrapesTotal = %d, want 3", info.ScrapesTotal)
	}
	if info.ScrapesShed != 1 {
		t.Errorf("ScrapesShed = %d, want 1", info.ScrapesShed)
	}
	if info.ScrapesFailed != 1 {
		t.Errorf("ScrapesFailed = %d, want 1", info.ScrapesFailed)
	}
	if info.InFlight != 0 {
		t.Errorf("InFlight = %d after every scrape completed, want 0 (the gauge must come back down)", info.InFlight)
	}
	if info.LastScrapeAt == "" {
		t.Error("LastScrapeAt is empty after a completed scrape")
	}
	if info.LastScrapeDurationMs < 0 {
		t.Errorf("LastScrapeDurationMs = %d, want >= 0", info.LastScrapeDurationMs)
	}
}

// TestMetricsScrapeInfo_GatherErrorClassifiedNeverRawText is the hard
// requirement from the brief: a Prometheus Gather error can embed a
// collector's internal detail (registry.go wraps a Collect() error verbatim
// via %w), so the status DTO must carry a bounded, closed classification —
// never the raw error text.
func TestMetricsScrapeInfo_GatherErrorClassifiedNeverRawText(t *testing.T) {
	resetMetricsScrapeHealth(t)
	cfg := config.Default()
	cfg.Prometheus.Enabled = true
	// Loopback: stays open with no token, matching scrapeTestApp elsewhere in
	// this package — this test is about gather-error classification, not auth.
	cfg.Prometheus.Listen = "127.0.0.1:2112"
	a := &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}

	const secretLookingText = "SECRET_MARKER_credential=hunter2_do_not_leak"
	reg := prometheus.NewRegistry()
	reg.MustRegister(erroringCollector{
		desc: prometheus.NewDesc("t2o_broken2", "broken", nil, nil),
		err:  errors.New(secretLookingText),
	})
	// ContinueOnError still 500s when Gather yields nothing at all (see
	// metrics_selfobs_test.go); a healthy survivor keeps this on the
	// partial-success path #377 is about.
	healthy := prometheus.NewCounter(prometheus.CounterOpts{Name: "t2o_healthy2_total", Help: "survivor"})
	reg.MustRegister(healthy)
	healthy.Inc()

	scrape(t, a, reg, "")

	info := a.metricsScrapeInfo()
	if info.GatherErrors != 1 {
		t.Fatalf("GatherErrors = %d, want 1", info.GatherErrors)
	}
	if strings.Contains(info.LastGatherError, secretLookingText) || strings.Contains(info.LastGatherError, "credential") {
		t.Fatalf("LastGatherError leaked the raw error text: %q", info.LastGatherError)
	}
	valid := map[string]bool{
		gatherErrClassDuplicateSeries:   true,
		gatherErrClassLabelInconsistent: true,
		gatherErrClassTypeMismatch:      true,
		gatherErrClassCollectorError:    true,
		gatherErrClassOther:             true,
	}
	if !valid[info.LastGatherError] {
		t.Errorf("LastGatherError = %q, not a member of the closed classification set", info.LastGatherError)
	}
	if info.LastGatherErrorAt == "" {
		t.Error("LastGatherErrorAt is empty after a gather error")
	}
}

// TestMetricsServingConfig_ExcludesSecrets is the second hard requirement:
// the effective-config echo must never carry the bearer token value or any
// TLS file's path/contents — only presence/mode booleans.
func TestMetricsServingConfig_ExcludesSecrets(t *testing.T) {
	resetMetricsScrapeHealth(t)
	cfg := config.Default()
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Auth.Token = config.Secret("s3cret-token-value")
	cfg.Prometheus.TLS.CertFile = "/tmp/does-not-matter.pem"
	cfg.Prometheus.TLS.KeyFile = "/tmp/does-not-matter-key.pem"
	cfg.Prometheus.TLS.ClientCAFile = "/tmp/ca-bundle-with-secret-material.pem"
	cfg.Prometheus.TLS.ClientAuth = "require_and_verify"
	a := &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}

	info := a.metricsScrapeInfo()
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "s3cret-token-value") {
		t.Error("the auth token value leaked into the effective-config echo")
	}
	if strings.Contains(body, "does-not-matter") || strings.Contains(body, "ca-bundle-with-secret-material") {
		t.Error("a TLS file path leaked into the effective-config echo")
	}
	if !info.Config.AuthTokenSet {
		t.Error("AuthTokenSet = false, want true (a token IS configured)")
	}
	if !info.Config.TLSEnabled {
		t.Error("TLSEnabled = false, want true (both cert/key are set)")
	}
	if !info.Config.ClientCAConfigured {
		t.Error("ClientCAConfigured = false, want true (client_ca_file is set)")
	}
	if info.Config.ClientAuthMode != "require_and_verify" {
		t.Errorf("ClientAuthMode = %q, want require_and_verify", info.Config.ClientAuthMode)
	}
}
