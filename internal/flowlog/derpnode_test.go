package flowlog_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// A relayed physical entry's destination is 127.3.3.40 — Tailscale's loopback
// MARKER for "this peer was reached through a relay" — with the DERP region ID
// in the port field. It is not a device and never was one, so no dimension that
// names a device or a service may be derived from it. The counterparty is on the
// SRC side of a physical entry, where it stays.
//
// These tests pin that on every surface the marker reaches: the raw families,
// the rollup families, the unique-peer gauges, the flow log and the flow store.

// relayedRegion is the region ID these tests put in the marker's port field. It
// is deliberately a number that IS a registered IANA port (22 -> ssh), so a
// surface that mapped the region through the port registry would mint a visibly
// wrong service name rather than an empty one.
const relayedRegion = "22"

// TestRelayedPhysical_MetricsOmitDstNode covers both metric-mode families and
// BOTH settings of cardinality.flow.collapse_external, which is the toggle that
// decides HOW the marker was wrong before: collapsed to the "external" sentinel
// (a DERP relay is Tailscale's own infrastructure, not an external peer) or kept
// as the raw 127.3.3.40 (a loopback address presented as a destination device).
func TestRelayedPhysical_MetricsOmitDstNode(t *testing.T) {
	for _, keep := range []bool{false, true} {
		for _, mode := range []string{"all", "rollup"} {
			t.Run(fmt.Sprintf("collapse_external=%v/%s", !keep, mode), func(t *testing.T) {
				rec := telemetrytest.New()
				p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
					FlowMetricsMode:   mode,
					NodeDims:          true,
					KeepExternalAddrs: keep,
				})
				p.Process(physicalRecord("127.3.3.40:"+relayedRegion), rec.Emitter())
				p.FlushRollup(rec.Emitter())

				metric := flowlog.MetricIO
				if mode == "rollup" {
					metric = flowlog.MetricIORollup
				}
				pts := rec.MetricPoints(metric)
				if len(pts) == 0 {
					t.Fatalf("no %s points emitted", metric)
				}
				for _, pt := range pts {
					if v, ok := pt.Attrs[semconv.AttrDstNode]; ok {
						t.Errorf("relayed series carries %s=%q; the DERP marker is not a device",
							semconv.AttrDstNode, v)
					}
					// The relay is still fully described — by the two dimensions
					// that actually mean it — and the peer is still named.
					if got := pt.Attrs[semconv.AttrPath]; got != semconv.PathDERP {
						t.Errorf("%s = %q, want %q", semconv.AttrPath, got, semconv.PathDERP)
					}
					if got := pt.Attrs[semconv.AttrDERPRegionID]; got != relayedRegion {
						t.Errorf("%s = %q, want %q", semconv.AttrDERPRegionID, got, relayedRegion)
					}
					// Marked: only the record's own embedded identity names this peer.
					if got, want := pt.Attrs[semconv.AttrSrcNode], unverifiedName("gfmbp"); got != want {
						t.Errorf("%s = %q, want %q — the peer is on the src side and must survive",
							semconv.AttrSrcNode, got, want)
					}
				}
			})
		}
	}
}

// TestDirectPhysical_KeepsDstNode is the other half of the rule: a direct
// physical entry's destination IS a real endpoint, so dropping its node label
// too would throw away the underlay peer this change is not about.
func TestDirectPhysical_KeepsDstNode(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "all", NodeDims: true, KeepExternalAddrs: true,
	})
	p.Process(physicalRecord("81.187.237.31:41641"), rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatal("no raw io points emitted")
	}
	if got := pts[0].Attrs[semconv.AttrDstNode]; got != "81.187.237.31" {
		t.Errorf("%s = %q, want the direct endpoint 81.187.237.31", semconv.AttrDstNode, got)
	}
}

// TestRelayedPhysical_NoDstServiceEvenWhenProtoIsSet guards the coupling that
// makes dst.service dormant rather than safe today. serviceName is keyed by
// (transport, port); physical entries report proto 0 on live data, so the region
// never reaches the IANA registry. If Tailscale ever populates proto, region 22
// would map through tcp/22 and mint "ssh" — a service that was never contacted.
func TestRelayedPhysical_NoDstServiceEvenWhenProtoIsSet(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "all", NodeDims: true, IncludeDestinationPort: true,
	})
	flow := physicalRecord("127.3.3.40:" + relayedRegion)
	flow.PhysicalTraffic[0].Proto = protoTCP
	p.Process(flow, rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatal("no raw io points emitted")
	}
	if v, ok := pts[0].Attrs[semconv.AttrDstService]; ok {
		t.Errorf("relayed series carries %s=%q; a DERP region ID is not a port and maps to no service",
			semconv.AttrDstService, v)
	}
	// The region is still readable, in the field that means it.
	if got := pts[0].Attrs[semconv.AttrDERPRegionID]; got != relayedRegion {
		t.Errorf("%s = %q, want %q", semconv.AttrDERPRegionID, got, relayedRegion)
	}
}

