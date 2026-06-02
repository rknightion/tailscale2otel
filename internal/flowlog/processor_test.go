package flowlog_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// cacheWith returns a DeviceCache populated with the two tailnet devices used in
// the sample flow logs (100.64.0.1 -> "laptop", 100.64.0.2 -> "server").
func cacheWith(t *testing.T) *enrich.DeviceCache {
	t.Helper()
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{
		{NodeID: "nLaptop", Hostname: "laptop", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
		{NodeID: "nServer", Hostname: "server", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.2")}},
	})
	return c
}

// findPoint returns the first MetricPoint whose attrs match every key/value in
// want, or fails the test.
func findPoint(t *testing.T, pts []telemetrytest.MetricPoint, want map[string]string) telemetrytest.MetricPoint {
	t.Helper()
outer:
	for _, p := range pts {
		for k, v := range want {
			if p.Attrs[k] != v {
				continue outer
			}
		}
		return p
	}
	t.Fatalf("no metric point matching %v in %+v", want, pts)
	return telemetrytest.MetricPoint{}
}

func virtualTCPFlow() flowlog.FlowLog {
	return flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		Start:  time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		End:    time.Date(2024, 6, 6, 15, 26, 26, 0, time.UTC),
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: "TCP", Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxPkts: 10, TxBytes: 1000, RxPkts: 8, RxBytes: 800},
		},
	}
}

func TestProcessVirtualTCPMetrics(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true})
	p.Process(virtualTCPFlow(), rec.Emitter())

	// MetricIO: transmit + receive points.
	io := rec.MetricPoints(flowlog.MetricIO)
	if len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 (%+v)", len(io), io)
	}
	tx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Value != 1000 {
		t.Fatalf("io transmit value = %v, want 1000", tx.Value)
	}
	if tx.Unit != semconv.UnitBytes {
		t.Fatalf("io unit = %q, want %q", tx.Unit, semconv.UnitBytes)
	}
	if tx.Kind != "sum" || !tx.Monotonic {
		t.Fatalf("io transmit kind/monotonic = %q/%v, want sum/true", tx.Kind, tx.Monotonic)
	}
	if tx.Attrs[semconv.NetworkTransport] != "tcp" {
		t.Fatalf("io transport = %q, want tcp", tx.Attrs[semconv.NetworkTransport])
	}
	if tx.Attrs[semconv.AttrTrafficType] != semconv.TrafficVirtual {
		t.Fatalf("io traffic_type = %q, want %q", tx.Attrs[semconv.AttrTrafficType], semconv.TrafficVirtual)
	}
	if tx.Attrs[semconv.AttrSrcNode] != "laptop" {
		t.Fatalf("io src.node = %q, want laptop", tx.Attrs[semconv.AttrSrcNode])
	}
	if tx.Attrs[semconv.AttrDstNode] != "server" {
		t.Fatalf("io dst.node = %q, want server", tx.Attrs[semconv.AttrDstNode])
	}
	// Ports default off.
	if _, ok := tx.Attrs[semconv.SourcePort]; ok {
		t.Fatalf("io should not carry source.port by default, got %q", tx.Attrs[semconv.SourcePort])
	}

	rx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionReceive})
	if rx.Value != 800 {
		t.Fatalf("io receive value = %v, want 800", rx.Value)
	}

	// MetricPackets: transmit + receive points.
	pkts := rec.MetricPoints(flowlog.MetricPackets)
	if len(pkts) != 2 {
		t.Fatalf("MetricPackets points = %d, want 2 (%+v)", len(pkts), pkts)
	}
	ptx := findPoint(t, pkts, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if ptx.Value != 10 {
		t.Fatalf("packets transmit = %v, want 10", ptx.Value)
	}
	if ptx.Unit != semconv.UnitPackets {
		t.Fatalf("packets unit = %q, want %q", ptx.Unit, semconv.UnitPackets)
	}
	prx := findPoint(t, pkts, map[string]string{semconv.NetworkIODirection: semconv.DirectionReceive})
	if prx.Value != 8 {
		t.Fatalf("packets receive = %v, want 8", prx.Value)
	}

	// MetricFlows: single point of value 1.
	flows := rec.MetricPoints(flowlog.MetricFlows)
	if len(flows) != 1 {
		t.Fatalf("MetricFlows points = %d, want 1 (%+v)", len(flows), flows)
	}
	if flows[0].Value != 1 {
		t.Fatalf("flows value = %v, want 1", flows[0].Value)
	}
	if flows[0].Unit != semconv.UnitFlows {
		t.Fatalf("flows unit = %q, want %q", flows[0].Unit, semconv.UnitFlows)
	}
	if flows[0].Attrs[semconv.NetworkTransport] != "tcp" {
		t.Fatalf("flows transport = %q, want tcp", flows[0].Attrs[semconv.NetworkTransport])
	}
	if flows[0].Attrs[semconv.AttrTrafficType] != semconv.TrafficVirtual {
		t.Fatalf("flows traffic_type = %q", flows[0].Attrs[semconv.AttrTrafficType])
	}
}

