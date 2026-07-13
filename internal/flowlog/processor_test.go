package flowlog_test

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
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

// protoTCP and protoUDP are the IANA protocol numbers the API returns.
const (
	protoTCP = 6
	protoUDP = 17
)

func virtualTCPFlow() flowlog.FlowLog {
	return flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		Start:  time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
		End:    time.Date(2024, 6, 6, 15, 26, 26, 0, time.UTC),
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxPkts: 10, TxBytes: 1000, RxPkts: 8, RxBytes: 800},
		},
	}
}

// realNetworkBody mirrors the captured GET /logging/network shape: proto is a
// NUMBER, and the absent-proto physical entry decodes to 0. Covers 6=tcp,
// 17=udp, 1=icmp, 99 (no IANA name -> decimal string), and absent -> unknown.
const realNetworkBody = `{
  "logs": [
    {
      "logged": "2026-06-02T19:00:01.346001489Z",
      "nodeId": "nLaptop",
      "start": "2026-06-02T18:59:54.278418352Z",
      "end": "2026-06-02T18:59:59.279306235Z",
      "virtualTraffic": [
        {"proto":6,"src":"100.64.0.1:22","dst":"100.64.0.2:58544","txPkts":51,"txBytes":6420}
      ],
      "subnetTraffic": [
        {"proto":17,"src":"100.64.0.1:53","dst":"100.64.0.2:60980","txPkts":1,"txBytes":115},
        {"proto":99,"src":"100.64.0.1:0","dst":"100.64.0.2:0","txPkts":10,"txBytes":270},
        {"proto":1,"src":"100.64.0.1:0","dst":"100.64.0.2:0","txPkts":2,"txBytes":40}
      ],
      "physicalTraffic": [
        {"src":"100.64.0.2:0","dst":"10.0.0.183:57532","txPkts":53,"txBytes":8708}
      ]
    }
  ]
}`

// TestProcessNumericProtoTransport is the regression for the real-data bug:
// proto arrives as a number, and the processor must map it to a transport name
// used on every metric/log. It decodes the captured shape with NO error.
func TestProcessNumericProtoTransport(t *testing.T) {
	var resp flowlog.NetworkResponse
	if err := json.Unmarshal([]byte(realNetworkBody), &resp); err != nil {
		t.Fatalf("unmarshal real-shaped body: %v", err)
	}

	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})
	p.ProcessAll(resp, rec.Emitter())

	flows := rec.MetricPoints(flowlog.MetricFlows)
	got := make(map[string]bool)
	for _, f := range flows {
		got[f.Attrs[semconv.NetworkTransport]] = true
	}
	// 6->tcp, 17->udp, 1->icmp, 99->"99" (no IANA name), absent->unknown.
	for _, want := range []string{"tcp", "udp", "icmp", "99", "unknown"} {
		if !got[want] {
			t.Fatalf("missing transport %q in flows; got %v", want, got)
		}
	}
}

// TestProcessOutOfRangeProtoClampsTransport is the regression for #77: proto is
// an attacker-controlled JSON number on the stream ingestion path (shared with
// poll via the same Processor). Values outside the IANA protocol-number range
// (0-255) must clamp to "unknown" instead of echoing the raw wire integer,
// which would otherwise mint unbounded transport cardinality. In-range but
// unrecognized numbers (e.g. 99, see TestProcessNumericProtoTransport) keep
// their existing decimal-string behavior — that space is already bounded to
// at most 256 distinct values.
func TestProcessOutOfRangeProtoClampsTransport(t *testing.T) {
	cases := []struct {
		name  string
		proto int
	}{
		{"large positive", 999999999},
		{"just above range", 256},
		{"negative", -1},
		{"very negative", -999999999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})
			p.Process(flowlog.FlowLog{
				NodeID: "nLaptop",
				Start:  time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
				End:    time.Date(2024, 6, 6, 15, 26, 26, 0, time.UTC),
				VirtualTraffic: []flowlog.ConnectionCounts{
					{Proto: tc.proto, Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxPkts: 1, TxBytes: 1},
				},
			}, rec.Emitter())

			flows := rec.MetricPoints(flowlog.MetricFlows)
			if len(flows) != 1 {
				t.Fatalf("MetricFlows points = %d, want 1", len(flows))
			}
			if got := flows[0].Attrs[semconv.NetworkTransport]; got != "unknown" {
				t.Errorf("transport = %q, want unknown for out-of-range proto %d", got, tc.proto)
			}
		})
	}
}

