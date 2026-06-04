package devices_test

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// fakeAPI returns a canned rich-device list and posture map, satisfying the
// collector's narrow api interface.
type fakeAPI struct {
	devices     []tsapi.RichDevice
	err         error
	calls       int
	posture     map[string]map[string]any
	postureErr  error
	postureIDs  []string
	postureFail string // device ID whose posture call should return postureErr
}

func (f *fakeAPI) DevicesRich(_ context.Context) ([]tsapi.RichDevice, error) {
	f.calls++
	return f.devices, f.err
}

func (f *fakeAPI) DevicePostureAttributes(_ context.Context, deviceID string) (map[string]any, error) {
	f.postureIDs = append(f.postureIDs, deviceID)
	if deviceID == f.postureFail {
		return nil, f.postureErr
	}
	return f.posture[deviceID], nil
}

// now anchors the deterministic timestamps used in fixtures.
var now = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

// sampleDevices returns three rich devices exercising the metric, route, DERP
// and cache paths: one online linux box (DERP latency + distro + routes), one
// offline windows box, one external phone.
func sampleDevices() []tsapi.RichDevice {
	return []tsapi.RichDevice{
		{
			// Online (ConnectedToControl), key expires, no update, linux,
			// authorized, internal, DERP latency, distro, advertised+enabled routes.
			ID:                 "3690401478992208",
			NodeID:             "nDdiLaptopCNTRL",
			Name:               "laptop.tail1a2b.ts.net",
			Hostname:           "laptop",
			OS:                 "linux",
			User:               "alice@example.com",
			Addresses:          []string{"100.64.0.1", "fd7a:115c:a1e0::1"},
			Authorized:         true,
			IsExternal:         false,
			KeyExpiryDisabled:  false,
			ConnectedToControl: true,
			UpdateAvailable:    false,
			Expires:            now.Add(48 * time.Hour),
			LastSeen:           now.Add(-1 * time.Minute),
			AdvertisedRoutes:   []string{"0.0.0.0/0", "10.0.0.0/24"},
			EnabledRoutes:      []string{"10.0.0.0/24"},
			Distro:             tsapi.DistroInfo{Name: "ubuntu", Version: "24.04", CodeName: "noble"},
			DERPLatency: map[string]tsapi.DERPRegion{
				"Frankfurt": {Preferred: true, LatencyMs: 12.5},
				"Amsterdam": {Preferred: false, LatencyMs: 8.0},
			},
		},
		{
			// Offline (not connected), key expiry disabled, update available,
			// windows, authorized, internal, no DERP, no routes.
			ID:                 "n-desktop",
			NodeID:             "nDdiDesktopCNTRL",
			Name:               "desktop.tail1a2b.ts.net",
			Hostname:           "desktop",
			OS:                 "windows",
			User:               "bob@example.com",
			Addresses:          []string{"100.64.0.2"},
			Authorized:         true,
			IsExternal:         false,
			KeyExpiryDisabled:  true,
			ConnectedToControl: false,
			UpdateAvailable:    true,
			Expires:            now.Add(72 * time.Hour),
			LastSeen:           now.Add(-2 * time.Hour),
		},
		{
			// External phone, never seen (zero LastSeen), zero Expires, linux,
			// unauthorized, offline, no distro version.
			ID:                 "n-phone",
			NodeID:             "nDdiPhoneCNTRL",
			Name:               "phone.tail1a2b.ts.net",
			Hostname:           "phone",
			OS:                 "linux",
			User:               "carol@example.com",
			Addresses:          []string{"100.64.0.3"},
			Authorized:         false,
			IsExternal:         true,
			KeyExpiryDisabled:  false,
			ConnectedToControl: false,
			UpdateAvailable:    false,
		},
	}
}

func newCollector(t *testing.T, devs []tsapi.RichDevice) (*devices.Collector, *enrich.DeviceCache, *fakeAPI) {
	t.Helper()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{devices: devs}
	c := devices.New(api, cache, 0, false, false)
	return c, cache, api
}

// pointByAttr finds the single metric point whose attrs match all of want.
func pointByAttr(pts []telemetrytest.MetricPoint, want map[string]string) (telemetrytest.MetricPoint, bool) {
	for _, p := range pts {
		ok := true
		for k, v := range want {
			if p.Attrs[k] != v {
				ok = false
				break
			}
		}
		if ok {
			return p, true
		}
	}
	return telemetrytest.MetricPoint{}, false
}

