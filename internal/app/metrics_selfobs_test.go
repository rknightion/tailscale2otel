package app

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// selfObsScrapeApp returns an App whose /metrics self-observability is live,
// plus the recorder its emitter writes to and the buffer its logger writes to.
// Log output is captured because half of #377 is what must NOT be logged.
func selfObsScrapeApp(t *testing.T, mutate func(*config.Config)) (*App, *telemetrytest.Recorder, *bytes.Buffer) {
	t.Helper()
	cfg := config.Default()
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Listen = "127.0.0.1:2112"
	cfg.SelfObservability.Enabled = true
	if mutate != nil {
		mutate(cfg)
	}
	rec := telemetrytest.New()
	var logs bytes.Buffer
	a := &App{
		cfg:         cfg,
		logger:      slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		procEmitter: rec.Emitter(),
	}
	return a, rec, &logs
}

// pointFor returns the single recorded datapoint of metric name whose attrs
// contain key=value, or fails.
func pointFor(t *testing.T, rec *telemetrytest.Recorder, name, key, value string) telemetrytest.MetricPoint {
	t.Helper()
	pts := rec.MetricPoints(name)
	if len(pts) == 0 {
		t.Fatalf("metric %q was never emitted", name)
	}
	for _, p := range pts {
		if key == "" || p.Attrs[key] == value {
			return p
		}
	}
	t.Fatalf("metric %q has no datapoint with %s=%q; got %+v", name, key, value, pts)
	return telemetrytest.MetricPoint{}
}

func TestMetricsScrapeIsInstrumented(t *testing.T) {
	a, rec, _ := selfObsScrapeApp(t, nil)
	scrape(t, a, scrapeTestRegistry(t, false), "")

	req := pointFor(t, rec, appcatalog.MetricMetricsScrapeRequests, attrOutcome, scrapeOutcomeSuccess)
	if req.Value != 1 {
		t.Errorf("%s{outcome=success} = %v, want 1", appcatalog.MetricMetricsScrapeRequests, req.Value)
	}

	dur := pointFor(t, rec, appcatalog.MetricMetricsScrapeDuration, attrOutcome, scrapeOutcomeSuccess)
	if dur.Kind != "histogram" || dur.Count != 1 {
		t.Errorf("%s: kind=%q count=%d, want a histogram with one observation",
			appcatalog.MetricMetricsScrapeDuration, dur.Kind, dur.Count)
	}

	// The gauge is symmetric: a completed scrape must leave it back at zero, or a
	// long-lived process drifts upward forever and the metric becomes a lie.
	inflight := pointFor(t, rec, appcatalog.MetricMetricsScrapeInFlight, "", "")
	if inflight.Value != 0 {
		t.Errorf("%s = %v after a completed scrape, want 0", appcatalog.MetricMetricsScrapeInFlight, inflight.Value)
	}
	if inflight.Monotonic {
		t.Errorf("%s is monotonic; an in-flight gauge must be able to come back down",
			appcatalog.MetricMetricsScrapeInFlight)
	}
}

// erroringCollector makes Gather fail the way a duplicate-series collision does,
// without needing two whole provider registries. err is optional — nil (the
// zero value every existing caller uses) defaults to a two-line synthetic
// error, so this stays backward compatible; metrics_status_test.go sets it
// explicitly to prove an arbitrary raw error never reaches the status DTO.
type erroringCollector struct {
	desc *prometheus.Desc
	err  error
}

func (c erroringCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }
func (c erroringCollector) Collect(ch chan<- prometheus.Metric) {
	err := c.err
	if err == nil {
		err = errors.New("collector exploded\nsecond line of the same error")
	}
	ch <- prometheus.NewInvalidMetric(c.desc, err)
}

func TestMetricsGatherErrorIsCountedAndLoggedOnOneLine(t *testing.T) {
	a, rec, logs := selfObsScrapeApp(t, nil)
	reg := prometheus.NewRegistry()
	reg.MustRegister(erroringCollector{desc: prometheus.NewDesc("t2o_broken", "broken", nil, nil)})
	// ContinueOnError still 500s when a Gather yields NOTHING at all, so the
	// registry has to hold one healthy series for this to exercise the
	// partial-success path that #103 is actually about.
	healthy := prometheus.NewCounter(prometheus.CounterOpts{Name: "t2o_healthy_total", Help: "survivor"})
	reg.MustRegister(healthy)
	healthy.Inc()

	// ContinueOnError (#103) must survive: a gather error still returns 200 with
	// whatever else gathered. scrape() fails the test on any other status.
	scrape(t, a, reg, "")

	errs := pointFor(t, rec, appcatalog.MetricMetricsScrapeGatherErrors, "", "")
	if errs.Value != 1 {
		t.Errorf("%s = %v, want 1", appcatalog.MetricMetricsScrapeGatherErrors, errs.Value)
	}

	out := logs.String()
	if out == "" {
		t.Fatal("a gather error produced no log line at all; the counter alone does not say WHICH series collided")
	}
	// prometheus.MultiError renders multi-line. One gather error must be one log
	// line or every log pipeline downstream splits it into orphaned fragments.
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n"); n != 0 {
		t.Errorf("gather error spans %d extra lines; want a single line:\n%s", n, out)
	}
}

