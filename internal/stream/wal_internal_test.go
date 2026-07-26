package stream

import (
	"strings"
	"testing"
)

// TestDecodeDurableBatch_DoesNotReapplyRecordBudget catches replay stranding a
// body that was accepted under an earlier request-admission ceiling.
func TestDecodeDurableBatch_DoesNotReapplyRecordBudget(t *testing.T) {
	body := []byte(strings.Repeat(`{}`+"\n", maxRecordsPerRequest+1))

	batch, err := decodeDurableBatch(body)
	if err != nil {
		t.Fatalf("decodeDurableBatch: %v", err)
	}
	if batch.classifyUnknown != maxRecordsPerRequest+1 {
		t.Fatalf("unclassified = %d, want %d", batch.classifyUnknown, maxRecordsPerRequest+1)
	}
}

// TestDecodeDurableBatch_DoesNotReapplyConnectionBudget catches replay
// stranding a body because the current nested-connection admission ceiling is
// lower than the ceiling under which it was accepted.
func TestDecodeDurableBatch_DoesNotReapplyConnectionBudget(t *testing.T) {
	body := []byte(`{"nodeId":"n1","virtualTraffic":[` +
		strings.Repeat(`{},`, maxConnectionsPerRequest) + `{}` + `]}`)

	batch, err := decodeDurableBatch(body)
	if err != nil {
		t.Fatalf("decodeDurableBatch: %v", err)
	}
	if len(batch.flows) != 1 {
		t.Fatalf("flows = %d, want 1", len(batch.flows))
	}
	if got := len(batch.flows[0].log.VirtualTraffic); got != maxConnectionsPerRequest+1 {
		t.Fatalf("connections = %d, want %d", got, maxConnectionsPerRequest+1)
	}
}
