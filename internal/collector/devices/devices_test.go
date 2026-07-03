package devices_test

import (
	"context"
	"slices"
	"strings"
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

	invites    map[string][]tsapi.DeviceInvite
	inviteErr  error
	inviteFail string // device ID whose invites call returns inviteErr
	inviteIDs  []string
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

func (f *fakeAPI) DeviceInvites(_ context.Context, deviceID string) ([]tsapi.DeviceInvite, error) {
	f.inviteIDs = append(f.inviteIDs, deviceID)
	if deviceID == f.inviteFail {
		return nil, f.inviteErr
	}
	return f.invites[deviceID], nil
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
			// Connectivity (B3): populated so the catalog drift guard exercises the
			// connectivity gauges + client_supports fleet rollup, not just hard_nat=0.
			HardNAT:        false,
			Endpoints:      []string{"203.0.113.5:41641", "[2001:db8::1]:41641"},
			ClientSupports: tsapi.ClientSupports{UDP: ptr(true), IPv6: ptr(true), UPnP: ptr(false)},
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

// hygieneDevices is a dedicated fixture for the fleet-hygiene roll-up tests
// (kept separate from sampleDevices so existing assertions are untouched).
//
//	laptop : tagged {servers,k8s}, v1.98.4, key expires in 48h
//	desktop: tagged {servers}, v1.96.4, EPHEMERAL, key expiry DISABLED
//	phone  : EXTERNAL, untagged, no version, zero Expires
//	srv2   : non-external, UNTAGGED, v1.96.4, key expires in 24h
func hygieneDevices() []tsapi.RichDevice {
	return []tsapi.RichDevice{
		{
			ID: "h-laptop", Hostname: "laptop", OS: "linux", User: "a", IsExternal: false,
			Tags: []string{"tag:servers", "tag:k8s"}, ClientVersion: "1.98.4-tabc",
			KeyExpiryDisabled: false, Expires: now.Add(48 * time.Hour),
			Addresses: []string{"100.64.0.1"},
		},
		{
			ID: "h-desktop", Hostname: "desktop", OS: "windows", User: "b", IsExternal: false,
			Tags: []string{"tag:servers"}, ClientVersion: "1.96.4", IsEphemeral: true,
			KeyExpiryDisabled: true, Expires: now.Add(72 * time.Hour),
			Addresses: []string{"100.64.0.2"},
		},
		{
			ID: "h-phone", Hostname: "phone", OS: "linux", User: "c", IsExternal: true,
			ClientVersion: "", KeyExpiryDisabled: false, // Expires left zero
			Addresses: []string{"100.64.0.3"},
		},
		{
			ID: "h-srv2", Hostname: "srv2", OS: "linux", User: "d", IsExternal: false,
			ClientVersion: "1.96.4", KeyExpiryDisabled: false, Expires: now.Add(24 * time.Hour),
			Addresses: []string{"100.64.0.4"},
		},
	}
}

// hygieneCollector builds a devices collector over hygieneDevices with the
// fixture clock pinned to `now` (so the key-expiry histogram is deterministic),
// plus any extra options, and returns it with a fresh recorder.
func hygieneCollector(t *testing.T, opts ...devices.Option) (*devices.Collector, *telemetrytest.Recorder) {
	t.Helper()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{devices: hygieneDevices()}
	all := append([]devices.Option{devices.WithClock(func() time.Time { return now })}, opts...)
	c := devices.New(api, cache, 0, false, false, all...)
	return c, telemetrytest.New()
}

func TestCollect_FleetHygiene(t *testing.T) {
	c, rec := hygieneCollector(t)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// untagged: srv2 only (non-external, no tags). phone is external → excluded.
	if pts := rec.MetricPoints("tailscale.devices.untagged"); len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("untagged = %+v, want single value 1 (srv2; phone external excluded)", pts)
	}

	// ephemeral: desktop only.
	if pts := rec.MetricPoints("tailscale.devices.ephemeral"); len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("ephemeral = %+v, want single value 1", pts)
	}

	// by_version: 1.98.4 (laptop)=1, 1.96.4 (desktop+srv2)=2; phone excluded (empty).
	vpts := rec.MetricPoints("tailscale.devices.by_version")
	if len(vpts) != 2 {
		t.Fatalf("by_version points = %d, want 2", len(vpts))
	}
	if p, ok := pointByAttr(vpts, map[string]string{"tailscale.client_version": "1.98.4"}); !ok || p.Value != 1 {
		t.Errorf("by_version 1.98.4 = %+v ok=%v, want value 1", p, ok)
	}
	if p, ok := pointByAttr(vpts, map[string]string{"tailscale.client_version": "1.96.4"}); !ok || p.Value != 2 {
		t.Errorf("by_version 1.96.4 = %+v ok=%v, want value 2", p, ok)
	}

	// by_tag (default gate on, default cap 50): servers (laptop+desktop)=2, k8s (laptop)=1.
	tpts := rec.MetricPoints("tailscale.devices.by_tag")
	if p, ok := pointByAttr(tpts, map[string]string{"tailscale.tag": "tag:servers"}); !ok || p.Value != 2 {
		t.Errorf("by_tag tag:servers = %+v ok=%v, want value 2", p, ok)
	}
	if p, ok := pointByAttr(tpts, map[string]string{"tailscale.tag": "tag:k8s"}); !ok || p.Value != 1 {
		t.Errorf("by_tag tag:k8s = %+v ok=%v, want value 1", p, ok)
	}

	// key_expiry histogram: laptop (~2d) + srv2 (~1d) in (0,7]; desktop excluded
	// (KeyExpiryDisabled); phone excluded (zero Expires). Count=2, bucket[1]=2.
	hpts := rec.MetricPoints("tailscale.devices.key_expiry")
	if len(hpts) != 1 {
		t.Fatalf("key_expiry points = %d, want 1", len(hpts))
	}
	h := hpts[0]
	if h.Kind != "histogram" || h.Count != 2 {
		t.Fatalf("key_expiry kind=%q count=%d, want histogram/2", h.Kind, h.Count)
	}
	// bounds [0,7,30,90,180,365] => 7 buckets; bucket[1]=(0,7].
	if h.BucketCounts[1] != 2 {
		t.Errorf("key_expiry buckets = %v, want [1]=2", h.BucketCounts)
	}
}

func TestCollect_KeyExpiryExpiredBucket(t *testing.T) {
	// Pin the clock well after both expiring keys so they read as already
	// expired → the (-inf,0] bucket (index 0). Proves negative days + clock.
	future := now.Add(72*time.Hour + 10*24*time.Hour)
	c, rec := hygieneCollector(t, devices.WithClock(func() time.Time { return future }))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	h := rec.MetricPoints("tailscale.devices.key_expiry")[0]
	if h.Count != 2 || h.BucketCounts[0] != 2 {
		t.Errorf("expected 2 expired keys in (-inf,0]; count=%d buckets=%v", h.Count, h.BucketCounts)
	}
}

