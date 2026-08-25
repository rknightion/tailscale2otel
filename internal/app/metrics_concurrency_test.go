package app

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// slowScrapeCollector is a slow scrape made deterministic: Collect announces itself
// on entered and then parks until release is closed, so a test can hold a Gather
// open for exactly as long as it needs.
type slowScrapeCollector struct {
	desc    *prometheus.Desc
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func newSlowScrapeCollector() *slowScrapeCollector {
	return &slowScrapeCollector{
		desc:    prometheus.NewDesc("t2o_slow_gauge", "a deliberately slow series", nil, nil),
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
}

func (c *slowScrapeCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *slowScrapeCollector) Collect(ch chan<- prometheus.Metric) {
	c.calls.Add(1)
	c.entered <- struct{}{}
	<-c.release
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, 1)
}

// scrapeAsync fires one /metrics request on its own goroutine and returns a
// channel carrying its recorder once it completes.
func scrapeAsync(srv *http.Server) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:2112/metrics", nil)
		req.Host = "127.0.0.1:2112"
		srv.Handler.ServeHTTP(rec, req)
		done <- rec
	}()
	return done
}

// inFlight reads the current value of the scrape in-flight gauge.
func inFlight(rec *telemetrytest.Recorder) float64 {
	pts := rec.MetricPoints(appcatalog.MetricMetricsScrapeInFlight)
	if len(pts) == 0 {
		return 0
	}
	return pts[0].Value
}

// waitForInFlight polls the in-flight gauge until it reaches want, so a test
// never has to guess how long a request takes to reach the handler.
func waitForInFlight(t *testing.T, rec *telemetrytest.Recorder, want float64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := inFlight(rec); got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("in-flight scrapes never reached %v (stuck at %v)", want, inFlight(rec))
		}
		time.Sleep(time.Millisecond)
	}
}

// A scrape interval shorter than the gather time is the failure mode #377 is
// about: without a cap, each overlapping scrape starts its own full Gather and
// holds its own snapshot, so the pile-up is unbounded. This proves the cap
// actually sheds the second scrape rather than merely being assigned to a field.
func TestMetricsMaxRequestsInFlightShedsConcurrentScrapes(t *testing.T) {
	a, rec, _ := selfObsScrapeApp(t, func(c *config.Config) {
		c.Prometheus.MaxRequestsInFlight = 1
	})
	slow := newSlowScrapeCollector()
	reg := prometheus.NewRegistry()
	reg.MustRegister(slow)
	srv := a.buildMetricsServer(reg)

	// Scrape A occupies the single slot and parks inside Collect.
	first := scrapeAsync(srv)
	<-slow.entered

	// Scrape B arrives while A is still gathering. It must be shed immediately,
	// NOT queued behind A and not given a Gather of its own.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:2112/metrics", nil)
	req.Host = "127.0.0.1:2112"
	srv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("concurrent scrape code = %d, want 503. max_requests_in_flight=1 did not bound "+
			"concurrent gathers; body:\n%s", rr.Code, rr.Body.String())
	}
	if got := slow.calls.Load(); got != 1 {
		t.Errorf("Collect ran %d times, want 1: the shed request still did work", got)
	}

	// A is still in flight, so the gauge proves it went UP and not just back down.
	if got := inFlight(rec); got != 1 {
		t.Errorf("%s = %v while one scrape is blocked, want 1", appcatalog.MetricMetricsScrapeInFlight, got)
	}

	close(slow.release)
	if rr := <-first; rr.Code != http.StatusOK {
		t.Fatalf("the admitted scrape code = %d, want 200; body:\n%s", rr.Code, rr.Body.String())
	}

	shed := pointFor(t, rec, appcatalog.MetricMetricsScrapeRequests, attrOutcome, scrapeOutcomeUnavailable)
	if shed.Value != 1 {
		t.Errorf("%s{outcome=unavailable} = %v, want 1", appcatalog.MetricMetricsScrapeRequests, shed.Value)
	}
	ok := pointFor(t, rec, appcatalog.MetricMetricsScrapeRequests, attrOutcome, scrapeOutcomeSuccess)
	if ok.Value != 1 {
		t.Errorf("%s{outcome=success} = %v, want 1", appcatalog.MetricMetricsScrapeRequests, ok.Value)
	}
	if got := inFlight(rec); got != 0 {
		t.Errorf("%s = %v once both scrapes finished, want 0", appcatalog.MetricMetricsScrapeInFlight, got)
	}
}