// TestProcessProtoFloodBoundsTransportCardinality covers a flood of distinct
// attacker-chosen proto values (as would arrive via the streaming receiver)
// and asserts the transport attribute never exceeds the bounded set: the
// well-known IANA names, any in-range (0-255) decimal fallback, and the single
// "unknown" overflow bucket for out-of-range values.
func TestProcessProtoFloodBoundsTransportCardinality(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{})

	for i := range 2000 {
		proto := 1000000 + i // always out of the 0-255 IANA range
		p.Process(flowlog.FlowLog{
			NodeID: "nLaptop",
			Start:  time.Date(2024, 6, 6, 15, 25, 26, 0, time.UTC),
			End:    time.Date(2024, 6, 6, 15, 26, 26, 0, time.UTC),
			VirtualTraffic: []flowlog.ConnectionCounts{
				{Proto: proto, Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxPkts: 1, TxBytes: 1},
			},
		}, rec.Emitter())
	}

	flows := rec.MetricPoints(flowlog.MetricFlows)
	distinct := make(map[string]bool)
	for _, f := range flows {
		distinct[f.Attrs[semconv.NetworkTransport]] = true
	}
	if len(distinct) != 1 || !distinct["unknown"] {
		t.Fatalf("distinct transports from a 2000-value out-of-range proto flood = %v, want only {unknown}", distinct)
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
			{Proto: protoUDP, Src: "100.64.0.1:53", Dst: "8.8.8.8:53", TxPkts: 1, TxBytes: 60, RxPkts: 1, RxBytes: 120},
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

// TestProcessServiceVIPDstResolvesToServiceName covers #166: a flow whose peer
// is a Tailscale Service VIP (a CGNAT-range address with no device entry)
// resolves to the service NAME via the cache's service map instead of
// collapsing to "unknown". Only the derived name is emitted — never the VIP.
func TestProcessServiceVIPDstResolvesToServiceName(t *testing.T) {
	rec := telemetrytest.New()
	cache := cacheWith(t)
	vip := netip.MustParseAddr("100.81.142.157")
	cache.ReplaceServices(map[netip.Addr]string{vip: "svc:argocd"})

	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:51000", Dst: vip.String() + ":443", TxPkts: 2, TxBytes: 200, RxPkts: 2, RxBytes: 400},
		},
	}
	p := flowlog.NewProcessor(cache, flowlog.Options{NodeDims: true})
	p.Process(flow, rec.Emitter())

	tx := findPoint(t, rec.MetricPoints(flowlog.MetricIO),
		map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrDstNode] != "svc:argocd" {
		t.Fatalf("metric dst.node = %q, want svc:argocd", tx.Attrs[semconv.AttrDstNode])
	}
	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	if got := logs[0].Attrs[semconv.AttrDstNode]; got != "svc:argocd" {
		t.Fatalf("log dst.node = %q, want svc:argocd", got)
	}
}

// externalFlow has one exit connection to a non-Tailscale destination (8.8.8.8)
// from a known tailnet source (100.64.0.1 -> laptop).
func externalFlow() flowlog.FlowLog {
	return flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		ExitTraffic: []flowlog.ConnectionCounts{
			{Proto: protoUDP, Src: "100.64.0.1:53", Dst: "8.8.8.8:53", TxPkts: 1, TxBytes: 60, RxPkts: 1, RxBytes: 120},
		},
	}
}

func TestProcessKeepExternalAddrsFalseCollapses(t *testing.T) {
	// Zero value: an external destination collapses to "external".
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true, KeepExternalAddrs: false})
	p.Process(externalFlow(), rec.Emitter())

	io := rec.MetricPoints(flowlog.MetricIO)
	tx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrDstNode] != "external" {
		t.Fatalf("dst.node = %q, want external with KeepExternalAddrs=false", tx.Attrs[semconv.AttrDstNode])
	}
	if tx.Attrs[semconv.AttrSrcNode] != "laptop" {
		t.Fatalf("src.node = %q, want laptop", tx.Attrs[semconv.AttrSrcNode])
	}
}

func TestProcessKeepExternalAddrsTrueKeepsRawIP(t *testing.T) {
	// KeepExternalAddrs=true: an external destination resolves to its raw host.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true, KeepExternalAddrs: true})
	p.Process(externalFlow(), rec.Emitter())

	io := rec.MetricPoints(flowlog.MetricIO)
	tx := findPoint(t, io, map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrDstNode] != "8.8.8.8" {
		t.Fatalf("dst.node = %q, want raw IP 8.8.8.8 with KeepExternalAddrs=true", tx.Attrs[semconv.AttrDstNode])
	}
	// Known tailnet source still resolves to its hostname.
	if tx.Attrs[semconv.AttrSrcNode] != "laptop" {
		t.Fatalf("src.node = %q, want laptop", tx.Attrs[semconv.AttrSrcNode])
	}
}

