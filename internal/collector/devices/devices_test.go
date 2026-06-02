package devices_test

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// fakeLister returns a canned device list, satisfying the collector's lister.
type fakeLister struct {
	devices []tsclient.Device
	err     error
	calls   int
}

func (f *fakeLister) Devices(_ context.Context) ([]tsclient.Device, error) {
	f.calls++
	return f.devices, f.err
}

// now is the deterministic clock used across tests.
var now = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

func tsTime(t time.Time) tsclient.Time { return tsclient.Time{Time: t} }

// sampleDevices returns three devices exercising the metric and cache paths.
func sampleDevices() []tsclient.Device {
	return []tsclient.Device{
		{
			// Online (seen 1m ago), key expires, no update, linux, authorized, internal.
			ID:                "n-laptop",
			Name:              "laptop.tail1a2b.ts.net",
			Hostname:          "laptop",
			OS:                "linux",
			User:              "alice@example.com",
			Tags:              []string{"tag:server"},
			Addresses:         []string{"100.64.0.1", "fd7a:115c:a1e0::1"},
			Authorized:        true,
			IsExternal:        false,
			KeyExpiryDisabled: false,
			Expires:           tsTime(now.Add(48 * time.Hour)),
			LastSeen:          tsTime(now.Add(-1 * time.Minute)),
			UpdateAvailable:   false,
		},
		{
			// Offline (seen 2h ago), key expiry disabled, update available, windows, authorized, internal.
			ID:                "n-desktop",
			Name:              "desktop.tail1a2b.ts.net",
			Hostname:          "desktop",
			OS:                "windows",
			User:              "bob@example.com",
			Addresses:         []string{"100.64.0.2"},
			Authorized:        true,
			IsExternal:        false,
			KeyExpiryDisabled: true,
			Expires:           tsTime(now.Add(72 * time.Hour)),
			LastSeen:          tsTime(now.Add(-2 * time.Hour)),
			UpdateAvailable:   true,
		},
		{
			// Never seen (zero LastSeen -> offline), zero Expires, linux, unauthorized, external.
			ID:                "n-phone",
			Name:              "phone.tail1a2b.ts.net",
			Hostname:          "phone",
			OS:                "linux",
			User:              "carol@example.com",
			Addresses:         []string{"100.64.0.3"},
			Authorized:        false,
			IsExternal:        true,
			KeyExpiryDisabled: false,
			// LastSeen left zero (never seen) and Expires left zero.
			UpdateAvailable: false,
		},
	}
}

func newCollector(t *testing.T, devs []tsclient.Device) (*devices.Collector, *enrich.DeviceCache, *fakeLister) {
	t.Helper()
	cache := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	lister := &fakeLister{devices: devs}
	c := devices.New(lister, cache, 0)
	devices.SetClock(c, func() time.Time { return now })
	return c, cache, lister
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
	c2 := devices.New(&fakeLister{}, cache, 30*time.Second)
	if got := c2.DefaultInterval(); got != 30*time.Second {
		t.Fatalf("DefaultInterval() = %v, want 30s (explicit)", got)
	}
}

func TestCollect_ReturnsListerError(t *testing.T) {
	cache := enrich.NewDeviceCache()
	lister := &fakeLister{err: context.DeadlineExceeded}
	c := devices.New(lister, cache, 0)
	rec := telemetrytest.New()
	if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
		t.Fatal("Collect() error = nil, want non-nil")
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

	laptop, ok := pointByAttr(pts, map[string]string{semconv.HostID: "n-laptop"})
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
		t.Fatalf("laptop online = %v, want 1 (seen 1m ago)", laptop.Value)
	}
	// Device-identity attrs.
	if laptop.Attrs[semconv.HostName] != "laptop" {
		t.Fatalf("online host.name = %q, want laptop", laptop.Attrs[semconv.HostName])
	}
	if laptop.Attrs[semconv.OSType] != "linux" {
		t.Fatalf("online os.type = %q, want linux", laptop.Attrs[semconv.OSType])
	}
	if laptop.Attrs[semconv.AttrUser] != "alice@example.com" {
		t.Fatalf("online tailscale.user = %q, want alice@example.com", laptop.Attrs[semconv.AttrUser])
	}

	desktop, ok := pointByAttr(pts, map[string]string{semconv.HostID: "n-desktop"})
	if !ok {
		t.Fatal("no online point for desktop")
	}
	if desktop.Value != 0 {
		t.Fatalf("desktop online = %v, want 0 (seen 2h ago)", desktop.Value)
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
	laptop, ok := pointByAttr(pts, map[string]string{semconv.HostID: "n-laptop"})
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
	if laptop.Attrs[semconv.HostID] != "n-laptop" {
		t.Fatalf("key.expiry host.id = %q, want n-laptop", laptop.Attrs[semconv.HostID])
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
	laptop, _ := pointByAttr(pts, map[string]string{semconv.HostID: "n-laptop"})
	if laptop.Value != 0 {
		t.Fatalf("laptop update_available = %v, want 0", laptop.Value)
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
	// Distinct (os.type, authorized, external) combos among the 3 devices:
	//   linux/true/false  (laptop)        -> 1
	//   windows/true/false (desktop)      -> 1
	//   linux/false/true  (phone)         -> 1
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
}