func TestCollect_TagRollupCap(t *testing.T) {
	// Cap of 1: only the busiest tag (servers=2) keeps its series; k8s (1) folds
	// into __other__.
	c, rec := hygieneCollector(t, devices.WithTagRollup(true, 1))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	tpts := rec.MetricPoints("tailscale.devices.by_tag")
	if len(tpts) != 2 {
		t.Fatalf("by_tag points = %d, want 2 (servers + __other__)", len(tpts))
	}
	if p, ok := pointByAttr(tpts, map[string]string{"tailscale.tag": "tag:servers"}); !ok || p.Value != 2 {
		t.Errorf("by_tag tag:servers = %+v, want value 2 (kept)", p)
	}
	if p, ok := pointByAttr(tpts, map[string]string{"tailscale.tag": "__other__"}); !ok || p.Value != 1 {
		t.Errorf("by_tag __other__ = %+v, want value 1 (tag:k8s folded)", p)
	}
}

func TestCollect_TagRollupDisabled(t *testing.T) {
	c, rec := hygieneCollector(t, devices.WithTagRollup(false, 50))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.devices.by_tag"); len(pts) != 0 {
		t.Errorf("by_tag emitted %d points with rollup disabled, want 0", len(pts))
	}
	// The other aggregates still emit when tag rollup is off.
	if pts := rec.MetricPoints("tailscale.devices.ephemeral"); len(pts) != 1 {
		t.Errorf("ephemeral should still emit when tag rollup off; got %d", len(pts))
	}
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