func TestProcessPerConnectionLog(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{LogMode: "per_connection"})
	p.Process(virtualTCPFlow(), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1 (%+v)", len(logs), logs)
	}
	lr := logs[0]
	if lr.EventName != "tailscale.network.flow" {
		t.Fatalf("event name = %q, want tailscale.network.flow", lr.EventName)
	}
	if lr.SeverityText != "INFO" {
		t.Fatalf("severity = %q, want INFO", lr.SeverityText)
	}
	if lr.Body == "" {
		t.Fatal("body should be a non-empty human summary")
	}
	// 5-tuple.
	if lr.Attrs[semconv.SourceAddress] != "100.64.0.1" {
		t.Fatalf("source.address = %q, want 100.64.0.1", lr.Attrs[semconv.SourceAddress])
	}
	if lr.Attrs[semconv.SourcePort] != "443" {
		t.Fatalf("source.port = %q, want 443", lr.Attrs[semconv.SourcePort])
	}
	if lr.Attrs[semconv.DestinationAddress] != "100.64.0.2" {
		t.Fatalf("destination.address = %q, want 100.64.0.2", lr.Attrs[semconv.DestinationAddress])
	}
	if lr.Attrs[semconv.DestinationPort] != "51820" {
		t.Fatalf("destination.port = %q, want 51820", lr.Attrs[semconv.DestinationPort])
	}
	if lr.Attrs[semconv.NetworkTransport] != "tcp" {
		t.Fatalf("network.transport = %q, want tcp", lr.Attrs[semconv.NetworkTransport])
	}
	if lr.Attrs[semconv.NetworkType] != semconv.NetworkTypeIPv4 {
		t.Fatalf("network.type = %q, want %q", lr.Attrs[semconv.NetworkType], semconv.NetworkTypeIPv4)
	}
	if lr.Attrs[semconv.AttrTrafficType] != semconv.TrafficVirtual {
		t.Fatalf("traffic_type = %q", lr.Attrs[semconv.AttrTrafficType])
	}
	if lr.Attrs[semconv.AttrSrcNode] != "laptop" {
		t.Fatalf("src.node = %q, want laptop", lr.Attrs[semconv.AttrSrcNode])
	}
	if lr.Attrs[semconv.AttrDstNode] != "server" {
		t.Fatalf("dst.node = %q, want server", lr.Attrs[semconv.AttrDstNode])
	}
	if lr.Attrs[semconv.AttrNodeID] != "nLaptop" {
		t.Fatalf("node.id = %q, want nLaptop", lr.Attrs[semconv.AttrNodeID])
	}
	// Byte/packet counts are emitted as int64 log attributes. The frozen
	// telemetrytest recorder stringifies log values via Value.AsString(), which
	// is empty for Int64-kind values, so assert key presence here and verify the
	// numeric values through the human-readable Body below.
	for _, k := range []string{"tailscale.tx.bytes", "tailscale.rx.bytes", "tailscale.tx.packets", "tailscale.rx.packets"} {
		if _, ok := lr.Attrs[k]; !ok {
			t.Fatalf("missing log attr %q", k)
		}
	}
	if !strings.Contains(lr.Body, "tx=1000B") || !strings.Contains(lr.Body, "rx=800B") {
		t.Fatalf("body %q missing tx/rx byte counts", lr.Body)
	}
}

func TestProcessLogModeOff(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{LogMode: "off"})
	p.Process(virtualTCPFlow(), rec.Emitter())

	if logs := rec.LogRecords(); len(logs) != 0 {
		t.Fatalf("log records = %d, want 0 with LogMode off (%+v)", len(logs), logs)
	}
	// Metrics still emitted.
	if io := rec.MetricPoints(flowlog.MetricIO); len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 even with logs off", len(io))
	}
	if flows := rec.MetricPoints(flowlog.MetricFlows); len(flows) != 1 {
		t.Fatalf("MetricFlows points = %d, want 1 even with logs off", len(flows))
	}
}

func TestProcessNodeDimsFalse(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: false})
	p.Process(virtualTCPFlow(), rec.Emitter())

	io := rec.MetricPoints(flowlog.MetricIO)
	if len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2", len(io))
	}
	for _, p := range io {
		if _, ok := p.Attrs[semconv.AttrSrcNode]; ok {
			t.Fatalf("src.node attr present with NodeDims=false: %+v", p.Attrs)
		}
		if _, ok := p.Attrs[semconv.AttrDstNode]; ok {
			t.Fatalf("dst.node attr present with NodeDims=false: %+v", p.Attrs)
		}
	}
}

