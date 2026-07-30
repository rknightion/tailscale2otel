package devices_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// day is the fixture's day-in-seconds constant, matching entityage's bucket unit.
const day = float64(24 * 60 * 60)

// epicCollector builds a devices collector over devs with the fixture clock
// pinned to `now`, plus any extra options, and returns it with the fake API and
// a fresh recorder.
func epicCollector(t *testing.T, api *fakeAPI, collectRoutes, collectPosture bool, opts ...devices.Option) (*devices.Collector, *telemetrytest.Recorder) {
	t.Helper()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	all := append([]devices.Option{devices.WithClock(func() time.Time { return now })}, opts...)
	return devices.New(api, cache, 0, collectRoutes, collectPosture, all...), telemetrytest.New()
}

// --- #414: SSH + key-expiry-disabled posture -------------------------------

func sshDevices() []tsapi.RichDevice {
	return []tsapi.RichDevice{
		{ID: "s1", Hostname: "s1", OS: "linux", User: "a", SSHEnabled: true, KeyExpiryDisabled: false},
		{ID: "s2", Hostname: "s2", OS: "linux", User: "b", SSHEnabled: false, KeyExpiryDisabled: true},
		{ID: "s3", Hostname: "s3", OS: "linux", User: "c", SSHEnabled: true, KeyExpiryDisabled: true},
	}
}

func TestCollect_SSHAndKeyExpiryDisabledPosture(t *testing.T) {
	c, rec := epicCollector(t, &fakeAPI{devices: sshDevices()}, false, false)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if pts := rec.MetricPoints("tailscale.devices.ssh_enabled"); len(pts) != 1 || pts[0].Value != 2 {
		t.Errorf("devices.ssh_enabled = %+v, want one point value 2", pts)
	}
	if pts := rec.MetricPoints("tailscale.devices.key_expiry_disabled"); len(pts) != 1 || pts[0].Value != 2 {
		t.Errorf("devices.key_expiry_disabled = %+v, want one point value 2", pts)
	}

	ssh := rec.MetricPoints("tailscale.device.ssh_enabled")
	if len(ssh) != 3 {
		t.Fatalf("device.ssh_enabled points = %d, want 3", len(ssh))
	}
	for _, want := range []struct {
		host string
		val  float64
	}{{"s1", 1}, {"s2", 0}, {"s3", 1}} {
		p, ok := pointByAttr(ssh, map[string]string{"host.id": want.host})
		if !ok || p.Value != want.val {
			t.Errorf("device.ssh_enabled[%s] = %+v ok=%v, want %v", want.host, p, ok, want.val)
		}
	}

	ked := rec.MetricPoints("tailscale.device.key_expiry_disabled")
	if len(ked) != 3 {
		t.Fatalf("device.key_expiry_disabled points = %d, want 3", len(ked))
	}
	for _, want := range []struct {
		host string
		val  float64
	}{{"s1", 0}, {"s2", 1}, {"s3", 1}} {
		p, ok := pointByAttr(ked, map[string]string{"host.id": want.host})
		if !ok || p.Value != want.val {
			t.Errorf("device.key_expiry_disabled[%s] = %+v ok=%v, want %v", want.host, p, ok, want.val)
		}
	}
}

func TestCollect_SSHAndKeyExpiryDisabled_UnsupportedProviderEmitsAbsence(t *testing.T) {
	c, rec := epicCollector(t, &fakeAPI{devices: sshDevices()}, false, false,
		devices.WithSSHData(false), devices.WithKeyExpiryDisabledData(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, name := range []string{
		"tailscale.devices.ssh_enabled", "tailscale.device.ssh_enabled",
		"tailscale.devices.key_expiry_disabled", "tailscale.device.key_expiry_disabled",
	} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("%s emitted with data-source option off: %+v (must be ABSENT, not a false zero)", name, pts)
		}
	}
}

