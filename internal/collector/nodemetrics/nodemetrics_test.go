package nodemetrics_test

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// ptr returns a pointer to s, for serveText's mutable-body argument.
func ptr(s string) *string { return &s }

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

func TestScrape_MaxResponseBytesRejectsOversizedBody(t *testing.T) {
	body := strings.Repeat("a", 32)
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets:          []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
		MaxResponseBytes: 16,
		MaxSamples:       10,
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect() error = nil, want oversized response to fail the only target")
	}
	if pts := rec.MetricPoints("tailscale.node.up"); len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("tailscale.node.up = %+v, want one down point", pts)
	}
}

func TestScrape_MaxSamplesRejectsOversizedSampleSet(t *testing.T) {
	body := "# TYPE m gauge\n" +
		"m{series=\"1\"} 1\n" +
		"m{series=\"2\"} 2\n" +
		"m{series=\"3\"} 3\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets:          []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
		MaxResponseBytes: 1024,
		MaxSamples:       2,
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect() error = nil, want oversized sample set to fail the only target")
	}
	if pts := rec.MetricPoints("m"); len(pts) != 0 {
		t.Fatalf("m = %+v, want no forwarded samples after sample-limit failure", pts)
	}
	if pts := rec.MetricPoints("tailscale.node.up"); len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("tailscale.node.up = %+v, want one down point", pts)
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
	if p.Attrs["tailscale.node"] != "node-a" {
		t.Fatalf("node_load tailscale.node = %q, want node-a", p.Attrs["tailscale.node"])
	}
	// Regression guard for the service.instance.id resource-promotion collision:
	// the per-node identity must NOT be emitted under the old "instance" key.
	if _, ok := p.Attrs["instance"]; ok {
		t.Fatalf("node_load emitted legacy instance attr = %q, want absent (renamed to tailscale.node)", p.Attrs["instance"])
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
	p2, ok := pointByAttr(pts, map[string]string{"kind": "cpu", "tailscale.node": "node-a"})
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
	if pts[0].Attrs["tailscale.node"] != "node-a" || pts[0].Attrs["path"] != "/" {
		t.Fatalf("reqs attrs = %+v, want tailscale.node=node-a path=/", pts[0].Attrs)
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
	// srv.URL is like http://127.0.0.1:PORT — tailscale.node should be host:port.
	want := srv.Listener.Addr().String()
	if pts[0].Attrs["tailscale.node"] != want {
		t.Fatalf("default tailscale.node = %q, want %q (host:port from URL)", pts[0].Attrs["tailscale.node"], want)
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
		"tailscale.node": "node-a",
		"role":           "relay",
		"dc":             "fra",
		"region":         "eu",
	})
	if !ok {
		t.Fatalf("no g point with merged labels; pts=%+v", pts)
	}
	if p.Value != 1 {
		t.Fatalf("g value = %v, want 1", p.Value)
	}
}

func TestReservedPrometheusIdentityLabelIsStripped(t *testing.T) {
	body := `# TYPE g gauge
g{tailscale_node="spoofed",region="eu"} 1
`
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:      srv.URL,
			Instance: "node-a",
			Labels:   map[string]string{"tailscale_node": "target-spoof", "role": "relay"},
		}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("g")
	if len(pts) != 1 {
		t.Fatalf("g points = %d, want 1; pts=%+v", len(pts), pts)
	}
	p := pts[0]
	if p.Attrs["tailscale.node"] != "node-a" {
		t.Fatalf("tailscale.node label = %q, want actual node identity", p.Attrs["tailscale.node"])
	}
	if _, ok := p.Attrs["tailscale_node"]; ok {
		t.Fatalf("reserved normalized identity label present = %q, want stripped", p.Attrs["tailscale_node"])
	}
	if p.Attrs["region"] != "eu" || p.Attrs["role"] != "relay" {
		t.Fatalf("non-reserved labels = %+v, want region and role preserved", p.Attrs)
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
	if up[0].Attrs["tailscale.node"] != "node-a" {
		t.Fatalf("up tailscale.node = %q, want node-a", up[0].Attrs["tailscale.node"])
	}
	// Regression guard for the service.instance.id resource-promotion collision:
	// tailscale.node.up must NOT carry the per-node identity under "instance"
	// (Grafana's OTLP->Prometheus promotes the resource service.instance.id to the
	// "instance" label, which would otherwise clobber per-node attribution).
	if _, ok := up[0].Attrs["instance"]; ok {
		t.Fatalf("up emitted legacy instance attr = %q, want absent (renamed to tailscale.node)", up[0].Attrs["instance"])
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
	gp, ok := pointByAttr(g, map[string]string{"tailscale.node": "good"})
	if !ok || gp.Value != 7 {
		t.Fatalf("good g point = %+v (ok=%v), want value 7 tailscale.node good", gp, ok)
	}

	up := rec.MetricPoints("tailscale.node.up")
	goodUp, ok := pointByAttr(up, map[string]string{"tailscale.node": "good"})
	if !ok || goodUp.Value != 1 {
		t.Fatalf("good up = %+v (ok=%v), want 1", goodUp, ok)
	}
	badUp, ok := pointByAttr(up, map[string]string{"tailscale.node": "bad"})
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
	p, ok := pointByAttr(pts, map[string]string{"msg": "warn: skew }, detected", "k": "v", "tailscale.node": "n"})
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

// TestBearerToken_ForwardedAsAuthorization verifies a static BearerToken is sent
// as the Authorization: Bearer header on every scrape request.
func TestBearerToken_ForwardedAsAuthorization(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n", BearerToken: "sekret"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer sekret")
	}
}

