package flowlog_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// pointsDir returns the subset of pts carrying the given network.io.direction.
func pointsDir(pts []telemetrytest.MetricPoint, dir string) []telemetrytest.MetricPoint {
	var out []telemetrytest.MetricPoint
	for _, p := range pts {
		if p.Attrs[semconv.NetworkIODirection] == dir {
			out = append(out, p)
		}
	}
	return out
}

// sumPoints sums the values of every point in pts.
func sumPoints(pts []telemetrytest.MetricPoint) float64 {
	var s float64
	for _, p := range pts {
		s += p.Value
	}
	return s
}

// exitConns builds N exit ConnectionCounts from laptop (100.64.0.1) to distinct
// external IPs 203.0.113.<i+1>:<port>, with the given per-connection tx/rx bytes.
func exitConns(port string, txBytes, rxBytes []int64) []flowlog.ConnectionCounts {
	out := make([]flowlog.ConnectionCounts, len(txBytes))
	for i := range txBytes {
		out[i] = flowlog.ConnectionCounts{
			Proto:   protoTCP,
			Src:     "100.64.0.1:50000",
			Dst:     "203.0.113." + string(rune('1'+i)) + ":" + port,
			TxBytes: txBytes[i], RxBytes: rxBytes[i],
			TxPkts: txBytes[i] / 100, RxPkts: 1,
		}
	}
	return out
}

func rollupProc(t *testing.T, opts flowlog.Options) *flowlog.Processor {
	t.Helper()
	if opts.FlowMetricsMode == "" {
		opts.FlowMetricsMode = "rollup"
	}
	return flowlog.NewProcessor(cacheWith(t), opts)
}

// TestRollupModeEmitsRollupNotRaw: in rollup mode the raw io/packets families are
// absent; FlushRollup emits the bounded *.rollup families with nodes + dst.service
// and no L4 ports. The low-card flows counter still emits.
func TestRollupModeEmitsRollupNotRaw(t *testing.T) {
	rec := telemetrytest.New()
	p := rollupProc(t, flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})
	p.Process(httpsFlow(), rec.Emitter())

	if got := rec.MetricPoints(flowlog.MetricIO); len(got) != 0 {
		t.Fatalf("raw MetricIO points = %d, want 0 in rollup mode (%+v)", len(got), got)
	}
	if got := rec.MetricPoints(flowlog.MetricPackets); len(got) != 0 {
		t.Fatalf("raw MetricPackets points = %d, want 0 in rollup mode", len(got))
	}
	if got := rec.MetricPoints(flowlog.MetricFlows); len(got) != 1 {
		t.Fatalf("MetricFlows points = %d, want 1 (emits in all modes)", len(got))
	}
	// Nothing in the rollup family until a flush.
	if got := rec.MetricPoints(flowlog.MetricIORollup); len(got) != 0 {
		t.Fatalf("io.rollup points = %d before flush, want 0", len(got))
	}

	p.FlushRollup(rec.Emitter())
	tx := pointsDir(rec.MetricPoints(flowlog.MetricIORollup), semconv.DirectionTransmit)
	pt := findPoint(t, tx, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if pt.Value != 1000 {
		t.Fatalf("io.rollup transmit = %v, want 1000", pt.Value)
	}
	if pt.Unit != semconv.UnitBytes || pt.Kind != "sum" || !pt.Monotonic {
		t.Fatalf("io.rollup unit/kind/monotonic = %q/%q/%v, want By/sum/true", pt.Unit, pt.Kind, pt.Monotonic)
	}
	if pt.Attrs[semconv.NetworkTransport] != "tcp" {
		t.Fatalf("io.rollup transport = %q, want tcp", pt.Attrs[semconv.NetworkTransport])
	}
	if pt.Attrs[semconv.AttrTrafficType] != semconv.TrafficVirtual {
		t.Fatalf("io.rollup traffic_type = %q, want virtual", pt.Attrs[semconv.AttrTrafficType])
	}
	if pt.Attrs[semconv.AttrSrcNode] != "laptop" || pt.Attrs[semconv.AttrDstNode] != "server" {
		t.Fatalf("io.rollup nodes = %q->%q, want laptop->server", pt.Attrs[semconv.AttrSrcNode], pt.Attrs[semconv.AttrDstNode])
	}
	if pt.Attrs[semconv.AttrDstService] != "https" {
		t.Fatalf("io.rollup dst.service = %q, want https (dst port 443)", pt.Attrs[semconv.AttrDstService])
	}
	if _, ok := pt.Attrs[semconv.SourcePort]; ok {
		t.Fatalf("io.rollup must not carry source.port: %+v", pt.Attrs)
	}
	if _, ok := pt.Attrs[semconv.DestinationPort]; ok {
		t.Fatalf("io.rollup must not carry destination.port: %+v", pt.Attrs)
	}
}