func TestCollect_SSHAndKeyExpiryDisabled_PerEntityGate(t *testing.T) {
	c, rec := epicCollector(t, &fakeAPI{devices: sshDevices()}, false, false,
		devices.WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, name := range []string{"tailscale.device.ssh_enabled", "tailscale.device.key_expiry_disabled"} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("%s emitted with per_entity off: %+v", name, pts)
		}
	}
	if pts := rec.MetricPoints("tailscale.devices.ssh_enabled"); len(pts) != 1 {
		t.Errorf("fleet devices.ssh_enabled must survive per_entity=false, got %+v", pts)
	}
	if pts := rec.MetricPoints("tailscale.devices.key_expiry_disabled"); len(pts) != 1 {
		t.Errorf("fleet devices.key_expiry_disabled must survive per_entity=false, got %+v", pts)
	}
}

// --- #427: Linux distribution inventory ------------------------------------

func distroDevices() []tsapi.RichDevice {
	return []tsapi.RichDevice{
		{ID: "d1", Hostname: "d1", OS: "linux", Distro: tsapi.DistroInfo{Name: "ubuntu", Version: "24.04", CodeName: "noble"}},
		{ID: "d2", Hostname: "d2", OS: "linux", Distro: tsapi.DistroInfo{Name: "Ubuntu ", Version: "24.04", CodeName: "Noble"}},
		{ID: "d3", Hostname: "d3", OS: "linux", Distro: tsapi.DistroInfo{Name: "debian", Version: "12"}},
		{ID: "d4", Hostname: "d4", OS: "windows"},
	}
}

func TestCollect_DistroInventory(t *testing.T) {
	c, rec := epicCollector(t, &fakeAPI{devices: distroDevices()}, false, false)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.devices.by_distro")
	if len(pts) != 2 {
		t.Fatalf("by_distro series = %d (%+v), want 2 (ubuntu/noble, debian/unknown)", len(pts), pts)
	}
	if p, ok := pointByAttr(pts, map[string]string{
		"tailscale.distro.name": "ubuntu", "tailscale.distro.codename": "noble",
	}); !ok || p.Value != 2 {
		t.Errorf("by_distro{ubuntu,noble} = %+v ok=%v, want 2 (case/whitespace normalized)", p, ok)
	}
	if p, ok := pointByAttr(pts, map[string]string{
		"tailscale.distro.name": "debian", "tailscale.distro.codename": "unknown",
	}); !ok || p.Value != 1 {
		t.Errorf("by_distro{debian,unknown} = %+v ok=%v, want 1 (empty codename normalized)", p, ok)
	}

	per := rec.MetricPoints("tailscale.device.distro")
	if len(per) != 3 {
		t.Fatalf("device.distro points = %d (%+v), want 3 (d4 reports no distro at all)", len(per), per)
	}
	if p, ok := pointByAttr(per, map[string]string{"host.id": "d3"}); !ok ||
		p.Attrs["tailscale.distro.name"] != "debian" || p.Attrs["tailscale.distro.codename"] != "unknown" {
		t.Errorf("device.distro[d3] = %+v ok=%v", p, ok)
	}
	if _, ok := pointByAttr(per, map[string]string{"host.id": "d4"}); ok {
		t.Error("device.distro emitted for a device with no distro block; absence is required")
	}
	// The raw OS version must never become a by_distro dimension.
	for _, p := range pts {
		if _, bad := p.Attrs["os.version"]; bad {
			t.Errorf("by_distro carries os.version: %+v", p.Attrs)
		}
	}
}

