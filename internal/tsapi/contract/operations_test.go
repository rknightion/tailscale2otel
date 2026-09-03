package contract_test

import (
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/oas"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi/contract"
)

// TestOperationDispositionsInSync is the #423 gate. It is deliberately SEPARATE
// from decode drift: this asks "has upstream published an operation we have not
// classified?", never "did a consumed response body change shape?".
func TestOperationDispositionsInSync(t *testing.T) {
	spec := loadSpec(t)
	inv := contract.OperationInventory(spec)

	raw, err := os.ReadFile(contract.OperationDispositionsFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read %s: %v", contract.OperationDispositionsFile, err)
	}
	var base *contract.OperationDispositionBaseline
	if err == nil {
		base, err = contract.LoadOperationDispositions(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", contract.OperationDispositionsFile, err)
		}
	}

	if *updateBaselines {
		merged, added, removed := contract.MergeOperationDispositions(base, inv)
		out, err := contract.MarshalOperationDispositions(merged)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(contract.OperationDispositionsFile, out, 0o644); err != nil { //nolint:gosec // committed data file
			t.Fatalf("write %s: %v", contract.OperationDispositionsFile, err)
		}
		t.Logf("regenerated %s: %d operations (%d added, %d removed)",
			contract.OperationDispositionsFile, len(merged.Operations), len(added), len(removed))
		for _, a := range added {
			t.Logf("  ADDED (needs a disposition): %s %s %s", a.Method, a.Path, a.ID)
		}
		for _, r := range removed {
			t.Logf("  REMOVED upstream: %s", r.ID)
		}
		return
	}

	if problems := contract.ValidateOperationDispositions(base, inv, contract.ConsumedOpIDs()); len(problems) > 0 {
		t.Fatalf("operation disposition contract broken (%d problem(s)):\n  %s\n\n"+
			"Run: go test ./internal/tsapi/contract -run TestOperationDispositionsInSync -update\n"+
			"then assign a disposition to every row it added.",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// TestOperationInventory_CoversEveryVerb proves the inventory is spec-wide, not
// GET-only — the blind spot #423 exists to close.
func TestOperationInventory_CoversEveryVerb(t *testing.T) {
	inv := contract.OperationInventory(loadSpec(t))
	byMethod := map[string]int{}
	for _, e := range inv {
		byMethod[e.Method]++
	}
	for _, verb := range []string{"get", "post", "delete", "patch", "put"} {
		if byMethod[verb] == 0 {
			t.Errorf("inventory has no %s operations — spec-wide enumeration is broken", verb)
		}
	}
	for i := 1; i < len(inv); i++ {
		if inv[i-1].ID >= inv[i].ID {
			t.Fatalf("inventory not sorted by ID at %d: %s after %s", i, inv[i].ID, inv[i-1].ID)
		}
	}
}

// TestValidateOperationDispositions_FlagsNewReadOperation is the core regression:
// a read-capable operation appearing upstream with no baseline row must be
// reported, and reported as read-capable so the lane can prioritize it.
func TestValidateOperationDispositions_FlagsNewReadOperation(t *testing.T) {
	base := &contract.OperationDispositionBaseline{
		Operations: []contract.OperationDisposition{
			{ID: "known", Method: "get", Path: "/known", Disposition: contract.OpParked, Note: "n"},
		},
	}
	inv := []oas.OperationRef{
		{ID: "known", Method: "get", Path: "/known"},
		{ID: "brandNewRead", Method: "get", Path: "/brand-new"},
		{ID: "brandNewWrite", Method: "post", Path: "/brand-new"},
	}
	problems := contract.ValidateOperationDispositions(base, inv, nil)
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "brandNewRead") || !strings.Contains(joined, "READ-CAPABLE") {
		t.Errorf("new GET not flagged as read-capable:\n%s", joined)
	}
	if !strings.Contains(joined, "brandNewWrite") {
		t.Errorf("new non-GET operation not flagged at all:\n%s", joined)
	}
}

// TestValidateOperationDispositions_ConsumedMustSayConsumed stops the baseline
// drifting away from the manifest: an op we actually call cannot be filed as
// "parked" or "privacy-rejected".
func TestValidateOperationDispositions_ConsumedMustSayConsumed(t *testing.T) {
	base := &contract.OperationDispositionBaseline{
		Operations: []contract.OperationDisposition{
			{ID: "listUsers", Method: "get", Path: "/users", Disposition: contract.OpParked, Note: "n"},
		},
	}
	inv := []oas.OperationRef{{ID: "listUsers", Method: "get", Path: "/users"}}
	problems := contract.ValidateOperationDispositions(base, inv, []string{"listUsers"})
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "consumed") {
		t.Errorf("consumed op filed as parked was not flagged: %v", problems)
	}
}

// TestValidateOperationDispositions_RejectsUnknownDisposition keeps the vocabulary closed.
func TestValidateOperationDispositions_RejectsUnknownDisposition(t *testing.T) {
	base := &contract.OperationDispositionBaseline{
		Operations: []contract.OperationDisposition{
			{ID: "x", Method: "get", Path: "/x", Disposition: "maybe-later", Note: "n"},
		},
	}
	inv := []oas.OperationRef{{ID: "x", Method: "get", Path: "/x"}}
	if problems := contract.ValidateOperationDispositions(base, inv, nil); len(problems) != 1 {
		t.Fatalf("want 1 problem for an unknown disposition, got %v", problems)
	}
}

// TestOperationDrift_ReportsOnlyNewOperations proves the new-operation lane and
// the decode-drift lane do not overlap: an operation present in both specs
// produces no operation-drift signal no matter how its schema changed.
func TestOperationDrift_ReportsOnlyNewOperations(t *testing.T) {
	base := &contract.OperationDispositionBaseline{
		Operations: []contract.OperationDisposition{
			{ID: "stable", Method: "get", Path: "/s", Disposition: contract.OpImplement, Note: "n"},
		},
	}
	inv := []oas.OperationRef{{ID: "stable", Method: "get", Path: "/s", Summary: "changed summary"}}
	if problems := contract.ValidateOperationDispositions(base, inv, nil); len(problems) != 0 {
		t.Fatalf("stable operation produced drift: %v", problems)
	}
}

// TestOperationCoverageReport_NoLiveData: the report is built from the spec and
// the baseline only.
func TestOperationCoverageReport_NoLiveData(t *testing.T) {
	raw, err := os.ReadFile(contract.OperationDispositionsFile)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	base, err := contract.LoadOperationDispositions(raw)
	if err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	rep := contract.OperationCoverageReport(base)
	if strings.Contains(rep, "@") {
		t.Error("operation coverage report contains an email-looking token")
	}
	if !strings.Contains(rep, "| operation |") {
		t.Errorf("report has no operation table:\n%s", rep)
	}
}