func TestProcessKeepExternalAddrsTrueUnknownTailnet(t *testing.T) {
	// A Tailscale-range address not in the cache is "unknown" by default but
	// becomes its raw IP when KeepExternalAddrs=true.
	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:443", Dst: "100.64.0.9:51820", TxBytes: 10, RxBytes: 20},
		},
	}

	recOff := telemetrytest.New()
	flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true}).Process(flow, recOff.Emitter())
	txOff := findPoint(t, recOff.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if txOff.Attrs[semconv.AttrDstNode] != "unknown" {
		t.Fatalf("dst.node = %q, want unknown for uncached tailnet addr", txOff.Attrs[semconv.AttrDstNode])
	}

	recOn := telemetrytest.New()
	flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true, KeepExternalAddrs: true}).Process(flow, recOn.Emitter())
	txOn := findPoint(t, recOn.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if txOn.Attrs[semconv.AttrDstNode] != "100.64.0.9" {
		t.Fatalf("dst.node = %q, want raw IP 100.64.0.9 with KeepExternalAddrs=true", txOn.Attrs[semconv.AttrDstNode])
	}
}

func TestProcessNetworkTypeIPv6(t *testing.T) {
	rec := telemetrytest.New()
	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "[fd7a:115c:a1e0::1]:443", Dst: "[fd7a:115c:a1e0::2]:51820", TxBytes: 5, RxBytes: 7},
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
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{IncludeSourcePort: true, IncludeDestinationPort: true})
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
			{Proto: protoTCP, Src: "100.64.0.1:443", Dst: "100.64.0.2:51820", TxBytes: 1000, RxBytes: 800},
		},
		ExitTraffic: []flowlog.ConnectionCounts{
			{Proto: protoUDP, Src: "100.64.0.1:53", Dst: "8.8.8.8:53", TxBytes: 60, RxBytes: 120},
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

// httpsFlow has one virtual connection whose DESTINATION is a well-known port
// (tcp/443 -> "https"); the source port (50000) is unmapped.
func httpsFlow() flowlog.FlowLog {
	return flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "100.64.0.2:443", TxPkts: 10, TxBytes: 1000, RxPkts: 8, RxBytes: 800},
		},
	}
}

func TestProcessDestinationServiceOnLogs(t *testing.T) {
	// The mapped destination service name is ALWAYS added to flow LOGS (no toggle)
	// when the destination port maps to a known IANA service.
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{LogMode: "per_connection"})
	p.Process(httpsFlow(), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	if logs[0].Attrs[semconv.AttrDstService] != "https" {
		t.Fatalf("dst.service = %q, want https", logs[0].Attrs[semconv.AttrDstService])
	}
}

func TestProcessDestinationServiceMetricsGated(t *testing.T) {
	// Off by default: no dst.service on metrics.
	recOff := telemetrytest.New()
	flowlog.NewProcessor(cacheWith(t), flowlog.Options{}).Process(httpsFlow(), recOff.Emitter())
	for _, pt := range recOff.MetricPoints(flowlog.MetricIO) {
		if _, ok := pt.Attrs[semconv.AttrDstService]; ok {
			t.Fatalf("dst.service present on metrics by default: %+v", pt.Attrs)
		}
	}

	// On: dst.service on the io metric points.
	recOn := telemetrytest.New()
	flowlog.NewProcessor(cacheWith(t), flowlog.Options{IncludeDestinationService: true}).Process(httpsFlow(), recOn.Emitter())
	tx := findPoint(t, recOn.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrDstService] != "https" {
		t.Fatalf("io dst.service = %q, want https", tx.Attrs[semconv.AttrDstService])
	}
}