func TestCollect_DistroInventory_PerEntityGate(t *testing.T) {
	c, rec := epicCollector(t, &fakeAPI{devices: distroDevices()}, false, false,
		devices.WithPerEntity(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.device.distro"); len(pts) != 0 {
		t.Errorf("device.distro emitted with per_entity off: %+v", pts)
	}
	if pts := rec.MetricPoints("tailscale.devices.by_distro"); len(pts) != 2 {
		t.Errorf("fleet by_distro must survive per_entity=false, got %+v", pts)
	}
}

func TestCollect_DistroInventory_CapsSeries(t *testing.T) {
	var devs []tsapi.RichDevice
	for i := range 60 {
		devs = append(devs, tsapi.RichDevice{
			ID:       fmt.Sprintf("c%02d", i),
			Hostname: fmt.Sprintf("c%02d", i),
			Distro:   tsapi.DistroInfo{Name: fmt.Sprintf("distro%02d", i), CodeName: "cn"},
		})
	}
	c, rec := epicCollector(t, &fakeAPI{devices: devs}, false, false)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints("tailscale.devices.by_distro")
	if len(pts) != 51 {
		t.Fatalf("by_distro series = %d, want 51 (50 capped + __other__)", len(pts))
	}
	other, ok := pointByAttr(pts, map[string]string{
		"tailscale.distro.name": "__other__", "tailscale.distro.codename": "__other__",
	})
	if !ok || other.Value != 10 {
		t.Errorf("by_distro __other__ = %+v ok=%v, want 10", other, ok)
	}
	total := 0.0
	for _, p := range pts {
		total += p.Value
	}
	if total != 60 {
		t.Errorf("by_distro total = %v, want 60 (the cap must preserve the total)", total)
	}
}

// --- #426 (devices slice): fleet age histogram -----------------------------

func TestCollect_DevicesAgeHistogram(t *testing.T) {
	devs := []tsapi.RichDevice{
		{ID: "a1", Hostname: "a1", Created: now.Add(time.Duration(-10*day) * time.Second)},
		{ID: "a2", Hostname: "a2", Created: now.Add(time.Duration(-40*day) * time.Second)},
		{ID: "a3", Hostname: "a3"}, // zero Created — provider did not supply it
	}
	c, rec := epicCollector(t, &fakeAPI{devices: devs}, false, false)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints("tailscale.devices.age")
	if len(pts) != 1 {
		t.Fatalf("devices.age points = %d (%+v), want 1 histogram series", len(pts), pts)
	}
	h := pts[0]
	if h.Kind != "histogram" {
		t.Fatalf("devices.age kind = %q, want histogram", h.Kind)
	}
	if h.Unit != "s" {
		t.Errorf("devices.age unit = %q, want s", h.Unit)
	}
	if h.Count != 2 {
		t.Errorf("devices.age count = %d, want 2 (the zero-Created device must be SKIPPED, not recorded as age 0)", h.Count)
	}
	if want := 50 * day; h.Value != want {
		t.Errorf("devices.age sum = %v, want %v", h.Value, want)
	}
	if len(h.Bounds) != 7 || h.Bounds[0] != day {
		t.Errorf("devices.age bounds = %v, want entityage.BucketsSeconds()", h.Bounds)
	}
}

// --- #413: device-invite age + delivery state ------------------------------

func inviteFixture() map[string][]tsapi.DeviceInvite {
	return map[string][]tsapi.DeviceInvite{
		"i1": {
			// Emailed, pending, 10 days old, last resent 2 days ago.
			{Accepted: false, Email: "a@example.com",
				Created:         now.Add(time.Duration(-10*day) * time.Second),
				LastEmailSentAt: now.Add(time.Duration(-2*day) * time.Second)},
			// Link-only (no email, never emailed), pending, 3 days old.
			{Accepted: false, Created: now.Add(time.Duration(-3*day) * time.Second)},
			// Emailed and already accepted — contributes no pending age.
			{Accepted: true, Email: "b@example.com", AcceptedByLogin: "b@example.com",
				Created: now.Add(time.Duration(-40*day) * time.Second)},
			// Nothing to classify on and no creation time: unknown.
			{Accepted: false},
		},
	}
}

func TestCollect_DeviceInviteDeliveryAndAge(t *testing.T) {
	api := &fakeAPI{
		devices: []tsapi.RichDevice{{ID: "i1", Hostname: "i1"}},
		invites: inviteFixture(),
	}
	c, rec := epicCollector(t, api, false, false, devices.WithDeviceInvites(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	pts := rec.MetricPoints("tailscale.device_invites.count")
	for _, want := range []struct {
		delivery string
		accepted string
		val      float64
	}{
		{"emailed", "false", 1},
		{"manual_link", "false", 1},
		{"emailed", "true", 1},
		{"unknown", "false", 1},
	} {
		p, ok := pointByAttr(pts, map[string]string{
			"tailscale.device_invite.delivery":  want.delivery,
			"tailscale.device_invite.accepted":  want.accepted,
			"tailscale.device_invite.multi_use": "false",
		})
		if !ok || p.Value != want.val {
			t.Errorf("device_invites.count{delivery=%s,accepted=%s} = %+v ok=%v, want %v",
				want.delivery, want.accepted, p, ok, want.val)
		}
	}

	age := rec.MetricPoints("tailscale.device_invites.pending_age")
	if len(age) != 1 {
		t.Fatalf("device_invites.pending_age points = %d (%+v), want 1", len(age), age)
	}
	h := age[0]
	if h.Unit != "s" {
		t.Errorf("pending_age unit = %q, want s", h.Unit)
	}
	if h.Count != 2 {
		t.Errorf("pending_age count = %d, want 2 (accepted invites and zero-Created invites excluded)", h.Count)
	}
	if wantSum := 13 * day; h.Value != wantSum {
		t.Errorf("pending_age sum = %v, want %v", h.Value, wantSum)
	}

	// The delivery state must also reach the per-invite log event.
	var sawEmailed bool
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "tailscale.device_invite" {
			continue
		}
		if lr.Attrs["tailscale.device_invite.delivery"] == "emailed" {
			sawEmailed = true
		}
	}
	if !sawEmailed {
		t.Error("tailscale.device_invite log event carries no tailscale.device_invite.delivery attribute")
	}
}

// --- #421: per-device subrequest coverage ----------------------------------

func coverageDevices() []tsapi.RichDevice {
	return []tsapi.RichDevice{
		{ID: "k1", Hostname: "k1"},
		{ID: "k2", Hostname: "k2"},
		{ID: "k3", Hostname: "k3"},
	}
}

func TestCollect_SubrequestCoverage(t *testing.T) {
	cov := apistate.NewCoverage()
	api := &fakeAPI{
		devices:     coverageDevices(),
		inviteFail:  "k1",
		inviteErr:   &tsapi.StatusError{Method: "GET", Code: 403},
		postureFail: "k2",
		postureErr:  context.DeadlineExceeded,
		posture:     map[string]map[string]any{"k1": {"node:os": "linux"}},
	}
	c, rec := epicCollector(t, api, false, true,
		devices.WithDeviceInvites(true), devices.WithCoverage(cov))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect must stay non-fatal on per-device subrequest failures: %v", err)
	}

	attempts := rec.MetricPoints("tailscale2otel.subrequest.attempts")
	for _, sub := range []string{"device_invites", "posture_attributes"} {
		p, ok := pointByAttr(attempts, map[string]string{
			"tailscale.collector": "devices", "tailscale.subrequest": sub,
		})
		if !ok || p.Value != 3 {
			t.Errorf("subrequest.attempts{%s} = %+v ok=%v, want 3", sub, p, ok)
		}
	}

	failures := rec.MetricPoints("tailscale2otel.subrequest.failures")
	if p, ok := pointByAttr(failures, map[string]string{
		"tailscale.subrequest": "device_invites", "tailscale.api.state": "scope_denied",
	}); !ok || p.Value != 1 {
		t.Errorf("subrequest.failures{device_invites,scope_denied} = %+v ok=%v, want 1", p, ok)
	}
	if p, ok := pointByAttr(failures, map[string]string{
		"tailscale.subrequest": "posture_attributes", "tailscale.api.state": "transient_failure",
	}); !ok || p.Value != 1 {
		t.Errorf("subrequest.failures{posture_attributes,transient_failure} = %+v ok=%v, want 1", p, ok)
	}

	coverage := rec.MetricPoints("tailscale2otel.subrequest.coverage")
	for _, sub := range []string{"device_invites", "posture_attributes"} {
		p, ok := pointByAttr(coverage, map[string]string{"tailscale.subrequest": sub})
		if !ok || p.Value < 0.66 || p.Value > 0.67 {
			t.Errorf("subrequest.coverage{%s} = %+v ok=%v, want ~0.667", sub, p, ok)
		}
	}

	// The in-process tally the status page reads must agree with the metrics.
	snap := cov.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Coverage.Snapshot() = %d entries, want 2", len(snap))
	}
	for _, e := range snap {
		if e.Collector != "devices" || e.Attempted != 3 || e.Succeeded != 2 || e.Failed != 1 {
			t.Errorf("coverage entry %+v, want collector=devices attempted=3 succeeded=2 failed=1", e)
		}
	}
}

