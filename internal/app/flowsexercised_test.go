package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

// The API resolves every exercised-rule row against the policy version that
// produced it, server-side. The alternative — shipping bare indexes and letting
// a consumer join them to Policy.Rules — is the bug: after a reorder, that join
// silently names a rule the traffic never touched (#302).

func policyStoreWith(t *testing.T, docs ...string) *aclpolicy.Store {
	t.Helper()
	var s aclpolicy.Store
	for _, d := range docs {
		if err := s.SetDocument([]byte(d)); err != nil {
			t.Fatalf("compile %s: %v", d, err)
		}
	}
	return &s
}

const docV1 = `{"grants":[{"src":["group:eng"],"dst":["tag:prod"],"ip":["443"]}]}`
const docV2 = `{"grants":[{"src":["group:ops"],"dst":["tag:db"],"ip":["5432"]}]}`

func TestExercisedRulesResolveAgainstTheirOwnPolicyVersion(t *testing.T) {
	s := policyStoreWith(t, docV1, docV2)
	old, err := versionOf(s, docV1)
	if err != nil {
		t.Fatal(err)
	}
	cur := s.Policy().Version()
	if old == cur {
		t.Fatal("the two documents share a version; the rest of this test proves nothing")
	}

	got := exercisedRules(s, []flowstore.RuleStat{
		{Rule: 0, PolicyVersion: old},
		{Rule: 0, PolicyVersion: cur},
	})
	if len(got) != 2 {
		t.Fatalf("got %d exercised rows, want 2", len(got))
	}
	for _, r := range got {
		if !r.Available {
			t.Fatalf("row for version %s reported unavailable, but the snapshot is retained", r.PolicyVersion)
		}
	}
	if got[0].Source == got[1].Source {
		t.Errorf("both rows resolved to the same rule text %q. Index 0 means a DIFFERENT rule under "+
			"each policy, and reporting the current one for both is the misattribution this prevents",
			got[0].Source)
	}
	if want := "443"; !contains(got[0].Source, want) && !contains(got[1].Source, want) {
		t.Errorf("neither row carries the superseded rule's text (looking for %q): %+v", want, got)
	}
}

func TestExercisedRuleWithNoRetainedSnapshotIsMarkedUnavailable(t *testing.T) {
	s := policyStoreWith(t, docV1)

	got := exercisedRules(s, []flowstore.RuleStat{{Rule: 0, PolicyVersion: "deadbeefdeadbeef"}})
	if len(got) != 1 {
		t.Fatalf("got %d exercised rows, want 1", len(got))
	}
	if got[0].Available {
		t.Fatal("an unretained policy version was reported as available")
	}
	if got[0].Source != "" || got[0].Kind != "" {
		t.Errorf("an unavailable row carries rule text kind=%q source=%q. It MUST stay empty: "+
			"filling it from the current policy names a rule that never applied, which reads as an "+
			"answer rather than as the gap it is", got[0].Kind, got[0].Source)
	}
}

func TestExercisedRuleOutOfRangeForItsSnapshotIsUnavailable(t *testing.T) {
	s := policyStoreWith(t, docV1) // exactly one rule
	got := exercisedRules(s, []flowstore.RuleStat{{Rule: 7, PolicyVersion: s.Policy().Version()}})
	if len(got) != 1 {
		t.Fatalf("got %d exercised rows, want 1", len(got))
	}
	if got[0].Available {
		t.Error("rule index 7 resolved against a one-rule policy; an out-of-range index must not " +
			"be silently clamped or wrapped onto a real rule")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// versionOf recompiles doc in isolation to learn the version it produces,
// without disturbing s.
func versionOf(_ *aclpolicy.Store, doc string) (string, error) {
	p, err := aclpolicy.Compile([]byte(doc), aclpolicy.Directory{})
	if err != nil {
		return "", err
	}
	return p.Version(), nil
}
