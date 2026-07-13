package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
)

// TestDedupInfo_ReportsAllRuntimes pins #60: in multi-tailnet mode every runtime's
// flow/audit dedup sets appear (tailnet-prefixed), not just runtimes[0]'s.
func TestDedupInfo_ReportsAllRuntimes(t *testing.T) {
	a := &App{runtimes: []*tailnetRuntime{
		{name: "a.example.com", flowDedup: dedup.New(10), auditDedup: dedup.New(10)},
		{name: "b.example.com", flowDedup: dedup.New(10), auditDedup: dedup.New(10)},
	}}
	got := map[string]bool{}
	for _, d := range a.dedupInfo() {
		got[d.Name] = true
	}
	for _, want := range []string{"a.example.com/flow", "a.example.com/audit", "b.example.com/flow", "b.example.com/audit"} {
		if !got[want] {
			t.Errorf("dedupInfo missing %q; got %v", want, got)
		}
	}
}

// TestDedupInfo_SingleTailnetUnprefixed keeps the single-tailnet names bare.
func TestDedupInfo_SingleTailnetUnprefixed(t *testing.T) {
	a := &App{runtimes: []*tailnetRuntime{
		{name: "solo", flowDedup: dedup.New(10), auditDedup: dedup.New(10)},
	}}
	got := map[string]bool{}
	for _, d := range a.dedupInfo() {
		got[d.Name] = true
	}
	if !got["flow"] || !got["audit"] {
		t.Errorf("single-tailnet dedupInfo should be unprefixed flow/audit; got %v", got)
	}
}
