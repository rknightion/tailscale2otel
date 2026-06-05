package postureintegrations

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

type fakeAPI struct {
	ints []tsapi.PostureIntegration
	err  error
}

func (f *fakeAPI) PostureIntegrations(context.Context) ([]tsapi.PostureIntegration, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ints, nil
}

var _ collector.SnapshotCollector = (*Collector)(nil)

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "posture_integrations" {
		t.Fatalf("Name() = %q, want posture_integrations", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}
}

func TestCollectEmitsPerIntegration(t *testing.T) {
	sync := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	api := &fakeAPI{ints: []tsapi.PostureIntegration{{
		ID:       "p1",
		Provider: "intune",
		Status: tsapi.PostureIntegrationStatus{
			LastSync: sync, MatchedCount: 4, PossibleMatchedCount: 5, ProviderHostCount: 10,
		},
	}}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if cnt := rec.MetricPoints("tailscale.posture_integrations.count"); len(cnt) != 1 || cnt[0].Value != 1 {
		t.Fatalf("count = %+v, want one point value 1", cnt)
	}

	single := func(name string) telemetrytest.MetricPoint {
		t.Helper()
		pts := rec.MetricPoints(name)
		if len(pts) != 1 {
			t.Fatalf("%s points = %d, want 1", name, len(pts))
		}
		if pts[0].Attrs["tailscale.posture.provider"] != "intune" {
			t.Errorf("%s provider attr = %q, want intune", name, pts[0].Attrs["tailscale.posture.provider"])
		}
		if pts[0].Attrs["tailscale.posture.integration"] != "p1" {
			t.Errorf("%s integration attr = %q, want p1", name, pts[0].Attrs["tailscale.posture.integration"])
		}
		return pts[0]
	}

	if p := single("tailscale.posture_integration.matched"); p.Value != 4 {
		t.Errorf("matched = %v, want 4", p.Value)
	}
	if p := single("tailscale.posture_integration.possible_matched"); p.Value != 5 {
		t.Errorf("possible_matched = %v, want 5", p.Value)
	}
	if p := single("tailscale.posture_integration.provider_hosts"); p.Value != 10 {
		t.Errorf("provider_hosts = %v, want 10", p.Value)
	}
	ls := single("tailscale.posture_integration.last_sync")
	if ls.Unit != "s" {
		t.Errorf("last_sync unit = %q, want s", ls.Unit)
	}
	if ls.Value != float64(sync.Unix()) {
		t.Errorf("last_sync = %v, want %v", ls.Value, float64(sync.Unix()))
	}
}

func TestCollectEmptyIntegrations(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{ints: nil}, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if cnt := rec.MetricPoints("tailscale.posture_integrations.count"); len(cnt) != 1 || cnt[0].Value != 0 {
		t.Fatalf("count = %+v, want one point value 0", cnt)
	}
	if m := rec.MetricPoints("tailscale.posture_integration.matched"); len(m) != 0 {
		t.Fatalf("matched points = %d, want 0", len(m))
	}
}

func TestLastSyncSkippedWhenZero(t *testing.T) {
	api := &fakeAPI{ints: []tsapi.PostureIntegration{{
		ID: "p1", Provider: "intune",
		Status: tsapi.PostureIntegrationStatus{MatchedCount: 1}, // zero LastSync
	}}}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if ls := rec.MetricPoints("tailscale.posture_integration.last_sync"); len(ls) != 0 {
		t.Fatalf("last_sync points = %d, want 0 when LastSync is zero", len(ls))
	}
	if m := rec.MetricPoints("tailscale.posture_integration.matched"); len(m) != 1 {
		t.Fatalf("matched points = %d, want 1 (other metrics still emitted)", len(m))
	}
}

func TestCollectPropagatesError(t *testing.T) {
	rec := telemetrytest.New()
	if err := New(&fakeAPI{err: context.DeadlineExceeded}, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("expected error, got nil")
	}
}
