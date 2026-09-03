package flowlog_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// The two shapes real exit traffic actually takes. In a live 3h capture all 504
// exitTraffic entries carried NO dst; 270 carried a src (the exit node's own
// tailnet address, always port 0) and the remaining 234 carried neither. No
// entry carried proto.
//
// The pre-existing exitnode_test fixtures used a fully-populated 5-tuple, which
// no real exit entry has ever had — which is why the fabricated destination
// below went unnoticed.
func exitTrafficShapes() []flowlog.ConnectionCounts {
	return []flowlog.ConnectionCounts{
		{Src: "100.64.0.1:0", TxPkts: 5, TxBytes: 320},
		{TxPkts: 2, TxBytes: 128},
	}
}

// An absent destination is structurally absent, not a cache miss. Emitting
// dst_node="unknown" claims we looked and failed, when there was never anything
// to look up.
func TestProcess_ExitTrafficOmitsAbsentDestination(t *testing.T) {
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "all",
		NodeDims:        true,
	})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID:      "nLaptop",
		ExitTraffic: exitTrafficShapes(),
	}, rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatal("no io metric points emitted for exit traffic")
	}
	for _, pt := range pts {
		if got, ok := pt.Attrs[semconv.AttrDstNode]; ok {
			t.Errorf("%s = %q present on exit traffic that carries no destination", semconv.AttrDstNode, got)
		}
	}

	for _, r := range rec.LogRecords() {
		for _, k := range []string{semconv.AttrDstNode, semconv.DestinationAddress, semconv.DestinationPort} {
			if got, ok := r.Attrs[k]; ok {
				t.Errorf("log attribute %s = %q present on exit traffic that carries no destination", k, got)
			}
		}
	}
}

// The source is present on roughly half of real exit entries. Where it IS
// present it must still resolve and be emitted — the fix must not blanket-drop
// both endpoints.
func TestProcess_ExitTrafficKeepsPresentSource(t *testing.T) {
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "all",
		NodeDims:        true,
	})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID: "nLaptop",
		ExitTraffic: []flowlog.ConnectionCounts{
			{Src: "100.64.0.1:0", TxPkts: 5, TxBytes: 320},
		},
	}, rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatal("no io metric points emitted")
	}
	if got := pts[0].Attrs[semconv.AttrSrcNode]; got != "laptop" {
		t.Errorf("%s = %q, want %q", semconv.AttrSrcNode, got, "laptop")
	}
}

// Symmetry: an entry with neither endpoint must emit neither, rather than two
// fabricated "unknown" labels.
func TestProcess_OmitsAbsentSourceToo(t *testing.T) {
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "all",
		NodeDims:        true,
	})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID: "nLaptop",
		ExitTraffic: []flowlog.ConnectionCounts{
			{TxPkts: 2, TxBytes: 128},
		},
	}, rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatal("no io metric points emitted")
	}
	for _, k := range []string{semconv.AttrSrcNode, semconv.AttrDstNode} {
		if got, ok := pts[0].Attrs[k]; ok {
			t.Errorf("%s = %q present on an entry carrying neither endpoint", k, got)
		}
	}
}

// A destination that does not exist is not a distinct peer. Counting it inflates
// the unique-peer gauge by exactly one phantom peer per source node.
func TestProcess_ExitTrafficDoesNotCountPhantomPeer(t *testing.T) {
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "rollup",
		NodeDims:        true,
	})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID:      "nLaptop",
		ExitTraffic: exitTrafficShapes(),
	}, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	for _, pt := range rec.MetricPoints(flowlog.MetricUniqueDstPeers) {
		if pt.Value != 0 {
			t.Errorf("unique dst peers = %v for exit traffic with no destinations, want 0 (attrs %v)",
				pt.Value, pt.Attrs)
		}
	}
}

// The exit-node attribution counters are the intended way to measure exit
// traffic and must keep working on the real record shape.
func TestProcess_ExitNodeAttributionOnRealShape(t *testing.T) {
	cache := enrich.NewDeviceCache()
	seedNode(t, cache, "nRelay", "relay")

	p := flowlog.NewProcessor(cache, flowlog.Options{
		ExitNodeAttribution: true,
		FlowMetricsMode:     "rollup",
	})
	rec := telemetrytest.New()

	p.Process(flowlog.FlowLog{
		NodeID:      "nRelay",
		ExitTraffic: exitTrafficShapes(),
	}, rec.Emitter())

	got, found := counterValue(t, rec, flowlog.MetricExitNodeIO, map[string]string{
		semconv.AttrExitNode:       "relay",
		semconv.NetworkIODirection: semconv.DirectionTransmit,
	})
	if !found {
		t.Fatal("no exit-node io counter emitted for real-shape exit traffic")
	}
	if want := float64(320 + 128); got != want {
		t.Errorf("exit-node tx bytes = %v, want %v", got, want)
	}
}