func TestCollect_OnlineTags(t *testing.T) {
	// Local fixture (not sampleDevices) so this can't disturb other tests:
	// one tagged device, one untagged.
	devs := []tsapi.RichDevice{
		{ID: "id-tagged", Hostname: "server1", OS: "linux",
			ConnectedToControl: true, Tags: []string{"tag:server", "tag:lab"}},
		{ID: "id-untagged", Hostname: "laptop1", OS: "darwin",
			User: "alice@example.com", ConnectedToControl: true},
	}
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device.online")

	// Tagged device carries comma-joined tags (matches nodediscovery formatting).
	tagged, ok := pointByAttr(pts, map[string]string{semconv.HostID: "id-tagged"})
	if !ok {
		t.Fatal("no online point for tagged device")
	}
	if tagged.Attrs[semconv.AttrTags] != "tag:server,tag:lab" {
		t.Fatalf("online tailscale.tags = %q, want %q", tagged.Attrs[semconv.AttrTags], "tag:server,tag:lab")
	}

	// Untagged device omits the label entirely (like os.version when empty).
	untagged, ok := pointByAttr(pts, map[string]string{semconv.HostID: "id-untagged"})
	if !ok {
		t.Fatal("no online point for untagged device")
	}
	if _, present := untagged.Attrs[semconv.AttrTags]; present {
		t.Fatalf("untagged tailscale.tags present = %q, want omitted", untagged.Attrs[semconv.AttrTags])
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

// TestCollect_UpdateAvailableDataFalse guards issue #64 sub-item 4: when the
// control-plane doesn't report update-available status at all (e.g.
// Headscale), WithUpdateAvailableData(false) must suppress
// tailscale.device.update_available entirely rather than reporting a
// fabricated "no update available" (false) as if it were real data.
func TestCollect_UpdateAvailableDataFalse(t *testing.T) {
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	c := devices.New(&fakeAPI{devices: sampleDevices()}, cache, 0, false, false, devices.WithUpdateAvailableData(false))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("tailscale.device.update_available"); len(pts) != 0 {
		t.Errorf("update_available emitted with WithUpdateAvailableData(false): %+v", pts)
	}
	// Unrelated per-device gauges must be unaffected.
	if pts := rec.MetricPoints("tailscale.device.online"); len(pts) == 0 {
		t.Error("online gauge should still be emitted with WithUpdateAvailableData(false)")
	}
}

// TestCollect_UpdateAvailableDataDefaultTrue is the control: with no option
// supplied, behavior is unchanged from before issue #64.
func TestCollect_UpdateAvailableDataDefaultTrue(t *testing.T) {
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	c := devices.New(&fakeAPI{devices: sampleDevices()}, cache, 0, false, false)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("tailscale.device.update_available"); len(pts) != 3 {
		t.Errorf("update_available points = %d, want 3 by default", len(pts))
	}
}

// TestCollect_EphemeralDataFalse guards issue #64 sub-item 4: when the
// control-plane doesn't report per-device ephemeral status at all (e.g.
// Headscale's node listing), WithEphemeralData(false) must suppress the
// tailscale.devices.ephemeral fleet aggregate entirely rather than reporting a
// fabricated all-zero count as if it were real data.
func TestCollect_EphemeralDataFalse(t *testing.T) {
	c, rec := hygieneCollector(t, devices.WithEphemeralData(false))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.devices.ephemeral"); len(pts) != 0 {
		t.Errorf("ephemeral aggregate emitted with WithEphemeralData(false): %+v", pts)
	}
	// Unrelated fleet aggregates must be unaffected.
	if pts := rec.MetricPoints("tailscale.devices.untagged"); len(pts) == 0 {
		t.Error("untagged aggregate should still be emitted with WithEphemeralData(false)")
	}
}

// TestCollect_EphemeralDataDefaultTrue is the control: with no option
// supplied, behavior is unchanged from before issue #64.
func TestCollect_EphemeralDataDefaultTrue(t *testing.T) {
	c, rec := hygieneCollector(t)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.devices.ephemeral"); len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("ephemeral = %+v, want single value 1 by default", pts)
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
	// The raw dynamic posture keys must NOT be spread onto the log as
	// individual top-level attributes (#56: that bypasses PII classification).
	// Instead they are carried JSON-encoded under the classified
	// tailscale.device.posture.details key.
	if _, ok := laptop.Attrs["custom:foo"]; ok {
		t.Fatalf("posture log carries raw dynamic key %q directly; want it routed through tailscale.device.posture.details", "custom:foo")
	}
	if _, ok := laptop.Attrs["node:os"]; ok {
		t.Fatalf("posture log carries raw dynamic key %q directly; want it routed through tailscale.device.posture.details", "node:os")
	}
	details, ok := laptop.Attrs["tailscale.device.posture.details"]
	if !ok {
		t.Fatal("posture log missing classified attribute tailscale.device.posture.details")
	}
	if !strings.Contains(details, `"custom:foo":"bar"`) {
		t.Errorf("posture details = %q, want it to contain custom:foo=bar", details)
	}
	if !strings.Contains(details, `"node:os":"linux"`) {
		t.Errorf("posture details = %q, want it to contain node:os=linux", details)
	}
	// Body must include the attribute count but NOT the hostname — the hostname
	// lives in the host.name attribute (redactable via pii_filter.hostnames).
	// laptop has 2 posture attrs in this test: custom:foo + node:os.
	wantBody := "device has 2 posture attribute(s)"
	if laptop.Body != wantBody {
		t.Fatalf("posture body = %q, want %q", laptop.Body, wantBody)
	}
	if laptop.Attrs[semconv.HostName] != "laptop" {
		t.Fatalf("posture host.name attr = %q, want laptop (hostname still in attr)", laptop.Attrs[semconv.HostName])
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

// TestCollect_PostureLog_DynamicAttrsRoutedThroughClassifiedKey is the
// regression test for issue #56: the posture log's dynamic attribute map
// (arbitrary provider-namespaced keys like "custom:foo", "intune:isEncrypted")
// must never be spread onto the log record as raw top-level attribute keys,
// because none of those keys are registered in the PII registry and the
// redactor's redactKey always falls through to "never redact" for an
// unclassified key. Instead the full posture map must be carried under a
// single, PII-registry-classified key (CatFreeTextDetails) so the existing
// pii_filter.free_text_details toggle actually gates it — consistent with the
// tailscale.device.attribute.info metric path's classified "value" label.
func TestCollect_PostureLog_DynamicAttrsRoutedThroughClassifiedKey(t *testing.T) {
	devs := sampleDevices()[:1]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	posture := map[string]map[string]any{
		"3690401478992208": {
			"node:os":            "linux",
			"custom:foo":         "bar",
			"intune:isEncrypted": true,
		},
	}
	api := &fakeAPI{devices: devs, posture: posture}
	c := devices.New(api, cache, 0, false, true)

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var postureLog *telemetrytest.LogRecord
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.device.posture" {
			l := lr
			postureLog = &l
		}
	}
	if postureLog == nil {
		t.Fatal("posture log not emitted")
	}

	for _, rawKey := range []string{"custom:foo", "intune:isEncrypted", "node:os"} {
		if _, ok := postureLog.Attrs[rawKey]; ok {
			t.Errorf("posture log carries raw dynamic key %q directly as a top-level attribute; it bypasses PII classification and must be routed through a classified key instead", rawKey)
		}
	}

	const classifiedKey = "tailscale.device.posture.details"
	details, ok := postureLog.Attrs[classifiedKey]
	if !ok {
		t.Fatalf("posture log missing classified attribute %q carrying the dynamic posture map", classifiedKey)
	}
	for _, want := range []string{"custom:foo", "bar", "intune:isEncrypted"} {
		if !strings.Contains(details, want) {
			t.Errorf("classified attribute %q = %q, want it to contain %q", classifiedKey, details, want)
		}
	}
}

// TestCollect_LastPostureCache_PrunedWhenDeviceLeavesFleet is the regression
// test for #61: lastPosture must be pruned to the current tick's fleet each
// scrape, not retained forever for a device that has left the tailnet. This is
// asserted via the only externally-observable effect of the (unexported)
// lastPosture map: postureLogChanges mode diffs a device's posture against its
// remembered signature to decide whether the change-log fires. If a device
// leaves the fleet (absent from DevicesRich) and later rejoins with the EXACT
// SAME posture, a stale unpruned entry would compare equal and stay silent;
// after pruning, the collector has no memory of it and must treat it as
// first-seen again (a fresh baseline log).
func TestCollect_LastPostureCache_PrunedWhenDeviceLeavesFleet(t *testing.T) {
	laptop := sampleDevices()[0]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	posture := map[string]map[string]any{laptop.ID: richPosture()}
	api := &fakeAPI{devices: []tsapi.RichDevice{laptop}, posture: posture}
	c := devices.New(api, cache, 0, false, true)

	// Scrape 1: device present -> baseline (first-seen) posture log.
	rec1 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec1.Emitter()); err != nil {
		t.Fatalf("Collect 1: %v", err)
	}
	if got := postureLogCount(rec1); got != 1 {
		t.Fatalf("scrape 1 posture logs = %d, want 1 (baseline)", got)
	}

	// Scrape 2: device leaves the fleet entirely (e.g. removed from the
	// tailnet). No posture call is made for it, and its lastPosture entry must
	// be pruned during this tick.
	api.devices = nil
	rec2 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	if got := postureLogCount(rec2); got != 0 {
		t.Fatalf("scrape 2 posture logs = %d, want 0 (no devices in fleet)", got)
	}

	// Scrape 3: device rejoins with the SAME (unchanged) posture. If the entry
	// had survived scrape 2 unpruned, this would stay silent (posture
	// "unchanged"). Pruning during the absence must make it look first-seen.
	api.devices = []tsapi.RichDevice{laptop}
	rec3 := telemetrytest.New()
	if err := c.Collect(context.Background(), rec3.Emitter()); err != nil {
		t.Fatalf("Collect 3: %v", err)
	}
	if got := postureLogCount(rec3); got != 1 {
		t.Fatalf("scrape 3 posture logs = %d, want 1 (rejoined device treated as first-seen after lastPosture was pruned)", got)
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

// TestCollect_PopulatesCache_Tags is the regression test for issue #102:
// toMetas must copy a device's ACL tags into enrich.DeviceMeta.Tags, so the
// admin status page's per-device tags column (which reads
// DeviceCache.Snapshot()[i].Tags) is not always blank.
func TestCollect_PopulatesCache_Tags(t *testing.T) {
	devs := []tsapi.RichDevice{
		{
			ID:        "tagged-1",
			NodeID:    "nDdiTagged1",
			Name:      "tagged1.tail1a2b.ts.net",
			Hostname:  "tagged1",
			OS:        "linux",
			Addresses: []string{"100.64.0.9"},
			Tags:      []string{"tag:servers", "tag:k8s"},
		},
	}
	c, cache, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	m, ok := cache.LookupNode("nDdiTagged1")
	if !ok {
		t.Fatal("LookupNode(nDdiTagged1) not found")
	}
	want := []string{"tag:servers", "tag:k8s"}
	if !slices.Equal(m.Tags, want) {
		t.Fatalf("cached device Tags = %v, want %v", m.Tags, want)
	}
}

// TestCollect_PopulatesCache_IDAndOnline is the regression test for issue #85:
// toMetas must copy a device's numeric ID and ConnectedToControl (online)
// status into enrich.DeviceMeta, so node-metrics discovery can read them from
// the shared cache instead of issuing its own DevicesRich() poll.
func TestCollect_PopulatesCache_IDAndOnline(t *testing.T) {
	devs := []tsapi.RichDevice{
		{
			ID:                 "999888777",
			NodeID:             "nDdiOnline1",
			Name:               "online1.tail1a2b.ts.net",
			Hostname:           "online1",
			OS:                 "linux",
			Addresses:          []string{"100.64.0.10"},
			ConnectedToControl: true,
		},
		{
			ID:                 "999888778",
			NodeID:             "nDdiOffline1",
			Name:               "offline1.tail1a2b.ts.net",
			Hostname:           "offline1",
			OS:                 "linux",
			Addresses:          []string{"100.64.0.11"},
			ConnectedToControl: false,
		},
	}
	c, cache, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	online, ok := cache.LookupNode("nDdiOnline1")
	if !ok {
		t.Fatal("LookupNode(nDdiOnline1) not found")
	}
	if online.ID != "999888777" {
		t.Errorf("online.ID = %q, want 999888777", online.ID)
	}
	if !online.Online {
		t.Error("online.Online = false, want true (ConnectedToControl was true)")
	}

	offline, ok := cache.LookupNode("nDdiOffline1")
	if !ok {
		t.Fatal("LookupNode(nDdiOffline1) not found")
	}
	if offline.ID != "999888778" {
		t.Errorf("offline.ID = %q, want 999888778", offline.ID)
	}
	if offline.Online {
		t.Error("offline.Online = true, want false (ConnectedToControl was false)")
	}
}

func TestCollect_CacheSelfObs(t *testing.T) {
	devs := sampleDevices()
	c, _, _ := newCollector(t, devs)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	// cache_age is NO LONGER emitted by the devices collector (it was always ~0
	// right after refresh); the app-level reporter emits it at export time so it
	// grows while stale (#108). The collector still emits cache_size.
	if age := rec.MetricPoints("tailscale2otel.enrich.cache_age"); len(age) != 0 {
		t.Fatalf("devices collector should no longer emit cache_age (moved to app reporter); got %d points", len(age))
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

// --- device posture attribute metrics: tailscale.device.attribute{,.info} ---

// attrPostureMap spans value types and namespaces for the attribute-metric tests.
func attrPostureMap() map[string]any {
	return map[string]any{
		"intune:isEncrypted":     true,        // bool   -> numeric gauge (1)
		"intune:isSupervised":    false,       // bool   -> numeric gauge (0)
		"intune:complianceState": "compliant", // string -> info gauge (value label)
		"ip:country":             "GB",        // string -> info gauge
		"custom:myScore":         float64(87), // number -> numeric gauge (87)
		"node:os":                "linux",     // string, node namespace
	}
}

// attrCollector builds a single-device posture collector with collect_posture on
// and the given attribute-namespace allow-list, plus a fresh recorder.
func attrCollector(devID string, posture map[string]any, ns ...string) (*devices.Collector, *telemetrytest.Recorder, *fakeAPI) {
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: []tsapi.RichDevice{{ID: devID, Hostname: "laptop", OS: "linux", ConnectedToControl: true}},
		posture: map[string]map[string]any{devID: posture},
	}
	c := devices.New(api, cache, 0, false, true, devices.WithAttributeNamespaces(ns))
	return c, telemetrytest.New(), api
}

func TestCollect_AttributeNumericBool(t *testing.T) {
	c, rec, _ := attrCollector("dev1", map[string]any{
		"intune:isEncrypted":  true,
		"intune:isSupervised": false,
	}, "intune")
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints("tailscale.device.attribute")
	if len(pts) != 2 {
		t.Fatalf("numeric attribute points = %d, want 2; pts=%+v", len(pts), pts)
	}
	enc, ok := pointByAttr(pts, map[string]string{"attribute": "intune:isEncrypted"})
	if !ok {
		t.Fatalf("no numeric point for intune:isEncrypted; pts=%+v", pts)
	}
	if enc.Kind != "gauge" {
		t.Fatalf("attribute kind = %q, want gauge", enc.Kind)
	}
	if enc.Unit != semconv.UnitDimensionless {
		t.Fatalf("attribute unit = %q, want %q", enc.Unit, semconv.UnitDimensionless)
	}
	if enc.Value != 1 {
		t.Fatalf("intune:isEncrypted=true => %v, want 1", enc.Value)
	}
	if enc.Attrs[semconv.HostID] != "dev1" || enc.Attrs[semconv.HostName] != "laptop" {
		t.Fatalf("attribute identity labels = %+v", enc.Attrs)
	}
	if _, present := enc.Attrs["value"]; present {
		t.Errorf("numeric attribute carries a value label = %q, want absent", enc.Attrs["value"])
	}
	sup, _ := pointByAttr(pts, map[string]string{"attribute": "intune:isSupervised"})
	if sup.Value != 0 {
		t.Fatalf("intune:isSupervised=false => %v, want 0", sup.Value)
	}
}

func TestCollect_AttributeNumericNumber(t *testing.T) {
	c, rec, _ := attrCollector("dev1", map[string]any{"custom:myScore": float64(87)}, "custom")
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	pts := rec.MetricPoints("tailscale.device.attribute")
	score, ok := pointByAttr(pts, map[string]string{"attribute": "custom:myScore"})
	if !ok {
		t.Fatalf("no numeric point for custom:myScore; pts=%+v", pts)
	}
	if score.Value != 87 {
		t.Fatalf("custom:myScore = %v, want 87", score.Value)
	}
}

func TestCollect_AttributeInfoString(t *testing.T) {
	c, rec, _ := attrCollector("dev1", map[string]any{
		"intune:complianceState": "compliant",
		"ip:country":             "GB",
	}, "intune", "ip")
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.device.attribute"); len(pts) != 0 {
		t.Fatalf("numeric points = %d, want 0 (all strings)", len(pts))
	}
	info := rec.MetricPoints("tailscale.device.attribute.info")
	if len(info) != 2 {
		t.Fatalf("info points = %d, want 2; pts=%+v", len(info), info)
	}
	comp, ok := pointByAttr(info, map[string]string{"attribute": "intune:complianceState", "value": "compliant"})
	if !ok {
		t.Fatalf("no info point for intune:complianceState=compliant; pts=%+v", info)
	}
	if comp.Kind != "gauge" || comp.Unit != semconv.UnitDimensionless {
		t.Fatalf("info kind/unit = %q/%q, want gauge/%q", comp.Kind, comp.Unit, semconv.UnitDimensionless)
	}
	if comp.Value != 1 {
		t.Fatalf("info value = %v, want 1 (constant)", comp.Value)
	}
	if comp.Attrs[semconv.HostID] != "dev1" {
		t.Fatalf("info host.id = %q, want dev1", comp.Attrs[semconv.HostID])
	}
	if _, ok := pointByAttr(info, map[string]string{"attribute": "ip:country", "value": "GB"}); !ok {
		t.Fatalf("no info point for ip:country=GB; pts=%+v", info)
	}
}

func TestCollect_AttributeNamespaceAllowList(t *testing.T) {
	// allow-list [intune, ip]: intune/ip keys promoted; node/custom dropped.
	c, rec, _ := attrCollector("dev1", attrPostureMap(), "intune", "ip")
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	num := rec.MetricPoints("tailscale.device.attribute")
	info := rec.MetricPoints("tailscale.device.attribute.info")
	if _, ok := pointByAttr(num, map[string]string{"attribute": "intune:isEncrypted"}); !ok {
		t.Error("intune:isEncrypted not promoted (allow-listed)")
	}
	if _, ok := pointByAttr(info, map[string]string{"attribute": "ip:country"}); !ok {
		t.Error("ip:country not promoted (allow-listed)")
	}
	if _, ok := pointByAttr(num, map[string]string{"attribute": "custom:myScore"}); ok {
		t.Error("custom:myScore promoted but custom is not allow-listed")
	}
	if _, ok := pointByAttr(info, map[string]string{"attribute": "node:os"}); ok {
		t.Error("node:os promoted but node is not allow-listed")
	}
}

func TestCollect_AttributeWildcard(t *testing.T) {
	// ["*"] promotes every namespace, including node and custom.
	c, rec, _ := attrCollector("dev1", attrPostureMap(), "*")
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	info := rec.MetricPoints("tailscale.device.attribute.info")
	num := rec.MetricPoints("tailscale.device.attribute")
	if _, ok := pointByAttr(info, map[string]string{"attribute": "node:os", "value": "linux"}); !ok {
		t.Errorf("node:os not promoted under wildcard; info=%+v", info)
	}
	if _, ok := pointByAttr(num, map[string]string{"attribute": "custom:myScore"}); !ok {
		t.Errorf("custom:myScore not promoted under wildcard; num=%+v", num)
	}
}

func TestCollect_AttributeDisabledWithoutAllowList(t *testing.T) {
	// No WithAttributeNamespaces => no attribute metrics, but the posture info
	// gauge and posture log are unaffected.
	devs := sampleDevices()[:1]
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{devices: devs, posture: map[string]map[string]any{"3690401478992208": richPosture()}}
	c := devices.New(api, cache, 0, false, true)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if pts := rec.MetricPoints("tailscale.device.attribute"); len(pts) != 0 {
		t.Errorf("numeric attribute points = %d, want 0 (no allow-list)", len(pts))
	}
	if pts := rec.MetricPoints("tailscale.device.attribute.info"); len(pts) != 0 {
		t.Errorf("info attribute points = %d, want 0 (no allow-list)", len(pts))
	}
	if pts := rec.MetricPoints("tailscale.device.posture"); len(pts) != 1 {
		t.Errorf("posture info gauge = %d, want 1 (unaffected)", len(pts))
	}
	if postureLogCount(rec) != 1 {
		t.Errorf("posture logs = %d, want 1 (unaffected)", postureLogCount(rec))
	}
}

func TestCollect_DeviceInvitesGroupedCounts(t *testing.T) {
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: sampleDevices(),
		invites: map[string][]tsapi.DeviceInvite{
			"3690401478992208": {
				{Accepted: true, AllowExitNode: false, MultiUse: false},
				{Accepted: false, AllowExitNode: true, MultiUse: true},
			},
			"n-desktop": {
				{Accepted: false, AllowExitNode: false, MultiUse: false},
			},
			// n-phone: no invites (nil)
		},
	}
	c := devices.New(api, cache, 0, false, false, devices.WithDeviceInvites(true))

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	pts := rec.MetricPoints("tailscale.device_invites.count")
	if len(pts) != 3 {
		t.Fatalf("got %d invite series, want 3", len(pts))
	}

	accepted, ok := pointByAttr(pts, map[string]string{
		"tailscale.device_invite.accepted":        "true",
		"tailscale.device_invite.allow_exit_node": "false",
		"tailscale.device_invite.multi_use":       "false",
	})
	if !ok || accepted.Value != 1 {
		t.Errorf("accepted-only series = %+v ok=%v, want value 1", accepted, ok)
	}
	exitMulti, ok := pointByAttr(pts, map[string]string{
		"tailscale.device_invite.accepted":        "false",
		"tailscale.device_invite.allow_exit_node": "true",
		"tailscale.device_invite.multi_use":       "true",
	})
	if !ok || exitMulti.Value != 1 {
		t.Errorf("pending exit+multi series = %+v ok=%v, want value 1", exitMulti, ok)
	}
	plain, ok := pointByAttr(pts, map[string]string{
		"tailscale.device_invite.accepted":        "false",
		"tailscale.device_invite.allow_exit_node": "false",
		"tailscale.device_invite.multi_use":       "false",
	})
	if !ok || plain.Value != 1 {
		t.Errorf("pending plain series = %+v ok=%v, want value 1", plain, ok)
	}

	if len(api.inviteIDs) != 3 {
		t.Errorf("inviteIDs = %v, want 3 device probes", api.inviteIDs)
	}
}

func TestCollect_DeviceInvitesDisabledByDefault(t *testing.T) {
	c, _, api := newCollector(t, sampleDevices()) // no WithDeviceInvites
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if pts := rec.MetricPoints("tailscale.device_invites.count"); len(pts) != 0 {
		t.Errorf("got %d invite series, want 0 when gate is off", len(pts))
	}
	if len(api.inviteIDs) != 0 {
		t.Errorf("inviteIDs = %v, want no probes when gate is off", api.inviteIDs)
	}
}

func TestCollect_DeviceInvitesLogEvents(t *testing.T) {
	// J-A2: per-invite log event carries invitee email, acceptedBy login, and
	// the sharing device's hostname (host.name) and nodeId (host.id).
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: sampleDevices(),
		invites: map[string][]tsapi.DeviceInvite{
			// laptop: two invites with identifying info.
			"3690401478992208": {
				// Accepted invite: email + acceptedByLogin present.
				{Accepted: true, AllowExitNode: false, MultiUse: false,
					Email: "alice@external.example", AcceptedByLogin: "alice@external.example"},
				// Pending invite: email set, acceptedByLogin empty.
				{Accepted: false, AllowExitNode: true, MultiUse: true,
					Email: "bob@external.example", AcceptedByLogin: ""},
			},
			// desktop: one invite with neither email nor acceptedByLogin —
			// anonymous link share, not yet accepted: must NOT emit a log event.
			"n-desktop": {
				{Accepted: false, AllowExitNode: false, MultiUse: false,
					Email: "", AcceptedByLogin: ""},
			},
			// n-phone: no invites — no log events.
		},
	}
	c := devices.New(api, cache, 0, false, false, devices.WithDeviceInvites(true))

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	// Should have exactly 2 invite log events: laptop×2 (both have email).
	// The anonymous desktop invite is skipped.
	inviteLogs := func() []telemetrytest.LogRecord {
		var out []telemetrytest.LogRecord
		for _, lr := range rec.LogRecords() {
			if lr.EventName == "tailscale.device_invite" {
				out = append(out, lr)
			}
		}
		return out
	}()
	if len(inviteLogs) != 2 {
		t.Fatalf("invite log events = %d, want 2; all logs: %+v", len(inviteLogs), rec.LogRecords())
	}

	// Find each log event by the tailscale.user attribute.
	var accepted, pending *telemetrytest.LogRecord
	for i := range inviteLogs {
		lr := &inviteLogs[i]
		switch lr.Attrs["tailscale.user"] {
		case "alice@external.example":
			accepted = lr
		case "bob@external.example":
			pending = lr
		}
	}
	if accepted == nil {
		t.Fatalf("no invite log event with tailscale.user=alice@external.example; logs=%+v", inviteLogs)
	}
	if pending == nil {
		t.Fatalf("no invite log event with tailscale.user=bob@external.example; logs=%+v", inviteLogs)
	}

	// Check accepted invite attributes.
	if got := accepted.Attrs["host.name"]; got != "laptop" {
		t.Errorf("accepted host.name = %q, want laptop", got)
	}
	if got := accepted.Attrs["host.id"]; got != "3690401478992208" {
		t.Errorf("accepted host.id = %q, want 3690401478992208 (device id)", got)
	}
	if got := accepted.Attrs["tailscale.actor.login"]; got != "alice@external.example" {
		t.Errorf("accepted tailscale.actor.login = %q, want alice@external.example", got)
	}
	if accepted.SeverityText != "INFO" {
		t.Errorf("accepted severity = %q, want INFO", accepted.SeverityText)
	}
	// Body must NOT contain the hostname (PII); the hostname lives in host.name attr.
	if accepted.Body != "device share invite accepted" {
		t.Errorf("accepted body = %q, want %q", accepted.Body, "device share invite accepted")
	}

	// Check pending invite attributes.
	if got := pending.Attrs["host.name"]; got != "laptop" {
		t.Errorf("pending host.name = %q, want laptop", got)
	}
	if got := pending.Attrs["host.id"]; got != "3690401478992208" {
		t.Errorf("pending host.id = %q, want 3690401478992208 (device id)", got)
	}
	if got := pending.Attrs["tailscale.actor.login"]; got != "" {
		t.Errorf("pending tailscale.actor.login = %q, want empty (not yet accepted)", got)
	}
	// Body must NOT contain the hostname (PII); the hostname lives in host.name attr.
	if pending.Body != "device share invite pending" {
		t.Errorf("pending body = %q, want %q", pending.Body, "device share invite pending")
	}

	// Existing count gauge must still be emitted unchanged.
	if pts := rec.MetricPoints("tailscale.device_invites.count"); len(pts) == 0 {
		t.Error("tailscale.device_invites.count gauge was not emitted")
	}
}