func TestMetricsAuthRejectionIsCountedNotLogged(t *testing.T) {
	cases := []struct {
		name       string
		authHeader string
		wantReason string
	}{
		{"no credential", "", reasonMissingCredentials},
		{"wrong credential", "Bearer wrong", reasonBadCredentials},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, rec, logs := selfObsScrapeApp(t, func(c *config.Config) {
				c.Prometheus.Auth.Token = config.Secret("s3cret")
			})
			h := a.requireMetricsAuth(okHandler())
			// Several requests: a per-request log line is exactly the spam #377
			// removes, so the count of rejections must rise while the log stays quiet.
			for range 3 {
				req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:2112/metrics", nil)
				if tc.authHeader != "" {
					req.Header.Set("Authorization", tc.authHeader)
				}
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				if rr.Code != http.StatusUnauthorized {
					t.Fatalf("code = %d, want 401", rr.Code)
				}
			}
			p := pointFor(t, rec, appcatalog.MetricMetricsAuthRejected, attrReason, tc.wantReason)
			if p.Value != 3 {
				t.Errorf("%s{reason=%s} = %v, want 3", appcatalog.MetricMetricsAuthRejected, tc.wantReason, p.Value)
			}
			if out := logs.String(); out != "" {
				t.Errorf("a rejected credential logged per request; that is the spam #377 removes:\n%s", out)
			}
		})
	}
}

// The misconfiguration branch (no token on a network bind) is worth telling an
// operator about exactly once. Every rejection is still counted.
func TestMetricsAuthRequiredLogsOnceAndCountsEvery(t *testing.T) {
	a, rec, logs := selfObsScrapeApp(t, func(c *config.Config) {
		c.Prometheus.Listen = "0.0.0.0:2112"
	})
	h := a.requireMetricsAuth(okHandler())
	for range 4 {
		req := httptest.NewRequest(http.MethodGet, "http://example.internal:2112/metrics", nil)
		req.Host = "example.internal:2112"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", rr.Code)
		}
	}
	p := pointFor(t, rec, appcatalog.MetricMetricsAuthRejected, attrReason, reasonAuthRequired)
	if p.Value != 4 {
		t.Errorf("%s{reason=%s} = %v, want 4", appcatalog.MetricMetricsAuthRejected, reasonAuthRequired, p.Value)
	}
	if n := strings.Count(logs.String(), "remedy="); n != 1 {
		t.Errorf("the misconfiguration was logged %d times across 4 scrapes, want exactly 1:\n%s", n, logs.String())
	}
}

// Every metric this package declares for /metrics serving must be in
// appcatalog.Catalog() with a matching unit, instrument and description, so
// docs/metrics.md cannot drift from what is emitted.
func TestMetricsSelfObsDescriptorsMatchCatalog(t *testing.T) {
	declared := map[string]bool{}
	for _, m := range appcatalog.Catalog() {
		declared[m.Name] = true
	}
	for _, name := range []string{
		appcatalog.MetricMetricsScrapeRequests,
		appcatalog.MetricMetricsScrapeInFlight,
		appcatalog.MetricMetricsScrapeDuration,
		appcatalog.MetricMetricsScrapeGatherErrors,
		appcatalog.MetricMetricsAuthRejected,
	} {
		if !declared[name] {
			t.Errorf("%s is emitted but not declared in appcatalog.Catalog()", name)
		}
	}

	// Drift guard: compare the emitted unit/description/instrument against the
	// declaration for each one actually produced by a real request.
	a, rec, _ := selfObsScrapeApp(t, nil)
	scrape(t, a, scrapeTestRegistry(t, false), "")
	b, rec2, _ := selfObsScrapeApp(t, func(c *config.Config) {
		c.Prometheus.Auth.Token = config.Secret("s3cret")
	})
	rr := httptest.NewRecorder()
	b.requireMetricsAuth(okHandler()).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	byName := map[string]struct {
		unit, desc string
		instrument string
	}{}
	for _, m := range appcatalog.Catalog() {
		byName[m.Name] = struct {
			unit, desc string
			instrument string
		}{m.Unit, m.Description, string(m.Instrument)}
	}
	for _, r := range []*telemetrytest.Recorder{rec, rec2} {
		for _, name := range r.MetricNames() {
			if !strings.HasPrefix(name, "tailscale2otel.metrics.") {
				continue
			}
			d, ok := byName[name]
			if !ok {
				t.Errorf("emitted %q is not declared in appcatalog.Catalog()", name)
				continue
			}
			p := r.MetricPoints(name)[0]
			if p.Unit != d.unit {
				t.Errorf("%s: emitted unit %q != catalog unit %q", name, p.Unit, d.unit)
			}
			if p.Description != d.desc {
				t.Errorf("%s: emitted description %q != catalog description %q", name, p.Description, d.desc)
			}
		}
	}
}
