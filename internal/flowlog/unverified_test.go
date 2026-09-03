package flowlog_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// GHSA-pjfv-prc8-4fc9. srcNode/dstNodes are written by the node that reported
// the record, so a compromised node can claim any identity for any address. The
// processor feeds them to the device cache, which everything else reads.

// poisonRecord is a flow record whose embedded identity claims addr belongs to a
// node called claim.
func poisonRecord(nodeID, claim, addr string) flowlog.FlowLog {
	return flowlog.FlowLog{
		NodeID: nodeID,
		Start:  time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
		End:    time.Date(2026, 7, 25, 10, 1, 0, 0, time.UTC),
		SrcNode: &flowlog.NodeRef{
			NodeID:    nodeID,
			Name:      claim + ".tail1a2b.ts.net",
			Addresses: []string{addr},
		},
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: addr + ":443", Dst: "100.64.0.1:51820", TxBytes: 1, TxPkts: 1},
		},
	}
}

// A name only a flow record ever claimed must be marked wherever it surfaces,
// so a spoofed name is never indistinguishable from a control-plane one.
func TestUnverified_ClaimedNamesAreMarked(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{
		{NodeID: "nLaptop", Hostname: "laptop", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
	})
	p := flowlog.NewProcessor(c, flowlog.Options{LogMode: "per_connection", NodeDims: true})
	rec := telemetrytest.New()

	p.Process(poisonRecord("nEvil", "prod-db", "100.64.9.9"), rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("got %d log records, want 1", len(logs))
	}
	if got, want := logs[0].Attrs[semconv.AttrSrcNode], "unverified:prod-db"; got != want {
		t.Errorf("%s = %q, want %q — a node-claimed name must carry its provenance", semconv.AttrSrcNode, got, want)
	}
	// The authoritative endpoint on the very same connection stays unmarked.
	if got, want := logs[0].Attrs[semconv.AttrDstNode], "laptop"; got != want {
		t.Errorf("%s = %q, want %q — the devices collector confirmed this one", semconv.AttrDstNode, got, want)
	}
	// tailscale.node.hostname is resolved from the reporting node ID, and takes
	// the same rule.
	if got, want := logs[0].Attrs["tailscale.node.hostname"], "unverified:prod-db"; got != want {
		t.Errorf("tailscale.node.hostname = %q, want %q", got, want)
	}
	pts := rec.MetricPoints(flowlog.MetricIO)
	if len(pts) == 0 {
		t.Fatal("no io points emitted")
	}
	for _, pt := range pts {
		if got := pt.Attrs[semconv.AttrSrcNode]; got != "unverified:prod-db" {
			t.Errorf("metric %s = %q, want the marked name", semconv.AttrSrcNode, got)
		}
	}
}

// The poisoning itself: one node's claim must not decide what a LATER record
// says about that address, and must not survive the devices collector's next
// refresh.
func TestUnverified_ClaimDoesNotPoisonLaterRecords(t *testing.T) {
	c := enrich.NewDeviceCache()
	p := flowlog.NewProcessor(c, flowlog.Options{LogMode: "per_connection"})

	// The devices collector has a current view of the tailnet...
	c.Replace([]enrich.DeviceMeta{
		{NodeID: "nServer", Hostname: "server", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.2")}},
		{NodeID: "nLaptop", Hostname: "laptop", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
	})
	// ...and a compromised node then claims two identities that do not exist:
	// one for an address the control plane already owns, one for a fresh address.
	p.Process(poisonRecord("nEvil", "prod-db", "100.64.0.2"), telemetrytest.New().Emitter())
	p.Process(poisonRecord("nGhost", "prod-db-2", "100.64.9.9"), telemetrytest.New().Emitter())

	rec := telemetrytest.New()
	p.Process(flowlog.FlowLog{
		NodeID: "nLaptop",
		Start:  time.Date(2026, 7, 25, 10, 2, 0, 0, time.UTC),
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.2:443", Dst: "100.64.0.1:51820", TxBytes: 1, TxPkts: 1},
		},
	}, rec.Emitter())

	logs := rec.LogRecords()
	if len(logs) != 1 {
		t.Fatalf("got %d log records, want 1", len(logs))
	}
	if got := logs[0].Attrs[semconv.AttrSrcNode]; got != "server" {
		t.Errorf("%s = %q, want the authoritative %q — an earlier node claim decided what a later record says",
			semconv.AttrSrcNode, got, "server")
	}
	// And neither claimed identity became a device. Snapshot is what the admin
	// status device table and the node-metrics scrape target list are built from,
	// so a claim landing there picks what the operator sees and what this process
	// connects out to.
	for _, id := range []string{"nEvil", "nGhost"} {
		if m, ok := c.LookupNode(id); ok {
			t.Errorf("claimed identity %s entered the authoritative tier: %+v", id, m)
		}
	}
	if snap := c.Snapshot(); len(snap) != 2 {
		t.Errorf("Snapshot has %d devices, want 2 — a claim reached the device table: %+v", len(snap), snap)
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d, want 2 — enrich.cache_size inflated by node claims", c.Len())
	}
}