func TestCollect_DeviceInvitesLogEvents_AnonymousLinkSkipped(t *testing.T) {
	// An invite with no email and no acceptedByLogin (anonymous link, not yet
	// accepted) must NOT emit a log event — there is no PII worth recording.
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices: sampleDevices(),
		invites: map[string][]tsapi.DeviceInvite{
			"3690401478992208": {
				{Accepted: false, AllowExitNode: false, MultiUse: false,
					Email: "", AcceptedByLogin: ""},
			},
		},
	}
	c := devices.New(api, cache, 0, false, false, devices.WithDeviceInvites(true))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.device_invite" {
			t.Errorf("got unexpected invite log event for anonymous link: %+v", lr)
		}
	}
	// Count gauge still emitted.
	if pts := rec.MetricPoints("tailscale.device_invites.count"); len(pts) == 0 {
		t.Error("tailscale.device_invites.count gauge was not emitted")
	}
}

// --- B6: per-device + fleet version-skew ---

func TestDeviceVersionSkew(t *testing.T) {
	rec := telemetrytest.New()
	fake := &fakeAPI{devices: []tsapi.RichDevice{
		{ID: "n1", Hostname: "h1", ClientVersion: "1.95.0"},      // 3 behind -> outdated@3
		{ID: "n2", Hostname: "h2", ClientVersion: "1.98.4-tabc"}, // current -> skew 0
		{ID: "n3", Hostname: "h3", ClientVersion: "1.98.2"},      // patch-only -> skew 0
		{ID: "n4", Hostname: "h4", ClientVersion: ""},            // no version -> skipped
	}}
	cache := enrich.NewDeviceCache()
	c := devices.New(fake, cache, time.Minute, false, false,
		devices.WithUpstreamLatest(func() (string, bool) { return "1.98.4", true }, 3))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}

	// Per-device version_skew: n1=3, n2=0, n3=0; n4 skipped (no version).
	skew := rec.MetricPoints("tailscale.device.version_skew")
	got := map[string]float64{}
	for _, p := range skew {
		got[p.Attrs[semconv.HostID]] = p.Value
	}
	if got["n1"] != 3 {
		t.Errorf("n1 version_skew = %v, want 3", got["n1"])
	}
	if got["n2"] != 0 {
		t.Errorf("n2 version_skew = %v, want 0", got["n2"])
	}
	if got["n3"] != 0 {
		t.Errorf("n3 version_skew = %v, want 0", got["n3"])
	}
	if _, ok := got["n4"]; ok {
		t.Error("n4 (no version) should emit no skew point")
	}

	// fleet outdated: only n1 is >= 3 minors behind.
	outdated := rec.MetricPoints("tailscale.devices.outdated")
	if len(outdated) != 1 || outdated[0].Value != 1 {
		t.Errorf("devices.outdated = %+v, want 1 point value 1", outdated)
	}

	// fleet latest_version: value 1, label tailscale.client_version="1.98.4".
	latest := rec.MetricPoints("tailscale.fleet.latest_version")
	if len(latest) != 1 || latest[0].Value != 1 {
		t.Errorf("fleet.latest_version = %+v, want 1 point value 1", latest)
	}
	if latest[0].Attrs["tailscale.client_version"] != "1.98.4" {
		t.Errorf("fleet.latest_version tailscale.client_version = %q, want 1.98.4", latest[0].Attrs["tailscale.client_version"])
	}
}

