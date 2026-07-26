package flowstore_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMemory_RecentCarriesReporterDiagnosis(t *testing.T) {
	s := newMemory(0)
	o := conn(base, "100.64.0.1:12345", "100.64.0.2:443", 1)
	o.ReporterNodeID = "reporter"
	o.ReporterTrust = "trusted"
	o.ReporterConsistency = "mismatch"
	s.Record(o)

	got := s.Recent(1)
	if len(got) != 1 {
		t.Fatalf("Recent = %d, want 1", len(got))
	}
	if got[0].ReporterNodeID != "reporter" || got[0].ReporterTrust != "trusted" || got[0].ReporterConsistency != "mismatch" {
		t.Errorf("recent reporter diagnosis = %+v", got[0])
	}

	body, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal recent: %v", err)
	}
	for _, want := range []string{"\"reporter_node_id\":\"reporter\"", "\"reporter_trust\":\"trusted\"", "\"reporter_consistency\":\"mismatch\""} {
		if !strings.Contains(string(body), want) {
			t.Errorf("Recent JSON %s does not contain %s", body, want)
		}
	}
}
