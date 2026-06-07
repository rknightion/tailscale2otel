package flowlog_test

import (
	"net/netip"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// seedNode populates the device cache with a node whose hostname is known, so
// exitNodeLabel resolves it from the NodeID.
func seedNode(t *testing.T, cache *enrich.DeviceCache, nodeID, hostname string) {
	t.Helper()
	cache.Replace([]enrich.DeviceMeta{
		{NodeID: nodeID, Hostname: hostname, Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
	})
}

// counterValue sums all MetricPoints for the named metric whose attrs match
// every key/value in want. Returns the total value and whether any points
// matched.
func counterValue(t *testing.T, rec *telemetrytest.Recorder, name string, want map[string]string) (float64, bool) {
	t.Helper()
	pts := rec.MetricPoints(name)
	var total float64
	var found bool
outer:
	for _, p := range pts {
		for k, v := range want {
			if p.Attrs[k] != v {
				continue outer
			}
		}
		total += p.Value
		found = true
	}
	return total, found
}

func TestProcess_ExitNodeAttribution(t *testing.T) {
	cache := enrich.NewDeviceCache()
	seedNode(t, cache, "n1", "relay")

	p := flowlog.NewProcessor(cache, flowlog.Options{ExitNodeAttribution: true, FlowMetricsMode: "rollup"})
	rec := telemetrytest.New()

	flow := flowlog.FlowLog{NodeID: "n1", ExitTraffic: []flowlog.ConnectionCounts{
		{Src: "100.64.0.1:1234", Dst: "1.1.1.1:443", Proto: 6, TxBytes: 100, RxBytes: 900, TxPkts: 1, RxPkts: 2},
	}}
	p.Process(flow, rec.Emitter())

	// Transmit bytes: TxBytes = 100.
	txVal, txFound := counterValue(t, rec, flowlog.MetricExitNodeIO, map[string]string{
		semconv.AttrExitNode:       "relay",
		semconv.NetworkIODirection: semconv.DirectionTransmit,
	})
	if !txFound {
		t.Fatal("MetricExitNodeIO transmit point missing")
	}
	if txVal != 100 {
		t.Errorf("MetricExitNodeIO transmit = %v, want 100", txVal)
	}

	// Receive bytes: RxBytes = 900.
	rxVal, rxFound := counterValue(t, rec, flowlog.MetricExitNodeIO, map[string]string{
		semconv.AttrExitNode:       "relay",
		semconv.NetworkIODirection: semconv.DirectionReceive,
	})
	if !rxFound {
		t.Fatal("MetricExitNodeIO receive point missing")
	}
	if rxVal != 900 {
		t.Errorf("MetricExitNodeIO receive = %v, want 900", rxVal)
	}

	// Receive packets: RxPkts = 2.
	rxPkts, rxPktsFound := counterValue(t, rec, flowlog.MetricExitNodePackets, map[string]string{
		semconv.AttrExitNode:       "relay",
		semconv.NetworkIODirection: semconv.DirectionReceive,
	})
	if !rxPktsFound {
		t.Fatal("MetricExitNodePackets receive point missing")
	}
	if rxPkts != 2 {
		t.Errorf("MetricExitNodePackets receive = %v, want 2", rxPkts)
	}
}

func TestProcess_ExitNodeAttribution_OnlyExitTraffic(t *testing.T) {
	// Virtual traffic (non-exit) must NOT produce exit-node attribution metrics.
	p := flowlog.NewProcessor(nil, flowlog.Options{ExitNodeAttribution: true})
	rec := telemetrytest.New()

	flow := flowlog.FlowLog{NodeID: "n9", VirtualTraffic: []flowlog.ConnectionCounts{
		{Src: "100.64.0.1:1", Dst: "100.64.0.2:2", Proto: 6, TxBytes: 5},
	}}
	p.Process(flow, rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricExitNodeIO)
	if len(pts) != 0 {
		t.Errorf("MetricExitNodeIO emitted %d points for virtual traffic, want 0 (%+v)", len(pts), pts)
	}
}

func TestProcess_ExitNodeAttribution_GatedOff(t *testing.T) {
	// ExitNodeAttribution=false must suppress the exit-node metrics even for exit traffic.
	p := flowlog.NewProcessor(nil, flowlog.Options{ExitNodeAttribution: false})
	rec := telemetrytest.New()

	flow := flowlog.FlowLog{NodeID: "n1", ExitTraffic: []flowlog.ConnectionCounts{
		{Src: "a:1", Dst: "b:2", Proto: 6, TxBytes: 5},
	}}
	p.Process(flow, rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricExitNodeIO)
	if len(pts) != 0 {
		t.Errorf("MetricExitNodeIO emitted %d points with ExitNodeAttribution=false, want 0", len(pts))
	}
}

func TestProcess_ExitNodeAttribution_NilCacheFallsBackToNodeID(t *testing.T) {
	// Nil cache: exitNodeLabel falls back to nodeID.
	p := flowlog.NewProcessor(nil, flowlog.Options{ExitNodeAttribution: true})
	rec := telemetrytest.New()

	flow := flowlog.FlowLog{NodeID: "n42", ExitTraffic: []flowlog.ConnectionCounts{
		{Src: "1.1.1.1:1", Dst: "2.2.2.2:443", Proto: 6, TxBytes: 50},
	}}
	p.Process(flow, rec.Emitter())

	_, found := counterValue(t, rec, flowlog.MetricExitNodeIO, map[string]string{
		semconv.AttrExitNode:       "n42",
		semconv.NetworkIODirection: semconv.DirectionTransmit,
	})
	if !found {
		t.Error("MetricExitNodeIO transmit with nodeID fallback label missing")
	}
}

func TestProcess_ExitNodeAttribution_EmptyNodeIDFallsBackToUnknown(t *testing.T) {
	// Empty nodeID and nil cache: exitNodeLabel returns "unknown".
	p := flowlog.NewProcessor(nil, flowlog.Options{ExitNodeAttribution: true})
	rec := telemetrytest.New()

	flow := flowlog.FlowLog{NodeID: "", ExitTraffic: []flowlog.ConnectionCounts{
		{Src: "1.1.1.1:1", Dst: "2.2.2.2:443", Proto: 6, TxBytes: 10},
	}}
	p.Process(flow, rec.Emitter())

	_, found := counterValue(t, rec, flowlog.MetricExitNodeIO, map[string]string{
		semconv.AttrExitNode:       "unknown",
		semconv.NetworkIODirection: semconv.DirectionTransmit,
	})
	if !found {
		t.Error("MetricExitNodeIO transmit with 'unknown' fallback label missing")
	}
}

func TestProcess_ExitNodeAttribution_CacheMissFallsBackToNodeID(t *testing.T) {
	// Cache miss (nodeID not in cache): exitNodeLabel falls back to nodeID.
	cache := enrich.NewDeviceCache()
	seedNode(t, cache, "other-node", "other")

	p := flowlog.NewProcessor(cache, flowlog.Options{ExitNodeAttribution: true})
	rec := telemetrytest.New()

	flow := flowlog.FlowLog{NodeID: "n99", ExitTraffic: []flowlog.ConnectionCounts{
		{Src: "1.1.1.1:1", Dst: "2.2.2.2:80", Proto: 6, TxBytes: 20},
	}}
	p.Process(flow, rec.Emitter())

	_, found := counterValue(t, rec, flowlog.MetricExitNodeIO, map[string]string{
		semconv.AttrExitNode:       "n99",
		semconv.NetworkIODirection: semconv.DirectionTransmit,
	})
	if !found {
		t.Error("MetricExitNodeIO transmit with nodeID-fallback on cache miss missing")
	}
}