func TestProcessDestinationServiceUnmappedOmitted(t *testing.T) {
	// virtualTCPFlow's destination port (51820) is not a registered service, so
	// dst.service is omitted entirely even with the metric toggle on.
	rec := telemetrytest.New()
	flowlog.NewProcessor(cacheWith(t), flowlog.Options{LogMode: "per_connection", IncludeDestinationService: true}).Process(virtualTCPFlow(), rec.Emitter())
	for _, pt := range rec.MetricPoints(flowlog.MetricIO) {
		if _, ok := pt.Attrs[semconv.AttrDstService]; ok {
			t.Fatalf("dst.service present for unmapped port on metrics: %+v", pt.Attrs)
		}
	}
	if _, ok := rec.LogRecords()[0].Attrs[semconv.AttrDstService]; ok {
		t.Fatalf("dst.service present on log for unmapped port")
	}
}

// fakeRDNS is a synchronous reverse-DNS resolver for tests (the real cache is
// async; the processor only needs the narrow Resolver interface).
type fakeRDNS map[netip.Addr]string

func (f fakeRDNS) LookupName(a netip.Addr) (string, bool) {
	n, ok := f[a]
	return n, ok
}

func TestProcessExternalReverseDNSHit(t *testing.T) {
	rec := telemetrytest.New()
	r := fakeRDNS{netip.MustParseAddr("8.8.8.8"): "dns.google"}
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true, LogMode: "per_connection", RDNS: r})
	p.Process(externalFlow(), rec.Emitter())

	tx := findPoint(t, rec.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrDstNode] != "dns.google" {
		t.Fatalf("metric dst.node = %q, want dns.google (reverse-DNS)", tx.Attrs[semconv.AttrDstNode])
	}
	if logs := rec.LogRecords(); logs[0].Attrs[semconv.AttrDstNode] != "dns.google" {
		t.Fatalf("log dst.node = %q, want dns.google", logs[0].Attrs[semconv.AttrDstNode])
	}
}

func TestProcessExternalReverseDNSMissFallsBack(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true, RDNS: fakeRDNS{}})
	p.Process(externalFlow(), rec.Emitter())

	tx := findPoint(t, rec.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrDstNode] != "external" {
		t.Fatalf("metric dst.node = %q, want external on reverse-DNS miss", tx.Attrs[semconv.AttrDstNode])
	}
}

func TestProcessReverseDNSSkipsTailnetUnknown(t *testing.T) {
	// Reverse DNS only enriches external addresses; an uncached tailnet address
	// stays "unknown" even if the resolver would answer for it.
	rec := telemetrytest.New()
	flow := flowlog.FlowLog{
		Logged: time.Date(2024, 6, 6, 15, 27, 26, 0, time.UTC),
		NodeID: "nLaptop",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:443", Dst: "100.64.0.9:51820", TxBytes: 10, RxBytes: 20},
		},
	}
	r := fakeRDNS{netip.MustParseAddr("100.64.0.9"): "should-not-be-used"}
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{NodeDims: true, RDNS: r})
	p.Process(flow, rec.Emitter())

	tx := findPoint(t, rec.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if tx.Attrs[semconv.AttrDstNode] != "unknown" {
		t.Fatalf("metric dst.node = %q, want unknown (rDNS must not touch tailnet addrs)", tx.Attrs[semconv.AttrDstNode])
	}
}

func TestProcessIndependentPortToggles(t *testing.T) {
	// Source-only: source.port present, destination.port absent.
	recSrc := telemetrytest.New()
	flowlog.NewProcessor(cacheWith(t), flowlog.Options{IncludeSourcePort: true}).Process(virtualTCPFlow(), recSrc.Emitter())
	txSrc := findPoint(t, recSrc.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if txSrc.Attrs[semconv.SourcePort] != "443" {
		t.Fatalf("source.port = %q, want 443 (src-only)", txSrc.Attrs[semconv.SourcePort])
	}
	if _, ok := txSrc.Attrs[semconv.DestinationPort]; ok {
		t.Fatalf("destination.port present with src-only toggle: %+v", txSrc.Attrs)
	}

	// Destination-only: destination.port present, source.port absent.
	recDst := telemetrytest.New()
	flowlog.NewProcessor(cacheWith(t), flowlog.Options{IncludeDestinationPort: true}).Process(virtualTCPFlow(), recDst.Emitter())
	txDst := findPoint(t, recDst.MetricPoints(flowlog.MetricIO), map[string]string{semconv.NetworkIODirection: semconv.DirectionTransmit})
	if txDst.Attrs[semconv.DestinationPort] != "51820" {
		t.Fatalf("destination.port = %q, want 51820 (dst-only)", txDst.Attrs[semconv.DestinationPort])
	}
	if _, ok := txDst.Attrs[semconv.SourcePort]; ok {
		t.Fatalf("source.port present with dst-only toggle: %+v", txDst.Attrs)
	}
}
