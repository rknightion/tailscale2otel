package catalog_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/catalog"
)

// The catalog -> panel direction (#526).
//
// dashboardrefs_test.go already checks the other direction: that a panel never
// queries a name the exporter cannot emit. Nothing checked THIS one. Coverage was
// approximated by frozen per-family inventories in deploy/grafana/gen/test_*.py,
// each of which asserts only about the family it was written for, so a signal
// belonging to no inventory was covered by nothing at all.
//
// That is how 42 of 310 signals came to reach no panel — 7 of them while being
// ALERTABLE, which is the worst shape available: an operator is paged by
// something they then cannot see anywhere.
//
// Accepted limitation, recorded so it is not re-derived: this is a token match
// over query expressions. It proves a metric name appears in some query, not that
// an operator can ever see the result — a panel on a row whose sentinel is never
// satisfied still counts. That is deliberate. Evaluating conditional rendering
// would need a fixture of a hypothetical tailnet, and the honest stronger check
// is the live-contract lane. The claim here is the narrower "nothing was
// forgotten entirely", which is worth having at that strength. Do not widen it
// with per-panel visibility heuristics.

func TestEverySignalReachesAPanel(t *testing.T) {
	refs := artifactRefs(t)
	base := loadManifest(t)
	if base == nil {
		t.Fatal("no disposition manifest; this test would pass vacuously")
	}

	exempt := map[string]catalog.StructuralExemption{}
	for _, e := range catalog.StructuralExemptions() {
		exempt[e.Kind+"\x00"+e.Name] = e
	}
	pending := map[string]bool{}
	for _, row := range base.Signals {
		for _, d := range row.Dispositions {
			if d == catalog.DispPendingPanel {
				pending[row.Kind+"\x00"+row.Name] = true
			}
		}
	}

	var missing []string
	for _, s := range catalog.Signals() {
		key := s.Kind + "\x00" + s.Name
		if refs.Observed(s) != nil && hasVisualized(refs, s) {
			continue
		}
		if _, ok := exempt[key]; ok {
			continue
		}
		if pending[key] {
			continue
		}
		missing = append(missing, s.Kind+" "+s.Name)
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%d catalog signal(s) reach no panel on either dashboard, and are neither "+
			"structurally exempt nor on the #526 pending ledger:\n  %s\n\n"+
			"Every emitted signal must be visible somewhere: a signal nothing charts is one "+
			"an operator can only find by knowing it exists. Add a panel. The only alternative "+
			"is a StructuralExemption, and only for one of the three structural classes — "+
			"never as a way to make this failure go away.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

func hasVisualized(refs catalog.ArtifactRefs, s catalog.Signal) bool {
	for _, d := range refs.Observed(s) {
		if d == catalog.DispVisualized {
			return true
		}
	}
	return false
}

// The pending ledger is TRANSITIONAL and shrink-only. Without this, a lane could
// "fix" a coverage failure by adding a row to the ledger, which is the exact
// behavior the ledger exists to prevent — and the reason raw_only and omitted
// were deleted rather than kept.
//
// #526 drives this to zero, at which point the ledger, DispPendingPanel and this
// test are deleted together.
func TestPendingPanelLedgerOnlyShrinks(t *testing.T) {
	const ceiling = 42 // measured at 681fe1e, before any panel work

	base := loadManifest(t)
	if base == nil {
		t.Fatal("no disposition manifest; this test would pass vacuously")
	}
	var pending []string
	for _, row := range base.Signals {
		for _, d := range row.Dispositions {
			if d == catalog.DispPendingPanel {
				pending = append(pending, row.Kind+" "+row.Name)
			}
		}
	}
	sort.Strings(pending)

	if len(pending) > ceiling {
		t.Errorf("the pending-panel ledger holds %d signals, above the %d ceiling:\n  %s\n\n"+
			"A signal leaves this ledger by getting a panel. Nothing may join it.",
			len(pending), ceiling, strings.Join(pending, "\n  "))
	}
	if len(pending) == 0 {
		t.Log("the pending ledger is empty — delete DispPendingPanel, the ledger rows and " +
			"this test; #526's coverage work is done")
	}
}

// A structural exemption for a signal the code no longer emits is a standing
// license for a future signal of the same name to skip the gate entirely.
func TestNoStructuralExemptionIsUnused(t *testing.T) {
	live := map[string]bool{}
	for _, s := range catalog.Signals() {
		live[s.Kind+"\x00"+s.Name] = true
	}
	exemptions := catalog.StructuralExemptions()
	if len(exemptions) == 0 {
		// The templated webhook event has no other way to pass, so an empty list
		// means either it was deleted or the gate stopped running — both of which
		// make TestEverySignalReachesAPanel weaker than it reads.
		t.Fatal("no structural exemptions at all; at least the templated webhook event " +
			"needs one, so either it was dropped or the gate is no longer being applied")
	}
	for _, e := range exemptions {
		if !live[e.Kind+"\x00"+e.Name] {
			t.Errorf("structural exemption for %s %s, which the code no longer emits. "+
				"Delete it: a stale exemption silently pre-approves the next signal to "+
				"take that name.", e.Kind, e.Name)
		}
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("structural exemption for %s %s has no reason. Each one is an "+
				"individually justified entry, not a category anyone may join.",
				e.Kind, e.Name)
		}
		switch e.Class {
		case catalog.ExemptHistogramBase, catalog.ExemptTemplatedEvent, catalog.ExemptRecordedOnly:
		default:
			t.Errorf("structural exemption for %s %s has class %q. There are exactly three "+
				"classes and each is structural rather than a judgement call; a new class "+
				"is a change to the coverage bar and belongs on the issue, not here.",
				e.Kind, e.Name, e.Class)
		}
	}
}

// The recorded_only class is only legitimate when the recording rule's OUTPUT
// metric is itself on a panel — otherwise it is an ordinary coverage hole wearing
// a structural label.
//
// It matches ZERO signals today: nothing is `recorded` without also being
// `visualized`. It exists so the gate does not have to be reopened later. Do not
// go looking for members.
func TestRecordedOnlyExemptionsNamePanelledOutputs(t *testing.T) {
	refs := artifactRefs(t)
	for _, e := range catalog.StructuralExemptions() {
		if e.Class != catalog.ExemptRecordedOnly {
			continue
		}
		if strings.TrimSpace(e.Output) == "" {
			t.Errorf("%s %s claims recorded_only but names no output metric; the class means "+
				"'the recording rule's output is paneled instead', so the output has to be "+
				"stated and checked", e.Kind, e.Name)
			continue
		}
		if !refs.Dashboard[e.Output] {
			t.Errorf("%s %s claims recorded_only, but its output metric %q is on no panel "+
				"either. That is an ordinary coverage hole, not a structural exemption.",
				e.Kind, e.Name, e.Output)
		}
	}
}
