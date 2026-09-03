package flowlog_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// Tailscale writes ":0" wherever the addr:port shape demands a port and the
// thing being described has none. Measured over 3h of a live tailnet — 120,901
// connection entries — it happens in exactly two places and nowhere else:
//
//   - a portless IP protocol writes it on BOTH ends, every time: 1,884/1,884
//     ICMP entries and 1,340/1,340 TSMP ones;
//   - every physical entry writes it on its SOURCE (46,193/46,193), because the
//     underlay's src is the peer's overlay identity rather than a socket.
//
// It is never a real port. Port 0 cannot be a TCP/UDP endpoint, and no tcp/udp
// entry carried it on either end (0/71,253). So it is an absent port, and these
// tests pin that every surface treats it as one.
const protoICMP = 1

// portlessRecord is one connection of a protocol that has no ports, between two
// identified nodes, in the shape the API delivers it.
func portlessRecord(proto int) flowlog.FlowLog {
	return flowlog.FlowLog{
		NodeID: "nReporter",
		Start:  time.Date(2026, 7, 24, 9, 30, 0, 0, time.UTC),
		SrcNode: &flowlog.NodeRef{
			NodeID: "nSrc", Name: "camden.example.ts.net",
			Addresses: []string{"100.64.0.1"}, Tags: []string{"tag:servers"},
		},
		DstNodes: []flowlog.NodeRef{{
			NodeID: "nDst", Name: "gfmbp.example.ts.net",
			Addresses: []string{"100.64.0.2"}, User: "rob@example.com",
		}},
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: proto, Src: "100.64.0.1:0", Dst: "100.64.0.2:0", TxBytes: 252, TxPkts: 3},
		},
	}
}

// The ports dimensions are the ones that must go: an attribute reading
// destination.port=0 says a port was contacted, and none was. The addresses are
// carried by the record and stay — same rule as the DERP marker.
func TestProcess_PortlessProtocolReportsNoPort(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proto int
	}{{"icmp", protoICMP}, {"tsmp", protoTSMP}} {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
				FlowMetricsMode:        "all",
				NodeDims:               true,
				IncludeSourcePort:      true,
				IncludeDestinationPort: true,
			})
			p.Process(portlessRecord(tc.proto), rec.Emitter())

			pts := rec.MetricPoints(flowlog.MetricIO)
			if len(pts) == 0 {
				t.Fatal("no io points emitted")
			}
			for _, attr := range []string{semconv.SourcePort, semconv.DestinationPort, semconv.AttrDstService} {
				if v, ok := pts[0].Attrs[attr]; ok {
					t.Errorf("metric carries %s=%q; %s has no ports", attr, v, tc.name)
				}
			}

			logs := rec.LogRecords()
			if len(logs) != 1 {
				t.Fatalf("log records = %d, want 1", len(logs))
			}
			for _, attr := range []string{semconv.SourcePort, semconv.DestinationPort} {
				if v, ok := logs[0].Attrs[attr]; ok {
					t.Errorf("log carries %s=%q; %s has no ports", attr, v, tc.name)
				}
			}
			// What the record DID carry is untouched.
			if got := logs[0].Attrs[semconv.SourceAddress]; got != "100.64.0.1" {
				t.Errorf("log %s = %q, want the address kept", semconv.SourceAddress, got)
			}
			if got := logs[0].Attrs[semconv.DestinationAddress]; got != "100.64.0.2" {
				t.Errorf("log %s = %q, want the address kept", semconv.DestinationAddress, got)
			}
			if got := logs[0].Attrs[semconv.AttrDstNode]; got != unverifiedName("gfmbp") {
				t.Errorf("log %s = %q, want the peer still named (marked unverified)", semconv.AttrDstNode, got)
			}
		})
	}
}

// The underlay half: a physical entry's src is the peer's overlay address, which
// is an identity and not a socket, so it always reports :0. Its DESTINATION port
// is real (the underlay endpoint, or the DERP region) and must survive.
func TestProcess_PhysicalSourceReportsNoPort(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode:        "all",
		IncludeSourcePort:      true,
		IncludeDestinationPort: true,
	})
	p.Process(physicalRecord("81.187.237.31:41641"), rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatal("no io points emitted")
	}
	if v, ok := pts[0].Attrs[semconv.SourcePort]; ok {
		t.Errorf("metric carries %s=%q; a physical entry's src is an identity, not a socket",
			semconv.SourcePort, v)
	}
	if got := pts[0].Attrs[semconv.DestinationPort]; got != "41641" {
		t.Errorf("metric %s = %q, want the underlay endpoint's real port", semconv.DestinationPort, got)
	}
}

