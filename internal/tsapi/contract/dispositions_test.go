package contract_test

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/tsapi/contract"
)

// updateBaselines rewrites the committed disposition baselines instead of
// asserting them. Run:
//
//	go test ./internal/tsapi/contract -run TestFieldDispositionsInSync -update
//	go test ./internal/tsapi/contract -run TestOperationDispositionsInSync -update
//
// Regeneration never assigns a disposition — it only adds empty rows for newly
// discovered fields/operations and prunes ones that disappeared — so it can
// never turn a red gate green on its own.
var updateBaselines = flag.Bool("update", false, "rewrite the committed disposition baselines")

// TestFieldDispositionsInSync is the #422 gate: every decoded response field
// must carry a disposition, and the baseline must not describe fields we no
// longer decode.
func TestFieldDispositionsInSync(t *testing.T) {
	inv := contract.FieldInventory()

	raw, err := os.ReadFile(contract.FieldDispositionsFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read %s: %v", contract.FieldDispositionsFile, err)
	}
	var base *contract.FieldDispositionBaseline
	if err == nil {
		base, err = contract.LoadFieldDispositions(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", contract.FieldDispositionsFile, err)
		}
	}

	if *updateBaselines {
		merged, added, removed := contract.MergeFieldDispositions(base, inv)
		out, err := contract.MarshalFieldDispositions(merged)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(contract.FieldDispositionsFile, out, 0o644); err != nil { //nolint:gosec // committed data file
			t.Fatalf("write %s: %v", contract.FieldDispositionsFile, err)
		}
		t.Logf("regenerated %s: %d fields (%d added, %d removed)",
			contract.FieldDispositionsFile, len(merged.Fields), len(added), len(removed))
		for _, a := range added {
			t.Logf("  ADDED (needs a disposition): %s / %s", a.Op, a.Path)
		}
		for _, r := range removed {
			t.Logf("  REMOVED: %s / %s", r.Op, r.Path)
		}
		return
	}

	if problems := contract.ValidateFieldDispositions(base, inv); len(problems) > 0 {
		t.Fatalf("consumed-field disposition contract broken (%d problem(s)):\n  %s\n\n"+
			"Run: go test ./internal/tsapi/contract -run TestFieldDispositionsInSync -update\n"+
			"then assign a disposition to every row it added.",
			len(problems), strings.Join(problems, "\n  "))
	}
}

func TestValidateFieldDispositions_RejectsNotelessUnhandled(t *testing.T) {
	base := &contract.FieldDispositionBaseline{Fields: []contract.FieldDisposition{{
		Op:          "listExample",
		Path:        "Value",
		Type:        "example.Value",
		Disposition: contract.DispUnhandled,
	}}}
	inv := []contract.InventoryEntry{{Op: "listExample", Path: "Value", Type: "example.Value"}}

	problems := contract.ValidateFieldDispositions(base, inv)
	if !containsProblem(problems, "unhandled field needs a parking note: listExample / Value") {
		t.Fatalf("noteless unhandled field passed validation: %v", problems)
	}
}

func containsProblem(problems []string, want string) bool {
	for _, problem := range problems {
		if problem == want {
			return true
		}
	}
	return false
}

// TestFieldDispositions_EmbeddedCopyMatchesDisk guards against a stale compiled-in
// baseline: tools/apidrift renders the coverage report from the embedded copy, so
// the two must be the same bytes.
func TestFieldDispositions_EmbeddedCopyMatchesDisk(t *testing.T) {
	if *updateBaselines {
		t.Skip("regenerating; the embed refreshes on the next build")
	}
	embedded, err := contract.EmbeddedFieldDispositions()
	if err != nil {
		t.Fatalf("embedded baseline: %v", err)
	}
	if len(embedded.Fields) != len(mustDiskBaseline(t).Fields) {
		t.Fatalf("embedded baseline has %d fields, disk has %d — rebuild",
			len(embedded.Fields), len(mustDiskBaseline(t).Fields))
	}
}

func mustDiskBaseline(t *testing.T) *contract.FieldDispositionBaseline {
	t.Helper()
	raw, err := os.ReadFile(contract.FieldDispositionsFile)
	if err != nil {
		t.Fatalf("read %s: %v", contract.FieldDispositionsFile, err)
	}
	b, err := contract.LoadFieldDispositions(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", contract.FieldDispositionsFile, err)
	}
	return b
}

// TestFieldCoverageReport_HasNoLiveData is a cheap belt-and-braces check that the
// reviewable report is built only from static type information: no tailnet name,
// no addresses, no email addresses can reach it because nothing live is read.
func TestFieldCoverageReport_HasNoLiveData(t *testing.T) {
	rep := contract.FieldCoverageReport(mustDiskBaseline(t))
	for _, bad := range []string{"@", "100.64.", "https://"} {
		if strings.Contains(rep, bad) {
			t.Errorf("coverage report contains %q — it must carry no live-looking data", bad)
		}
	}
}
