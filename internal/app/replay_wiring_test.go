package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
)

func TestReplayStoreForRuntimeUsesSchedulerNamespaceShape(t *testing.T) {
	const key = "flowlogs/replay/seen/digest"
	value := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

	t.Run("single tailnet preserves legacy keys", func(t *testing.T) {
		base := collector.NewMemoryStore()
		if err := replayStoreForRuntime(base, "alpha", false).Set(key, value); err != nil {
			t.Fatal(err)
		}
		if got, ok := base.Get(key); !ok || !got.Equal(value) {
			t.Fatalf("base.Get(%q) = %v, %v; want %v, true", key, got, ok, value)
		}
	})

	t.Run("multiple tailnets isolate replay identities", func(t *testing.T) {
		base := collector.NewMemoryStore()
		if err := replayStoreForRuntime(base, "beta", true).Set(key, value); err != nil {
			t.Fatal(err)
		}
		if _, ok := base.Get(key); ok {
			t.Fatalf("base.Get(%q) unexpectedly found an unnamespaced key", key)
		}
		namespaced := "beta/" + key
		if got, ok := base.Get(namespaced); !ok || !got.Equal(value) {
			t.Fatalf("base.Get(%q) = %v, %v; want %v, true", namespaced, got, ok, value)
		}
	})
}
