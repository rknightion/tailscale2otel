package flowstore_test

import (
	"fmt"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// The destination-services section answers "what was talked TO". Only the
// overlay traffic types can answer it: a physical entry describes the WireGuard
// underlay, whose destination port is the ephemeral one the peer happened to be
// listening on — and on a relayed path is not a port at all, but the DERP region
// ID Tailscale writes where a port would go.
//
// These tests pin the ports table to overlay traffic, and pin that excluding the
// underlay changes nothing else about it.

// underlayPort builds a physical connection as the processor hands it over,
// carrying the underlay endpoint's port. Physical entries report proto 0, so the
// transport is unknown on every one of them.
func underlayPort(path, region, port string, bytes int64) flowstore.Observation {
	o := underlay("gfmbp", path, region, bytes)
	o.Transport = "unknown"
	o.DstPort = port
	return o
}

// portKeys flattens the ports table into the shape the page renders it in.
func portKeys(in []flowstore.PortStat) map[flowstore.PortKey]int64 {
	out := map[flowstore.PortKey]int64{}
	for _, p := range in {
		out[p.PortKey] = p.Counts.Bytes()
	}
	return out
}

// Both underlay shapes at once, because they are the same mistake in two
// disguises: a direct path contributes an ephemeral WireGuard port, a relayed
// one contributes a region ID. On a live tailnet the first is the higher-volume
// half — the two top rows of the section, by bytes, were ephemeral underlay
// ports — and the second is the more obviously wrong one.
func TestPorts_UnderlayPortsAreNotDestinationServices(t *testing.T) {
	m := newMemory(0)
	m.Record(obs(at, "web", "db", 100, 0))                                    // overlay: tcp/443 https
	m.Record(underlayPort(flowstore.PathDirectIPv4, "", "41641", 6_750_000))  // ephemeral underlay port
	m.Record(underlayPort(flowstore.PathDERP, "8", "8", 61))                  // a DERP region ID
	m.Record(underlayPort(flowstore.PathDirectIPv6, "", "59879", 17_000_000)) // and another

	got := portKeys(query(m).Ports)
	want := flowstore.PortKey{Port: "443", Transport: "tcp", Service: "https"}
	if len(got) != 1 || got[want] != 100 {
		t.Errorf("ports = %+v, want only the overlay service %+v", got, want)
	}
}

// Excluding the underlay from ONE section must not remove it from the page. The
// path breakdown is the section that reads physical traffic, and the totals,
// devices and traffic-type split all still describe every byte.
func TestPorts_PhysicalStillCountsEverywhereElse(t *testing.T) {
	m := newMemory(0)
	m.Record(obs(at, "web", "db", 100, 0))
	m.Record(underlayPort(flowstore.PathDERP, "8", "8", 900))

	res := query(m)
	if want := int64(1000); res.Totals.Bytes() != want {
		t.Errorf("total bytes = %d, want %d — dropping a dimension must not drop data", res.Totals.Bytes(), want)
	}
	if got := labelFlows(res.TrafficTypes)["physical"]; got != 1 {
		t.Errorf("physical flows = %d, want 1 (traffic types = %v)", got, labelFlows(res.TrafficTypes))
	}
	if got := labelFlows(res.Paths)[flowstore.PathDERP]; got != 1 {
		t.Errorf("relayed flows = %d, want 1 — the path section is where the underlay is read", got)
	}
	if len(res.PeerPaths) != 1 {
		t.Errorf("peer paths = %+v, want the relayed peer still named", res.PeerPaths)
	}
}

// The section is capped per bucket, and ephemeral underlay ports are unbounded
// by nature — a fresh one per connection. Letting them in does not just add
// wrong rows, it evicts right ones: past the cap the real services fold into
// __other__ and the page reports partial coverage of a table it could have
// covered completely.
func TestPorts_UnderlayChurnCannotEvictRealServices(t *testing.T) {
	m := newMemory(0)
	for i := range flowstore.MaxPortsPerBucket + 500 {
		m.Record(underlayPort(flowstore.PathDirectIPv4, "", fmt.Sprint(20000+i), 1))
	}
	m.Record(obs(at, "web", "db", 100, 0))

	res := query(m)
	got := portKeys(res.Ports)
	want := flowstore.PortKey{Port: "443", Transport: "tcp", Service: "https"}
	if got[want] != 100 {
		t.Errorf("https row = %d bytes, want 100 — underlay churn folded a real service into %s",
			got[want], flowstore.Other)
	}
	if res.Truncated != 0 {
		t.Errorf("truncated = %d, want 0 — nothing overflowed once the underlay stayed out", res.Truncated)
	}
}
