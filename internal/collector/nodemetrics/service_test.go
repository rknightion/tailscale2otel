package nodemetrics_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	lognoop "go.opentelemetry.io/otel/log/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const serviceIO = "tailscale.node.service.io"

func serviceFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/serve.prom")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func serviceAttrs(node, service, direction string) map[string]string {
	return map[string]string{"tailscale.node": node, "tailscale.service.name": service, "network.io.direction": direction}
}

// Drive the real HTTP collector and SDK recorder over three scrapes, including
// a reset. Both raw and curated counters must consume exactly the same delta.
func TestServiceIO_CapturedShapeSuccessiveScrapesAndReset(t *testing.T) {
	body := serviceFixture(t)
	srv := serveText(&body)
	defer srv.Close()
	c := nodemetrics.New(nodemetrics.Options{Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}}})
	for tick := 0; tick < 3; tick++ {
		rec := telemetrytest.New()
		if tick == 1 {
			body = strings.NewReplacer(" 100\n", " 110\n", " 200\n", " 220\n", " 300\n", " 330\n", " 400\n", " 440\n").Replace(body)
		}
		if tick == 2 {
			body = strings.NewReplacer(" 110\n", " 1\n", " 220\n", " 2\n", " 330\n", " 3\n", " 440\n", " 4\n").Replace(body)
		}
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		if tick == 0 {
			for _, name := range []string{serviceIO, "tailscaled_serve_inbound_bytes_total", "tailscaled_serve_outbound_bytes_total"} {
				if pts := rec.MetricPoints(name); len(pts) != 0 {
					t.Fatalf("baseline emitted %s: %+v", name, pts)
				}
			}
			continue
		}
		if pts := rec.MetricPoints(serviceIO); len(pts) != 4 {
			t.Fatalf("tick %d curated series=%d, want 4", tick, len(pts))
		}
		scale := 10.0
		if tick == 2 {
			scale = 1
		}
		for i, tc := range []struct{ direction, family, service string }{
			{"receive", "tailscaled_serve_inbound_bytes_total", "svc:sample-api"},
			{"receive", "tailscaled_serve_inbound_bytes_total", "svc:sample-web"},
			{"transmit", "tailscaled_serve_outbound_bytes_total", "svc:sample-api"},
			{"transmit", "tailscaled_serve_outbound_bytes_total", "svc:sample-web"},
		} {
			want := float64(i+1) * scale
			wantCounter(t, rec, serviceIO, serviceAttrs("node-a", tc.service, tc.direction), want)
			wantCounter(t, rec, tc.family, map[string]string{"tailscale.node": "node-a", "service": tc.service}, want)
			raw, _ := pointByAttr(rec.MetricPoints(tc.family), map[string]string{"service": tc.service})
			if raw.Unit != "" || len(raw.Attrs) != 2 {
				t.Errorf("raw forwarding changed: %+v", raw)
			}
		}
		assertServiceCatalogAndPanel(t, rec)
	}
}

