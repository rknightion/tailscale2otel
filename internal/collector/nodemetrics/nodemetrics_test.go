package nodemetrics_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// serveText returns an *httptest.Server that responds to every request with the
// current value of *body, allowing a test to mutate the served payload between
// scrapes. The caller is responsible for Close().
func serveText(body *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(*body))
	}))
}

// pointByAttr finds the single metric point whose attrs match all of want.
func pointByAttr(pts []telemetrytest.MetricPoint, want map[string]string) (telemetrytest.MetricPoint, bool) {
	for _, p := range pts {
		ok := true
		for k, v := range want {
			if p.Attrs[k] != v {
				ok = false
				break
			}
		}
		if ok {
			return p, true
		}
	}
	return telemetrytest.MetricPoint{}, false
}

func TestNameAndDefaultInterval(t *testing.T) {
	c := nodemetrics.New(nodemetrics.Options{})
	if c.Name() != "nodemetrics" {
		t.Fatalf("Name() = %q, want nodemetrics", c.Name())
	}
	if got := c.DefaultInterval(); got != 60*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 60s (zero default)", got)
	}
	c2 := nodemetrics.New(nodemetrics.Options{Interval: 15 * time.Second})
	if got := c2.DefaultInterval(); got != 15*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 15s (explicit)", got)
	}
}

func TestEmptyTargets_CollectNil(t *testing.T) {
	c := nodemetrics.New(nodemetrics.Options{})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() with no targets error = %v, want nil", err)
	}
	if names := rec.MetricNames(); len(names) != 0 {
		t.Fatalf("MetricNames with no targets = %v, want none", names)
	}
}

func TestGauge_CurrentValueEachScrape(t *testing.T) {
	body := "# HELP tailscaled_inbound_packets_total help\n" +
		"# TYPE node_load gauge\n" +
		"node_load{kind=\"cpu\"} 0.5\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("node_load")
	if len(pts) != 1 {
		t.Fatalf("node_load points = %d, want 1; pts=%+v", len(pts), pts)
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Fatalf("node_load kind = %q, want gauge", p.Kind)
	}
	if p.Value != 0.5 {
		t.Fatalf("node_load value = %v, want 0.5", p.Value)
	}
	if p.Unit != "" {
		t.Fatalf("node_load unit = %q, want empty", p.Unit)
	}
	if p.Attrs["instance"] != "node-a" {
		t.Fatalf("node_load instance = %q, want node-a", p.Attrs["instance"])
	}
	if p.Attrs["kind"] != "cpu" {
		t.Fatalf("node_load kind label = %q, want cpu", p.Attrs["kind"])
	}

	// Second scrape with a changed value: a gauge always reports the current value.
	body = "# TYPE node_load gauge\nnode_load{kind=\"cpu\"} 0.9\n"
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts = rec.MetricPoints("node_load")
	p2, ok := pointByAttr(pts, map[string]string{"kind": "cpu", "instance": "node-a"})
	if !ok {
		t.Fatalf("no node_load point after second scrape; pts=%+v", pts)
	}
	if p2.Value != 0.9 {
		t.Fatalf("node_load value after second scrape = %v, want 0.9", p2.Value)
	}
}

func TestCounter_BaselineThenDelta(t *testing.T) {
	body := "# TYPE reqs counter\nreqs{path=\"/\"} 100\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})

	// First scrape: baseline only, no counter emission.
	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec1.MetricPoints("reqs"); len(pts) != 0 {
		t.Fatalf("reqs points after first scrape = %d, want 0 (baseline only); pts=%+v", len(pts), pts)
	}

	// Second scrape: value increased by 25, expect a delta of 25.
	body = "# TYPE reqs counter\nreqs{path=\"/\"} 125\n"
	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec2.MetricPoints("reqs")
	if len(pts) != 1 {
		t.Fatalf("reqs points after second scrape = %d, want 1; pts=%+v", len(pts), pts)
	}
	if pts[0].Kind != "sum" {
		t.Fatalf("reqs kind = %q, want sum (counter)", pts[0].Kind)
	}
	if !pts[0].Monotonic {
		t.Fatalf("reqs monotonic = false, want true (counter)")
	}
	if pts[0].Value != 25 {
		t.Fatalf("reqs delta = %v, want 25", pts[0].Value)
	}
	if pts[0].Attrs["instance"] != "node-a" || pts[0].Attrs["path"] != "/" {
		t.Fatalf("reqs attrs = %+v, want instance=node-a path=/", pts[0].Attrs)
	}

	// Third scrape with no change: delta 0, nothing emitted to a fresh recorder.
	rec3 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec3.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec3.MetricPoints("reqs"); len(pts) != 0 {
		t.Fatalf("reqs points after unchanged scrape = %d, want 0 (delta 0); pts=%+v", len(pts), pts)
	}
}

