package flowstore_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/flowstore"
)

// fmtAddr mints a distinct peer address, for the bounding test.
func fmtAddr(i int) string { return fmt.Sprintf("100.64.%d.%d:0", i/256, i%256) }

// underlay builds one physical connection as the processor hands it over: the
// peer is the node the record resolved from the overlay address, and the path is
// how that peer was reached.
func underlay(peer, path, region string, bytes int64) flowstore.Observation {
	return flowstore.Observation{
		Time:        at,
		TrafficType: "physical",
		SrcNode:     peer,
		SrcAddr:     "100.64.0.9:0",
		Path:        path,
		DERPRegion:  region,
		Counts:      flowstore.Counts{TxBytes: bytes, Flows: 1},
	}
}

// peerPaths flattens the per-peer breakdown into a comparable shape.
func peerPaths(in []flowstore.PeerPathStat) map[string][2]int64 {
	out := map[string][2]int64{}
	for _, p := range in {
		out[p.Peer] = [2]int64{p.Direct.Bytes(), p.Relayed.Bytes()}
	}
	return out
}

func TestPaths_BreakdownCountsEachPathSeparately(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(underlay("camden", flowstore.PathDirectIPv4, "", 100))
	m.Record(underlay("camden", flowstore.PathDirectIPv6, "", 200))
	m.Record(underlay("gfmbp", flowstore.PathDERP, "8", 50))

	got := labelFlows(query(m).Paths)
	want := map[string]int64{
		flowstore.PathDirectIPv4: 1,
		flowstore.PathDirectIPv6: 1,
		flowstore.PathDERP:       1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("paths[%s] = %d, want %d (got %v)", k, got[k], v, got)
		}
	}
}

// Overlay traffic has no path. Counting it would put every tailnet connection
// into the direct column and drown the underlay measurement the section is for.
func TestPaths_IgnoreTrafficWithNoPath(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(evaluated(flowstore.VerdictPermitted, 0, false)) // virtual traffic
	m.Record(underlay("camden", flowstore.PathDirectIPv4, "", 100))

	res := query(m)
	if got := labelFlows(res.Paths); len(got) != 1 || got[flowstore.PathDirectIPv4] != 1 {
		t.Errorf("paths = %v, want only the one physical connection", got)
	}
	if len(res.PeerPaths) != 1 {
		t.Errorf("peer paths = %+v, want only the one physical connection", res.PeerPaths)
	}
}

// The question the section answers is "which of my peers is being relayed", so
// the per-peer split is the deliverable and the headline share is derived from it.
func TestPeerPaths_SplitDirectFromRelayedPerPeer(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(underlay("camden", flowstore.PathDirectIPv4, "", 1000))
	m.Record(underlay("camden", flowstore.PathDirectIPv6, "", 500))
	m.Record(underlay("camden", flowstore.PathDERP, "8", 100))
	m.Record(underlay("gfmbp", flowstore.PathDERP, "8", 900))

	got := peerPaths(query(m).PeerPaths)
	if want := [2]int64{1500, 100}; got["camden"] != want {
		t.Errorf("camden direct/relayed = %v, want %v — both IP families are direct", got["camden"], want)
	}
	if want := [2]int64{0, 900}; got["gfmbp"] != want {
		t.Errorf("gfmbp direct/relayed = %v, want %v", got["gfmbp"], want)
	}
}

// A peer being relayed is the finding; a peer moving bulk traffic directly is
// not. Ranking by volume alone would bury the one worth acting on.
func TestPeerPaths_RankRelayedFirst(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(underlay("bulk", flowstore.PathDirectIPv4, "", 1_000_000))
	m.Record(underlay("relayed", flowstore.PathDERP, "8", 10))

	got := query(m).PeerPaths
	if len(got) != 2 {
		t.Fatalf("peer paths = %+v, want 2", got)
	}
	if got[0].Peer != "relayed" {
		t.Errorf("first peer = %q, want the relayed one ahead of the busier direct one", got[0].Peer)
	}
}

