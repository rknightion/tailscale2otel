package flowstore_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

// Rule identity in the store is (policy version, rule index), never the index
// alone. A retention window that spans a policy edit holds observations whose
// index 0 meant two different rules, and folding them together reports traffic
// against a rule that never carried it (#302).

const (
	verA = "aaaaaaaaaaaaaaaa"
	verB = "bbbbbbbbbbbbbbbb"
)

func permitted(at time.Time, version string, rule int) flowstore.Observation {
	o := obs(at, "a", "b", 10, 10)
	o.Verdict = flowstore.VerdictPermitted
	o.PolicyVersion = version
	o.Rule = rule
	return o
}

func TestRuleStatsDoNotFoldAcrossPolicyVersions(t *testing.T) {
	s := newMemory(0)
	// The reorder case: the same index under two different policies.
	s.Record(permitted(base.Add(-time.Minute), verA, 0))
	s.Record(permitted(base, verB, 0))

	res := s.Query(flowstore.Query{})
	if len(res.Rules) != 2 {
		t.Fatalf("got %d rule stats, want 2 (one per policy version): %+v\n"+
			"Folding them reports the older traffic against the NEW rule 0, which after a reorder "+
			"is a different rule entirely — the misattribution #302 exists to prevent.",
			len(res.Rules), res.Rules)
	}
	seen := map[string]int64{}
	for _, r := range res.Rules {
		if r.Rule != 0 {
			t.Errorf("rule index %d, want 0", r.Rule)
		}
		seen[r.PolicyVersion] += r.Counts.Flows
	}
	for _, v := range []string{verA, verB} {
		if got := seen[v]; got != 1 {
			t.Errorf("policy version %s carries %d flows, want 1 (seen: %v)", v, got, seen)
		}
	}
}

func TestRuleStatsStillAggregateWithinOneVersion(t *testing.T) {
	s := newMemory(0)
	s.Record(permitted(base.Add(-time.Minute), verA, 0))
	s.Record(permitted(base, verA, 0))

	res := s.Query(flowstore.Query{})
	if len(res.Rules) != 1 {
		t.Fatalf("got %d rule stats, want 1: %+v. Versioning must not stop the ordinary case — "+
			"the same rule under an unchanged policy — from aggregating.", len(res.Rules), res.Rules)
	}
	if got := res.Rules[0].Counts.Flows; got != 2 {
		t.Errorf("flows = %d, want 2", got)
	}
}

func TestRecentConnectionsCarryThePolicyVersion(t *testing.T) {
	s := newMemory(0)
	s.Record(permitted(base, verA, 3))

	recent := s.Recent(10)
	if len(recent) != 1 {
		t.Fatalf("got %d recent rows, want 1", len(recent))
	}
	if got := recent[0].PolicyVersion; got != verA {
		t.Errorf("recent row policy version = %q, want %q. Without it the drill-down list joins its "+
			"rule index to whatever the policy says today", got, verA)
	}
}
