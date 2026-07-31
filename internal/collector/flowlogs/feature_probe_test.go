package flowlogs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
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

// TestFeatureProbe_ErrorReportsFailureWithoutFeatureGauge verifies that a check
// error leaves the poll collector's fail-open behavior alone but makes the
// independent probe failure visible to the scheduler status tracker.
func TestFeatureProbe_ErrorReportsFailureWithoutFeatureGauge(t *testing.T) {
	wantErr := errors.New("transient settings error")
	p := NewFeatureProbe(func(context.Context) (bool, error) {
		return false, wantErr
	}, 0)
	rec := telemetrytest.New()

	if err := p.Collect(context.Background(), rec.Emitter()); !errors.Is(err, wantErr) {
		t.Fatalf("Collect() error = %v, want original %v", err, wantErr)
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

// TestFeatureProbe_RecordsAvailability is the #524 regression for this probe.
//
// In a stream-only deployment the flow-log poller is not registered and the
// probe stands in for it, but the probe never recorded an availability state —
// so its capability-matrix row read `unknown` forever and a revoked credential
// on the settings endpoint it polls could fire neither ts2o-api-credential-
// rejected nor ts2o-api-scope-denied.
func TestFeatureProbe_RecordsAvailability(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want apistate.State
	}{
		{"success", nil, apistate.StateSupported},
		{"401 revoked credential", &tsapi.StatusError{Code: 401}, apistate.StateCredentialRejected},
		{"403 missing scope", &tsapi.StatusError{Code: 403}, apistate.StateScopeDenied},
		{"400 malformed request", &tsapi.StatusError{Code: 400}, apistate.StateRequestRejected},
		{"transport failure", context.DeadlineExceeded, apistate.StateTransientFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := telemetrytest.New()
			tr := apistate.NewTracker()
			p := NewFeatureProbe(func(context.Context) (bool, error) { return true, tc.err }, 0,
				WithFeatureProbeAPIState(tr))

			err := p.Collect(context.Background(), rec.Emitter())
			if (err != nil) != (tc.err != nil) {
				t.Fatalf("Collect() error = %v, want error = %v (control flow must be unchanged)", err, tc.err != nil)
			}
			if got := availabilityStates(t, rec)[opGetTailnetSettings]; got != string(tc.want) {
				t.Errorf("availability[%s] = %q, want %q", opGetTailnetSettings, got, tc.want)
			}
			snap := tr.Snapshot()
			if len(snap) != 1 || snap[0].Collector != "flowlogs-feature" || snap[0].Operation != opGetTailnetSettings {
				t.Fatalf("tracker snapshot = %+v, want one flowlogs-feature/%s entry", snap, opGetTailnetSettings)
			}
			if snap[0].State != tc.want {
				t.Errorf("tracker state = %q, want %q", snap[0].State, tc.want)
			}
		})
	}
}

// TestFeatureProbe_NilCheckRecordsNoAvailability pins the honest-unknown rule: a
// probe with no check makes no API call, so claiming `supported` would report a
// probe that never happened.
func TestFeatureProbe_NilCheckRecordsNoAvailability(t *testing.T) {
	rec := telemetrytest.New()
	tr := apistate.NewTracker()
	p := NewFeatureProbe(nil, 0, WithFeatureProbeAPIState(tr))

	if err := p.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints(apistate.MetricAvailability); len(pts) != 0 {
		t.Fatalf("MetricPoints(%q) = %d, want 0 (no check = no probe)", apistate.MetricAvailability, len(pts))
	}
	if snap := tr.Snapshot(); len(snap) != 0 {
		t.Fatalf("tracker snapshot = %+v, want empty", snap)
	}
}

// TestFeatureProbe_NilTrackerStillEmits pins that the metric is independent of
// the in-process tracker, matching every other apistate call site.
func TestFeatureProbe_NilTrackerStillEmits(t *testing.T) {
	rec := telemetrytest.New()
	p := NewFeatureProbe(func(context.Context) (bool, error) { return true, nil }, 0)

	if err := p.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got := availabilityStates(t, rec)[opGetTailnetSettings]; got != string(apistate.StateSupported) {
		t.Errorf("availability = %q, want supported", got)
	}
}
