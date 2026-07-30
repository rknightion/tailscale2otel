package flowstore_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
)

// TestDefaultProfileMatchesHardcodedBehavior proves #329's "safe defaults
// remain unchanged" acceptance criterion: a store built with no capacity
// option (the pre-#329 call shape every existing test and call site uses)
// reports exactly today's hardcoded limits, unchanged.
func TestDefaultProfileMatchesHardcodedBehavior(t *testing.T) {
	s := flowstore.NewMemory(0)
	lim := s.Limits()

	if lim.Profile != flowstore.ProfileDefault {
		t.Errorf("Profile = %q, want %q", lim.Profile, flowstore.ProfileDefault)
	}
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"MaxPairsPerBucket", lim.MaxPairsPerBucket, flowstore.MaxPairsPerBucket},
		{"MaxNodesPerBucket", lim.MaxNodesPerBucket, flowstore.MaxNodesPerBucket},
		{"MaxPortsPerBucket", lim.MaxPortsPerBucket, flowstore.MaxPortsPerBucket},
		{"MaxLabelsPerKind", lim.MaxLabelsPerKind, flowstore.MaxLabelsPerKind},
		{"MaxMatrixCellsPerBucket", lim.MaxMatrixCellsPerBucket, flowstore.MaxMatrixCellsPerBucket},
		{"MaxUnexplainedPerBucket", lim.MaxUnexplainedPerBucket, flowstore.MaxUnexplainedPerBucket},
		{"MaxRulesPerBucket", lim.MaxRulesPerBucket, flowstore.MaxRulesPerBucket},
		{"MaxPeerPathsPerBucket", lim.MaxPeerPathsPerBucket, flowstore.MaxPeerPathsPerBucket},
		{"MaxRecent", lim.MaxRecent, flowstore.MaxRecent},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d (the hardcoded pre-#329 value)", c.name, c.got, c.want)
		}
	}
	if lim.EstimatedBytes <= 0 {
		t.Errorf("EstimatedBytes = %d, want > 0", lim.EstimatedBytes)
	}
}

// TestWithCapacityProfileDefault proves WithCapacityProfile(ProfileDefault)
// is indistinguishable from the zero-option store — the option exists to let
// a caller assert the profile explicitly, not to change default behavior.
func TestWithCapacityProfileDefault(t *testing.T) {
	s := flowstore.NewMemory(0, flowstore.WithCapacityProfile(flowstore.ProfileDefault))
	got := s.Limits()
	want := flowstore.NewMemory(0).Limits()
	got.BucketCapacity, want.BucketCapacity = 0, 0 // both use the same capacity(0) default anyway
	if got != want {
		t.Errorf("Limits() with explicit ProfileDefault = %+v, want %+v (zero-option store)", got, want)
	}
}