func TestDeviceVersionSkewDisabled(t *testing.T) {
	rec := telemetrytest.New()
	fake := &fakeAPI{devices: []tsapi.RichDevice{{ID: "n1", ClientVersion: "1.95.0"}}}
	cache := enrich.NewDeviceCache()
	// No WithUpstreamLatest option -> B6 entirely off.
	c := devices.New(fake, cache, time.Minute, false, false)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tailscale.device.version_skew", "tailscale.devices.outdated", "tailscale.fleet.latest_version"} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("%s emitted while disabled: %+v", name, pts)
		}
	}
}

func TestDeviceVersionSkewPerEntityGate(t *testing.T) {
	// With perEntity=false, version_skew per-device gauge must NOT emit,
	// but devices.outdated and fleet.latest_version MUST still emit.
	rec := telemetrytest.New()
	fake := &fakeAPI{devices: []tsapi.RichDevice{
		{ID: "n1", Hostname: "h1", ClientVersion: "1.95.0"}, // 3 behind
	}}
	cache := enrich.NewDeviceCache()
	c := devices.New(fake, cache, time.Minute, false, false,
		devices.WithPerEntity(false),
		devices.WithUpstreamLatest(func() (string, bool) { return "1.98.4", true }, 3))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	if pts := rec.MetricPoints("tailscale.device.version_skew"); len(pts) != 0 {
		t.Errorf("version_skew emitted with perEntity=false: %+v", pts)
	}
	if pts := rec.MetricPoints("tailscale.devices.outdated"); len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("devices.outdated = %+v, want 1 point value 1 (not gated by perEntity)", pts)
	}
	if pts := rec.MetricPoints("tailscale.fleet.latest_version"); len(pts) != 1 {
		t.Errorf("fleet.latest_version = %+v, want 1 point (not gated by perEntity)", pts)
	}
}

