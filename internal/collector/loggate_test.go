package collector

import (
	"testing"
	"time"
)

func TestStateLogGate(t *testing.T) {
	gate := NewStateLogGate[string]()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !gate.Due("a", "one", at) || gate.Due("a", "one", at.Add(23*time.Hour)) {
		t.Fatal("first observation must emit, unchanged state before a day must not")
	}
	if !gate.Due("a", "two", at.Add(23*time.Hour)) {
		t.Fatal("changed signature must emit")
	}
	if gate.Due("a", "two", at.Add(24*time.Hour)) || !gate.Due("a", "two", at.Add(47*time.Hour)) {
		t.Fatal("daily interval must be measured from the last emission")
	}
	gate.Retain(map[string]struct{}{"a": {}})
	if gate.Due("a", "two", at.Add(47*time.Hour)) {
		t.Fatal("retained entity emitted twice")
	}
	gate.Retain(nil)
	if !gate.Due("a", "two", at.Add(47*time.Hour)) {
		t.Fatal("forgotten entity must be first-seen again")
	}
}
