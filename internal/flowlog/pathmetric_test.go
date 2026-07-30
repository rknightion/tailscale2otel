package flowlog_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// The underlay path is the one dimension that answers "how much of this tailnet
// is actually relayed", and until now it existed only in the in-memory /flows
// view — bounded by flows.retention and lost on restart. These tests pin it onto
// the metric surface, in BOTH modes, using the same tailscale.path vocabulary the
// node-metrics collector already emits so one query spans both families.

// TestRawMetricsCarryPath: the raw families carry the folded path vocabulary, and
// a relayed connection additionally carries the numeric DERP region.
func TestRawMetricsCarryPath(t *testing.T) {
	tests := []struct {
		name       string
		dst        string
		wantPath   string
		wantRegion string
	}{
		{"relayed", "127.3.3.40:8", semconv.PathDERP, "8"},
		{"direct ipv4", "81.187.237.31:41641", semconv.PathDirect, ""},
		{"direct ipv6", "[2001:8b0:1f05::100e]:41641", semconv.PathDirect, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "all", NodeDims: true})
			p.Process(physicalRecord(tc.dst), rec.Emitter())

			pts := rec.MetricPoints(flowlog.MetricIO)
			if len(pts) == 0 {
				t.Fatalf("no raw io points emitted")
			}
			if got := pts[0].Attrs[semconv.AttrPath]; got != tc.wantPath {
				t.Errorf("raw io tailscale.path = %q, want %q", got, tc.wantPath)
			}
			got, ok := pts[0].Attrs[semconv.AttrDERPRegionID]
			if tc.wantRegion == "" {
				// An absent region is the honest encoding for a direct path: the
				// attribute is omitted entirely rather than carrying a sentinel.
				if ok {
					t.Errorf("direct path must not carry %s, got %q", semconv.AttrDERPRegionID, got)
				}
				return
			}
			if got != tc.wantRegion {
				t.Errorf("raw io %s = %q, want %q", semconv.AttrDERPRegionID, got, tc.wantRegion)
			}
		})
	}
}

// TestRollupCarriesPath: the same two dimensions survive the rollup accumulator,
// which is the whole point — rollup is the product default, so a dimension only
// present on the raw families is invisible to most deployments.
func TestRollupCarriesPath(t *testing.T) {
	rec := telemetrytest.New()
	p := rollupProc(t, flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})
	p.Process(physicalRecord("127.3.3.40:27"), rec.Emitter())
	p.FlushRollup(rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIORollup)
	if len(pts) == 0 {
		t.Fatalf("no rollup io points emitted")
	}
	if got := pts[0].Attrs[semconv.AttrPath]; got != semconv.PathDERP {
		t.Errorf("io.rollup tailscale.path = %q, want %q", got, semconv.PathDERP)
	}
	if got := pts[0].Attrs[semconv.AttrDERPRegionID]; got != "27" {
		t.Errorf("io.rollup %s = %q, want 27", semconv.AttrDERPRegionID, got)
	}
}

// TestOverlayTrafficCarriesNoPath: only physicalTraffic describes HOW two nodes
// reached each other. The overlay types describe what the tailnet carried, so
// giving them a path would fold "we cannot tell" into the direct column an
// operator reads as good news.
func TestOverlayTrafficCarriesNoPath(t *testing.T) {
	for _, mode := range []string{"all", "rollup"} {
		t.Run(mode, func(t *testing.T) {
			rec := telemetrytest.New()
			p := rollupProc(t, flowlog.Options{FlowMetricsMode: mode, NodeDims: true})
			p.Process(httpsFlow(), rec.Emitter())
			p.FlushRollup(rec.Emitter())

			name := flowlog.MetricIO
			if mode == "rollup" {
				name = flowlog.MetricIORollup
			}
			pts := rec.MetricPoints(name)
			if len(pts) == 0 {
				t.Fatalf("no %s points emitted", name)
			}
			for _, pt := range pts {
				if v, ok := pt.Attrs[semconv.AttrPath]; ok {
					t.Errorf("virtual traffic must carry no %s, got %q", semconv.AttrPath, v)
				}
				if v, ok := pt.Attrs[semconv.AttrDERPRegionID]; ok {
					t.Errorf("virtual traffic must carry no %s, got %q", semconv.AttrDERPRegionID, v)
				}
			}
		})
	}
}
