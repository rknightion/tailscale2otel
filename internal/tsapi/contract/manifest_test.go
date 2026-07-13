package contract_test

import (
	"os"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/oas"
	"github.com/rknightion/tailscale2otel/v2/internal/tsapi/contract"
)

func TestManifest_WellFormed(t *testing.T) {
	if len(contract.Manifest) < 14 {
		t.Fatalf("manifest has %d ops, expected >=14", len(contract.Manifest))
	}
	seen := map[string]bool{}
	for _, op := range contract.Manifest {
		if op.ID == "" || op.Method != "GET" || op.Invoke == nil {
			t.Errorf("malformed op: %+v", op)
		}
		if seen[op.ID] {
			t.Errorf("duplicate op ID %q", op.ID)
		}
		seen[op.ID] = true
	}
}

// TestManifest_EveryIDExistsInVendoredOAS catches a wrong/renamed operationId in
// the manifest — otherwise the OpenAPI-drift lane would silently cover nothing
// for that op.
func TestManifest_EveryIDExistsInVendoredOAS(t *testing.T) {
	b, err := os.ReadFile("../../../spec/tailscale-api.json")
	if err != nil {
		t.Fatalf("read vendored spec: %v", err)
	}
	s, err := oas.ParseSpec(b)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	for _, id := range contract.ConsumedOpIDs() {
		if _, ok := s.Ops[id]; !ok {
			t.Errorf("manifest op %q not found in vendored OAS — wrong operationId?", id)
		}
	}
}