// The region says WHERE traffic was relayed through, which is the difference
// between "my peers relay via London" and "my peers relay via Sydney".
func TestDERPRegions_CountedOnlyForRelayedTraffic(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(underlay("camden", flowstore.PathDERP, "8", 100))
	m.Record(underlay("gfmbp", flowstore.PathDERP, "8", 50))
	m.Record(underlay("oli", flowstore.PathDERP, "27", 25))
	// A relayed connection with an unreadable region still counts as relayed but
	// names no region.
	m.Record(underlay("odd", flowstore.PathDERP, "", 1))
	// Contradictory on purpose: a DIRECT path that nonetheless carries a region.
	// The processor never produces one, but the region breakdown means "which
	// relays carried the relayed share", so it must be keyed on the path rather
	// than on whichever field happens to be populated.
	m.Record(underlay("bulk", flowstore.PathDirectIPv4, "99", 999))

	res := query(m)
	got := labelFlows(res.DERPRegions)
	if want := map[string]int64{"8": 2, "27": 1}; len(got) != 2 || got["8"] != want["8"] || got["27"] != want["27"] {
		t.Errorf("regions = %v, want %v", got, want)
	}
	// The unreadable one is still relayed traffic and must not vanish from the
	// share the page reports.
	if labelFlows(res.Paths)[flowstore.PathDERP] != 4 {
		t.Errorf("relayed flows = %v, want all four counted", labelFlows(res.Paths))
	}
}

// The peer is named by its device name, never by its tags: a per-peer table
// keyed on tags would merge every tagged server into one row and answer a
// different question from the one being asked.
func TestPeerPaths_NameThePeerNotItsTags(t *testing.T) {
	m := flowstore.NewMemory(0)
	o := underlay("camden", flowstore.PathDERP, "8", 100)
	o.SrcTags = "tag:servers"
	m.Record(o)
	o2 := underlay("scotty", flowstore.PathDERP, "8", 100)
	o2.SrcTags = "tag:servers"
	m.Record(o2)

	got := query(m).PeerPaths
	if len(got) != 2 {
		t.Fatalf("peer paths = %+v, want one row per device, not one per tag", got)
	}
}

// An unresolved peer keeps its address: the collapse sentinels are not device
// names, and a row called "external" tells an operator nothing they can act on.
func TestPeerPaths_FallBackToTheAddress(t *testing.T) {
	m := flowstore.NewMemory(0)
	o := underlay("external", flowstore.PathDERP, "8", 100)
	o.SrcAddr = "100.64.0.9:0"
	m.Record(o)

	got := query(m).PeerPaths
	if len(got) != 1 || got[0].Peer != "100.64.0.9" {
		t.Errorf("peer = %+v, want the address rather than a sentinel", got)
	}
}

// Physical traffic arrives from the same ingress as everything else, so the
// per-peer dimension needs the same insert-time cap as the rest of the store.
func TestPeerPaths_IsBounded(t *testing.T) {
	m := flowstore.NewMemory(0)
	for i := range flowstore.MaxPeerPathsPerBucket * 2 {
		o := underlay("", flowstore.PathDERP, "8", 1)
		o.SrcAddr = fmtAddr(i)
		m.Record(o)
	}

	res := m.Query(flowstore.Query{Start: at.Add(-time.Hour), End: at.Add(time.Hour), TopN: 1 << 20})
	if len(res.PeerPaths) > flowstore.MaxPeerPathsPerBucket+1 {
		t.Errorf("peer paths = %d entries, want at most the cap plus the overflow row", len(res.PeerPaths))
	}
	// Everything still counts, whether or not it kept its own row.
	var total int64
	for _, p := range res.PeerPaths {
		total += p.Direct.Bytes() + p.Relayed.Bytes()
	}
	if want := int64(flowstore.MaxPeerPathsPerBucket * 2); total != want {
		t.Errorf("total bytes = %d, want %d — overflow must fold, not drop", total, want)
	}
	if res.Truncated == 0 {
		t.Error("truncated = 0; the page would imply complete coverage")
	}
}