// TestRollupModeAllNoRollup: in all mode FlushRollup is a no-op (nil accumulator).
func TestRollupModeAllNoRollup(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "all", NodeDims: true})
	p.Process(httpsFlow(), rec.Emitter())
	p.FlushRollup(rec.Emitter()) // must not panic and must emit nothing

	if got := rec.MetricPoints(flowlog.MetricIO); len(got) != 2 {
		t.Fatalf("raw MetricIO points = %d, want 2 in all mode", len(got))
	}
	if got := rec.MetricPoints(flowlog.MetricIORollup); len(got) != 0 {
		t.Fatalf("io.rollup points = %d in all mode, want 0", len(got))
	}
}

// TestRollupConservationWithOther: global top-N keeps the busiest pairs; the
// remainder folds into a per-(transport,traffic_type,dst_service) __other__
// series so totals are preserved in BOTH directions.
func TestRollupConservationWithOther(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "rollup", NodeDims: true, KeepExternalAddrs: true, RollupTopN: 2,
	})
	// Five exit flows, one group (tcp/exit, unmapped port 51820 -> no service).
	conns := exitConns("51820",
		[]int64{500, 400, 300, 200, 100},
		[]int64{50, 40, 30, 20, 10})
	p.Process(flowlog.FlowLog{NodeID: "nLaptop", ExitTraffic: conns}, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	tx := pointsDir(rec.MetricPoints(flowlog.MetricIORollup), semconv.DirectionTransmit)
	if len(tx) != 3 {
		t.Fatalf("io.rollup transmit points = %d, want 3 (2 kept + __other__): %+v", len(tx), tx)
	}
	if total := sumPoints(tx); total != 1500 {
		t.Fatalf("io.rollup transmit total = %v, want 1500 (conservation)", total)
	}
	other := findPoint(t, tx, map[string]string{semconv.AttrSrcNode: semconv.RollupOther})
	if other.Value != 600 {
		t.Fatalf("__other__ transmit = %v, want 600 (300+200+100)", other.Value)
	}
	if other.Attrs[semconv.AttrDstNode] != semconv.RollupOther {
		t.Fatalf("__other__ dst.node = %q, want %q", other.Attrs[semconv.AttrDstNode], semconv.RollupOther)
	}
	findPoint(t, tx, map[string]string{semconv.AttrDstNode: "203.0.113.1"}) // busiest kept
	findPoint(t, tx, map[string]string{semconv.AttrDstNode: "203.0.113.2"})

	rx := pointsDir(rec.MetricPoints(flowlog.MetricIORollup), semconv.DirectionReceive)
	if total := sumPoints(rx); total != 150 {
		t.Fatalf("io.rollup receive total = %v, want 150 (conservation)", total)
	}
	// Packets conserved too.
	ptx := pointsDir(rec.MetricPoints(flowlog.MetricPacketsRollup), semconv.DirectionTransmit)
	if total := sumPoints(ptx); total != 15 {
		t.Fatalf("packets.rollup transmit total = %v, want 15 (5+4+3+2+1)", total)
	}
}