func TestCollect_SubrequestCoverage_ResetsEachTick(t *testing.T) {
	cov := apistate.NewCoverage()
	api := &fakeAPI{
		devices:    coverageDevices(),
		inviteFail: "k1",
		inviteErr:  &tsapi.StatusError{Method: "GET", Code: 403},
	}
	c, rec := epicCollector(t, api, false, false,
		devices.WithDeviceInvites(true), devices.WithCoverage(cov))
	for range 2 {
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
	}
	snap := cov.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Coverage.Snapshot() = %d entries, want 1", len(snap))
	}
	if snap[0].Attempted != 3 {
		t.Errorf("Attempted = %d after two ticks, want 3 (Reset must run per tick)", snap[0].Attempted)
	}
}

// TestEpic479SignalsMatchCatalog is the declaration<->emission drift guard for
// the signals added by #413/#414/#421/#426/#427. It exists alongside
// TestCatalogMatchesEmitted because that test's fixture leaves Created unset on
// every device and invite, so neither new age histogram is ever emitted there —
// a unit or description drift on them would sail straight through.
func TestEpic479SignalsMatchCatalog(t *testing.T) {
	api := &fakeAPI{
		devices: []tsapi.RichDevice{{
			ID: "e1", Hostname: "e1", OS: "linux", User: "u",
			SSHEnabled: true, KeyExpiryDisabled: true,
			Created: now.Add(time.Duration(-10*day) * time.Second),
			Distro:  tsapi.DistroInfo{Name: "ubuntu", Version: "24.04", CodeName: "noble"},
		}},
		invites: map[string][]tsapi.DeviceInvite{
			"e1": {{Accepted: false, Email: "a@example.com",
				Created: now.Add(time.Duration(-5*day) * time.Second)}},
		},
	}
	c, rec := epicCollector(t, api, false, false,
		devices.WithDeviceInvites(true), devices.WithCoverage(apistate.NewCoverage()))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	for _, m := range devices.Catalog() {
		declared[m.Name] = m
	}
	for _, name := range []string{
		"tailscale.devices.ssh_enabled", "tailscale.device.ssh_enabled",
		"tailscale.devices.key_expiry_disabled", "tailscale.device.key_expiry_disabled",
		"tailscale.devices.by_distro", "tailscale.device.distro",
		"tailscale.devices.age", "tailscale.device_invites.pending_age",
	} {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			t.Errorf("%s not emitted by the all-signals fixture", name)
			continue
		}
		d, ok := declared[name]
		if !ok {
			t.Errorf("%s is not declared in devices.Catalog()", name)
			continue
		}
		if pts[0].Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, pts[0].Unit, d.Unit)
		}
		if pts[0].Description != d.Description {
			t.Errorf("%s: emitted description != catalog description", name)
		}
		wantHistogram := d.Instrument == metricdoc.Histogram
		if got := pts[0].Kind == "histogram"; got != wantHistogram {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q", name, d.Instrument, pts[0].Kind)
		}
	}

	// The coverage signals belong to internal/apistate's catalog, NOT this
	// collector's — assert they are emitted but deliberately absent here, so a
	// future "fix" that copies them into devices.Catalog() double-declares them
	// in docs/metrics.md and fails loudly instead of silently.
	for _, name := range []string{
		"tailscale2otel.subrequest.attempts",
		"tailscale2otel.subrequest.failures",
		"tailscale2otel.subrequest.coverage",
	} {
		if len(rec.MetricPoints(name)) == 0 {
			t.Errorf("%s not emitted with a Coverage wired", name)
		}
		if _, ok := declared[name]; ok {
			t.Errorf("%s must be declared by internal/apistate, not devices.Catalog()", name)
		}
	}
}

func TestCollect_SubrequestCoverage_NilCoverageIsNoOp(t *testing.T) {
	api := &fakeAPI{
		devices:    coverageDevices(),
		inviteFail: "k1",
		inviteErr:  &tsapi.StatusError{Method: "GET", Code: 403},
	}
	c, rec := epicCollector(t, api, false, false, devices.WithDeviceInvites(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, name := range []string{
		"tailscale2otel.subrequest.attempts",
		"tailscale2otel.subrequest.failures",
		"tailscale2otel.subrequest.coverage",
	} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("%s emitted without WithCoverage: %+v", name, pts)
		}
	}
}