// TestCapacityProfilesAreBounded proves every named profile is a fixed,
// hard-coded preset that scales EVERY dimension in the same direction — an
// operator can trade memory for fidelity only by picking one of three known
// points, never an arbitrary/unbounded number (#329 "do not expose unbounded
// raw limits").
func TestCapacityProfilesAreBounded(t *testing.T) {
	compact := mustCaps(t, flowstore.ProfileCompact)
	def := mustCaps(t, flowstore.ProfileDefault)
	expanded := mustCaps(t, flowstore.ProfileExpanded)

	dims := []struct {
		name           string
		compact, d, ex int
	}{
		{"MaxPairsPerBucket", compact.MaxPairsPerBucket, def.MaxPairsPerBucket, expanded.MaxPairsPerBucket},
		{"MaxNodesPerBucket", compact.MaxNodesPerBucket, def.MaxNodesPerBucket, expanded.MaxNodesPerBucket},
		{"MaxPortsPerBucket", compact.MaxPortsPerBucket, def.MaxPortsPerBucket, expanded.MaxPortsPerBucket},
		{"MaxLabelsPerKind", compact.MaxLabelsPerKind, def.MaxLabelsPerKind, expanded.MaxLabelsPerKind},
		{"MaxMatrixCellsPerBucket", compact.MaxMatrixCellsPerBucket, def.MaxMatrixCellsPerBucket, expanded.MaxMatrixCellsPerBucket},
		{"MaxUnexplainedPerBucket", compact.MaxUnexplainedPerBucket, def.MaxUnexplainedPerBucket, expanded.MaxUnexplainedPerBucket},
		{"MaxRulesPerBucket", compact.MaxRulesPerBucket, def.MaxRulesPerBucket, expanded.MaxRulesPerBucket},
		{"MaxPeerPathsPerBucket", compact.MaxPeerPathsPerBucket, def.MaxPeerPathsPerBucket, expanded.MaxPeerPathsPerBucket},
		{"MaxRecent", compact.MaxRecent, def.MaxRecent, expanded.MaxRecent},
	}
	for _, dm := range dims {
		if dm.compact >= dm.d || dm.d >= dm.ex {
			t.Errorf("%s: compact=%d default=%d expanded=%d, want strictly increasing", dm.name, dm.compact, dm.d, dm.ex)
		}
		// Hard safety ceiling: expanded must never exceed 4x default on any
		// dimension, so a "trade memory for fidelity" choice can never become
		// an effectively unbounded one.
		if dm.ex > dm.d*4 {
			t.Errorf("%s: expanded=%d exceeds the 4x-default hard ceiling (default=%d)", dm.name, dm.ex, dm.d)
		}
	}

	if _, ok := flowstore.CapsForProfile("unbounded"); ok {
		t.Error("CapsForProfile(\"unbounded\") = ok, want a rejected/unknown profile")
	}
	if _, ok := flowstore.CapsForProfile(""); ok {
		t.Error(`CapsForProfile("") = ok, want a rejected/unknown profile (callers must name one of the three)`)
	}
}

func mustCaps(t *testing.T, profile string) flowstore.Caps {
	t.Helper()
	c, ok := flowstore.CapsForProfile(profile)
	if !ok {
		t.Fatalf("CapsForProfile(%q) not ok", profile)
	}
	return c
}

// TestLimitsReflectsConfiguredProfile proves a store built with a non-default
// profile reports it back faithfully, including a proportionally larger
// EstimatedBytes — this is the seam the admin status page will read (#329).
func TestLimitsReflectsConfiguredProfile(t *testing.T) {
	compact := flowstore.NewMemory(10, flowstore.WithCapacityProfile(flowstore.ProfileCompact)).Limits()
	def := flowstore.NewMemory(10, flowstore.WithCapacityProfile(flowstore.ProfileDefault)).Limits()
	expanded := flowstore.NewMemory(10, flowstore.WithCapacityProfile(flowstore.ProfileExpanded)).Limits()

	if compact.Profile != flowstore.ProfileCompact || def.Profile != flowstore.ProfileDefault || expanded.Profile != flowstore.ProfileExpanded {
		t.Fatalf("profile names not round-tripped: compact=%q default=%q expanded=%q", compact.Profile, def.Profile, expanded.Profile)
	}
	if compact.EstimatedBytes >= def.EstimatedBytes || def.EstimatedBytes >= expanded.EstimatedBytes {
		t.Errorf("EstimatedBytes not monotonic in profile size: compact=%d default=%d expanded=%d",
			compact.EstimatedBytes, def.EstimatedBytes, expanded.EstimatedBytes)
	}
	if compact.MaxPairsPerBucket >= def.MaxPairsPerBucket {
		t.Errorf("compact.MaxPairsPerBucket=%d not below default=%d", compact.MaxPairsPerBucket, def.MaxPairsPerBucket)
	}
	if got, want := def.BucketCapacity, 10; got != want {
		t.Errorf("BucketCapacity = %d, want %d (the constructor's capacity argument, independent of profile)", got, want)
	}
}