func TestNameAndDefaultInterval(t *testing.T) {
	c, _, _ := newCollector(t, nil)
	if c.Name() != "devices" {
		t.Fatalf("Name() = %q, want devices", c.Name())
	}
	if got := c.DefaultInterval(); got != 60*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 60s (zero interval default)", got)
	}

	cache := enrich.NewDeviceCache()
	c2 := devices.New(&fakeAPI{}, cache, 30*time.Second, false, false)
	if got := c2.DefaultInterval(); got != 30*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 30s (explicit)", got)
	}
}

func TestCollect_ReturnsAPIError(t *testing.T) {
	cache := enrich.NewDeviceCache()
	api := &fakeAPI{err: context.DeadlineExceeded}
	c := devices.New(api, cache, 0, false, false)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect() error = nil, want non-nil")
	}
}

func TestCollect_PerEntityFalse(t *testing.T) {
	// WithPerEntity(false) suppresses every per-device gauge (incl. routes, which
	// would otherwise emit since collectRoutes=true) while keeping the aggregate
	// devices.count rollup and the enrichment-cache self-metrics.
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	c := devices.New(&fakeAPI{devices: sampleDevices()}, cache, 0, true /*routes*/, false /*posture*/, devices.WithPerEntity(false))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, name := range []string{
		"tailscale.device.online",
		"tailscale.device.last_seen",
		"tailscale.device.key.expiry",
		"tailscale.device.update_available",
		"tailscale.device.derp.latency",
		"tailscale.device.routes.advertised",
		"tailscale.device.routes.enabled",
	} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("per-entity gauge %q emitted with WithPerEntity(false): %+v", name, pts)
		}
	}

	if pts := rec.MetricPoints("tailscale.devices.count"); len(pts) == 0 {
		t.Error("aggregate tailscale.devices.count not emitted with WithPerEntity(false)")
	}
	if pts := rec.MetricPoints("tailscale2otel.enrich.cache_size"); len(pts) == 0 {
		t.Error("enrich.cache_size self-metric not emitted with WithPerEntity(false)")
	}
}

func TestCollect_Online(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device.online")
	if len(pts) != 3 {
		t.Fatalf("online points = %d, want 3", len(pts))
	}

	laptop, ok := pointByAttr(pts, map[string]string{semconv.HostID: "3690401478992208"})
	if !ok {
		t.Fatalf("no online point for laptop; points=%+v", pts)
	}
	if laptop.Unit != semconv.UnitDimensionless {
		t.Fatalf("online unit = %q, want %q", laptop.Unit, semconv.UnitDimensionless)
	}
	if laptop.Kind != "gauge" {
		t.Fatalf("online kind = %q, want gauge", laptop.Kind)
	}
	if laptop.Value != 1 {
		t.Fatalf("laptop online = %v, want 1 (connected to control)", laptop.Value)
	}
	// Device-identity attrs.
	if laptop.Attrs[semconv.HostName] != "laptop" {
		t.Fatalf("online host.name = %q, want laptop", laptop.Attrs[semconv.HostName])
	}
	if laptop.Attrs[semconv.OSType] != "linux" {
		t.Fatalf("online os.type = %q, want linux", laptop.Attrs[semconv.OSType])
	}
	if laptop.Attrs[semconv.OSVersion] != "24.04" {
		t.Fatalf("online os.version = %q, want 24.04", laptop.Attrs[semconv.OSVersion])
	}
	if laptop.Attrs[semconv.AttrUser] != "alice@example.com" {
		t.Fatalf("online tailscale.user = %q, want alice@example.com", laptop.Attrs[semconv.AttrUser])
	}

	desktop, ok := pointByAttr(pts, map[string]string{semconv.HostID: "n-desktop"})
	if !ok {
		t.Fatal("no online point for desktop")
	}
	if desktop.Value != 0 {
		t.Fatalf("desktop online = %v, want 0 (not connected)", desktop.Value)
	}
	// A device with empty distro version must omit os.version entirely.
	if _, present := desktop.Attrs[semconv.OSVersion]; present {
		t.Fatalf("desktop os.version present = %q, want omitted", desktop.Attrs[semconv.OSVersion])
	}
}