// TestBearerTokenFile_ReadFreshEachScrape verifies the token is read from a file
// on every scrape (rotation-safe): rewriting the file changes the sent header, and
// BearerTokenFile takes precedence over a static BearerToken.
func TestBearerTokenFile_ReadFreshEachScrape(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("first\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:             srv.URL,
			Instance:        "n",
			BearerToken:     "ignored", // file takes precedence
			BearerTokenFile: tokenPath,
		}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if gotAuth != "Bearer first" {
		t.Fatalf("Authorization = %q, want %q (trimmed file contents)", gotAuth, "Bearer first")
	}

	// Rotate the token; the next scrape must pick up the new contents.
	if err := os.WriteFile(tokenPath, []byte("second"), 0o600); err != nil {
		t.Fatalf("rewrite token: %v", err)
	}
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if gotAuth != "Bearer second" {
		t.Fatalf("Authorization after rotation = %q, want %q", gotAuth, "Bearer second")
	}
}

// TestBearerTokenFile_ReadErrorFailsScrape verifies a missing/unreadable token
// file fails the scrape so tailscale.node.up reports 0.
func TestBearerTokenFile_ReadErrorFailsScrape(t *testing.T) {
	srv := serveText(ptr("# TYPE g gauge\ng 1\n"))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:             srv.URL,
			Instance:        "n",
			BearerTokenFile: filepath.Join(t.TempDir(), "does-not-exist"),
		}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect() error = nil, want non-nil (token file unreadable)")
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 || up[0].Value != 0 {
		t.Fatalf("up = %+v, want single value 0 (scrape failed)", up)
	}
	if g := rec.MetricPoints("g"); len(g) != 0 {
		t.Fatalf("g = %+v, want none (scrape failed before fetch)", g)
	}
}

// TestCustomHeaders_Forwarded verifies arbitrary per-target headers are sent.
func TestCustomHeaders_Forwarded(t *testing.T) {
	var gotProxy, gotX string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProxy = r.Header.Get("X-Proxy-Auth")
		gotX = r.Header.Get("X-Tenant")
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:      srv.URL,
			Instance: "n",
			Headers:  map[string]string{"X-Proxy-Auth": "abc", "X-Tenant": "acme"},
		}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if gotProxy != "abc" || gotX != "acme" {
		t.Fatalf("headers = X-Proxy-Auth %q, X-Tenant %q; want abc, acme", gotProxy, gotX)
	}
}

// writeCAFile PEM-encodes the TLS server's certificate to a temp file and returns
// its path, for use as a per-target TLS CAFile.
func writeCAFile(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cert := srv.Certificate()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	return path
}

// TestTLS_CAFileTrustSucceeds verifies that supplying the TLS server's own
// certificate as the per-target CAFile lets the HTTPS scrape succeed (node.up==1).
func TestTLS_CAFileTrustSucceeds(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:      srv.URL,
			Instance: "n",
			TLS:      &nodemetrics.TLSClientConfig{CAFile: writeCAFile(t, srv)},
		}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (CA trusts server)", err)
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 || up[0].Value != 1 {
		t.Fatalf("up = %+v, want single value 1 (TLS trusted)", up)
	}
	if g := rec.MetricPoints("g"); len(g) != 1 || g[0].Value != 1 {
		t.Fatalf("g = %+v, want single value 1", g)
	}
}