// TestUnknownCapacityProfileOptionFailsSafe proves WithCapacityProfile with
// an unrecognized name does NOT silently produce an unbounded store: it is
// ignored and the store keeps the safe ProfileDefault caps. Config.Validate()
// is what actually rejects a bad value before the app ever gets here; this is
// defense in depth inside the package itself.
func TestUnknownCapacityProfileOptionFailsSafe(t *testing.T) {
	s := flowstore.NewMemory(0, flowstore.WithCapacityProfile("not-a-real-profile"))
	lim := s.Limits()
	if lim.Profile != flowstore.ProfileDefault {
		t.Errorf("Profile = %q after an unknown profile option, want fail-safe %q", lim.Profile, flowstore.ProfileDefault)
	}
	if lim.MaxRecent != flowstore.MaxRecent {
		t.Errorf("MaxRecent = %d after an unknown profile option, want the safe default %d", lim.MaxRecent, flowstore.MaxRecent)
	}
}

// TestCapacityProfileGovernsPerBucketCapsAtRuntime proves the profile is not
// merely reported by Limits() but actually enforced: a compact-profile store
// folds into Other well before the default profile's MaxPairsPerBucket cap.
//
// Every field of PairKey (Src, Dst, TrafficType) also feeds its OWN bounded
// dimension (nodes, again nodes, and the trafficTypes label map
// respectively), each with a smaller compact cap than MaxPairsPerBucket. So
// varying any single field to grow pair cardinality would overflow that
// field's own dimension first and mask a broken pairs guard behind an
// unrelated Truncated increment. This test instead holds TrafficType
// constant (one label, nowhere near its cap) and takes the CROSS PRODUCT of a
// small, below-cap set of Src and Dst node names — enough distinct (Src,Dst)
// pairs to exceed the compact pairs cap while the total distinct node count
// (Src ∪ Dst) stays well under the compact node cap.
func TestCapacityProfileGovernsPerBucketCapsAtRuntime(t *testing.T) {
	clock := func() time.Time { return base }
	s := flowstore.NewMemory(1, flowstore.WithClock(clock), flowstore.WithCapacityProfile(flowstore.ProfileCompact))
	lim := s.Limits()
	compactPairsCap := lim.MaxPairsPerBucket
	if compactPairsCap >= flowstore.MaxPairsPerBucket {
		t.Fatalf("compact MaxPairsPerBucket = %d, want below the default %d", compactPairsCap, flowstore.MaxPairsPerBucket)
	}

	// side must be large enough that side*side exceeds compactPairsCap, and
	// 2*side (the Src ∪ Dst node count, since the two name sets are disjoint)
	// must stay under compactNodesCap.
	side := 1
	for side*side <= compactPairsCap {
		side++
	}
	if 2*side >= lim.MaxNodesPerBucket {
		t.Fatalf("cross-product isolation broke down: 2*side=%d, compact node cap=%d — the profile's dimensions shrank in a way this test no longer assumes", 2*side, lim.MaxNodesPerBucket)
	}

	var recorded int64
	for si := range side {
		for di := range side {
			s.Record(flowstore.Observation{
				Time:        base,
				TrafficType: "virtual",
				Transport:   "tcp",
				SrcNode:     "src" + strconv.Itoa(si),
				DstNode:     "dst" + strconv.Itoa(di),
				Counts:      flowstore.Counts{TxBytes: 1, Flows: 1},
			})
			recorded++
		}
	}
	if recorded <= int64(compactPairsCap) {
		t.Fatalf("recorded %d unique pairs, want more than the compact cap %d", recorded, compactPairsCap)
	}

	res := s.Query(flowstore.Query{TopN: -1})
	if got := res.Truncated; got == 0 {
		t.Error("Truncated = 0, want overflow counted once the compact pairs cap was exceeded")
	}
	if res.Totals.TxBytes != recorded {
		t.Errorf("total tx = %d, want %d — folding must not lose bytes", res.Totals.TxBytes, recorded)
	}
}