func TestProcessExternalDstResolvesViaCache(t *testing.T) {
	rec := telemetrytest.New()
	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		ExitTraffic: []flowlog.ConnectionCounts{
			{Proto: "udp", Src: "100.64.0.1:53", Dst: "8.8.8.8:53", TxPkts: 1, TxBytes: 60, RxPkts: 1, RxBytes: 120},
		},
	}
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true})
	p.Process(flow, rec.Emitter())

	io := rec.MetricPoints(flowlog.MetricIO)
	if len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 (%+v)", len(io), io)
	}
	tx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrTrafficType] != semconv.TrafficExit {
		t.Fatalf("traffic_type = %q, want %q", tx.Attrs[semconv.AttrTrafficType], semconv.TrafficExit)
	}
	if tx.Attrs[semconv.AttrSrcNode] != "laptop" {
		t.Fatalf("src.node = %q, want laptop", tx.Attrs[semconv.AttrSrcNode])
	}
	// 8.8.8.8 is outside Tailscale ranges -> "external".
	if tx.Attrs[semconv.AttrDstNode] != "external" {
		t.Fatalf("dst.node = %q, want external", tx.Attrs[semconv.AttrDstNode])
	}
}

func TestProcessNetworkTypeIPv6(t *testing.T) {
	rec := telemetrytest.New()
	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: "tcp", Src: "[fd7a:115c:a1e0::1]:443", Dst: "[fd7a:115c:a1e0::2]:51820", TxBytes: 5, RxBytes: 7},
		},
	}
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_connection"})
	p.Process(flow, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	if logs[0].Attrs[semconv.NetworkType] != semconv.NetworkTypeIPv6 {
		t.Fatalf("network.type = %q, want %q", logs[0].Attrs[semconv.NetworkType], semconv.NetworkTypeIPv6)
	}
	if logs[0].Attrs[semconv.SourceAddress] != "fd7a:115c:a1e0::1" {
		t.Fatalf("source.address = %q, want fd7a:115c:a1e0::1", logs[0].Attrs[semconv.SourceAddress])
	}
}

func TestProcessIncludePorts(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{IncludePorts: true})
	p.Process(virtualTCPFlow(), rec.Emitter())

	io := rec.MetricPoints(flowlog.MetricIO)
	tx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.SourcePort] != "443" {
		t.Fatalf("io source.port = %q, want 443", tx.Attrs[semconv.SourcePort])
	}
	if tx.Attrs[semconv.DestinationPort] != "51820" {
		t.Fatalf("io destination.port = %q, want 51820", tx.Attrs[semconv.DestinationPort])
	}
}

func TestProcessAllLoops(t *testing.T) {
	rec := telemetrytest.New()
	resp := flowlog.NetworkResponse{Logs: []flowlog.FlowLog{virtualTCPFlow(), virtualTCPFlow()}}
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})
	p.ProcessAll(resp, rec.Emitter())

	flows := rec.MetricPoints(flowlog.MetricFlows)
	if len(flows) != 1 {
		t.Fatalf("MetricFlows points = %d, want 1 aggregated point", len(flows))
	}
	// Two identical flows accumulate into the same sum point: 1 + 1 = 2.
	if flows[0].Value != 2 {
		t.Fatalf("flows value = %v, want 2 across two records", flows[0].Value)
	}
	io := rec.MetricPoints(flowlog.MetricIO)
	tx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Value != 2000 {
		t.Fatalf("io transmit value = %v, want 2000 across two records", tx.Value)
	}
}

func TestProcessPerRecordLog(t *testing.T) {
	rec := telemetrytest.New()
	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: "tcp", Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxBytes: 1000, RxBytes: 800},
		},
		ExitTraffic: []flowlog.ConnectionCounts{
			{Proto: "udp", Src: "100.64.0.1:53", Dst: "8.8.8.8:53", TxBytes: 60, RxBytes: 120},
		},
	}
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{LogMode: "per_record"})
	p.Process(flow, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("per_record log records = %d, want 1 (%+v)", len(logs), logs)
	}
	if logs[0].Attrs[semconv.AttrNodeID] != "nLaptop" {
		t.Fatalf("node.id = %q, want nLaptop", logs[0].Attrs[semconv.AttrNodeID])
	}
	// Numeric summary attrs are int64; assert presence (see note in
	// TestProcessPerConnectionLog) and verify totals via the Body.
	for _, k := range []string{"tailscale.connections", "tailscale.tx.bytes", "tailscale.rx.bytes"} {
		if _, ok := logs[0].Attrs[k]; !ok {
			t.Fatalf("missing log attr %q", k)
		}
	}
	if !strings.Contains(logs[0].Body, "2 connections") {
		t.Fatalf("body %q missing connection count", logs[0].Body)
	}
	if !strings.Contains(logs[0].Body, "tx=1060B") || !strings.Contains(logs[0].Body, "rx=920B") {
		t.Fatalf("body %q missing total tx/rx byte counts", logs[0].Body)
	}
}

func TestNewProcessorNilCacheSafe(t *testing.T) {
	// A nil cache must not panic; node resolution falls back to "unknown".
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(nil, flowlog.Options{NodeDims: true})
	p.Process(virtualTCPFlow(), rec.Emitter())

	io := rec.MetricPoints(flowlog.MetricIO)
	if len(io) != 2 {
		t.Fatalf("MetricIO points = %d, want 2 with nil cache", len(io))
	}
	tx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrSrcNode] != "unknown" {
		t.Fatalf("src.node = %q, want unknown for nil cache", tx.Attrs[semconv.AttrSrcNode])
	}
}
