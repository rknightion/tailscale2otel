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
	a := newRollupAccumulator(500, true)
	// One fixed (transport, trafficType, dstService) group; vary only the node
	// dimensions so every record is a distinct rollupKey.
	for i := range maxRollupKeys + 5000 {
		node := strconv.Itoa(i)
		a.record("tcp", "virtual", node, node, "https", 1, 1, 1, 1)
	}
	if got := len(a.entries); got > maxRollupKeys+1 {
		t.Fatalf("entries = %d, want <= %d (insert-time cap + one __other__ fold)", got, maxRollupKeys+1)
	}
}

// TestRollupAccumulatorBoundsUniqueSetsUnderFlood asserts the per-source unique
// peer/port sets are bounded in both dimensions: the number of source-node
// buckets and each bucket's size.
func TestRollupAccumulatorBoundsUniqueSetsUnderFlood(t *testing.T) {
	a := newRollupAccumulator(500, true)
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
	b := newRollupAccumulator(500, true)
	for p := range maxUniquePerSrc + 1000 {
		b.observeUnique("hot", "peer", strconv.Itoa(p))
	}
	if got := len(b.dstPorts["hot"]); got > maxUniquePerSrc {
		t.Fatalf("dstPorts[hot] set = %d, want <= %d", got, maxUniquePerSrc)
	}
}