// TestTLS_NoCAFails verifies an HTTPS scrape against an untrusted server with no
// CA and InsecureSkipVerify=false fails (node.up==0). It scrapes the SAME server
// twice in one Collect — once with an empty TLS config (verify on) and once with
// InsecureSkipVerify=true — so the empty-config target failing WHILE the
// skip-verify target succeeds proves the failure is the per-target TLS
// verification (not mere connectivity or the shared default client): the default
// client would reject BOTH, so a passing "skip" target can only mean the
// per-target client was actually built and applied.
func TestTLS_NoCAFails(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{
			{URL: srv.URL, Instance: "verify", TLS: &nodemetrics.TLSClientConfig{}},                       // no CA, verify on -> fail
			{URL: srv.URL, Instance: "skip", TLS: &nodemetrics.TLSClientConfig{InsecureSkipVerify: true}}, // -> succeed
		},
	})
	rec := telemetrytest.New()
	// One target succeeds, so Collect (which errors only when EVERY target fails)
	// returns nil.
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (the skip-verify target succeeds)", err)
	}
	byInstance := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale.node.up") {
		byInstance[p.Attrs["tailscale.node"]] = p.Value
	}
	if byInstance["verify"] != 0 {
		t.Fatalf("verify-target up = %v, want 0 (untrusted cert, verify on)", byInstance["verify"])
	}
	if byInstance["skip"] != 1 {
		t.Fatalf("skip-target up = %v, want 1 (per-target InsecureSkipVerify applied)", byInstance["skip"])
	}
}

// TestTLS_InsecureSkipVerifySucceeds verifies InsecureSkipVerify=true lets the
// scrape succeed against an untrusted TLS server with no CA (node.up==1).
func TestTLS_InsecureSkipVerifySucceeds(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:      srv.URL,
			Instance: "n",
			TLS:      &nodemetrics.TLSClientConfig{InsecureSkipVerify: true},
		}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (insecure skip verify)", err)
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 || up[0].Value != 1 {
		t.Fatalf("up = %+v, want single value 1 (verify skipped)", up)
	}
}

// TestPlainTarget_NoNewFields_NoAuthHeader is a regression test: a target with no
// auth/TLS fields set scrapes a plain HTTP server and sends NO Authorization
// header (byte-for-byte the prior plain-GET behavior).
func TestPlainTarget_NoNewFields_NoAuthHeader(t *testing.T) {
	var gotAuth string
	var sawAuthHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, sawAuthHeader = r.Header["Authorization"]
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 5\n"))
	}))
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if sawAuthHeader || gotAuth != "" {
		t.Fatalf("plain target sent Authorization = %q (present=%v), want none", gotAuth, sawAuthHeader)
	}
	g := rec.MetricPoints("g")
	if len(g) != 1 || g[0].Value != 5 {
		t.Fatalf("g = %+v, want single value 5", g)
	}
}