func TestCollect_DeviceInvitesErrorIsNonFatal(t *testing.T) {
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{
		devices:    sampleDevices(),
		inviteFail: "3690401478992208",
		inviteErr:  context.DeadlineExceeded,
		invites: map[string][]tsapi.DeviceInvite{
			"n-desktop": {{Accepted: false, AllowExitNode: false, MultiUse: false}},
		},
	}
	c := devices.New(api, cache, 0, false, false, devices.WithDeviceInvites(true))

	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() must not fail when one device's invite fetch errors: %v", err)
	}
	pts := rec.MetricPoints("tailscale.device_invites.count")
	plain, ok := pointByAttr(pts, map[string]string{
		"tailscale.device_invite.accepted":        "false",
		"tailscale.device_invite.allow_exit_node": "false",
		"tailscale.device_invite.multi_use":       "false",
	})
	if !ok || plain.Value != 1 {
		t.Errorf("healthy-device series = %+v ok=%v, want value 1", plain, ok)
	}
	if len(rec.MetricPoints("tailscale.devices.count")) == 0 {
		t.Error("tailscale.devices.count not emitted; invite failure broke the devices snapshot")
	}
}

// --- B3 connectivity + B4 routing analytics ---

// ptr returns a pointer to v (for ClientSupports tri-state fields).
func ptr[T any](v T) *T { return &v }

