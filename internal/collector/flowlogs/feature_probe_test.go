package flowlogs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// Compile-time guarantee: *FeatureProbe is a SnapshotCollector.
var _ collector.SnapshotCollector = (*FeatureProbe)(nil)

// TestFeatureProbe_Enabled verifies that when the check reports (true, nil) the
// probe emits a single tailscale.feature.enabled=1 point carrying the
// network_flow_logging feature attribute, and returns no error.
func TestFeatureProbe_Enabled(t *testing.T) {
	p := NewFeatureProbe(func(context.Context) (bool, error) { return true, nil }, 0)
	rec := telemetrytest.New()

	if err := p.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pt := featurePoint(t, rec)
	if pt.Value != 1 {
		t.Fatalf("feature.enabled = %v, want 1", pt.Value)
	}
	if got := pt.Attrs[semconv.AttrFeature]; got != "network_flow_logging" {
		t.Fatalf("feature attr = %q, want network_flow_logging", got)
	}
}

// TestFeatureProbe_Disabled verifies that when the check reports (false, nil)
// the probe emits feature.enabled=0 with the same attribute and no error.
func TestFeatureProbe_Disabled(t *testing.T) {
	p := NewFeatureProbe(func(context.Context) (bool, error) { return false, nil }, 0)
	rec := telemetrytest.New()

	if err := p.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pt := featurePoint(t, rec)
	if pt.Value != 0 {
		t.Fatalf("feature.enabled = %v, want 0", pt.Value)
	}
	if got := pt.Attrs[semconv.AttrFeature]; got != "network_flow_logging" {
		t.Fatalf("feature attr = %q, want network_flow_logging", got)
	}
}

// TestFeatureProbe_ErrorFailsOpen verifies that a check error fails open: the
// probe emits NO points and returns nil (mirroring the poller's semantics).
func TestFeatureProbe_ErrorFailsOpen(t *testing.T) {
	p := NewFeatureProbe(func(context.Context) (bool, error) {
		return false, errors.New("transient settings error")
	}, 0)
	rec := telemetrytest.New()

	if err := p.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (fail-open)", err)
	}
	if pts := rec.MetricPoints(metricFeatureEnabled); len(pts) != 0 {
		t.Fatalf("MetricPoints(%q) = %d, want 0 (no gauge on check error)", metricFeatureEnabled, len(pts))
	}
}

// TestFeatureProbe_NameAndInterval verifies the stable Name and the
// DefaultInterval behavior (300s default when non-positive, explicit otherwise).
func TestFeatureProbe_NameAndInterval(t *testing.T) {
	def := NewFeatureProbe(func(context.Context) (bool, error) { return true, nil }, 0)
	if def.Name() != "flowlogs-feature" {
		t.Fatalf("Name() = %q, want flowlogs-feature", def.Name())
	}
	if got := def.DefaultInterval(); got != 300*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 300s (non-positive default)", got)
	}

	ovr := NewFeatureProbe(func(context.Context) (bool, error) { return true, nil }, 90*time.Second)
	if got := ovr.DefaultInterval(); got != 90*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 90s (override)", got)
	}
}