func TestCollect_LastSeen(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device.last_seen")
	// phone has zero LastSeen and must be skipped -> 2 points.
	if len(pts) != 2 {
		t.Fatalf("last_seen points = %d, want 2 (phone skipped)", len(pts))
	}
	laptop, ok := pointByAttr(pts, map[string]string{semconv.HostID: "3690401478992208"})
	if !ok {
		t.Fatal("no last_seen point for laptop")
	}
	if laptop.Unit != semconv.UnitSeconds {
		t.Fatalf("last_seen unit = %q, want s", laptop.Unit)
	}
	want := float64(now.Add(-1 * time.Minute).Unix())
	if laptop.Value != want {
		t.Fatalf("laptop last_seen = %v, want %v", laptop.Value, want)
	}
	if _, ok := pointByAttr(pts, map[string]string{semconv.HostID: "n-phone"}); ok {
		t.Fatal("phone last_seen point present, want skipped (zero LastSeen)")
	}
}

func TestCollect_KeyExpiry(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device.key.expiry")
	// laptop: emitted. desktop: KeyExpiryDisabled -> skipped. phone: zero Expires -> skipped.
	if len(pts) != 1 {
		t.Fatalf("key.expiry points = %d, want 1 (only laptop)", len(pts))
	}
	laptop := pts[0]
	if laptop.Attrs[semconv.HostID] != "3690401478992208" {
		t.Fatalf("key.expiry host.id = %q, want laptop id", laptop.Attrs[semconv.HostID])
	}
	if laptop.Unit != semconv.UnitSeconds {
		t.Fatalf("key.expiry unit = %q, want s", laptop.Unit)
	}
	want := float64(now.Add(48 * time.Hour).Unix())
	if laptop.Value != want {
		t.Fatalf("laptop key.expiry = %v, want %v", laptop.Value, want)
	}
}

func TestCollect_UpdateAvailable(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device.update_available")
	if len(pts) != 3 {
		t.Fatalf("update_available points = %d, want 3", len(pts))
	}
	desktop, ok := pointByAttr(pts, map[string]string{semconv.HostID: "n-desktop"})
	if !ok {
		t.Fatal("no update_available point for desktop")
	}
	if desktop.Unit != semconv.UnitDimensionless {
		t.Fatalf("update_available unit = %q, want 1", desktop.Unit)
	}
	if desktop.Value != 1 {
		t.Fatalf("desktop update_available = %v, want 1", desktop.Value)
	}
	laptop, _ := pointByAttr(pts, map[string]string{semconv.HostID: "3690401478992208"})
	if laptop.Value != 0 {
		t.Fatalf("laptop update_available = %v, want 0", laptop.Value)
	}
}

func TestCollect_DERPLatency(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device.derp.latency")
	// Only the laptop has DERP latency: two regions.
	if len(pts) != 2 {
		t.Fatalf("derp.latency points = %d, want 2; points=%+v", len(pts), pts)
	}

	frankfurt, ok := pointByAttr(pts, map[string]string{
		semconv.HostID:             "3690401478992208",
		"tailscale.derp.region":    "Frankfurt",
		"tailscale.derp.preferred": "true",
	})
	if !ok {
		t.Fatalf("no derp.latency point for Frankfurt (preferred); points=%+v", pts)
	}
	if frankfurt.Unit != semconv.UnitSeconds {
		t.Fatalf("derp.latency unit = %q, want s", frankfurt.Unit)
	}
	if frankfurt.Kind != "gauge" {
		t.Fatalf("derp.latency kind = %q, want gauge", frankfurt.Kind)
	}
	if frankfurt.Value != 12.5/1000 {
		t.Fatalf("Frankfurt latency = %v, want %v (ms/1000)", frankfurt.Value, 12.5/1000)
	}
	if frankfurt.Attrs[semconv.HostName] != "laptop" {
		t.Fatalf("derp.latency host.name = %q, want laptop", frankfurt.Attrs[semconv.HostName])
	}

	amsterdam, ok := pointByAttr(pts, map[string]string{
		semconv.HostID:             "3690401478992208",
		"tailscale.derp.region":    "Amsterdam",
		"tailscale.derp.preferred": "false",
	})
	if !ok {
		t.Fatalf("no derp.latency point for Amsterdam; points=%+v", pts)
	}
	if amsterdam.Value != 8.0/1000 {
		t.Fatalf("Amsterdam latency = %v, want %v", amsterdam.Value, 8.0/1000)
	}
}

