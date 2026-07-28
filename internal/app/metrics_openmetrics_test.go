package app

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// openMetricsAccept is what Prometheus 2.5+ actually sends: OpenMetrics first,
// classic text as the fallback. Exemplars only cross the wire in the OpenMetrics
// exposition format, so a handler that never negotiates it silently discards
// every exemplar the SDK attached (#368).
const openMetricsAccept = "application/openmetrics-text;version=1.0.0," +
	"text/plain;version=0.0.4;q=0.5,*/*;q=0.1"

// scrapeTestApp builds an App whose /metrics is reachable without credentials.
// These tests are about content negotiation, not the auth matrix (see
// metricsauth_test.go for that).
func scrapeTestApp(t *testing.T) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Listen = "127.0.0.1:2112"
	return &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}
}

// scrapeTestRegistry returns a registry holding one counter. withExemplar
// attaches a trace exemplar, standing in for a datapoint recorded while tracing
// was enabled; without it the registry is exactly what a tracing-disabled
// process produces.
func scrapeTestRegistry(t *testing.T, withExemplar bool) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "t2o_scrape_probe_total",
		Help: "probe counter for the content-negotiation tests",
	})
	reg.MustRegister(c)
	if withExemplar {
		adder, ok := c.(prometheus.ExemplarAdder)
		if !ok {
			t.Fatalf("counter %T does not implement prometheus.ExemplarAdder", c)
		}
		adder.AddWithExemplar(1, prometheus.Labels{"trace_id": "0102030405060708090a0b0c0d0e0f10"})
	} else {
		c.Add(1)
	}
	return reg
}

// scrape drives one request through the assembled /metrics handler. An empty
// accept omits the header entirely, which is what a plain `curl` sends.
func scrape(t *testing.T, a *App, g prometheus.Gatherer, accept string) *httptest.ResponseRecorder {
	t.Helper()
	srv := a.buildMetricsServer(g)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:2112/metrics", nil)
	req.Host = "127.0.0.1:2112"
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics code = %d, want 200; body:\n%s", rec.Code, rec.Body.String())
	}
	return rec
}

func TestMetricsNegotiatesOpenMetricsForExemplars(t *testing.T) {
	a := scrapeTestApp(t)
	reg := scrapeTestRegistry(t, true)
	rec := scrape(t, a, reg, openMetricsAccept)

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/openmetrics-text") {
		t.Fatalf("Content-Type = %q, want application/openmetrics-text. A scraper that asks for "+
			"OpenMetrics and is answered in classic text cannot receive exemplars at all.", ct)
	}
	body := rec.Body.String()
	// OpenMetrics renders an exemplar as a trailing `# {labels} value` on the
	// sample line. Its absence means the exemplar was dropped on the wire.
	if !strings.Contains(body, "# {trace_id=") {
		t.Errorf("no exemplar in the OpenMetrics body:\n%s", body)
	}
	if !strings.Contains(body, "0102030405060708090a0b0c0d0e0f10") {
		t.Errorf("exemplar trace id missing from the OpenMetrics body:\n%s", body)
	}
}

// The change must be pure negotiation: a client that does not ask for
// OpenMetrics keeps getting exactly the classic exposition it got before, with
// no exemplar smuggled into it (classic text has no syntax for one).
func TestMetricsKeepsClassicTextForClientsThatDoNotAsk(t *testing.T) {
	a := scrapeTestApp(t)
	reg := scrapeTestRegistry(t, true)

	for _, accept := range []string{"", "text/plain"} {
		name := "absent Accept"
		if accept != "" {
			name = accept
		}
		t.Run(name, func(t *testing.T) {
			rec := scrape(t, a, reg, accept)
			ct := rec.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("Content-Type = %q, want a text/plain classic exposition", ct)
			}
			if strings.Contains(ct, "openmetrics") {
				t.Errorf("Content-Type = %q: OpenMetrics was forced on a client that did not ask", ct)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "t2o_scrape_probe_total 1") {
				t.Errorf("classic body lost its sample line:\n%s", body)
			}
			if strings.Contains(body, "trace_id") {
				t.Errorf("exemplar leaked into the classic exposition:\n%s", body)
			}
			if strings.Contains(body, "# EOF") {
				t.Errorf("OpenMetrics EOF marker in a classic exposition:\n%s", body)
			}
		})
	}
}

// With tracing disabled nothing carries an exemplar, so enabling negotiation
// must not change a single byte of what either format reports.
func TestMetricsOutputUnchangedWithoutExemplars(t *testing.T) {
	a := scrapeTestApp(t)
	reg := scrapeTestRegistry(t, false)

	classic := scrape(t, a, reg, "").Body.String()
	if !strings.Contains(classic, "t2o_scrape_probe_total 1") {
		t.Fatalf("classic body:\n%s", classic)
	}
	if strings.Contains(classic, "#") && strings.Contains(classic, "trace_id") {
		t.Errorf("exemplar syntax appeared with tracing disabled:\n%s", classic)
	}

	om := scrape(t, a, reg, openMetricsAccept).Body.String()
	if !strings.Contains(om, "t2o_scrape_probe_total 1") {
		t.Errorf("OpenMetrics body lost the sample line:\n%s", om)
	}
	if strings.Contains(om, "trace_id") {
		t.Errorf("OpenMetrics body invented an exemplar with tracing disabled:\n%s", om)
	}
}

// _created series double the series count for every counter and histogram and
// are mis-ingested by scrapers without created-timestamp support, so the
// separate EnableOpenMetricsTextCreatedSamples opt-in stays off.
func TestMetricsOpenMetricsHasNoCreatedSamples(t *testing.T) {
	a := scrapeTestApp(t)
	reg := scrapeTestRegistry(t, true)
	body := scrape(t, a, reg, openMetricsAccept).Body.String()
	if strings.Contains(body, "_created") {
		t.Errorf("_created samples are enabled; they double counter/histogram series and are "+
			"mis-ingested by scrapers without created-timestamp support:\n%s", body)
	}
}