func TestCounter_ResetEmitsCurrent(t *testing.T) {
	body := "# TYPE reqs counter\nreqs 100\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})

	// Baseline at 100.
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	// Reset: cur (30) < prev (100) => delta = cur = 30.
	body = "# TYPE reqs counter\nreqs 30\n"
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("reqs")
	if len(pts) != 1 {
		t.Fatalf("reqs points after reset = %d, want 1; pts=%+v", len(pts), pts)
	}
	if pts[0].Value != 30 {
		t.Fatalf("reqs delta after reset = %v, want 30 (cur)", pts[0].Value)
	}
}

func TestHistogramFamily_ForwardedAsCounters(t *testing.T) {
	body := "# HELP http_request_duration_seconds latency\n" +
		"# TYPE http_request_duration_seconds histogram\n" +
		"http_request_duration_seconds_bucket{le=\"0.1\"} 10\n" +
		"http_request_duration_seconds_bucket{le=\"+Inf\"} 20\n" +
		"http_request_duration_seconds_sum 3.5\n" +
		"http_request_duration_seconds_count 20\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})
	// Baseline.
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	// Second scrape: all components grow.
	body = "# TYPE http_request_duration_seconds histogram\n" +
		"http_request_duration_seconds_bucket{le=\"0.1\"} 15\n" +
		"http_request_duration_seconds_bucket{le=\"+Inf\"} 33\n" +
		"http_request_duration_seconds_sum 6.0\n" +
		"http_request_duration_seconds_count 33\n"
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	bkt := rec.MetricPoints("http_request_duration_seconds_bucket")
	b01, ok := pointByAttr(bkt, map[string]string{"le": "0.1"})
	if !ok {
		t.Fatalf("no bucket point for le=0.1; pts=%+v", bkt)
	}
	if b01.Kind != "sum" || !b01.Monotonic {
		t.Fatalf("bucket le=0.1 kind=%q monotonic=%v, want sum+monotonic", b01.Kind, b01.Monotonic)
	}
	if b01.Value != 5 {
		t.Fatalf("bucket le=0.1 delta = %v, want 5", b01.Value)
	}
	bInf, ok := pointByAttr(bkt, map[string]string{"le": "+Inf"})
	if !ok {
		t.Fatalf("no bucket point for le=+Inf; pts=%+v", bkt)
	}
	if bInf.Value != 13 {
		t.Fatalf("bucket le=+Inf delta = %v, want 13", bInf.Value)
	}

	sum := rec.MetricPoints("http_request_duration_seconds_sum")
	if len(sum) != 1 || sum[0].Value != 2.5 {
		t.Fatalf("sum delta = %+v, want single point value 2.5", sum)
	}
	if sum[0].Kind != "sum" {
		t.Fatalf("_sum kind = %q, want sum (counter)", sum[0].Kind)
	}
	cnt := rec.MetricPoints("http_request_duration_seconds_count")
	if len(cnt) != 1 || cnt[0].Value != 13 {
		t.Fatalf("count delta = %+v, want single point value 13", cnt)
	}
}

func TestDefaultInstanceFromURL(t *testing.T) {
	body := "# TYPE g gauge\ng 1\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		// No Instance: default must be derived from the URL host:port.
		Targets: []nodemetrics.Target{{URL: srv.URL}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("g")
	if len(pts) != 1 {
		t.Fatalf("g points = %d, want 1", len(pts))
	}
	// srv.URL is like http://127.0.0.1:PORT — instance should be host:port.
	want := srv.Listener.Addr().String()
	if pts[0].Attrs["instance"] != want {
		t.Fatalf("default instance = %q, want %q (host:port from URL)", pts[0].Attrs["instance"], want)
	}
}

func TestLabelPassthrough(t *testing.T) {
	body := "# TYPE g gauge\ng{region=\"eu\"} 1\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:      srv.URL,
			Instance: "node-a",
			Labels:   map[string]string{"role": "relay", "dc": "fra"},
		}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("g")
	p, ok := pointByAttr(pts, map[string]string{
		"instance": "node-a",
		"role":     "relay",
		"dc":       "fra",
		"region":   "eu",
	})
	if !ok {
		t.Fatalf("no g point with merged labels; pts=%+v", pts)
	}
	if p.Value != 1 {
		t.Fatalf("g value = %v, want 1", p.Value)
	}
}

