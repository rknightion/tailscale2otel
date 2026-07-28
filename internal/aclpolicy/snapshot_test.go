package aclpolicy

import (
	"fmt"
	"testing"
)

// A store that only ever holds the newest policy cannot answer "what did rule 0
// say when this connection was observed?", which is the whole of #302. These
// tests pin the retention contract: superseded rule lists stay reachable by
// version, the history is bounded, and a version that has fallen out of it
// reports itself missing rather than returning today's rules.

func grantDoc(port string) string {
	return fmt.Sprintf(`{"grants":[{"src":["group:eng"],"dst":["tag:prod"],"ip":[%q]}]}`, port)
}

func TestStoreRetainsSupersededRuleLists(t *testing.T) {
	var s Store
	if err := s.SetDocument([]byte(grantDoc("443"))); err != nil {
		t.Fatalf("set first document: %v", err)
	}
	first := s.Policy().Version()

	if err := s.SetDocument([]byte(grantDoc("8443"))); err != nil {
		t.Fatalf("set second document: %v", err)
	}
	if s.Policy().Version() == first {
		t.Fatal("editing the document did not change the policy version; the rest of this test proves nothing")
	}

	rules, ok := s.Snapshot(first)
	if !ok {
		t.Fatal("the superseded policy is unreachable by version. Traffic observed under it can " +
			"then only be shown against the CURRENT rules, which is the misattribution #302 exists to stop")
	}
	if len(rules) != 1 {
		t.Fatalf("snapshot has %d rules, want 1", len(rules))
	}
	if got := rules[0].Source; got == "" {
		t.Error("retained rule has no source text, so it cannot be displayed to an operator")
	}
	// The retained list must be the OLD one, not an alias of the current.
	cur, ok := s.Snapshot(s.Policy().Version())
	if !ok {
		t.Fatal("the current policy is not in its own snapshot history")
	}
	if rules[0].Source == cur[0].Source {
		t.Errorf("the superseded snapshot returned the CURRENT rule text %q", cur[0].Source)
	}
}

func TestSnapshotReportsAnUnknownVersionMissing(t *testing.T) {
	var s Store
	if err := s.SetDocument([]byte(grantDoc("443"))); err != nil {
		t.Fatalf("set document: %v", err)
	}
	if rules, ok := s.Snapshot("0000000000000000"); ok {
		t.Errorf("an unknown version resolved to %d rules. It must report missing so the caller "+
			"says \"policy version unavailable\" instead of naming a rule that never applied", len(rules))
	}
}

func TestSnapshotOfTheEmptyStoreIsMissing(t *testing.T) {
	var s Store
	if _, ok := s.Snapshot("whatever"); ok {
		t.Error("the zero Store resolved a snapshot; it holds no policy at all")
	}
}

func TestSnapshotHistoryIsBounded(t *testing.T) {
	var s Store
	versions := make([]string, 0, MaxRetainedPolicies+5)
	for i := range MaxRetainedPolicies + 5 {
		if err := s.SetDocument([]byte(grantDoc(fmt.Sprintf("%d", 1000+i)))); err != nil {
			t.Fatalf("set document %d: %v", i, err)
		}
		versions = append(versions, s.Policy().Version())
	}
	if n := s.retained(); n > MaxRetainedPolicies {
		t.Errorf("store retains %d policies, cap is %d; an unbounded history is a memory leak an "+
			"operator cannot see", n, MaxRetainedPolicies)
	}
	// The newest MaxRetainedPolicies must survive: eviction is oldest-first, so
	// the versions most likely to be referenced by retained traffic are the ones kept.
	for _, v := range versions[len(versions)-MaxRetainedPolicies:] {
		if _, ok := s.Snapshot(v); !ok {
			t.Errorf("recent version %s was evicted while older ones could have been", v)
		}
	}
	if _, ok := s.Snapshot(versions[0]); ok {
		t.Errorf("the oldest version %s survived %d newer policies; eviction is not oldest-first",
			versions[0], len(versions)-1)
	}
}

func TestRecompilingTheSamePolicyDoesNotConsumeHistory(t *testing.T) {
	var s Store
	if err := s.SetDocument([]byte(grantDoc("443"))); err != nil {
		t.Fatalf("set document: %v", err)
	}
	first := s.Policy().Version()

	// Each directory update recompiles the SAME rules under the same version.
	// Counting the retained map would not catch a store that re-files them: the
	// key is identical, so the size never moves. The consequence is only visible
	// at the eviction boundary, so drive it there.
	for i := range MaxRetainedPolicies * 2 {
		if err := s.SetDirectory(Directory{Roles: map[string]string{
			fmt.Sprintf("u%d@example.com", i): "admin",
		}}); err != nil {
			t.Fatalf("set directory %d: %v", i, err)
		}
	}
	if _, ok := s.Snapshot(first); !ok {
		t.Errorf("the only policy ever compiled was evicted after %d recompiles of the SAME rules. "+
			"A store that spends a history slot per recompile evicts the versions retained traffic "+
			"actually refers to, while the rule list never changed at all", MaxRetainedPolicies*2)
	}
}