func TestCollect_DevicesCount(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.devices.count")
	if len(pts) != 3 {
		t.Fatalf("devices.count points = %d, want 3 distinct combos; points=%+v", len(pts), pts)
	}
	for _, p := range pts {
		if p.Unit != semconv.UnitDimensionless {
			t.Fatalf("devices.count unit = %q, want 1", p.Unit)
		}
		if p.Kind != "gauge" {
			t.Fatalf("devices.count kind = %q, want gauge", p.Kind)
		}
	}

	linuxAuthInternal, ok := pointByAttr(pts, map[string]string{
		semconv.OSType:         "linux",
		"tailscale.authorized": "true",
		"tailscale.external":   "false",
	})
	if !ok {
		t.Fatalf("no devices.count for linux/authorized/internal; points=%+v", pts)
	}
	if linuxAuthInternal.Value != 1 {
		t.Fatalf("linux/auth/internal count = %v, want 1", linuxAuthInternal.Value)
	}

	linuxUnauthExternal, ok := pointByAttr(pts, map[string]string{
		semconv.OSType:         "linux",
		"tailscale.authorized": "false",
		"tailscale.external":   "true",
	})
	if !ok {
		t.Fatalf("no devices.count for linux/unauthorized/external; points=%+v", pts)
	}
	if linuxUnauthExternal.Value != 1 {
		t.Fatalf("linux/unauth/external count = %v, want 1", linuxUnauthExternal.Value)
	}

	windows, ok := pointByAttr(pts, map[string]string{
		semconv.OSType:         "windows",
		"tailscale.authorized": "true",
		"tailscale.external":   "false",
	})
	if !ok {
		t.Fatal("no devices.count for windows/authorized/internal")
	}
	if windows.Value != 1 {
		t.Fatalf("windows count = %v, want 1", windows.Value)
	}
}

func TestCollect_RoutesDisabledByDefault(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("tailscale.device.routes.advertised"); len(pts) != 0 {
		t.Fatalf("routes.advertised points = %d, want 0 (collectRoutes off)", len(pts))
	}
	if pts := rec.MetricPoints("tailscale.device.routes.enabled"); len(pts) != 0 {
		t.Fatalf("routes.enabled points = %d, want 0 (collectRoutes off)", len(pts))
	}
}

func TestCollect_RoutesEnabled(t *testing.T) {
	devs := sampleDevices()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{devices: devs}
	c := devices.New(api, cache, 0, true, false)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	adv := rec.MetricPoints("tailscale.device.routes.advertised")
	if len(adv) != 3 {
		t.Fatalf("routes.advertised points = %d, want 3 (one per device)", len(adv))
	}
	laptopAdv, ok := pointByAttr(adv, map[string]string{semconv.HostID: "3690401478992208"})
	if !ok {
		t.Fatal("no routes.advertised point for laptop")
	}
	if laptopAdv.Unit != semconv.UnitRoutes {
		t.Fatalf("routes.advertised unit = %q, want %q", laptopAdv.Unit, semconv.UnitRoutes)
	}
	if laptopAdv.Value != 2 {
		t.Fatalf("laptop routes.advertised = %v, want 2", laptopAdv.Value)
	}
	if laptopAdv.Attrs[semconv.HostName] != "laptop" {
		t.Fatalf("routes.advertised host.name = %q, want laptop", laptopAdv.Attrs[semconv.HostName])
	}

	en := rec.MetricPoints("tailscale.device.routes.enabled")
	laptopEn, ok := pointByAttr(en, map[string]string{semconv.HostID: "3690401478992208"})
	if !ok {
		t.Fatal("no routes.enabled point for laptop")
	}
	if laptopEn.Value != 1 {
		t.Fatalf("laptop routes.enabled = %v, want 1", laptopEn.Value)
	}
	desktopAdv, _ := pointByAttr(adv, map[string]string{semconv.HostID: "n-desktop"})
	if desktopAdv.Value != 0 {
		t.Fatalf("desktop routes.advertised = %v, want 0", desktopAdv.Value)
	}
}