func TestHealthUp_OnSuccess(t *testing.T) {
	body := "# TYPE g gauge\ng 1\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 {
		t.Fatalf("up points = %d, want 1; pts=%+v", len(up), up)
	}
	if up[0].Kind != "gauge" {
		t.Fatalf("up kind = %q, want gauge", up[0].Kind)
	}
	if up[0].Value != 1 {
		t.Fatalf("up value = %v, want 1", up[0].Value)
	}
	if up[0].Unit != "1" {
		t.Fatalf("up unit = %q, want 1", up[0].Unit)
	}
	if up[0].Attrs["instance"] != "node-a" {
		t.Fatalf("up instance = %q, want node-a", up[0].Attrs["instance"])
	}
}

func TestMultipleTargets_OneFailsOtherHealthy(t *testing.T) {
	goodBody := "# TYPE g gauge\ng 7\n"
	good := serveText(&goodBody)
	defer good.Close()

	// A server that always 500s.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{
			{URL: good.URL, Instance: "good"},
			{URL: bad.URL, Instance: "bad"},
		},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (one target healthy)", err)
	}

	// Healthy target still emits its series.
	g := rec.MetricPoints("g")
	gp, ok := pointByAttr(g, map[string]string{"instance": "good"})
	if !ok || gp.Value != 7 {
		t.Fatalf("good g point = %+v (ok=%v), want value 7 instance good", gp, ok)
	}

	up := rec.MetricPoints("tailscale.node.up")
	goodUp, ok := pointByAttr(up, map[string]string{"instance": "good"})
	if !ok || goodUp.Value != 1 {
		t.Fatalf("good up = %+v (ok=%v), want 1", goodUp, ok)
	}
	badUp, ok := pointByAttr(up, map[string]string{"instance": "bad"})
	if !ok || badUp.Value != 0 {
		t.Fatalf("bad up = %+v (ok=%v), want 0", badUp, ok)
	}
}

func TestAllTargetsFail_ReturnsError(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{
			{URL: bad.URL, Instance: "bad1"},
			{URL: "http://127.0.0.1:0/", Instance: "bad2"}, // connection refused
		},
	})
	rec := telemetrytest.New()
	err := c.Collect(context.Background(), rec.Emitter())
	if err == nil {
		t.Fatal("Collect() error = nil, want non-nil (all targets failed)")
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 2 {
		t.Fatalf("up points = %d, want 2 (both down)", len(up))
	}
	for _, p := range up {
		if p.Value != 0 {
			t.Fatalf("up value = %v for %+v, want 0", p.Value, p.Attrs)
		}
	}
}

func TestUntypedAndUnknownAsGauge(t *testing.T) {
	// No TYPE line for "u" => unknown => gauge. "v" explicitly untyped => gauge.
	body := "u 42\n# TYPE v untyped\nv 3.14\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	u := rec.MetricPoints("u")
	if len(u) != 1 || u[0].Kind != "gauge" || u[0].Value != 42 {
		t.Fatalf("u = %+v, want single gauge value 42", u)
	}
	v := rec.MetricPoints("v")
	if len(v) != 1 || v[0].Kind != "gauge" || v[0].Value != 3.14 {
		t.Fatalf("v = %+v, want single gauge value 3.14", v)
	}
}

func TestLabelValueUnescaping(t *testing.T) {
	body := "# TYPE g gauge\n" + `g{msg="a\\b\"c\nd"} 1` + "\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("g")
	if len(pts) != 1 {
		t.Fatalf("g points = %d, want 1", len(pts))
	}
	want := "a\\b\"c\nd"
	if pts[0].Attrs["msg"] != want {
		t.Fatalf("unescaped label = %q, want %q", pts[0].Attrs["msg"], want)
	}
}

func TestMalformedLinesSkipped(t *testing.T) {
	body := "# TYPE g gauge\n" +
		"\n" + // blank
		"this is not valid\n" + // no value
		"g 5\n" +
		"g{unterminated 6\n" + // malformed labels
		"g2 notanumber\n" // bad float
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("g")
	if len(pts) != 1 || pts[0].Value != 5 {
		t.Fatalf("g = %+v, want single value 5 (malformed lines skipped)", pts)
	}
	if g2 := rec.MetricPoints("g2"); len(g2) != 0 {
		t.Fatalf("g2 = %+v, want none (bad float skipped)", g2)
	}
}

