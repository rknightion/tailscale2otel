package flowlog_test

import (
	"strconv"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// identityFlow is one virtual connection whose record carries the endpoint
// identity blocks the control plane embeds, so no API call is needed to resolve
// who the two ends belong to.
func identityFlow() flowlog.FlowLog {
	return flowlog.FlowLog{
		NodeID: "nLaptop",
		SrcNode: &flowlog.NodeRef{
			NodeID: "nLaptop", Name: "laptop", Addresses: []string{"100.64.0.1"},
			User: "rob@example.com", OS: "macOS",
		},
		DstNodes: []flowlog.NodeRef{{
			NodeID: "nServer", Name: "server", Addresses: []string{"100.64.0.2"},
			Tags: []string{"tag:servers", "tag:prod"}, OS: "linux",
		}},
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: protoTCP, Src: "100.64.0.1:50000", Dst: "100.64.0.2:443", TxBytes: 1000, TxPkts: 10},
		},
	}
}

// TestRollupCarriesIdentity: identity_dims reaches the rollup families, not just
// the raw ones. This is the whole point of the change — rollup is the product
// default, so a dimension the raw families alone carry is invisible to most
// deployments, and the toggle reads as a lie.
func TestRollupCarriesIdentity(t *testing.T) {
	rec := telemetrytest.New()
	p := rollupProc(t, flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true, IdentityDims: true})
	p.Process(identityFlow(), rec.Emitter())
	p.FlushRollup(rec.Emitter())

	pts := rec.MetricPoints(flowlog.MetricIORollup)
	if len(pts) == 0 {
		t.Fatalf("no rollup points emitted")
	}
	want := map[string]string{
		semconv.AttrSrcUser: "rob@example.com",
		semconv.AttrSrcOS:   "macOS",
		semconv.AttrDstTags: "tag:prod,tag:servers",
		semconv.AttrDstOS:   "linux",
	}
	for k, v := range want {
		if got := pts[0].Attrs[k]; got != v {
			t.Errorf("io.rollup %s = %q, want %q", k, got, v)
		}
	}
	// The source node carries no tags and the destination no user, and neither
	// record field was populated — so both attributes stay absent rather than
	// becoming empty labels. Absent means "the record carried nothing"; an empty
	// label would claim it carried a blank value.
	for _, k := range []string{semconv.AttrSrcTags, semconv.AttrDstUser} {
		if v, ok := pts[0].Attrs[k]; ok {
			t.Errorf("%s should be absent, got %q", k, v)
		}
	}
}

// TestRollupOmitsIdentityWhenOff is the default-path guard: identity is
// PII-adjacent (user is an email address), so it must stay off the metric surface
// unless asked for.
func TestRollupOmitsIdentityWhenOff(t *testing.T) {
	rec := telemetrytest.New()
	p := rollupProc(t, flowlog.Options{FlowMetricsMode: "rollup", NodeDims: true})
	p.Process(identityFlow(), rec.Emitter())
	p.FlushRollup(rec.Emitter())

	for _, pt := range rec.MetricPoints(flowlog.MetricIORollup) {
		for _, k := range []string{semconv.AttrSrcUser, semconv.AttrSrcOS, semconv.AttrDstTags, semconv.AttrDstOS} {
			if v, ok := pt.Attrs[k]; ok {
				t.Errorf("%s present with identity_dims off: %q", k, v)
			}
		}
	}
}

// TestRollupOtherFoldDropsIdentity pins the fold invariant. The __other__
// remainder is by definition many nodes and therefore many users, so it has no
// single identity to report — and carrying real identity into the fold would
// break the bound that makes the fold safe in the first place, since the folded
// key space is only finite while every field in it comes from a fixed table.
func TestRollupOtherFoldDropsIdentity(t *testing.T) {
	rec := telemetrytest.New()
	// topN=1 forces everything past the busiest pair into the fold.
	p := rollupProc(t, flowlog.Options{
		FlowMetricsMode: "rollup", NodeDims: true, IdentityDims: true, RollupTopN: 1,
	})
	for i := range 5 {
		s := strconv.Itoa(i)
		p.Process(flowlog.FlowLog{
			NodeID:  "nLaptop",
			SrcNode: &flowlog.NodeRef{NodeID: "n" + s, Name: "host" + s, Addresses: []string{"100.64.1." + s}, User: "user" + s + "@example.com"},
			VirtualTraffic: []flowlog.ConnectionCounts{
				// Descending bytes so the fold membership is deterministic.
				{Proto: protoTCP, Src: "100.64.1." + s + ":50000", Dst: "100.64.0.2:443", TxBytes: int64(1000 - i*100), TxPkts: 1},
			},
		}, rec.Emitter())
	}
	p.FlushRollup(rec.Emitter())

	var folded int
	for _, pt := range rec.MetricPoints(flowlog.MetricIORollup) {
		if pt.Attrs[semconv.AttrSrcNode] != semconv.RollupOther {
			continue
		}
		folded++
		if v, ok := pt.Attrs[semconv.AttrSrcUser]; ok {
			t.Errorf("__other__ fold carries %s = %q, want it dropped", semconv.AttrSrcUser, v)
		}
	}
	if folded == 0 {
		t.Fatal("no __other__ series emitted; the fold was not exercised")
	}
}