func TestCollect_PostureDisabledByDefault(t *testing.T) {
	devs := sampleDevices()
	c, _, api := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(api.postureIDs) != 0 {
		t.Fatalf("posture API called %d times, want 0 (collectPosture off)", len(api.postureIDs))
	}
	if recs := rec.LogRecords(); len(recs) != 0 {
		t.Fatalf("log records = %d, want 0 (collectPosture off)", len(recs))
	}
}

func TestCollect_PostureEnabled(t *testing.T) {
	devs := sampleDevices()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{
			"3690401478992208": {"custom:foo": "bar", "node:os": "linux"},
			"n-desktop":        {"custom:foo": "baz"},
			"n-phone":          {},
		},
	}
	c := devices.New(api, cache, 0, false, true)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	// One posture API call per device.
	if len(api.postureIDs) != 3 {
		t.Fatalf("posture API calls = %d, want 3", len(api.postureIDs))
	}

	recs := rec.LogRecords()
	if len(recs) != 3 {
		t.Fatalf("posture log events = %d, want 3", len(recs))
	}
	var laptop *telemetrytest.LogRecord
	for i := range recs {
		if recs[i].Attrs[semconv.HostID] == "3690401478992208" {
			laptop = &recs[i]
		}
	}
	if laptop == nil {
		t.Fatalf("no posture event for laptop; recs=%+v", recs)
	}
	if laptop.EventName != "tailscale.device.posture" {
		t.Fatalf("posture event.name = %q, want tailscale.device.posture", laptop.EventName)
	}
	if laptop.Attrs[semconv.HostName] != "laptop" {
		t.Fatalf("posture host.name = %q, want laptop", laptop.Attrs[semconv.HostName])
	}
	if laptop.Attrs["custom:foo"] != "bar" {
		t.Fatalf("posture custom:foo = %q, want bar", laptop.Attrs["custom:foo"])
	}
	if laptop.Attrs["node:os"] != "linux" {
		t.Fatalf("posture node:os = %q, want linux", laptop.Attrs["node:os"])
	}
	if laptop.Body == "" {
		t.Fatal("posture event body is empty, want a summary")
	}
}

// richPosture returns a posture map containing every curated node:* key plus an
// uncurated custom key, used to assert the info-gauge labels.
func richPosture() map[string]any {
	return map[string]any{
		"node:os":               "linux",
		"node:osVersion":        "24.04",
		"node:tsVersion":        "1.78.1",
		"node:tsAutoUpdate":     true,
		"node:tsStateEncrypted": false,
		"node:tsReleaseTrack":   "stable",
		"custom:foo":            "bar",
	}
}

func TestCollect_PostureInfoGauge(t *testing.T) {
	// collectPosture on + a device with a full posture map => one
	// tailscale.device.posture GAUGE point, value 1, carrying the curated labels.
	devs := sampleDevices()[:1] // just the laptop
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{"3690401478992208": richPosture()},
	}
	c := devices.New(api, cache, 0, false, true)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device.posture")
	if len(pts) != 1 {
		t.Fatalf("posture info-gauge points = %d, want 1; pts=%+v", len(pts), pts)
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Fatalf("posture metric kind = %q, want gauge", p.Kind)
	}
	if p.Unit != semconv.UnitDimensionless {
		t.Fatalf("posture metric unit = %q, want %q", p.Unit, semconv.UnitDimensionless)
	}
	if p.Value != 1 {
		t.Fatalf("posture metric value = %v, want 1 (constant)", p.Value)
	}
	wantLabels := map[string]string{
		semconv.HostName: "laptop",
		semconv.HostID:   "3690401478992208",
		"os":             "linux",
		"os_version":     "24.04",
		"ts_version":     "1.78.1",
		"auto_update":    "true",
		"encrypted":      "false",
		"track":          "stable",
	}
	for k, want := range wantLabels {
		if got := p.Attrs[k]; got != want {
			t.Errorf("posture metric label %q = %q, want %q", k, got, want)
		}
	}
	// The uncurated custom key must NOT become a metric label.
	if _, present := p.Attrs["custom:foo"]; present {
		t.Errorf("posture metric carries uncurated label custom:foo = %q, want absent", p.Attrs["custom:foo"])
	}
}