func TestTimestampIgnored(t *testing.T) {
	body := "# TYPE g gauge\ng 9 1700000000000\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("g")
	if len(pts) != 1 || pts[0].Value != 9 {
		t.Fatalf("g = %+v, want single value 9 (trailing ts ignored)", pts)
	}
}

// TestParse_LabelValueWithBraceAndComma is a regression test: a Prometheus label
// value may legally contain an unescaped '}' and ',' (only \\, \" and \n are
// escapes). Such a series must be parsed (not silently dropped) and its value
// preserved verbatim. Two scrapes confirm the series is tracked end-to-end.
func TestParse_LabelValueWithBraceAndComma(t *testing.T) {
	body := "# TYPE m counter\n" + `m{msg="warn: skew }, detected",k="v"} 100` + "\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}}})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil { // baseline
		t.Fatalf("Collect 1: %v", err)
	}
	body = "# TYPE m counter\n" + `m{msg="warn: skew }, detected",k="v"} 150` + "\n"
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil { // delta 50
		t.Fatalf("Collect 2: %v", err)
	}

	pts := rec.MetricPoints("m")
	p, ok := pointByAttr(pts, map[string]string{"msg": "warn: skew }, detected", "k": "v", "instance": "n"})
	if !ok {
		t.Fatalf("series with brace/comma label value was dropped; points=%+v", pts)
	}
	if p.Value != 50 {
		t.Fatalf("delta = %v, want 50", p.Value)
	}
}

// TestSeriesKey_NoCollisionAcrossDistinctLabels is a regression test: two
// genuinely distinct label sets that a naive "k=v,..." key would conflate
// ({x="1,y=2"} vs {x="1",y="2"}) must be tracked as separate delta series.
func TestSeriesKey_NoCollisionAcrossDistinctLabels(t *testing.T) {
	mk := func(a, b int) string {
		return "# TYPE m counter\n" +
			`m{x="1,y=2",instance="n"} ` + strconv.Itoa(a) + "\n" +
			`m{x="1",y="2",instance="n"} ` + strconv.Itoa(b) + "\n"
	}
	body := mk(100, 7)
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}}})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil { // baselines
		t.Fatalf("Collect 1: %v", err)
	}
	body = mk(110, 9) // A +10, B +2
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}

	pts := rec.MetricPoints("m")
	a, okA := pointByAttr(pts, map[string]string{"x": "1,y=2"})
	if !okA || a.Value != 10 {
		t.Fatalf("series A delta = %v (ok=%v), want 10; pts=%+v", a.Value, okA, pts)
	}
	b, okB := pointByAttr(pts, map[string]string{"x": "1", "y": "2"})
	if !okB || b.Value != 2 {
		t.Fatalf("series B delta = %v (ok=%v), want 2; pts=%+v", b.Value, okB, pts)
	}
}

// TestPrune_StaleBaselineEvicted is a regression test: a counter series not seen
// for staleGenerations scrapes must have its delta baseline evicted, so a later
// re-appearance at a lower value is treated as a fresh baseline (no spurious
// reset delta). Total emitted delta stays at the single 50 from the early scrape.
func TestPrune_StaleBaselineEvicted(t *testing.T) {
	withC := "# TYPE m counter\nm{instance=\"n\"} %s\n"
	body := fmt.Sprintf(withC, "100")
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}}})
	rec := telemetrytest.New()
	collect := func() {
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
	}
	collect() // gen1 baseline 100
	body = fmt.Sprintf(withC, "150")
	collect()                              // gen2 delta 50
	body = "# TYPE other gauge\nother 1\n" // counter absent for many scrapes
	for i := 0; i < 6; i++ {
		collect() // gen3..gen8: m unobserved -> baseline evicted
	}
	body = fmt.Sprintf(withC, "10") // reappears LOWER than 150
	collect()                       // if baseline survived: 10<150 -> reset emit 10; if evicted: baseline, no emit

	var total float64
	for _, p := range rec.MetricPoints("m") {
		total += p.Value
	}
	if total != 50 {
		t.Fatalf("m total delta = %v, want 50 (stale baseline must be evicted so the re-add is a fresh baseline, not a +10 reset)", total)
	}
}
