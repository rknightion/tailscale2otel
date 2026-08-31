package devices_test

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// TestCatalogMatchesEmitted is the declaration<->emission drift guard: every
// metric this collector actually emits must be declared in Catalog() with a
// matching unit, instrument, and description (docs/metrics.md is generated from
// Catalog(), so this keeps the generated docs honest), and every emitted log
// event must be in LogCatalog().
//
// The collector is driven with collectRoutes=true AND collectPosture=true (and
// a populated posture map) so that all declared metrics — including the two
// route gauges, the two self-observability enrich.cache_* gauges, and the
// per-device posture info gauge — plus the posture log event are emitted in one
// pass.
func TestCatalogMatchesEmitted(t *testing.T) {
	devs := sampleDevices()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{
			"3690401478992208": {"custom:foo": "bar", "node:os": "linux", "intune:isEncrypted": true},
			"n-desktop":        {"custom:foo": "baz"},
			"n-phone":          {},
		},
		// custom:foo expires in 5 days (within the fixed 14-day warn window), so
		// both tailscale.device.attribute.expiry and the
		// tailscale.device.attribute.expiring WARN log are exercised here too.
		postureExpiries: map[string]map[string]time.Time{
			"3690401478992208": {"custom:foo": now.Add(5 * 24 * time.Hour)},
		},
		invites: map[string][]tsapi.DeviceInvite{
			"3690401478992208": {{Accepted: true, AllowExitNode: true, MultiUse: false}},
		},
	}
	// Wildcard attribute_namespaces so both the numeric and info attribute gauges
	// (tailscale.device.attribute{,.info}) are emitted and drift-checked too.
	// WithClock pins "now" so the attribute-expiry WARN log's day threshold is
	// deterministic (matches the other collectors' fixed-clock catalog tests).
	tr := apistate.NewTracker()
	c := devices.New(api, cache, 0, true, true,
		devices.WithAttributeNamespaces([]string{"*"}),
		devices.WithPostureComplianceChecks([]devices.PostureComplianceCheck{{
			Name: "managed", Attribute: "intune:isEncrypted", Equals: "true",
		}}),
		devices.WithDeviceInvites(true),
		devices.WithClock(func() time.Time { return now }),
		devices.WithAPIState(tr))

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	declared := map[string]metricdoc.Metric{}
	// The per-operation availability signals (tailscale2otel.api.availability,
	// tailscale2otel.api.last_probe) are declared once, in the shared
	// internal/apistate catalog (which internal/catalog aggregates), not per
	// collector — every collector wiring apistate.Observe emits the same two
	// descriptors (#524).
	for _, m := range append(devices.Catalog(), apistate.Catalog()...) {
		declared[m.Name] = m
	}

	for _, name := range rec.MetricNames() {
		pts := rec.MetricPoints(name)
		if len(pts) == 0 {
			continue
		}
		p0 := pts[0]
		d, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in devices.Catalog()", name)
			continue
		}
		if p0.Unit != d.Unit {
			t.Errorf("%s: emitted unit %q != catalog unit %q", name, p0.Unit, d.Unit)
		}
		if p0.Description != d.Description {
			t.Errorf("%s: emitted description %q != catalog description %q", name, p0.Description, d.Description)
		}
		wantCounter := d.Instrument == metricdoc.Counter
		gotCounter := p0.Kind == "sum" && p0.Monotonic
		if wantCounter != gotCounter {
			t.Errorf("%s: catalog instrument %q but emitted kind=%q monotonic=%v", name, d.Instrument, p0.Kind, p0.Monotonic)
		}
	}

	// The posture info gauge must be both emitted and declared.
	if pts := rec.MetricPoints("tailscale.device.posture"); len(pts) == 0 {
		t.Error("posture info gauge tailscale.device.posture not emitted with collectPosture=true")
	}
	if _, ok := declared["tailscale.device.posture"]; !ok {
		t.Error("posture info gauge tailscale.device.posture not declared in devices.Catalog()")
	}

	// The attribute metrics (hybrid: numeric + info) must be emitted under a
	// wildcard allow-list and declared in Catalog().
	for _, name := range []string{"tailscale.device.attribute", "tailscale.device.attribute.info"} {
		if pts := rec.MetricPoints(name); len(pts) == 0 {
			t.Errorf("attribute metric %q not emitted with attribute_namespaces=[*]", name)
		}
		if _, ok := declared[name]; !ok {
			t.Errorf("attribute metric %q not declared in devices.Catalog()", name)
		}
	}

	// The attribute-expiry gauge (issue #164) must be emitted (custom:foo carries
	// an expiry above) and declared in Catalog().
	if pts := rec.MetricPoints("tailscale.device.attribute.expiry"); len(pts) == 0 {
		t.Error("tailscale.device.attribute.expiry not emitted with an expiring attribute present")
	}
	if _, ok := declared["tailscale.device.attribute.expiry"]; !ok {
		t.Error("tailscale.device.attribute.expiry not declared in devices.Catalog()")
	}

	if pts := rec.MetricPoints("tailscale.devices.posture_compliance.failed"); len(pts) != 1 {
		t.Errorf("posture-compliance metric points = %d, want 1", len(pts))
	}
	if _, ok := declared["tailscale.devices.posture_compliance.failed"]; !ok {
		t.Error("tailscale.devices.posture_compliance.failed not declared in devices.Catalog()")
	}

	// The device-invites count gauge must be both emitted (collect_device_invites
	// on) and declared in Catalog().
	if pts := rec.MetricPoints("tailscale.device_invites.count"); len(pts) == 0 {
		t.Error("device_invites count not emitted with WithDeviceInvites(true)")
	}
	if _, ok := declared["tailscale.device_invites.count"]; !ok {
		t.Error("tailscale.device_invites.count not declared in devices.Catalog()")
	}

	logDeclared := map[string]bool{}
	for _, le := range devices.LogCatalog() {
		logDeclared[le.Name] = true
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName != "" && !logDeclared[lr.EventName] {
			t.Errorf("emitted log event %q is not declared in devices.LogCatalog()", lr.EventName)
		}
	}

	// The API-state signals belong to internal/apistate's catalog, NOT this
	// collector's — assert they are emitted (with a tracker wired, #524) but
	// deliberately absent from devices.Catalog() itself, so a future "fix" that
	// copies them in double-declares them in docs/metrics.md and fails loudly
	// instead of silently.
	ownDeclared := map[string]bool{}
	for _, m := range devices.Catalog() {
		ownDeclared[m.Name] = true
	}
	for _, name := range []string{apistate.MetricAvailability, apistate.MetricLastProbe} {
		if len(rec.MetricPoints(name)) == 0 {
			t.Errorf("%s not emitted with a tracker wired", name)
		}
		if ownDeclared[name] {
			t.Errorf("%s must be declared by internal/apistate, not devices.Catalog()", name)
		}
	}
}