// TestMixedTargets_TLSAndPlain verifies that, with one TLS target (trusted via
// CAFile) and one plain HTTP target, each uses the correct client: both succeed
// and emit their series. The plain target must not be affected by the other
// target's dedicated TLS client.
func TestMixedTargets_TLSAndPlain(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# TYPE g gauge\ng 1\n"))
	}))
	defer tlsSrv.Close()

	plainBody := "# TYPE g gauge\ng 2\n"
	plainSrv := serveText(&plainBody)
	defer plainSrv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{
			{URL: tlsSrv.URL, Instance: "secure", TLS: &nodemetrics.TLSClientConfig{CAFile: writeCAFile(t, tlsSrv)}},
			{URL: plainSrv.URL, Instance: "plain"},
		},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (both healthy)", err)
	}
	g := rec.MetricPoints("g")
	secure, okS := pointByAttr(g, map[string]string{"tailscale.node": "secure"})
	if !okS || secure.Value != 1 {
		t.Fatalf("secure g = %+v (ok=%v), want value 1", secure, okS)
	}
	plain, okP := pointByAttr(g, map[string]string{"tailscale.node": "plain"})
	if !okP || plain.Value != 2 {
		t.Fatalf("plain g = %+v (ok=%v), want value 2", plain, okP)
	}
	up := rec.MetricPoints("tailscale.node.up")
	for _, p := range up {
		if p.Value != 1 {
			t.Fatalf("up = %+v, want all 1", up)
		}
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

// --- Dynamic discovery test infrastructure ---------------------------------

// fakeDiscoverer is a controllable nodemetrics.Discoverer. targets/err are the
// values returned by Discover; calls counts invocations. It is mutex-guarded so
// a test can mutate err between scrapes and read calls without a race even if a
// future Collect calls Discover off the test goroutine.
type fakeDiscoverer struct {
	mu      sync.Mutex
	targets []nodemetrics.Target
	err     error
	calls   int
}

func (f *fakeDiscoverer) Discover(context.Context) ([]nodemetrics.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.targets, f.err
}

func (f *fakeDiscoverer) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeDiscoverer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// countingServer is an httptest server that serves a fixed Prometheus-text body
// and counts the requests it received.
func countingServer(t *testing.T, body string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(body))
	}))
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// TestStaticOnly_NoDiscovererUnchanged: with no Discoverer, a single static
// httptest target scrapes and emits exactly as today (the whole existing suite
// also asserts the static-only path is byte-for-byte preserved).
func TestStaticOnly_NoDiscovererUnchanged(t *testing.T) {
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
	g := rec.MetricPoints("g")
	if len(g) != 1 || g[0].Value != 1 || g[0].Attrs["tailscale.node"] != "node-a" {
		t.Fatalf("g = %+v, want single value 1 tailscale.node node-a", g)
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 || up[0].Value != 1 {
		t.Fatalf("up = %+v, want single value 1", up)
	}
}

// TestDiscovery_GatingHonorsInterval: a Discoverer is consulted only on its own
// DiscoveryInterval. First Collect discovers (calls==1); a Collect 1m later does
// NOT (not due); a Collect at/after the 5m interval discovers again (calls==2).
func TestDiscovery_GatingHonorsInterval(t *testing.T) {
	body := "# TYPE g gauge\ng 1\n"
	srv := serveText(&body)
	defer srv.Close()

	now := time.Unix(1_700_000_000, 0)
	fake := &fakeDiscoverer{targets: []nodemetrics.Target{{URL: srv.URL, Instance: "disc"}}}

	c := nodemetrics.New(nodemetrics.Options{
		Discoverer:        fake,
		DiscoveryInterval: 5 * time.Minute,
		Now:               func() time.Time { return now },
	})

	// First Collect: discovery is due (never run), so the discovered target is
	// scraped.
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect 1 error = %v", err)
	}
	if got := fake.callCount(); got != 1 {
		t.Fatalf("discover calls after first Collect = %d, want 1", got)
	}
	if up := rec.MetricPoints("tailscale.node.up"); len(up) != 1 || up[0].Attrs["tailscale.node"] != "disc" {
		t.Fatalf("up after first Collect = %+v, want single point tailscale.node disc", up)
	}

	// 1 minute later: not due, no new discovery.
	now = now.Add(1 * time.Minute)
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("Collect 2 error = %v", err)
	}
	if got := fake.callCount(); got != 1 {
		t.Fatalf("discover calls after 1m = %d, want 1 (not due)", got)
	}

	// Advance to >= 5m total since the last successful discovery: now due again.
	now = now.Add(4 * time.Minute)
	if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
		t.Fatalf("Collect 3 error = %v", err)
	}
	if got := fake.callCount(); got != 2 {
		t.Fatalf("discover calls after 5m = %d, want 2 (due again)", got)
	}
}

