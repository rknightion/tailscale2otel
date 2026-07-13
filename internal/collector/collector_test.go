package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// fakeCollector is a SnapshotCollector (Register now requires the typed interface
// at compile time — #58 — so a base-only Collector would not compile here).
type fakeCollector struct {
	name string
	def  time.Duration
}

func (f fakeCollector) Name() string                                     { return f.name }
func (f fakeCollector) DefaultInterval() time.Duration                   { return f.def }
func (f fakeCollector) Collect(context.Context, telemetry.Emitter) error { return nil }

func TestRegistry_UsesDefaultIntervalWhenUnset(t *testing.T) {
	r := collector.NewRegistry()
	r.Register(fakeCollector{name: "devices", def: 60 * time.Second}, 0)
	r.Register(fakeCollector{name: "users", def: 60 * time.Second}, 30*time.Second)

	es := r.Entries()
	if len(es) != 2 {
		t.Fatalf("Entries len = %d, want 2", len(es))
	}
	if es[0].Interval != 60*time.Second {
		t.Fatalf("entry[0] interval = %v, want 60s (defaulted)", es[0].Interval)
	}
	if es[1].Interval != 30*time.Second {
		t.Fatalf("entry[1] interval = %v, want 30s (explicit)", es[1].Interval)
	}
	if es[0].Collector.Name() != "devices" {
		t.Fatalf("entry[0] name = %q, want devices", es[0].Collector.Name())
	}
}