// TestRelayedPhysical_IsNotADistinctPeer: the unique-peer gauge counts distinct
// destination NODES per source. Counting the marker adds exactly one phantom
// peer per source node — and, through the same call, the region ID as a distinct
// destination port.
func TestRelayedPhysical_IsNotADistinctPeer(t *testing.T) {
	rec := telemetrytest.New()
	p := rollupProc(t, flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true, KeepExternalAddrs: true})

	flow := physicalRecord("81.187.237.31:41641")
	flow.PhysicalTraffic = append(flow.PhysicalTraffic, flowlog.ConnectionCounts{
		Src: "100.119.137.81:0", Dst: "127.3.3.40:" + relayedRegion, TxBytes: 90, TxPkts: 1,
	})
	p.Process(flow, rec.Emitter())
	p.FlushRollup(rec.Emitter())

	peers := findPoint(t, rec.MetricPoints(flowlog.MetricUniqueDstPeers),
		map[string]string{semconv.AttrSrcNode: unverifiedName("gfmbp")})
	if peers.Value != 1 {
		t.Errorf("unique dst peers = %v, want 1 (the direct endpoint only; the relay marker is not a peer)",
			peers.Value)
	}
	ports := findPoint(t, rec.MetricPoints(flowlog.MetricUniqueDstPorts),
		map[string]string{semconv.AttrSrcNode: unverifiedName("gfmbp")})
	if ports.Value != 1 {
		t.Errorf("unique dst ports = %v, want 1 (a DERP region ID is not a port)", ports.Value)
	}
}

// countingRDNS records every reverse-DNS lookup the processor asks for and
// answers none of them, which is what a real PTR lookup against 127.3.3.40 would
// do — except that the real cache also SCHEDULES a background lookup on each
// miss, so an address that can never resolve is re-queried for as long as it
// keeps appearing in the flow stream.
type countingRDNS struct{ calls int }

func (c *countingRDNS) LookupName(netip.Addr) (string, bool) { c.calls++; return "", false }

// TestRelayedPhysical_NoReverseDNSForTheMarker: resolution is skipped entirely
// for the marker, so the lookup that can never succeed is never asked for.
func TestRelayedPhysical_NoReverseDNSForTheMarker(t *testing.T) {
	r := &countingRDNS{}
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{
		FlowMetricsMode: "all", NodeDims: true, RDNS: r,
	})
	p.Process(physicalRecord("127.3.3.40:"+relayedRegion), rec.Emitter())

	if r.calls != 0 {
		t.Errorf("reverse-DNS lookups = %d, want 0; 127.3.3.40 is a marker and has no PTR to find", r.calls)
	}
}

// TestRelayedPhysical_LogKeepsAddressOmitsNode: the log is the full-fidelity
// record, so it keeps the endpoint the wire carried, marker and region included.
// What it drops is the NODE — a name for a device — because no device was named.
func TestRelayedPhysical_LogKeepsAddressOmitsNode(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(cacheWith(t), flowlog.Options{KeepExternalAddrs: true})
	p.Process(physicalRecord("127.3.3.40:"+relayedRegion), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("log records = %d, want 1", len(logs))
	}
	attrs := logs[0].Attrs
	if v, ok := attrs[semconv.AttrDstNode]; ok {
		t.Errorf("flow log carries %s=%q; the DERP marker is not a device", semconv.AttrDstNode, v)
	}
	if got := attrs[semconv.DestinationAddress]; got != "127.3.3.40" {
		t.Errorf("%s = %q, want the raw marker 127.3.3.40 — the log records what the wire said",
			semconv.DestinationAddress, got)
	}
	if got := attrs[semconv.DestinationPort]; got != relayedRegion {
		t.Errorf("%s = %q, want %q", semconv.DestinationPort, got, relayedRegion)
	}
	if got, want := attrs[semconv.AttrSrcNode], unverifiedName("gfmbp"); got != want {
		t.Errorf("%s = %q, want %q", semconv.AttrSrcNode, got, want)
	}
}

// TestRelayedPhysical_StoreKeepsAddressOmitsNode: the same rule on the in-memory
// view. The store already models this correctly for its path breakdown, which
// keys off the peer side; leaving DstNode filled here would still put the marker
// in the topology as a node with edges to it.
func TestRelayedPhysical_StoreKeepsAddressOmitsNode(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{KeepExternalAddrs: true})
	rec := telemetrytest.New()

	p.Process(physicalRecord("127.3.3.40:"+relayedRegion), rec.Emitter())

	o := only(t, fs)
	if o.DstNode != "" {
		t.Errorf("Observation.DstNode = %q, want empty; the DERP marker is not a device", o.DstNode)
	}
	if o.DstAddr != "127.3.3.40:"+relayedRegion {
		t.Errorf("Observation.DstAddr = %q, want the raw endpoint kept", o.DstAddr)
	}
	if o.Path != flowstore.PathDERP || o.DERPRegion != relayedRegion {
		t.Errorf("Path/DERPRegion = %q/%q, want derp/%s", o.Path, o.DERPRegion, relayedRegion)
	}
}

// TestRelayedPhysical_TotalsUnchanged: this drops labels, never data points. The
// bytes a relayed connection carried are still counted, on a series that simply
// no longer claims a destination device.
func TestRelayedPhysical_TotalsUnchanged(t *testing.T) {
	for _, mode := range []string{"all", "rollup"} {
		t.Run(mode, func(t *testing.T) {
			rec := telemetrytest.New()
			p := rollupProc(t, flowlog.Options{FlowMetricsMode: mode, NodeDims: true})
			p.Process(physicalRecord("127.3.3.40:"+relayedRegion), rec.Emitter())
			p.FlushRollup(rec.Emitter())

			metric := flowlog.MetricIO
			if mode == "rollup" {
				metric = flowlog.MetricIORollup
			}
			var total float64
			for _, pt := range rec.MetricPoints(metric) {
				total += pt.Value
			}
			if total != 148 {
				t.Errorf("total %s bytes = %v, want 148 (the record's txBytes)", metric, total)
			}
		})
	}
}