// Timeout bounds what the SCRAPER waits for, independently of the cap: a gather
// that never finishes must not hold the connection open past the configured
// deadline.
func TestMetricsTimeoutShedsASlowGather(t *testing.T) {
	a, rec, _ := selfObsScrapeApp(t, func(c *config.Config) {
		c.Prometheus.Timeout = config.Duration(50 * time.Millisecond)
	})
	slow := newSlowScrapeCollector()
	reg := prometheus.NewRegistry()
	reg.MustRegister(slow)
	// The background Gather outlives the request by design (promhttp documents
	// that it is not canceled), so release it before the test ends.
	t.Cleanup(func() { close(slow.release) })
	srv := a.buildMetricsServer(reg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:2112/metrics", nil)
	req.Host = "127.0.0.1:2112"
	srv.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503: prometheus.timeout did not bound a hung gather; body:\n%s",
			rr.Code, rr.Body.String())
	}
	p := pointFor(t, rec, appcatalog.MetricMetricsScrapeRequests, attrOutcome, scrapeOutcomeUnavailable)
	if p.Value != 1 {
		t.Errorf("%s{outcome=unavailable} = %v, want 1", appcatalog.MetricMetricsScrapeRequests, p.Value)
	}
}

// CoalesceGather is the other half of the pile-up defense: overlapping scrapes
// share one collection cycle instead of each running their own.
func TestMetricsCoalesceGatherSharesOneCollection(t *testing.T) {
	a, rec, _ := selfObsScrapeApp(t, func(c *config.Config) {
		c.Prometheus.CoalesceGather = true
	})
	slow := newSlowScrapeCollector()
	reg := prometheus.NewRegistry()
	reg.MustRegister(slow)
	srv := a.buildMetricsServer(reg)

	first := scrapeAsync(srv)
	<-slow.entered

	second := scrapeAsync(srv)
	// The in-flight gauge is incremented before promhttp is entered, so this
	// proves the second request is inside the handler and about to join the cycle
	// already running; the short settle covers the handful of instructions
	// between the two.
	waitForInFlight(t, rec, 2)
	time.Sleep(100 * time.Millisecond)

	close(slow.release)
	var bodies []string
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, ch := range []<-chan *httptest.ResponseRecorder{first, second} {
		wg.Add(1)
		go func(ch <-chan *httptest.ResponseRecorder) {
			defer wg.Done()
			rr := <-ch
			mu.Lock()
			defer mu.Unlock()
			if rr.Code != http.StatusOK {
				t.Errorf("coalesced scrape code = %d, want 200; body:\n%s", rr.Code, rr.Body.String())
			}
			bodies = append(bodies, rr.Body.String())
		}(ch)
	}
	wg.Wait()

	if got := slow.calls.Load(); got != 1 {
		t.Errorf("Collect ran %d times for two overlapping scrapes, want 1: coalesce_gather is not "+
			"deduplicating concurrent gathers", got)
	}
	if len(bodies) == 2 && bodies[0] != bodies[1] {
		t.Error("coalesced scrapes received different snapshots; they must share one collection cycle")
	}
}

// The guards are secure by default: a default deployment must not turn every
// overlapping scrape into an independent unbounded Gather.
func TestMetricsLoadGuardsSecureByDefault(t *testing.T) {
	cfg := config.Default()
	if cfg.Prometheus.MaxRequestsInFlight != 4 {
		t.Errorf("default max_requests_in_flight = %d, want 4", cfg.Prometheus.MaxRequestsInFlight)
	}
	if cfg.Prometheus.Timeout.D() != 8*time.Second {
		t.Errorf("default timeout = %v, want 8s", cfg.Prometheus.Timeout.D())
	}
	if !cfg.Prometheus.CoalesceGather {
		t.Error("default coalesce_gather = false, want true")
	}

	a, rec, _ := selfObsScrapeApp(t, nil)
	slow := newSlowScrapeCollector()
	reg := prometheus.NewRegistry()
	reg.MustRegister(slow)
	srv := a.buildMetricsServer(reg)

	first := scrapeAsync(srv)
	<-slow.entered
	second := scrapeAsync(srv)
	waitForInFlight(t, rec, 2)
	time.Sleep(100 * time.Millisecond)
	if got := slow.calls.Load(); got != 1 {
		t.Fatalf("Collect ran %d times for overlapping default scrapes, want one coalesced gather", got)
	}
	close(slow.release)
	for _, ch := range []<-chan *httptest.ResponseRecorder{first, second} {
		if rr := <-ch; rr.Code != http.StatusOK {
			t.Errorf("code = %d, want 200 with secure defaults; body:\n%s", rr.Code, rr.Body.String())
		}
	}
}
