package collector

import (
	"testing"
	"time"
)

func TestStatusTracker_RecordSuccessThenFailure(t *testing.T) {
	tr := NewStatusTracker()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr.record("devices", t0, t0.Add(2*time.Second), 2*time.Second, "")
	tr.record("devices", t0.Add(time.Minute), t0.Add(time.Minute+time.Second), time.Second, "boom")

	r, ok := tr.Snapshot()["devices"]
	if !ok {
		t.Fatalf("no record for devices")
	}
	if r.Runs != 2 {
		t.Errorf("Runs = %d, want 2", r.Runs)
	}
	if r.Failures != 1 {
		t.Errorf("Failures = %d, want 1", r.Failures)
	}
	if r.LastSuccess {
		t.Errorf("LastSuccess = true, want false (last run failed)")
	}
	if r.LastError != "boom" {
		t.Errorf("LastError = %q, want boom", r.LastError)
	}
	if r.LastDuration != time.Second {
		t.Errorf("LastDuration = %v, want 1s", r.LastDuration)
	}
	if !r.LastStarted.Equal(t0.Add(time.Minute)) {
		t.Errorf("LastStarted = %v, want %v", r.LastStarted, t0.Add(time.Minute))
	}
}

func TestStatusTracker_SuccessClearsLastError(t *testing.T) {
	tr := NewStatusTracker()
	t0 := time.Now()
	tr.record("x", t0, t0, time.Millisecond, "err")
	tr.record("x", t0, t0, time.Millisecond, "")

	r := tr.Snapshot()["x"]
	if !r.LastSuccess || r.LastError != "" {
		t.Fatalf("after success: LastSuccess=%v LastError=%q, want true/empty", r.LastSuccess, r.LastError)
	}
	if r.Failures != 1 {
		t.Errorf("Failures = %d, want 1 (the earlier failure still counts)", r.Failures)
	}
}

func TestStatusTracker_SnapshotIsCopy(t *testing.T) {
	tr := NewStatusTracker()
	tr.record("x", time.Now(), time.Now(), 0, "")

	snap := tr.Snapshot()
	snap["x"] = CollectorRun{Runs: 999}
	if got := tr.Snapshot()["x"].Runs; got != 1 {
		t.Fatalf("mutating the snapshot affected the tracker: Runs = %d, want 1", got)
	}
}

func TestStatusTracker_NilSafe(t *testing.T) {
	var tr *StatusTracker
	tr.record("x", time.Now(), time.Now(), 0, "") // must not panic
	if len(tr.Snapshot()) != 0 {
		t.Fatalf("nil tracker Snapshot non-empty")
	}
}
