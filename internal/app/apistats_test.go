package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/tsapi"
)

func TestAPIStats_RecordAggregates(t *testing.T) {
	s := NewAPIStats()
	s.Record(tsapi.RequestInfo{Endpoint: "devices", Status: 200, Attempts: 1, Duration: 50 * time.Millisecond})
	s.Record(tsapi.RequestInfo{Endpoint: "devices", Status: 429, Attempts: 2, Duration: 80 * time.Millisecond})
	s.Record(tsapi.RequestInfo{Endpoint: "devices", Status: 500, Attempts: 3, Duration: 120 * time.Millisecond})

	snaps := s.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("snapshot endpoints = %d, want 1", len(snaps))
	}
	d := snaps[0]
	if d.Endpoint != "devices" {
		t.Errorf("endpoint = %q, want devices", d.Endpoint)
	}
	if d.Requests != 3 {
		t.Errorf("requests = %d, want 3", d.Requests)
	}
	if d.Errors != 2 { // 429 and 500 are both >= 400
		t.Errorf("errors = %d, want 2", d.Errors)
	}
	if d.RateLimited != 1 { // only the 429
		t.Errorf("rateLimited = %d, want 1", d.RateLimited)
	}
	if d.Retries != 3 { // (1-1)+(2-1)+(3-1)
		t.Errorf("retries = %d, want 3", d.Retries)
	}
	if d.LastStatus != 500 {
		t.Errorf("lastStatus = %d, want 500", d.LastStatus)
	}
	if d.Last429At.IsZero() {
		t.Errorf("Last429At not set after a 429")
	}
	if len(d.DurMs) != 3 {
		t.Errorf("durations recorded = %d, want 3", len(d.DurMs))
	}
}

func TestAPIStats_TransportErrorCountsAsError(t *testing.T) {
	s := NewAPIStats()
	s.Record(tsapi.RequestInfo{Endpoint: "devices", Status: 0, Attempts: 1, Err: "dial tcp: timeout"})
	d := s.Snapshot()[0]
	if d.Errors != 1 {
		t.Errorf("errors = %d, want 1 (transport error, status 0)", d.Errors)
	}
	if d.LastErr != "dial tcp: timeout" {
		t.Errorf("lastErr = %q, want the transport error", d.LastErr)
	}
}

func TestAPIStats_SnapshotSortedAndNilSafe(t *testing.T) {
	var s *APIStats
	if s.Snapshot() != nil {
		t.Fatalf("nil Snapshot should be nil")
	}
	s.Record(tsapi.RequestInfo{Endpoint: "x"}) // nil receiver must not panic

	s2 := NewAPIStats()
	s2.Record(tsapi.RequestInfo{Endpoint: "zebra", Status: 200, Attempts: 1})
	s2.Record(tsapi.RequestInfo{Endpoint: "alpha", Status: 200, Attempts: 1})
	snaps := s2.Snapshot()
	if len(snaps) != 2 || snaps[0].Endpoint != "alpha" || snaps[1].Endpoint != "zebra" {
		t.Fatalf("snapshot not sorted by endpoint: %+v", snaps)
	}
}
