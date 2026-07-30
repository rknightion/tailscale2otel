package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// seedUnderlay pushes one flow record whose physical traffic reaches the same
// peer twice — once directly, once relayed — through the runtime's own
// processor, so the test exercises the real path from record to API.
func seedUnderlay(t *testing.T, a *App) {
	t.Helper()
	rec := telemetrytest.New()
	at := time.Now().Add(-30 * time.Second).UTC()
	a.runtimes[0].flowProc.Process(flowlog.FlowLog{
		NodeID: "nSrc", Start: at, End: at.Add(5 * time.Second), Logged: time.Now().UTC(),
		SrcNode: &flowlog.NodeRef{NodeID: "nSrc", Name: "camden.example.ts.net",
			Addresses: []string{"100.64.0.1"}},
		DstNodes: []flowlog.NodeRef{
			{NodeID: "nDst", Name: "mbp16.example.ts.net", Addresses: []string{"100.64.0.2"}},
		},
		PhysicalTraffic: []flowlog.ConnectionCounts{
			{Src: "100.64.0.2:0", Dst: "10.0.0.9:41641", TxBytes: 900, TxPkts: 9},
			{Src: "100.64.0.2:0", Dst: "127.3.3.40:8", TxBytes: 100, TxPkts: 1},
		},
	}, rec.Emitter())
}

// End to end: a real record through the real processor, classified, aggregated
// by the real store, out through the real handler.
func TestFlowsJSON_PathQualityReachesTheAPI(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedUnderlay(t, a)

	_, got := getFlows(t, a, "")

	paths := map[string]int64{}
	for _, p := range got.Result.Paths {
		paths[p.Label] = p.Counts.Flows
	}
	if paths[flowstore.PathDirectIPv4] != 1 || paths[flowstore.PathDERP] != 1 {
		t.Errorf("paths = %v, want one direct and one relayed", paths)
	}
	if len(got.Result.DERPRegions) != 1 || got.Result.DERPRegions[0].Label != "8" {
		t.Errorf("derp regions = %+v, want region 8", got.Result.DERPRegions)
	}
	if len(got.Result.PeerPaths) != 1 {
		t.Fatalf("peer paths = %+v, want the one peer", got.Result.PeerPaths)
	}
	p := got.Result.PeerPaths[0]
	if p.Peer != unverifiedName("mbp16") {
		t.Errorf("peer = %q, want %q — the peer is the far end, not the reporting node", p.Peer, unverifiedName("mbp16"))
	}
	if p.Direct.Bytes() != 900 || p.Relayed.Bytes() != 100 {
		t.Errorf("direct/relayed = %d/%d, want 900/100", p.Direct.Bytes(), p.Relayed.Bytes())
	}
}

// The connection list is where an operator drills in after seeing a relayed
// peer, so each raw connection has to say which of the two it was.
func TestFlowsJSON_RecentConnectionsCarryTheirPath(t *testing.T) {
	a := flowsTestApp(t, nil)
	seedUnderlay(t, a)
	seedFlows(t, a) // overlay traffic, which has no path at all

	_, got := getFlows(t, a, "")
	var relayed, direct, unpathed int
	for _, r := range got.Recent {
		switch r.Path {
		case flowstore.PathDERP:
			relayed++
			if r.DERPRegion != "8" {
				t.Errorf("relayed connection region = %q, want 8", r.DERPRegion)
			}
		case flowstore.PathDirectIPv4:
			direct++
		case "":
			unpathed++
		default:
			t.Errorf("unexpected path %q", r.Path)
		}
	}
	if relayed != 1 || direct != 1 || unpathed != 1 {
		t.Errorf("relayed/direct/unpathed = %d/%d/%d, want 1/1/1", relayed, direct, unpathed)
	}
}