func TestCollect_PostureInfoGauge_OmitsMissingLabels(t *testing.T) {
	// A device whose posture map lacks a curated key must omit that label.
	devs := sampleDevices()[:1]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{"3690401478992208": {"node:os": "linux"}},
	}
	c := devices.New(api, cache, 0, false, true)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	pts := rec.MetricPoints("tailscale.device.posture")
	if len(pts) != 1 {
		t.Fatalf("posture info-gauge points = %d, want 1", len(pts))
	}
	p := pts[0]
	if p.Attrs["os"] != "linux" {
		t.Fatalf("posture os label = %q, want linux", p.Attrs["os"])
	}
	for _, k := range []string{"os_version", "ts_version", "auto_update", "encrypted", "track"} {
		if _, present := p.Attrs[k]; present {
			t.Errorf("posture metric label %q present = %q, want omitted (key absent in posture map)", k, p.Attrs[k])
		}
	}
}

func TestCollect_PostureLogOnChange_Default(t *testing.T) {
	// Default log mode is "changes": baseline on first scrape, silent on an
	// unchanged repeat, fires again when posture changes. The info-gauge metric
	// is emitted on EVERY scrape regardless.
	devs := sampleDevices()[:1]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	posture := map[string]map[string]any{"3690401478992208": richPosture()}
	api := &fakeAPI{devices: devs, posture: posture}
	c := devices.New(api, cache, 0, false, true)

	// 1st scrape: first-seen => baseline log emitted.
	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("Collect 1: %v", err)
	}
	if got := postureLogCount(rec1); got != 1 {
		t.Fatalf("scrape 1 posture logs = %d, want 1 (baseline)", got)
	}
	if got := len(rec1.MetricPoints("tailscale.device.posture")); got != 1 {
		t.Fatalf("scrape 1 posture metric points = %d, want 1", got)
	}

	// 2nd scrape: unchanged posture => no log, metric still emitted.
	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	if got := postureLogCount(rec2); got != 0 {
		t.Fatalf("scrape 2 posture logs = %d, want 0 (unchanged)", got)
	}
	if got := len(rec2.MetricPoints("tailscale.device.posture")); got != 1 {
		t.Fatalf("scrape 2 posture metric points = %d, want 1 (metric every scrape)", got)
	}

	// Change the posture => log fires again.
	changed := richPosture()
	changed["node:osVersion"] = "24.10"
	posture["3690401478992208"] = changed
	rec3 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec3.Emitter()); err != nil {
		t.Fatalf("Collect 3: %v", err)
	}
	if got := postureLogCount(rec3); got != 1 {
		t.Fatalf("scrape 3 posture logs = %d, want 1 (posture changed)", got)
	}
	if got := len(rec3.MetricPoints("tailscale.device.posture")); got != 1 {
		t.Fatalf("scrape 3 posture metric points = %d, want 1", got)
	}
}

func TestCollect_PostureLogMode_Always(t *testing.T) {
	// "always" mode emits the posture log every scrape even when unchanged.
	devs := sampleDevices()[:1]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{"3690401478992208": richPosture()},
	}
	c := devices.New(api, cache, 0, false, true, devices.WithPostureLogMode("always"))

	for i := 1; i <= 3; i++ {
		rec := telemetrytest.New()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect %d: %v", i, err)
		}
		if got := postureLogCount(rec); got != 1 {
			t.Fatalf("scrape %d posture logs = %d, want 1 (always)", i, got)
		}
		if got := len(rec.MetricPoints("tailscale.device.posture")); got != 1 {
			t.Fatalf("scrape %d posture metric points = %d, want 1", i, got)
		}
	}
}

func TestCollect_PostureLogMode_Off(t *testing.T) {
	// "off" mode never emits the posture log, but the info-gauge metric is still
	// emitted on every scrape.
	devs := sampleDevices()[:1]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{"3690401478992208": richPosture()},
	}
	c := devices.New(api, cache, 0, false, true, devices.WithPostureLogMode("off"))

	for i := 1; i <= 2; i++ {
		rec := telemetrytest.New()
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect %d: %v", i, err)
		}
		if got := postureLogCount(rec); got != 0 {
			t.Fatalf("scrape %d posture logs = %d, want 0 (off)", i, got)
		}
		if got := len(rec.MetricPoints("tailscale.device.posture")); got != 1 {
			t.Fatalf("scrape %d posture metric points = %d, want 1 (metric still emitted when log off)", i, got)
		}
	}
}