// Label folding must occur AFTER per-source delta accounting. Hostile labels
// may distinguish raw baselines, but never add a curated dimension or duplicate
// an emitted attribute set. Passthrough filters still affect only raw samples.
func TestServiceIO_UnsafeShapesFoldAndFiltersStayRawOnly(t *testing.T) {
	shapes := []string{"", `service=""`, `service="sample-api"`, `service="svc:"`, `service="svc:203.0.113.7"`, `service="https://example.invalid/path"`, `service="svc:two words"`, `service="svc:-bad"`, `service="svc:bad-"`, `service="svc:UPPER"`, `service="svc:` + strings.Repeat("a", 64) + `"`}
	for _, opts := range []nodemetrics.Options{
		{}, {MetricDeny: []string{"tailscaled_serve_.*"}}, {MetricAllow: []string{"unrelated"}},
		{DropLabels: []string{"service", "extra", "tailscale.service.name", "network.io.direction", "tailscale.node"}},
	} {
		t.Run(fmt.Sprintf("%+v", opts), func(t *testing.T) {
			makeBody := func(tick int) string {
				var b strings.Builder
				for _, direction := range []string{"inbound", "outbound"} {
					fmt.Fprintf(&b, "# TYPE tailscaled_serve_%s_bytes_total counter\n", direction)
					for i, shape := range shapes {
						if shape != "" {
							shape += ","
						}
						fmt.Fprintf(&b, "tailscaled_serve_%s_bytes_total{%sextra=\"203.0.113.9\",tailscale_service_name=\"https://example.invalid\",tailscale_node=\"spoof\",network_io_direction=\"arbitrary\",source=\"%d\"} %d\n", direction, shape, i, 100+i*tick+tick)
					}
				}
				return b.String()
			}
			rec := scrapeTwice(t, opts, makeBody(0), makeBody(1))
			want := float64(len(shapes) * (len(shapes) + 1) / 2)
			pts := rec.MetricPoints(serviceIO)
			if len(pts) != 2 {
				t.Fatalf("folded curated points=%d, want 2: %+v", len(pts), pts)
			}
			for _, direction := range []string{"receive", "transmit"} {
				wantCounter(t, rec, serviceIO, serviceAttrs("node-a", "__other__", direction), want)
			}
			for _, p := range pts {
				if len(p.Attrs) != 3 {
					t.Errorf("unexpected curated dimensions: %+v", p)
				}
			}
			for _, family := range []string{"tailscaled_serve_inbound_bytes_total", "tailscaled_serve_outbound_bytes_total"} {
				raw := rec.MetricPoints(family)
				denied := len(opts.MetricDeny) > 0 || len(opts.MetricAllow) > 0
				if denied {
					if len(raw) != 0 {
						t.Errorf("denied raw emitted: %+v", raw)
					}
					continue
				}
				if len(raw) != len(shapes) {
					t.Fatalf("raw series=%d, want %d", len(raw), len(shapes))
				}
				var total float64
				for _, p := range raw {
					total += p.Value
					if p.Attrs["tailscale.node"] != "node-a" {
						t.Errorf("spoofed node: %+v", p)
					}
					if len(opts.DropLabels) > 0 {
						if _, ok := p.Attrs["service"]; ok {
							t.Error("raw service not dropped")
						}
					}
				}
				if total != want {
					t.Errorf("raw total=%v, curated total=%v", total, want)
				}
			}
		})
	}
}

func TestServiceIO_PIIIdentitiesMergeWithoutDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		cats                        pii.Categories
		node                        string
		servicePresent, nodePresent bool
	}{
		{"on", nil, "node-a", true, true},
		{"service off", pii.Categories{pii.CatServiceAddrs: false}, "node-a", false, true},
		{"hostname off", pii.Categories{pii.CatHostnames: false}, "node-a", true, false},
		{"IP off", pii.Categories{pii.CatTailscaleIPs: false}, "100.64.0.1:5252", true, false},
		{"both off", pii.Categories{pii.CatServiceAddrs: false, pii.CatHostnames: false}, "node-a", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := serviceFixture(t)
			srv := serveText(&body)
			defer srv.Close()
			secondNode := "node-b"
			if tc.name == "IP off" {
				secondNode = "100.64.0.2:5252" // same PII class on both targets
			}
			c := nodemetrics.New(nodemetrics.Options{Targets: []nodemetrics.Target{
				{URL: srv.URL, Instance: tc.node, Labels: map[string]string{"service": "svc:spoof", "tailscale_service_name": "svc:spoof"}},
				{URL: srv.URL, Instance: secondNode},
			}})
			rec := telemetrytest.NewWithPII(tc.cats)
			if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
				t.Fatal(err)
			}
			body = strings.NewReplacer(" 100\n", " 110\n", " 200\n", " 220\n", " 300\n", " 330\n", " 400\n", " 440\n").Replace(body)
			if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
				t.Fatal(err)
			}
			pts := rec.MetricPoints(serviceIO)
			wantPoints := 2
			if tc.servicePresent {
				wantPoints *= 2
			}
			if tc.nodePresent {
				wantPoints *= 2
			}
			if len(pts) != wantPoints {
				t.Fatalf("points=%d, want %d: %+v", len(pts), wantPoints, pts)
			}
			var total float64
			for _, p := range pts {
				_, svc := p.Attrs["tailscale.service.name"]
				_, node := p.Attrs["tailscale.node"]
				if svc != tc.servicePresent || node != tc.nodePresent {
					t.Errorf("PII attrs: %+v", p)
				}
				if len(p.Attrs) != 1+boolInt(svc)+boolInt(node) {
					t.Errorf("extra attrs: %+v", p)
				}
				total += p.Value
			}
			if total != 200 {
				t.Errorf("PII merged bytes=%v, want 200", total)
			}
		})
	}
}