// collectWith builds a devices collector with per-entity, connectivity and
// subnet-route rollup all ON, runs Collect over devs, and returns the recorder.
func collectWith(t *testing.T, devs []tsapi.RichDevice) *telemetrytest.Recorder {
	t.Helper()
	return collectWithOpts(t, devs, true, true, true)
}

// collectWithOpts builds a devices collector threading the connectivity,
// per-entity and subnet-route-rollup gates, runs Collect, and returns the
// recorder.
func collectWithOpts(t *testing.T, devs []tsapi.RichDevice, connectivity, perEntity, subnetRollup bool) *telemetrytest.Recorder {
	t.Helper()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{devices: devs}
	c := devices.New(api, cache, 0, false, false,
		devices.WithClock(func() time.Time { return now }),
		devices.WithConnectivity(connectivity),
		devices.WithPerEntity(perEntity),
		devices.WithSubnetRouteRollup(subnetRollup),
	)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return rec
}

// assertGauge asserts a single recorded gauge point for name whose attrs match
// want (string values; bools rendered as "true"/"false") has the given value.
func assertGauge(t *testing.T, rec *telemetrytest.Recorder, name string, want map[string]string, value float64) {
	t.Helper()
	p, ok := pointByAttr(rec.MetricPoints(name), want)
	if !ok {
		t.Errorf("%s: no point with attrs %v; points=%+v", name, want, rec.MetricPoints(name))
		return
	}
	if p.Value != value {
		t.Errorf("%s%v = %v, want %v", name, want, p.Value, value)
	}
}

// assertNoGauge asserts there is no recorded gauge point for name matching want.
func assertNoGauge(t *testing.T, rec *telemetrytest.Recorder, name string, want map[string]string) {
	t.Helper()
	if p, ok := pointByAttr(rec.MetricPoints(name), want); ok {
		t.Errorf("%s%v present = %+v, want absent", name, want, p)
	}
}

func TestCollect_Connectivity(t *testing.T) {
	devs := []tsapi.RichDevice{
		{ID: "1", Hostname: "a", HardNAT: true, Endpoints: []string{"x:1", "y:2"},
			ClientSupports: tsapi.ClientSupports{UDP: ptr(true), IPv6: ptr(false), PCP: ptr(false), PMP: ptr(false), UPnP: ptr(true)}},
		{ID: "2", Hostname: "b", HardNAT: false, Endpoints: []string{"z:3"},
			ClientSupports: tsapi.ClientSupports{UDP: ptr(true), IPv6: ptr(true)}},
	}
	rec := collectWith(t, devs)

	// Per-device: device 1 hard NAT, device 2 direct-capable.
	assertGauge(t, rec, "tailscale.device.connectivity.hard_nat", map[string]string{semconv.HostName: "a", semconv.HostID: "1"}, 1)
	assertGauge(t, rec, "tailscale.device.connectivity.hard_nat", map[string]string{semconv.HostName: "b", semconv.HostID: "2"}, 0)
	assertGauge(t, rec, "tailscale.device.connectivity.endpoints", map[string]string{semconv.HostName: "a", semconv.HostID: "1"}, 2)
	assertGauge(t, rec, "tailscale.device.connectivity.direct_capable", map[string]string{semconv.HostName: "b", semconv.HostID: "2"}, 1) // udp && !hardnat
	assertGauge(t, rec, "tailscale.device.connectivity.direct_capable", map[string]string{semconv.HostName: "a", semconv.HostID: "1"}, 0) // hard nat

	// Fleet rollups.
	assertGauge(t, rec, "tailscale.devices.hard_nat", nil, 1)
	assertGauge(t, rec, "tailscale.devices.direct_capable", nil, 1)
	assertGauge(t, rec, "tailscale.devices.client_supports", map[string]string{"tailscale.connectivity.capability": "udp"}, 2)
	assertGauge(t, rec, "tailscale.devices.client_supports", map[string]string{"tailscale.connectivity.capability": "ipv6"}, 1)
	assertGauge(t, rec, "tailscale.devices.client_supports", map[string]string{"tailscale.connectivity.capability": "upnp"}, 1)
}

func TestCollect_Routing(t *testing.T) {
	devs := []tsapi.RichDevice{
		{ID: "1", Hostname: "exit1", AdvertisedRoutes: []string{"0.0.0.0/0", "::/0", "10.0.0.0/24"}, EnabledRoutes: []string{"0.0.0.0/0", "::/0", "10.0.0.0/24"}},
		{ID: "2", Hostname: "exit2", AdvertisedRoutes: []string{"0.0.0.0/0", "::/0"}, EnabledRoutes: []string{}},
		{ID: "3", Hostname: "sub", AdvertisedRoutes: []string{"10.0.0.0/24", "192.168.9.0/24"}, EnabledRoutes: []string{"10.0.0.0/24"}},
	}
	rec := collectWith(t, devs)

	// 2 devices advertise exit; only exit1's default route is enabled.
	assertGauge(t, rec, "tailscale.exit_nodes.count", map[string]string{"tailscale.exit_node.state": "advertised"}, 2)
	assertGauge(t, rec, "tailscale.exit_nodes.count", map[string]string{"tailscale.exit_node.state": "enabled"}, 1)

	// Subnet CIDRs (exit defaults excluded): 10.0.0.0/24, 192.168.9.0/24 advertised.
	assertGauge(t, rec, "tailscale.subnet_routes.advertised", nil, 2)
	assertGauge(t, rec, "tailscale.subnet_routes.enabled", nil, 1)    // only 10.0.0.0/24 enabled
	assertGauge(t, rec, "tailscale.subnet_routes.unapproved", nil, 1) // 192.168.9.0/24 advertised, enabled nowhere

	// Redundancy: 10.0.0.0/24 advertised by exit1 + sub = 2 routers.
	assertGauge(t, rec, "tailscale.subnet_routes.routers", map[string]string{"tailscale.route.cidr": "10.0.0.0/24"}, 2)
	assertGauge(t, rec, "tailscale.subnet_routes.routers", map[string]string{"tailscale.route.cidr": "192.168.9.0/24"}, 1)

	// Per-device exit info: exit1 enabled, exit2 not; sub gets none.
	assertGauge(t, rec, "tailscale.device.exit_node", map[string]string{semconv.HostName: "exit1", semconv.HostID: "1", "tailscale.exit_node.enabled": "true"}, 1)
	assertGauge(t, rec, "tailscale.device.exit_node", map[string]string{semconv.HostName: "exit2", semconv.HostID: "2", "tailscale.exit_node.enabled": "false"}, 1)
	assertNoGauge(t, rec, "tailscale.device.exit_node", map[string]string{semconv.HostName: "sub", semconv.HostID: "3"})
}