// TestDiscovery_FirstRunUnionsStaticImmediately: with a static target and a
// Discoverer returning [], the very first Collect still scrapes the static
// target — active always includes static, even on the first (empty) discovery.
func TestDiscovery_FirstRunUnionsStaticImmediately(t *testing.T) {
	body := "# TYPE g gauge\ng 7\n"
	srv := serveText(&body)
	defer srv.Close()

	now := time.Unix(1_700_000_000, 0)
	fake := &fakeDiscoverer{targets: nil} // discovers nothing

	c := nodemetrics.New(nodemetrics.Options{
		Targets:           []nodemetrics.Target{{URL: srv.URL, Instance: "static-a"}},
		Discoverer:        fake,
		DiscoveryInterval: 5 * time.Minute,
		Now:               func() time.Time { return now },
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect error = %v", err)
	}
	if got := fake.callCount(); got != 1 {
		t.Fatalf("discover calls = %d, want 1 (ran on first Collect)", got)
	}
	g := rec.MetricPoints("g")
	if len(g) != 1 || g[0].Value != 7 || g[0].Attrs["tailscale.node"] != "static-a" {
		t.Fatalf("g = %+v, want single value 7 tailscale.node static-a (static scraped despite empty discovery)", g)
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 || up[0].Attrs["tailscale.node"] != "static-a" || up[0].Value != 1 {
		t.Fatalf("up = %+v, want single point tailscale.node static-a value 1", up)
	}
}

// TestDiscovery_UnionAndDedup: static target A and a discoverer returning A's
// SAME URL (with different label/instance) plus a distinct target B. After one
// Collect, A is scraped exactly once and still carries its STATIC label/instance
// (static wins the dedup), B is scraped once, and tailscale.node.up has exactly
// one point per distinct instance.
func TestDiscovery_UnionAndDedup(t *testing.T) {
	srvA, hitsA := countingServer(t, "# TYPE g gauge\ng 1\n")
	defer srvA.Close()
	srvB, hitsB := countingServer(t, "# TYPE g gauge\ng 2\n")
	defer srvB.Close()

	now := time.Unix(1_700_000_000, 0)
	fake := &fakeDiscoverer{targets: []nodemetrics.Target{
		// Same URL as the static A, but different instance/label: static must win.
		{URL: srvA.URL, Instance: "discovered-A", Labels: map[string]string{"src": "discovery"}},
		{URL: srvB.URL, Instance: "node-b", Labels: map[string]string{"src": "discovery"}},
	}}

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{
			URL:      srvA.URL,
			Instance: "static-A",
			Labels:   map[string]string{"src": "static"},
		}},
		Discoverer:        fake,
		DiscoveryInterval: 5 * time.Minute,
		Now:               func() time.Time { return now },
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect error = %v", err)
	}

	// A scraped exactly once (deduped), B exactly once.
	if got := hitsA(); got != 1 {
		t.Fatalf("A handler requests = %d, want 1 (deduped union)", got)
	}
	if got := hitsB(); got != 1 {
		t.Fatalf("B handler requests = %d, want 1", got)
	}

	// A's emitted series carries the STATIC identity/label, not the discovered one.
	g := rec.MetricPoints("g")
	a, okA := pointByAttr(g, map[string]string{"tailscale.node": "static-A", "src": "static"})
	if !okA || a.Value != 1 {
		t.Fatalf("A g point = %+v (ok=%v), want value 1 tailscale.node static-A src static (static wins); g=%+v", a, okA, g)
	}
	if _, dup := pointByAttr(g, map[string]string{"tailscale.node": "discovered-A"}); dup {
		t.Fatalf("found a discovered-A series; static should have won the dedup; g=%+v", g)
	}
	b, okB := pointByAttr(g, map[string]string{"tailscale.node": "node-b", "src": "discovery"})
	if !okB || b.Value != 2 {
		t.Fatalf("B g point = %+v (ok=%v), want value 2 tailscale.node node-b; g=%+v", b, okB, g)
	}

	// One up point per distinct node (A static + B), both healthy.
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 2 {
		t.Fatalf("up points = %d, want 2 (one per distinct node); up=%+v", len(up), up)
	}
	upA, okUA := pointByAttr(up, map[string]string{"tailscale.node": "static-A"})
	upB, okUB := pointByAttr(up, map[string]string{"tailscale.node": "node-b"})
	if !okUA || upA.Value != 1 || !okUB || upB.Value != 1 {
		t.Fatalf("up = %+v, want static-A=1 and node-b=1", up)
	}
}

// TestDiscovery_FailureKeepsStaleTargets: when a due discovery FAILS, the prior
// active set is retained (B keeps being scraped) and lastDiscACK is NOT advanced
// (so the next tick's due check fires immediately). Clearing the error then
// rediscovers. B is present throughout.
func TestDiscovery_FailureKeepsStaleTargets(t *testing.T) {
	srvB, hitsB := countingServer(t, "# TYPE g gauge\ng 5\n")
	defer srvB.Close()

	now := time.Unix(1_700_000_000, 0)
	interval := time.Minute
	fake := &fakeDiscoverer{targets: []nodemetrics.Target{{URL: srvB.URL, Instance: "node-b"}}}

	c := nodemetrics.New(nodemetrics.Options{
		Discoverer:        fake,
		DiscoveryInterval: interval,
		Now:               func() time.Time { return now },
	})

	collectExpectB := func(label string) {
		t.Helper()
		rec := telemetrytest.New()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("%s: Collect error = %v", label, err)
		}
		g := rec.MetricPoints("g")
		if p, ok := pointByAttr(g, map[string]string{"tailscale.node": "node-b"}); !ok || p.Value != 5 {
			t.Fatalf("%s: B g point = %+v (ok=%v), want value 5; g=%+v", label, p, ok, g)
		}
	}

	// First Collect: discovery succeeds, B scraped. calls==1.
	collectExpectB("first")
	if got := fake.callCount(); got != 1 {
		t.Fatalf("calls after first = %d, want 1", got)
	}
	hitsAfterFirst := hitsB()
	if hitsAfterFirst != 1 {
		t.Fatalf("B hits after first = %d, want 1", hitsAfterFirst)
	}

	// Advance past the interval and make discovery fail: due, Discover called and
	// errors -> prior active kept (B STILL scraped), lastDiscACK NOT advanced.
	now = now.Add(interval)
	fake.setErr(errors.New("boom"))
	collectExpectB("during-failure")
	if got := fake.callCount(); got != 2 {
		t.Fatalf("calls after failure = %d, want 2 (discovery attempted)", got)
	}
	if got := hitsB(); got != hitsAfterFirst+1 {
		t.Fatalf("B hits after failure = %d, want %d (still scraped from prior active)", got, hitsAfterFirst+1)
	}

	// Clear the error. Because lastDiscACK was NOT advanced by the failed attempt,
	// the due check fires IMMEDIATELY at the same now -> rediscovers (calls==3).
	fake.setErr(nil)
	collectExpectB("after-recovery")
	if got := fake.callCount(); got != 3 {
		t.Fatalf("calls after recovery = %d, want 3 (lastDiscACK not advanced by failure, so due immediately)", got)
	}
}

