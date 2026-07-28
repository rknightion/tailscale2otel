package aclpolicy

import "testing"

// A policy's version must identify its rule LIST, because that list is what an
// exercised-rule report joins on. Two policies whose rules differ in any way an
// operator could read — content or order — must not share a version, or retained
// traffic attributed under the old one would be displayed against the new rules.

func mustCompile(t *testing.T, doc string) *Policy {
	t.Helper()
	p, err := Compile([]byte(doc), Directory{})
	if err != nil {
		t.Fatalf("compile %s: %v", doc, err)
	}
	return p
}

const twoRules = `{"grants":[
  {"src":["group:eng"],"dst":["tag:prod"],"ip":["443"]},
  {"src":["group:ops"],"dst":["tag:db"],"ip":["5432"]}
]}`

const twoRulesReordered = `{"grants":[
  {"src":["group:ops"],"dst":["tag:db"],"ip":["5432"]},
  {"src":["group:eng"],"dst":["tag:prod"],"ip":["443"]}
]}`

const twoRulesEdited = `{"grants":[
  {"src":["group:eng"],"dst":["tag:prod"],"ip":["8443"]},
  {"src":["group:ops"],"dst":["tag:db"],"ip":["5432"]}
]}`

func TestVersionIsStableForTheSameRules(t *testing.T) {
	a := mustCompile(t, twoRules).Version()
	b := mustCompile(t, twoRules).Version()
	if a == "" {
		t.Fatal("Version() is empty; every compiled policy needs an identity to join on")
	}
	if a != b {
		t.Errorf("recompiling the same document produced versions %q and %q; a version that "+
			"changes on every compile invalidates every retained attribution", a, b)
	}
}

func TestVersionChangesWhenRulesAreReordered(t *testing.T) {
	a := mustCompile(t, twoRules).Version()
	b := mustCompile(t, twoRulesReordered).Version()
	if a == b {
		t.Errorf("reordered rules share version %q. Rule identity is (version, index), so a "+
			"shared version after a reorder means retained traffic joins to the wrong rule — "+
			"the exact failure this exists to prevent", a)
	}
}

func TestVersionChangesWhenARuleIsEdited(t *testing.T) {
	a := mustCompile(t, twoRules).Version()
	b := mustCompile(t, twoRulesEdited).Version()
	if a == b {
		t.Errorf("editing a rule's ports left the version at %q", a)
	}
}

func TestVersionIgnoresDocumentFormatting(t *testing.T) {
	spaced := mustCompile(t, twoRules).Version()
	compact := mustCompile(t, `{"grants":[{"src":["group:eng"],"dst":["tag:prod"],"ip":["443"]},{"src":["group:ops"],"dst":["tag:db"],"ip":["5432"]}]}`).Version()
	if spaced != compact {
		t.Errorf("reformatting the document changed the version (%q vs %q). The version identifies "+
			"the compiled rule list, not the bytes; churning it on a whitespace edit would discard "+
			"attributions that are still perfectly valid", spaced, compact)
	}
}

func TestEmptyPolicyStillHasAVersion(t *testing.T) {
	if v := mustCompile(t, `{"grants":[]}`).Version(); v == "" {
		t.Error("a policy with no rules has no version; an observation evaluated against it would " +
			"carry an empty version and be indistinguishable from one never evaluated")
	}
}