// TestRollupMultiGroupPerServiceTotals: global top-N=1 keeps only the single
// busiest flow, yet every (transport,traffic_type,dst_service) group's total is
// preserved via its own __other__ series (a whole service squeezed out of top-N
// still reports its total).
func TestRollupMultiGroupPerServiceTotals(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "rollup", NodeDims: true, KeepExternalAddrs: true, RollupTopN: 1,
	})
	conns := []flowlog.ConnectionCounts{
		{Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "203.0.113.1:443", TxBytes: 500, TxPkts: 5},
		{Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "203.0.113.2:443", TxBytes: 100, TxPkts: 1},
		{Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "203.0.113.3:80", TxBytes: 400, TxPkts: 4},
		{Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "203.0.113.4:80", TxBytes: 50, TxPkts: 1},
	}
	p.Process(flowlog.FlowLog{NodeID: "nLaptop", ExitTraffic: conns}, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	tx := pointsDir(rec.MetricPoints(flowlog.MetricIORollup), semconv.DirectionTransmit)
	// kept https.1 (500); __other__ https (100); __other__ http (400+50=450).
	keptHTTPS := findPoint(t, tx, map[string]string{semconv.AttrDstNode: "203.0.113.1", semconv.AttrDstService: "https"})
	if keptHTTPS.Value != 500 {
		t.Fatalf("kept https flow = %v, want 500", keptHTTPS.Value)
	}
	otherHTTPS := findPoint(t, tx, map[string]string{semconv.AttrSrcNode: semconv.RollupOther, semconv.AttrDstService: "https"})
	if otherHTTPS.Value != 100 {
		t.Fatalf("__other__ https = %v, want 100", otherHTTPS.Value)
	}
	otherHTTP := findPoint(t, tx, map[string]string{semconv.AttrSrcNode: semconv.RollupOther, semconv.AttrDstService: "http"})
	if otherHTTP.Value != 450 {
		t.Fatalf("__other__ http = %v, want 450 (400+50)", otherHTTP.Value)
	}
	// Per-service totals: https 500+100=600, http 0+450=450.
	if total := sumPoints(tx); total != 1050 {
		t.Fatalf("io.rollup transmit total = %v, want 1050 (600 https + 450 http)", total)
	}
}

// TestRollupBothModeEmitsBothFamilies: both mode emits the raw families
// immediately AND the rollup families on flush — two distinct, each-correct
// families (summing the two would double-count, which is the operator's call).
func TestRollupBothModeEmitsBothFamilies(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "both", NodeDims: true, RollupTopN: 100})
	p.Process(httpsFlow(), rec.Emitter())
	p.FlushRollup(rec.Emitter())

	rawTx := pointsDir(rec.MetricPoints(flowlog.MetricIO), semconv.DirectionTransmit)
	rollTx := pointsDir(rec.MetricPoints(flowlog.MetricIORollup), semconv.DirectionTransmit)
	if sumPoints(rawTx) != 1000 {
		t.Fatalf("raw io transmit = %v, want 1000", sumPoints(rawTx))
	}
	if sumPoints(rollTx) != 1000 {
		t.Fatalf("rollup io transmit = %v, want 1000 (same total, distinct family)", sumPoints(rollTx))
	}
}

// TestRollupDedupFeedsNeither: a duplicate connection suppressed by the dedup set
// reaches neither family (the dedup gate precedes both raw emit and accumulation).
func TestRollupDedupFeedsNeither(t *testing.T) {
	rec := telemetrytest.New()
	d := dedup.New(1024)
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true, Dedup: d})
	p.Process(httpsFlow(), rec.Emitter())
	p.Process(httpsFlow(), rec.Emitter()) // identical -> deduped
	p.FlushRollup(rec.Emitter())

	tx := pointsDir(rec.MetricPoints(flowlog.MetricIORollup), semconv.DirectionTransmit)
	if total := sumPoints(tx); total != 1000 {
		t.Fatalf("io.rollup transmit total = %v, want 1000 (second connection deduped)", total)
	}
}