// TestDiscovery_AllDiscoveredFail_ReturnsError: with NO static targets and a
// discoverer that returns one target whose server 500s, the active set is the
// single discovered target; scraping it fails, so Collect returns a non-nil
// error (every target failed) and emits tailscale.node.up=0 for it.
func TestDiscovery_AllDiscoveredFail_ReturnsError(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	now := time.Unix(1_700_000_000, 0)
	fake := &fakeDiscoverer{targets: []nodemetrics.Target{{URL: bad.URL, Instance: "disc-bad"}}}

	c := nodemetrics.New(nodemetrics.Options{
		Discoverer:        fake,
		DiscoveryInterval: 5 * time.Minute,
		Now:               func() time.Time { return now },
	})
	rec := telemetrytest.New()
	err := c.Collect(context.Background(), rec.Emitter())
	if err == nil {
		t.Fatal("Collect() error = nil, want non-nil (all discovered targets failed)")
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 {
		t.Fatalf("up points = %d, want 1; up=%+v", len(up), up)
	}
	if up[0].Attrs["tailscale.node"] != "disc-bad" || up[0].Value != 0 {
		t.Fatalf("up = %+v, want tailscale.node disc-bad value 0", up[0])
	}
}

// --- Passthrough filters (metric_allow / metric_deny / drop_labels) --------

// TestMetricAllow_ForwardsOnlyMatchingNames: with a non-empty MetricAllow, only
// metric names matching at least one anchored pattern are forwarded; others are
// dropped at the emitSample choke point.
func TestMetricAllow_ForwardsOnlyMatchingNames(t *testing.T) {
	body := "# TYPE node_load gauge\nnode_load 0.5\n# TYPE other_metric gauge\nother_metric 1\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets:     []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
		MetricAllow: []string{"node_load"},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("node_load"); len(pts) != 1 || pts[0].Value != 0.5 {
		t.Fatalf("node_load = %+v, want single value 0.5 (allowed)", pts)
	}
	if pts := rec.MetricPoints("other_metric"); len(pts) != 0 {
		t.Fatalf("other_metric = %+v, want none (not in allowlist)", pts)
	}
}

// TestMetricAllow_AnchoredMatch: allow patterns are anchored, so "node_lo" does
// NOT match "node_load" (no substring match), while "node_.*" matches both
// node_load and node_loss.
func TestMetricAllow_AnchoredMatch(t *testing.T) {
	body := "# TYPE node_load gauge\nnode_load 1\n# TYPE node_loss gauge\nnode_loss 2\n"
	srv := serveText(&body)
	defer srv.Close()

	// "node_lo" must NOT match "node_load" (anchored, not a substring).
	cExact := nodemetrics.New(nodemetrics.Options{
		Targets:     []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
		MetricAllow: []string{"node_lo"},
	})
	rec := telemetrytest.New()
	if err := cExact.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("node_load"); len(pts) != 0 {
		t.Fatalf("node_load = %+v, want none ('node_lo' is anchored, must not match node_load)", pts)
	}
	if pts := rec.MetricPoints("node_loss"); len(pts) != 0 {
		t.Fatalf("node_loss = %+v, want none ('node_lo' is anchored, must not match node_loss)", pts)
	}

	// "node_.*" matches both.
	cWild := nodemetrics.New(nodemetrics.Options{
		Targets:     []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
		MetricAllow: []string{"node_.*"},
	})
	rec2 := telemetrytest.New()
	if err := cWild.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec2.MetricPoints("node_load"); len(pts) != 1 {
		t.Fatalf("node_load = %+v, want 1 ('node_.*' matches)", pts)
	}
	if pts := rec2.MetricPoints("node_loss"); len(pts) != 1 {
		t.Fatalf("node_loss = %+v, want 1 ('node_.*' matches)", pts)
	}
}

// TestMetricDeny_DropsMatchingNames: with an empty MetricAllow and a MetricDeny,
// a name matching any deny pattern is dropped while others pass through.
func TestMetricDeny_DropsMatchingNames(t *testing.T) {
	body := "# TYPE keep_me gauge\nkeep_me 1\n# TYPE drop_me gauge\ndrop_me 2\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets:    []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
		MetricDeny: []string{"drop_me"},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("keep_me"); len(pts) != 1 || pts[0].Value != 1 {
		t.Fatalf("keep_me = %+v, want single value 1 (not denied)", pts)
	}
	if pts := rec.MetricPoints("drop_me"); len(pts) != 0 {
		t.Fatalf("drop_me = %+v, want none (denied)", pts)
	}
}

// TestMetricAllowDeny_DenyWinsAfterAllow: deny is applied AFTER allow, so a name
// matching both the allowlist and the denylist is dropped (deny precedence).
func TestMetricAllowDeny_DenyWinsAfterAllow(t *testing.T) {
	body := "# TYPE node_load gauge\nnode_load 1\n# TYPE node_temp gauge\nnode_temp 2\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets:     []nodemetrics.Target{{URL: srv.URL, Instance: "n"}},
		MetricAllow: []string{"node_.*"}, // allows both node_load and node_temp
		MetricDeny:  []string{"node_temp"},
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("node_load"); len(pts) != 1 {
		t.Fatalf("node_load = %+v, want 1 (allowed, not denied)", pts)
	}
	if pts := rec.MetricPoints("node_temp"); len(pts) != 0 {
		t.Fatalf("node_temp = %+v, want none (allowed but denied; deny wins)", pts)
	}
}

// TestDropLabels_RemovesLabelKeepsInstance: a key in DropLabels is stripped from
// every forwarded series, but the per-node identity label (tailscale.node) is
// NEVER dropped even if named.
func TestDropLabels_RemovesLabelKeepsInstance(t *testing.T) {
	body := "# TYPE g gauge\ng{region=\"eu\",keep=\"yes\"} 1\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets:    []nodemetrics.Target{{URL: srv.URL, Instance: "node-a", Labels: map[string]string{"role": "relay"}}},
		DropLabels: []string{"region", "role", "tailscale.node"}, // tailscale.node must be kept
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("g")
	if len(pts) != 1 {
		t.Fatalf("g points = %d, want 1; pts=%+v", len(pts), pts)
	}
	p := pts[0]
	if _, ok := p.Attrs["region"]; ok {
		t.Fatalf("region label present = %q, want dropped", p.Attrs["region"])
	}
	if _, ok := p.Attrs["role"]; ok {
		t.Fatalf("role label present = %q, want dropped", p.Attrs["role"])
	}
	if p.Attrs["keep"] != "yes" {
		t.Fatalf("keep label = %q, want yes (not in drop list)", p.Attrs["keep"])
	}
	if p.Attrs["tailscale.node"] != "node-a" {
		t.Fatalf("tailscale.node label = %q, want node-a (the identity label is never dropped)", p.Attrs["tailscale.node"])
	}
}

// TestNodeUp_EmittedDespiteMetricAllow is a regression guard: the per-target
// tailscale.node.up health gauge (and discovery gauges) bypass the passthrough
// filters entirely, so it still emits even when MetricAllow matches nothing.
func TestNodeUp_EmittedDespiteMetricAllow(t *testing.T) {
	body := "# TYPE g gauge\ng 1\n"
	srv := serveText(&body)
	defer srv.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets:     []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}},
		MetricAllow: []string{"nothing"}, // matches no forwarded sample
	})
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	// The forwarded sample is filtered out...
	if pts := rec.MetricPoints("g"); len(pts) != 0 {
		t.Fatalf("g = %+v, want none (allowlist matches nothing)", pts)
	}
	// ...but the health gauge is never subject to the filters.
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 || up[0].Value != 1 || up[0].Attrs["tailscale.node"] != "node-a" {
		t.Fatalf("up = %+v, want single value 1 tailscale.node node-a (never filtered)", up)
	}
}

