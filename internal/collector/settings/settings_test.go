package settings

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeAPI implements the narrow settings api interface for tests.
type fakeAPI struct {
	settings *tsclient.TailnetSettings
	err      error
	calls    int
}

func (f *fakeAPI) TailnetSettings(_ context.Context) (*tsclient.TailnetSettings, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.settings, nil
}

func TestNameAndDefaultInterval(t *testing.T) {
	c := New(&fakeAPI{}, 0)
	if c.Name() != "settings" {
		t.Fatalf("Name() = %q, want settings", c.Name())
	}
	if got := c.DefaultInterval(); got != 600*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 600s", got)
	}

	c2 := New(&fakeAPI{}, 90*time.Second)
	if got := c2.DefaultInterval(); got != 90*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 90s", got)
	}
}

// SnapshotCollector compile-time check.
var _ collector.SnapshotCollector = (*Collector)(nil)

func TestCollectEmitsEnabledPerBool(t *testing.T) {
	api := &fakeAPI{settings: &tsclient.TailnetSettings{
		DevicesApprovalOn:           true,
		DevicesAutoUpdatesOn:        false,
		UsersApprovalOn:             true,
		NetworkFlowLoggingOn:        false,
		RegionalRoutingOn:           true,
		PostureIdentityCollectionOn: false,
		DevicesKeyDurationDays:      180,
	}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.setting.enabled")
	byName := map[string]float64{}
	for _, p := range pts {
		if p.Kind != "gauge" {
			t.Fatalf("setting.enabled kind = %q, want gauge", p.Kind)
		}
		if p.Unit != "1" {
			t.Fatalf("setting.enabled unit = %q, want 1", p.Unit)
		}
		name := p.Attrs["tailscale.setting.name"]
		if name == "" {
			t.Fatalf("setting.enabled point missing tailscale.setting.name attr: %+v", p)
		}
		byName[name] = p.Value
	}

	want := map[string]float64{
		"devices_approval":            1,
		"devices_auto_updates":        0,
		"users_approval":              1,
		"network_flow_logging":        0,
		"regional_routing":            1,
		"posture_identity_collection": 0,
	}
	for name, val := range want {
		got, ok := byName[name]
		if !ok {
			t.Fatalf("missing setting.enabled point for %q; got names %v", name, keys(byName))
		}
		if got != val {
			t.Fatalf("setting %q value = %v, want %v", name, got, val)
		}
	}
	if len(pts) != len(want) {
		t.Fatalf("setting.enabled points = %d (%v), want %d", len(pts), keys(byName), len(want))
	}
}

func TestCollectEmitsKeyDuration(t *testing.T) {
	api := &fakeAPI{settings: &tsclient.TailnetSettings{DevicesKeyDurationDays: 90}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.setting.devices_key_duration")
	if len(pts) != 1 {
		t.Fatalf("devices_key_duration points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Fatalf("devices_key_duration kind = %q, want gauge", p.Kind)
	}
	if p.Unit != "d" {
		t.Fatalf("devices_key_duration unit = %q, want d", p.Unit)
	}
	if p.Value != 90 {
		t.Fatalf("devices_key_duration value = %v, want 90", p.Value)
	}
}

func TestCollectPropagatesError(t *testing.T) {
	api := &fakeAPI{err: context.DeadlineExceeded}
	rec := telemetrytest.New()
	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect: expected error, got nil")
	}
}

func keys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
