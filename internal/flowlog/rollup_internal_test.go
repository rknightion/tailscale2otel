package flowlog

import (
	"strconv"
	"testing"
)

// TestRollupAccumulatorBoundsEntriesUnderKeyFlood asserts the live entries map is
// capped at insert time. A flood of distinct rollup keys (the attacker-controlled
// src/dst-address case) must fold into the single per-group __other__ remainder
// rather than growing one map entry per key until the next 60s flush.
func TestRollupAccumulatorBoundsEntriesUnderKeyFlood(t *testing.T) {
	a := newRollupAccumulator(500, true, false, false)
	// One fixed (transport, trafficType, dstService) group; vary only the node
	// dimensions so every record is a distinct rollupKey.
	for i := range maxRollupKeys + 5000 {
		node := strconv.Itoa(i)
		a.record(rollupDims{
			transport: "tcp", trafficType: "virtual",
			srcNode: node, dstNode: node, dstService: "https",
		}, 1, 1, 1, 1)
	}
	if got := len(a.entries); got > maxRollupKeys+1 {
		t.Fatalf("entries = %d, want <= %d (insert-time cap + one __other__ fold)", got, maxRollupKeys+1)
	}
}

// TestRollupAccumulatorBoundsEntriesUnderIdentityFlood pins the same insert-time
// cap against identity, which is NEW key material reachable from the stream
// receiver: the identity fields come from the node blocks embedded in a pushed
// flow record, and that receiver can be run unauthenticated. A flood of distinct
// users/tags must fold into __other__ exactly like a flood of node names does,
// rather than growing one live map entry per identity until the next flush.
func TestRollupAccumulatorBoundsEntriesUnderIdentityFlood(t *testing.T) {
	a := newRollupAccumulator(500, true, true, false)
	// Hold the node dimensions FIXED so identity is the only thing varying —
	// otherwise the node cap would be what bounds this and the test would pass
	// without exercising identity at all.
	for i := range maxRollupKeys + 5000 {
		s := strconv.Itoa(i)
		a.record(rollupDims{
			transport: "tcp", trafficType: "virtual",
			srcNode: "laptop", dstNode: "server", dstService: "https",
			identity: identityKey{srcUser: "u" + s + "@example.com", srcTags: "tag:" + s},
		}, 1, 1, 1, 1)
	}
	if got := len(a.entries); got > maxRollupKeys+1 {
		t.Fatalf("entries = %d, want <= %d (insert-time cap + one __other__ fold)", got, maxRollupKeys+1)
	}
}

// TestRollupAccumulatorRefusesIdentityWithoutNodes pins the gate: identity is
// node-derived, so with node dimensions off it would stop being a free widening
// of an existing series and become the only dimension splitting the rollup —
// reintroducing the cardinality that turning node_dims off is meant to shed.
func TestRollupAccumulatorRefusesIdentityWithoutNodes(t *testing.T) {
	a := newRollupAccumulator(500, false, true, false)
	if a.identity {
		t.Fatal("identity must be forced off when node dimensions are off")
	}
	if a.wantsIdentity() {
		t.Fatal("wantsIdentity must report false so the caller skips resolving node blocks")
	}
	a.record(rollupDims{
		transport: "tcp", trafficType: "virtual",
		identity: identityKey{srcUser: "rob@example.com"},
	}, 1, 1, 1, 1)
	for k := range a.entries {
		if k.identity != (identityKey{}) {
			t.Fatalf("key carries identity despite the gate: %+v", k.identity)
		}
	}
}

// TestRollupAccumulatorBoundsUniqueSetsUnderFlood asserts the per-source unique
// peer/port sets are bounded in both dimensions: the number of source-node
// buckets and each bucket's size.
func TestRollupAccumulatorBoundsUniqueSetsUnderFlood(t *testing.T) {
	a := newRollupAccumulator(500, true, false, false)
	for i := range maxUniqueSrcNodes + 2000 {
		s := strconv.Itoa(i)
		a.observeUnique("src"+s, "peer"+s, s)
	}
	if got := len(a.dstPeers); got > maxUniqueSrcNodes {
		t.Fatalf("dstPeers source buckets = %d, want <= %d", got, maxUniqueSrcNodes)
	}
	if got := len(a.dstPorts); got > maxUniqueSrcNodes {
		t.Fatalf("dstPorts source buckets = %d, want <= %d", got, maxUniqueSrcNodes)
	}

	// A single source flooding distinct ports saturates its set, not grows it.
	b := newRollupAccumulator(500, true, false, false)
	for p := range maxUniquePerSrc + 1000 {
		b.observeUnique("hot", "peer", strconv.Itoa(p))
	}
	if got := len(b.dstPorts["hot"]); got > maxUniquePerSrc {
		t.Fatalf("dstPorts[hot] set = %d, want <= %d", got, maxUniquePerSrc)
	}
}