// The collector uses the ordinary Emitter counter path, so the existing SDK
// per-instrument budget must also contain an adversarial set of valid names.
func TestServiceIO_ExistingSDKCardinalityLimit(t *testing.T) {
	body := serviceFixture(t)
	srv := serveText(&body)
	defer srv.Close()
	c := nodemetrics.New(nodemetrics.Options{Targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}}})
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(3))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	e := telemetry.NewEmitter(mp.Meter("test"), lognoop.NewLoggerProvider().Logger("test"))
	if err := c.Collect(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	body = strings.NewReplacer(" 100\n", " 110\n", " 200\n", " 220\n", " 300\n", " 330\n", " 400\n", " 440\n").Replace(body)
	if err := c.Collect(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != serviceIO {
				continue
			}
			found = true
			sum, ok := m.Data.(metricdata.Sum[float64])
			if !ok {
				t.Fatalf("Service IO type=%T, want sum", m.Data)
			}
			if len(sum.DataPoints) != 3 {
				t.Fatalf("points=%d, want SDK limit 3", len(sum.DataPoints))
			}
			var total float64
			overflow := false
			for _, p := range sum.DataPoints {
				total += p.Value
				if v, ok := p.Attributes.Value("otel.metric.overflow"); ok && v.AsBool() {
					overflow = true
				}
			}
			if !overflow || total != 100 {
				t.Errorf("overflow=%v, total=%v, want true/100", overflow, total)
			}
		}
	}
	if !found {
		t.Fatal("Service IO not emitted")
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Connect the metric actually emitted by the collector to the generated panel,
// through the catalog's real OTLP-to-Prometheus translator, not a guessed name.
func assertServiceCatalogAndPanel(t *testing.T, rec *telemetrytest.Recorder) {
	t.Helper()
	var doc metricdoc.Metric
	for _, m := range nodemetrics.Catalog() {
		if m.Name == serviceIO {
			doc = m
		}
	}
	if doc.Name == "" {
		t.Fatal("emitted Service IO absent from catalog")
	}
	telemetrytest.AssertCatalogAttrs(t, rec, nodemetrics.Catalog(), nodemetrics.LogCatalog())
	for _, p := range rec.MetricPoints(serviceIO) {
		if p.Unit != doc.Unit || p.Unit != "By" || p.Description != doc.Description || doc.Instrument != metricdoc.Counter {
			t.Errorf("catalog mismatch: %+v %+v", p, doc)
		}
	}
	b, err := os.ReadFile("../../../deploy/grafana/tailscale2otel-tailnet.json")
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Spec struct {
			Elements map[string]struct {
				Spec struct {
					Title string
					Data  struct {
						Spec struct {
							Queries []struct {
								Spec struct {
									Query struct{ Spec struct{ Expr string } }
								}
							}
						}
					}
				}
			}
		}
	}
	if err := json.Unmarshal(b, &dashboard); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, el := range dashboard.Spec.Elements {
		if el.Spec.Title != "Service throughput by node" {
			continue
		}
		found++
		if len(el.Spec.Data.Spec.Queries) != 1 {
			t.Fatal("expected one Service IO query")
		}
		expr := el.Spec.Data.Spec.Queries[0].Spec.Query.Spec.Expr
		// Dashboard deployment controls must scope the emitted metric before aggregation.
		want := "sum by (tailscale_node, tailscale_service_name, network_io_direction) (rate(" + doc.PromName() + `{tailscale_tailnet=~"$tailnet", tailscale2otel_provider=~"$provider"}[$__rate_interval]))`
		if expr != want {
			t.Errorf("panel=%q, emitted metric contract=%q", expr, want)
		}
	}
	if found != 1 {
		t.Fatalf("Service IO panels=%d, want 1", found)
	}
}