func TestCollect_PostureLogMode_UnknownDefaultsToChanges(t *testing.T) {
	// An unknown/empty mode falls back to the default "changes" behavior.
	devs := sampleDevices()[:1]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{"3690401478992208": richPosture()},
	}
	c := devices.New(api, cache, 0, false, true, devices.WithPostureLogMode("bogus"))

	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("Collect 1: %v", err)
	}
	if got := postureLogCount(rec1); got != 1 {
		t.Fatalf("scrape 1 posture logs = %d, want 1 (unknown mode => changes baseline)", got)
	}
	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	if got := postureLogCount(rec2); got != 0 {
		t.Fatalf("scrape 2 posture logs = %d, want 0 (unknown mode => changes, unchanged)", got)
	}
}

// postureLogCount counts captured log records whose EventName is the posture
// event.
func postureLogCount(rec *telemetrytest.Recorder) int {
	n := 0
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.device.posture" {
			n++
		}
	}
	return n
}

func TestCollect_PostureContinuesOnError(t *testing.T) {
	devs := sampleDevices()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{
			"3690401478992208": {"custom:foo": "bar"},
			"n-phone":          {"custom:foo": "qux"},
		},
		postureFail: "n-desktop",
		postureErr:  context.DeadlineExceeded,
	}
	c := devices.New(api, cache, 0, false, true)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v, want nil (per-device posture errors are non-fatal)", err)
	}
	// The failing device emits no event; the other two still do.
	recs := rec.LogRecords()
	if len(recs) != 2 {
		t.Fatalf("posture log events = %d, want 2 (desktop failed)", len(recs))
	}
}

func TestCollect_PopulatesCache(t *testing.T) {
	devs := sampleDevices()
	c, cache, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if cache.Len() != len(devs) {
		t.Fatalf("cache.Len() = %d, want %d", cache.Len(), len(devs))
	}
	// ResolveName on a device's 100.x address (with port) returns its hostname.
	if got := cache.ResolveName("100.64.0.1:443"); got != "laptop" {
		t.Fatalf("ResolveName(100.64.0.1:443) = %q, want laptop", got)
	}
	if got := cache.ResolveName("100.64.0.2:51820"); got != "desktop" {
		t.Fatalf("ResolveName(100.64.0.2:51820) = %q, want desktop", got)
	}
	// IPv6 Tailscale address resolves too.
	if got := cache.ResolveName("[fd7a:115c:a1e0::1]:443"); got != "laptop" {
		t.Fatalf("ResolveName(ipv6 laptop) = %q, want laptop", got)
	}
	// The cache must key by the NodeID (control-plane node id used in flow
	// logs), not the numeric device ID.
	if _, ok := cache.LookupNode("nDdiLaptopCNTRL"); !ok {
		t.Fatal("LookupNode(nDdiLaptopCNTRL) not found; cache must key by NodeID")
	}
	m, _ := cache.LookupNode("nDdiLaptopCNTRL")
	if m.OSVersion != "24.04" {
		t.Fatalf("cached laptop OSVersion = %q, want 24.04", m.OSVersion)
	}
}

func TestCollect_CacheSelfObs(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	age := rec.MetricPoints("tailscale2otel.enrich.cache_age")
	if len(age) != 1 {
		t.Fatalf("cache_age points = %d, want 1", len(age))
	}
	if age[0].Unit != semconv.UnitSeconds {
		t.Fatalf("cache_age unit = %q, want s", age[0].Unit)
	}
	if age[0].Kind != "gauge" {
		t.Fatalf("cache_age kind = %q, want gauge", age[0].Kind)
	}

	size := rec.MetricPoints("tailscale2otel.enrich.cache_size")
	if len(size) != 1 {
		t.Fatalf("cache_size points = %d, want 1", len(size))
	}
	if size[0].Unit != semconv.UnitDimensionless {
		t.Fatalf("cache_size unit = %q, want 1", size[0].Unit)
	}
	if size[0].Value != float64(len(devs)) {
		t.Fatalf("cache_size = %v, want %d", size[0].Value, len(devs))
	}
}