// The unique-port gauge counts distinct destination ports per source node.
// Counting the placeholder adds exactly one phantom port to every source that
// ever pings anything.
func TestProcess_PortlessProtocolIsNotADistinctPort(t *testing.T) {
	rec := telemetrytest.New()
	p := rollupProc(t, flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})

	flow := portlessRecord(protoICMP)
	flow.VirtualTraffic = append(flow.VirtualTraffic, flowlog.ConnectionCounts{
		Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "100.64.0.2:443", TxBytes: 100, TxPkts: 1,
	})
	p.Process(flow, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	// rollupProc's device cache names 100.64.0.1 "laptop", and the cache wins over
	// the record's own node block.
	ports := findPoint(t, rec.MetricPoints(flowlog.MetricUniqueDstPorts),
		map[string]string{semconv.AttrSrcNode: "laptop"})
	if ports.Value != 1 {
		t.Errorf("unique dst ports = %v, want 1 (tcp/443 only; a portless protocol contributes none)",
			ports.Value)
	}
}

// End to end against the real store: the destination-services table must not
// gain a row for a protocol that contacted no service, and the traffic must
// still count everywhere else.
func TestProcess_PortlessProtocolAddsNoServicesRow(t *testing.T) {
	flow := portlessRecord(protoICMP)
	m := flowstore.NewMemory(0, flowstore.WithClock(func() time.Time { return flow.Start }))
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{Store: m})
	rec := telemetrytest.New()

	p.Process(flow, rec.Emitter())

	res := m.Query(flowstore.Query{Start: flow.Start.Add(-time.Hour), End: flow.Start.Add(time.Hour)})
	if len(res.Ports) != 0 {
		t.Errorf("ports = %+v, want none — icmp contacted no service", res.Ports)
	}
	if want := int64(252); res.Totals.Bytes() != want {
		t.Errorf("total bytes = %d, want %d — dropping a dimension must not drop data",
			res.Totals.Bytes(), want)
	}
}

// The evaluator already handles a portless protocol correctly — matchPorts skips
// a bare-port spec whenever the protocol is not tcp/udp — and dropping the
// placeholder port must not disturb that. A grant written the way an operator
// writes one for ICMP still explains the connection.
func TestProcess_PortlessProtocolVerdictIsUnchanged(t *testing.T) {
	const icmpPolicy = `{"grants": [
		{"src": ["tag:servers"], "dst": ["*"], "ip": ["icmp:*"]}
	]}`
	p, fs := policyProcessor(t, policyStore(t, icmpPolicy))
	rec := telemetrytest.New()

	p.Process(portlessRecord(protoICMP), rec.Emitter())

	if o := only(t, fs); o.Verdict != flowstore.VerdictPermitted {
		t.Errorf("verdict = %q, want %q — the icmp:* grant covers this connection",
			o.Verdict, flowstore.VerdictPermitted)
	}
}

// The relationship an unexplained portless connection aggregates into is the
// shape a grant would be written in, so it must not read "icmp/0" — not a port,
// and not a rule anyone could write. This used to be normalised inside the store
// (flowstore.portOrNone, removed with this change); it is asserted here instead,
// through the real store, because the processor is now the single place that
// decides a zero port is no port.
func TestProcess_PortlessUnexplainedRelationshipHasNoPort(t *testing.T) {
	const narrowPolicy = `{"grants": [
		{"src": ["tag:servers"], "dst": ["tag:ntp"], "ip": ["udp:123"]}
	]}`
	flow := portlessRecord(protoICMP)
	m := flowstore.NewMemory(0, flowstore.WithClock(func() time.Time { return flow.Start }))
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
		Store:  m,
		Policy: policyStore(t, narrowPolicy),
	})
	rec := telemetrytest.New()

	p.Process(flow, rec.Emitter())

	res := m.Query(flowstore.Query{Start: flow.Start.Add(-time.Hour), End: flow.Start.Add(time.Hour)})
	if len(res.Unexplained) != 1 {
		t.Fatalf("unexplained = %+v, want 1 (verdicts = %+v)", res.Unexplained, res.Verdicts)
	}
	if got := res.Unexplained[0].Port; got != "" {
		t.Errorf("port = %q, want empty — a protocol with no ports names none", got)
	}
	if got := res.Unexplained[0].Transport; got != "icmp" {
		t.Errorf("transport = %q, want icmp — the relationship still says what it was", got)
	}
}