// targetBySource indexes a Snapshot's targets by their (URL, source) pair so a
// test can assert each active target is classified correctly.
func sourceByURL(s nodemetrics.DiscoveryStatus) map[string]string {
	out := make(map[string]string, len(s.Targets))
	for _, ti := range s.Targets {
		out[ti.URL] = ti.Source
	}
	return out
}

// TestCollector_Snapshot: with a static target and a Discoverer returning one
// additional (different-URL) target, after a successful discovery Snapshot()
// reports discovery enabled and healthy, the static/active counts, and each
// active target's source ("static" vs "discovered"). With NO Discoverer the
// snapshot reports discovery disabled and every target as static.
func TestCollector_Snapshot(t *testing.T) {
	t.Run("with discoverer", func(t *testing.T) {
		srvStatic, _ := countingServer(t, "# TYPE g gauge\ng 1\n")
		defer srvStatic.Close()
		srvDisc, _ := countingServer(t, "# TYPE g gauge\ng 2\n")
		defer srvDisc.Close()

		now := time.Unix(1_700_000_000, 0)
		fake := &fakeDiscoverer{targets: []nodemetrics.Target{
			{URL: srvDisc.URL, Instance: "node-disc"},
		}}

		c := nodemetrics.New(nodemetrics.Options{
			Targets:           []nodemetrics.Target{{URL: srvStatic.URL, Instance: "node-static"}},
			Discoverer:        fake,
			DiscoveryInterval: 5 * time.Minute,
			Now:               func() time.Time { return now },
		})

		// Drive a discovery so active = static ∪ discovered.
		if err := c.Collect(context.Background(), telemetrytest.New().Emitter()); err != nil {
			t.Fatalf("Collect error = %v", err)
		}

		snap := c.Snapshot()
		if !snap.Enabled {
			t.Errorf("Enabled = false, want true (a Discoverer is set)")
		}
		if !snap.LastOK {
			t.Errorf("LastOK = false, want true (discovery succeeded)")
		}
		if snap.LastDiscovery != now {
			t.Errorf("LastDiscovery = %v, want %v", snap.LastDiscovery, now)
		}
		if snap.Static != 1 {
			t.Errorf("Static = %d, want 1", snap.Static)
		}
		if snap.Active != 2 {
			t.Errorf("Active = %d, want 2", snap.Active)
		}
		if len(snap.Targets) != 2 {
			t.Fatalf("len(Targets) = %d, want 2 (%+v)", len(snap.Targets), snap.Targets)
		}
		bySrc := sourceByURL(snap)
		if got := bySrc[srvStatic.URL]; got != "static" {
			t.Errorf("static target source = %q, want \"static\"", got)
		}
		if got := bySrc[srvDisc.URL]; got != "discovered" {
			t.Errorf("discovered target source = %q, want \"discovered\"", got)
		}
		// Instance passthrough.
		var staticInstance string
		for _, ti := range snap.Targets {
			if ti.URL == srvStatic.URL {
				staticInstance = ti.Instance
			}
		}
		if staticInstance != "node-static" {
			t.Errorf("static target Instance = %q, want node-static", staticInstance)
		}
	})

	t.Run("no discoverer", func(t *testing.T) {
		srv, _ := countingServer(t, "# TYPE g gauge\ng 1\n")
		defer srv.Close()

		c := nodemetrics.New(nodemetrics.Options{
			Targets: []nodemetrics.Target{
				{URL: srv.URL, Instance: "node-a"},
				{URL: srv.URL + "/x", Instance: "node-b"},
			},
		})

		snap := c.Snapshot()
		if snap.Enabled {
			t.Errorf("Enabled = true, want false (no Discoverer)")
		}
		if !snap.LastDiscovery.IsZero() {
			t.Errorf("LastDiscovery = %v, want zero (discovery never ran)", snap.LastDiscovery)
		}
		if snap.Static != 2 || snap.Active != 2 {
			t.Errorf("Static/Active = %d/%d, want 2/2", snap.Static, snap.Active)
		}
		if len(snap.Targets) != 2 {
			t.Fatalf("len(Targets) = %d, want 2 (%+v)", len(snap.Targets), snap.Targets)
		}
		for _, ti := range snap.Targets {
			if ti.Source != "static" {
				t.Errorf("target %q source = %q, want \"static\"", ti.URL, ti.Source)
			}
		}
	})
}