func TestCollect_ConnectivityGatedOff(t *testing.T) {
	devs := []tsapi.RichDevice{{ID: "1", Hostname: "a", HardNAT: true}}
	rec := collectWithOpts(t, devs, false /*connectivity*/, true /*perEntity*/, true /*subnetRollup*/)
	assertNoGauge(t, rec, "tailscale.device.connectivity.hard_nat", map[string]string{semconv.HostName: "a", semconv.HostID: "1"})
	assertNoGauge(t, rec, "tailscale.devices.hard_nat", nil)
}

func TestCollect_PerEntityOffKeepsFleet(t *testing.T) {
	devs := []tsapi.RichDevice{{ID: "1", Hostname: "a", HardNAT: true, ClientSupports: tsapi.ClientSupports{UDP: ptr(true)}}}
	rec := collectWithOpts(t, devs, true, false /*perEntity*/, true)
	assertNoGauge(t, rec, "tailscale.device.connectivity.hard_nat", map[string]string{semconv.HostName: "a", semconv.HostID: "1"}) // per-device dropped
	assertGauge(t, rec, "tailscale.devices.hard_nat", nil, 1)                                                                      // fleet kept
}

func TestCollect_SubnetRouteRollupOff(t *testing.T) {
	devs := []tsapi.RichDevice{{ID: "1", Hostname: "a", AdvertisedRoutes: []string{"10.0.0.0/24"}}}
	rec := collectWithOpts(t, devs, true, true, false /*subnetRollup*/)
	assertNoGauge(t, rec, "tailscale.subnet_routes.routers", map[string]string{"tailscale.route.cidr": "10.0.0.0/24"})
	assertGauge(t, rec, "tailscale.subnet_routes.advertised", nil, 1) // fleet count still emitted
}

// --- J-B5: per-device node-key-expiry log event ---

func TestCollect_DeviceKeyExpiring_LogEmitted(t *testing.T) {
	// A device whose key expires within the warn threshold (14d) must emit
	// tailscale.device.key_expiring WARN with host.name, host.id (d.ID), and
	// tailscale.device.key_expires_in_days; the histogram must still emit.
	devs := []tsapi.RichDevice{
		{
			// Within threshold: expires in 5 days.
			ID: "dev-warn", Hostname: "warn-host",
			KeyExpiryDisabled: false,
			Expires:           now.Add(5 * 24 * time.Hour),
		},
		{
			// Beyond threshold: expires in 30 days → no log.
			ID: "dev-ok", Hostname: "ok-host",
			KeyExpiryDisabled: false,
			Expires:           now.Add(30 * 24 * time.Hour),
		},
		{
			// Key expiry disabled → no log, no histogram.
			ID: "dev-disabled", Hostname: "disabled-host",
			KeyExpiryDisabled: true,
			Expires:           now.Add(3 * 24 * time.Hour),
		},
		{
			// Zero Expires → no log, no histogram.
			ID: "dev-zero", Hostname: "zero-host",
			KeyExpiryDisabled: false,
		},
	}
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{devices: devs}
	c := devices.New(api, cache, 0, false, false, devices.WithClock(func() time.Time { return now }))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	// Filter only key_expiring log events.
	var keyExpiringLogs []telemetrytest.LogRecord
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.device.key_expiring" {
			keyExpiringLogs = append(keyExpiringLogs, lr)
		}
	}

	// Exactly one log event: only dev-warn is within threshold.
	if len(keyExpiringLogs) != 1 {
		t.Fatalf("key_expiring log events = %d, want 1; all logs: %+v", len(keyExpiringLogs), rec.LogRecords())
	}

	lr := keyExpiringLogs[0]
	if lr.SeverityText != "WARN" {
		t.Errorf("severity = %q, want WARN", lr.SeverityText)
	}
	// Body must NOT contain the hostname (PII); hostname lives in host.name attr.
	if lr.Body != "device node key expiring soon" {
		t.Errorf("body = %q, want %q", lr.Body, "device node key expiring soon")
	}
	if got := lr.Attrs["host.name"]; got != "warn-host" {
		t.Errorf("host.name = %q, want warn-host", got)
	}
	// host.id must be the device ID (d.ID), consistent with per-device metrics.
	if got := lr.Attrs["host.id"]; got != "dev-warn" {
		t.Errorf("host.id = %q, want dev-warn (device ID)", got)
	}
	if got := lr.Attrs["tailscale.device.key_expires_in_days"]; got == "" {
		t.Error("tailscale.device.key_expires_in_days attr missing")
	}

	// The histogram must still have 2 observations: dev-warn (5d) + dev-ok (30d).
	hpts := rec.MetricPoints("tailscale.devices.key_expiry")
	if len(hpts) != 1 || hpts[0].Count != 2 {
		t.Errorf("key_expiry histogram count = %d, want 2", hpts[0].Count)
	}
}

func TestCollect_DeviceKeyExpiring_NoLogWhenDisabledOrFarFuture(t *testing.T) {
	// Ensures disabled/zero/far-future devices never emit the key_expiring log.
	devs := []tsapi.RichDevice{
		{ID: "d1", Hostname: "h1", KeyExpiryDisabled: true, Expires: now.Add(5 * 24 * time.Hour)},
		{ID: "d2", Hostname: "h2", KeyExpiryDisabled: false, Expires: now.Add(60 * 24 * time.Hour)},
		{ID: "d3", Hostname: "h3", KeyExpiryDisabled: false}, // zero Expires
	}
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	api := &fakeAPI{devices: devs}
	c := devices.New(api, cache, 0, false, false, devices.WithClock(func() time.Time { return now }))
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	for _, lr := range rec.LogRecords() {
		if lr.EventName == "tailscale.device.key_expiring" {
			t.Errorf("unexpected key_expiring log for %+v", lr)
		}
	}
}
