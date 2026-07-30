package settings

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// fakeAPI implements the narrow settings api interface for tests.
type fakeAPI struct {
	settings *tsapi.TailnetSettings
	err      error
	calls    int
}

func (f *fakeAPI) TailnetSettings(_ context.Context) (*tsapi.TailnetSettings, error) {
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
	api := &fakeAPI{settings: &tsapi.TailnetSettings{
		DevicesApprovalOn:           true,
		DevicesAutoUpdatesOn:        false,
		UsersApprovalOn:             true,
		NetworkFlowLoggingOn:        false,
		RegionalRoutingOn:           true,
		PostureIdentityCollectionOn: false,
		HTTPSEnabled:                true,
		ACLsExternallyManagedOn:     false,
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
		"https_enabled":               1,
		"acls_externally_managed":     0,
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
	api := &fakeAPI{settings: &tsapi.TailnetSettings{DevicesKeyDurationDays: 90}}
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

func TestCollectEmitsExternalTailnetsRole(t *testing.T) {
	api := &fakeAPI{settings: &tsapi.TailnetSettings{UsersRoleAllowedToJoinExternalTailnets: "member"}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.setting.users_external_tailnets_role")
	if len(pts) != 1 {
		t.Fatalf("role points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Fatalf("role kind = %q, want gauge", p.Kind)
	}
	if p.Value != 1 {
		t.Fatalf("role value = %v, want 1 (info gauge)", p.Value)
	}
	if got := p.Attrs["tailscale.setting.role"]; got != "member" {
		t.Fatalf("role attr = %q, want member", got)
	}
}

// TestCollectEmitsACLsExternalLinkSetWhenPresent covers #418: a non-nil,
// non-empty ACLsExternalLink must derive to a boolean 1, and the URI string
// itself must never appear anywhere in the emitted attrs.
func TestCollectEmitsACLsExternalLinkSetWhenPresent(t *testing.T) {
	link := "https://github.com/example/tailnet-policy"
	api := &fakeAPI{settings: &tsapi.TailnetSettings{ACLsExternalLink: &link}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.setting.enabled")
	var found bool
	for _, p := range pts {
		if p.Attrs["tailscale.setting.name"] != "acls_external_link_set" {
			continue
		}
		found = true
		if p.Value != 1 {
			t.Errorf("acls_external_link_set value = %v, want 1", p.Value)
		}
		for k, v := range p.Attrs {
			if v == link {
				t.Fatalf("attribute %q carries the raw ACL link URI %q — must never be emitted", k, v)
			}
		}
	}
	if !found {
		t.Fatal("missing acls_external_link_set point when ACLsExternalLink is set")
	}
}

// TestCollectEmitsACLsExternalLinkUnsetWhenPresentButEmpty covers "configured
// permission, no link configured": the pointer is non-nil but empty.
func TestCollectEmitsACLsExternalLinkUnsetWhenPresentButEmpty(t *testing.T) {
	empty := ""
	api := &fakeAPI{settings: &tsapi.TailnetSettings{ACLsExternalLink: &empty}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.setting.enabled")
	var found bool
	for _, p := range pts {
		if p.Attrs["tailscale.setting.name"] != "acls_external_link_set" {
			continue
		}
		found = true
		if p.Value != 0 {
			t.Errorf("acls_external_link_set value = %v, want 0", p.Value)
		}
	}
	if !found {
		t.Fatal("missing acls_external_link_set point when ACLsExternalLink is present-but-empty")
	}
}

// TestCollectOmitsACLsExternalLinkWhenAbsent covers #418's core premise
// reshape: an absent (nil) ACLsExternalLink means unsupported/permission-
// denied, and must be treated as ABSENCE — no data point at all — never a
// healthy-looking false.
func TestCollectOmitsACLsExternalLinkWhenAbsent(t *testing.T) {
	api := &fakeAPI{settings: &tsapi.TailnetSettings{ACLsExternalLink: nil}}
	rec := telemetrytest.New()

	if err := New(api, 0).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, p := range rec.MetricPoints("tailscale.setting.enabled") {
		if p.Attrs["tailscale.setting.name"] == "acls_external_link_set" {
			t.Fatalf("unexpected acls_external_link_set point when ACLsExternalLink is nil (absence must not emit a healthy zero): %+v", p)
		}
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