// TestRollupUniqueCounts: distinct destination peers and ports per source node
// are reported as gauges on flush.
func TestRollupUniqueCounts(t *testing.T) {
	rec := telemetrytest.New()
	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:5", Dst: "100.64.0.2:443", TxBytes: 10, RxBytes: 5},
		},
		ExitTraffic: []flowlog.ConnectionCounts{
			{Proto: protoUDP, Src: "100.64.0.1:6", Dst: "8.8.8.8:53", TxBytes: 7, RxBytes: 9},
		},
	}
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})
	p.Process(flow, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	peers := findPoint(t, rec.MetricPoints(flowlog.MetricUniqueDstPeers), map[string]string{semconv.AttrSrcNode: "laptop"})
	if peers.Value != 2 {
		t.Fatalf("unique dst_peers = %v, want 2 (server, external)", peers.Value)
	}
	if peers.Kind != "gauge" {
		t.Fatalf("unique dst_peers kind = %q, want gauge", peers.Kind)
	}
	ports := findPoint(t, rec.MetricPoints(flowlog.MetricUniqueDstPorts), map[string]string{semconv.AttrSrcNode: "laptop"})
	if ports.Value != 2 {
		t.Fatalf("unique dst_ports = %v, want 2 (443, 53)", ports.Value)
	}
}

// TestRollupResetBetweenFlushes: the accumulator resets each flush, so a flush
// with no intervening connections emits nothing.
func TestRollupResetBetweenFlushes(t *testing.T) {
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})

	rec1 := telemetrytest.New()
	p.Process(httpsFlow(), rec1.Emitter())
	p.FlushRollup(rec1.Emitter())
	if len(rec1.MetricPoints(flowlog.MetricIORollup)) == 0 {
		t.Fatal("first flush emitted no io.rollup points")
	}

	rec2 := telemetrytest.New()
	p.FlushRollup(rec2.Emitter()) // nothing processed since the reset
	if got := rec2.MetricPoints(flowlog.MetricIORollup); len(got) != 0 {
		t.Fatalf("second flush emitted %d io.rollup points, want 0 (reset)", len(got))
	}
}

// TestRollupNodeDimsFalse: with node dims off the rollup carries no src/dst node,
// and the per-source-node unique gauges are suppressed (they would reintroduce
// the node cardinality the operator turned off).
func TestRollupNodeDimsFalse(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "rollup", NodeDims: false})
	p.Process(httpsFlow(), rec.Emitter())
	p.FlushRollup(rec.Emitter())

	tx := pointsDir(rec.MetricPoints(flowlog.MetricIORollup), semconv.DirectionTransmit)
	if len(tx) == 0 {
		t.Fatal("io.rollup emitted no transmit points with node dims off")
	}
	for _, pt := range tx {
		if _, ok := pt.Attrs[semconv.AttrSrcNode]; ok {
			t.Fatalf("io.rollup carries src.node with NodeDims=false: %+v", pt.Attrs)
		}
		if _, ok := pt.Attrs[semconv.AttrDstNode]; ok {
			t.Fatalf("io.rollup carries dst.node with NodeDims=false: %+v", pt.Attrs)
		}
		if pt.Attrs[semconv.AttrDstService] != "https" {
			t.Fatalf("io.rollup dst.service = %q, want https (kept even with nodes off)", pt.Attrs[semconv.AttrDstService])
		}
	}
	if got := rec.MetricPoints(flowlog.MetricUniqueDstPeers); len(got) != 0 {
		t.Fatalf("unique dst_peers emitted %d points with node dims off, want 0", len(got))
	}
}

// TestRollupSkipsZeroDirection: a one-directional flow emits only the non-zero
// direction (no phantom zero-value series).
func TestRollupSkipsZeroDirection(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})
	p.Process(flowlog.FlowLog{
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:5", Dst: "100.64.0.2:443", TxBytes: 100, RxBytes: 0, TxPkts: 1, RxPkts: 0},
		},
	}, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	io := rec.MetricPoints(flowlog.MetricIORollup)
	if len(pointsDir(io, semconv.DirectionTransmit)) != 1 {
		t.Fatalf("io.rollup transmit points = %d, want 1", len(pointsDir(io, semconv.DirectionTransmit)))
	}
	if got := len(pointsDir(io, semconv.DirectionReceive)); got != 0 {
		t.Fatalf("io.rollup receive points = %d, want 0 (zero skipped)", got)
	}
}