// TestScrape_DoesNotFollowRedirects is the SSRF-via-redirect guard (F-02): a
// scrape target that 302-redirects to another host must NOT be followed. The
// scraper must treat the redirect as a failed scrape (tailscale.node.up=0) and
// must NOT emit the redirect target's metrics — otherwise a compromised tailnet
// node could bounce the scraper at internal URLs (cloud metadata, loopback admin
// ports) and have their bodies re-emitted.
func TestScrape_DoesNotFollowRedirects(t *testing.T) {
	// Server B serves a valid Prometheus body that must never be scraped.
	bodyB := "# TYPE foo_total counter\nfoo_total 1\n"
	srvB := serveText(&bodyB)
	defer srvB.Close()

	// Server A 302-redirects every request to server B.
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, srvB.URL, http.StatusFound)
	}))
	defer srvA.Close()

	c := nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: srvA.URL, Instance: "node-a"}},
	})
	rec := telemetrytest.New()
	// All targets fail (the redirect is a non-2xx scrape), so Collect returns an
	// error — that is the desired outcome.
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect() error = nil, want a redirected scrape to fail the only target")
	}
	if pts := rec.MetricPoints("foo_total"); len(pts) != 0 {
		t.Fatalf("foo_total = %+v, want none (redirect must not be followed)", pts)
	}
	up := rec.MetricPoints("tailscale.node.up")
	if len(up) != 1 || up[0].Value != 0 {
		t.Fatalf("tailscale.node.up = %+v, want one down point (redirect = failed scrape)", up)
	}
}
