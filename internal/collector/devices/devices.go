// Package devices implements the "devices" snapshot collector. On each tick it
// lists every device in the tailnet, emits per-device and aggregate metrics,
// and repopulates the shared enrich.DeviceCache so flow and audit records can be
// resolved to human-readable device identity.
//
// Notes on the pinned tailscale-client-go/v2 (v2.0.0-20250129222324): the
// Device type is a flat struct with no ClientConnectivity field and timestamps
// are value types (tsclient.Time embedding time.Time), not pointers. As a
// result:
//   - There is no live "connected to control" flag, so device online state is
//     derived from LastSeen recency within onlineWindow (default 5m).
//   - There is no DERP region/latency data on the device, so the per-region
//     DERP latency gauge has no source in this version and is not emitted.
//   - Nil-guards are expressed via tsclient.Time.IsZero() rather than nil
//     pointer checks. OSVersion is not present on the device and is left empty.
package devices

import (
	"context"
	"net/netip"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Metric names emitted by this collector.
const (
	metricOnline          = "tailscale.device.online"
	metricLastSeen        = "tailscale.device.last_seen"
	metricKeyExpiry       = "tailscale.device.key.expiry"
	metricUpdateAvailable = "tailscale.device.update_available"
	metricDevicesCount    = "tailscale.devices.count"
)

// Attribute keys specific to the aggregate device count.
const (
	attrAuthorized = "tailscale.authorized"
	attrExternal   = "tailscale.external"
)

const (
	defaultInterval = 60 * time.Second
	// defaultOnlineWindow is how recently a device must have been seen to be
	// considered online, given the API exposes no live connection flag.
	defaultOnlineWindow = 5 * time.Minute
)

// lister is the subset of the Tailscale API this collector needs. It is
// satisfied by *tsapi.Client.
type lister interface {
	Devices(ctx context.Context) ([]tsclient.Device, error)
}

// Collector implements collector.SnapshotCollector for the device inventory.
type Collector struct {
	api          lister
	cache        *enrich.DeviceCache
	interval     time.Duration
	onlineWindow time.Duration
	now          func() time.Time
}

// New returns a devices Collector that lists via api, repopulates cache, and
// uses interval as its poll cadence (a non-positive interval defaults to 60s).
func New(api lister, cache *enrich.DeviceCache, interval time.Duration) *Collector {
	return &Collector{
		api:          api,
		cache:        cache,
		interval:     interval,
		onlineWindow: defaultOnlineWindow,
		now:          time.Now,
	}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "devices" }

// DefaultInterval returns the configured interval, or 60s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect lists devices, repopulates the cache, and emits metrics.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	devs, err := c.api.Devices(ctx)
	if err != nil {
		return err
	}

	c.cache.Replace(toMetas(devs))

	now := c.now()
	type countKey struct {
		os         string
		authorized bool
		external   bool
	}
	counts := make(map[countKey]int, len(devs))

	for i := range devs {
		d := &devs[i]
		idAttrs := telemetry.Attrs{
			semconv.HostName: d.Hostname,
			semconv.HostID:   d.ID,
			semconv.OSType:   d.OS,
			semconv.AttrUser: d.User,
		}

		online := 0.0
		if !d.LastSeen.IsZero() && now.Sub(d.LastSeen.Time) <= c.onlineWindow {
			online = 1
		}
		e.Gauge(metricOnline, semconv.UnitDimensionless, "device seen within the online window", online, idAttrs)

		if !d.LastSeen.IsZero() {
			e.Gauge(metricLastSeen, semconv.UnitSeconds, "device last-seen time (unix seconds)",
				float64(d.LastSeen.Unix()), idAttrs)
		}

		if !d.KeyExpiryDisabled && !d.Expires.IsZero() {
			e.Gauge(metricKeyExpiry, semconv.UnitSeconds, "device node-key expiry time (unix seconds)",
				float64(d.Expires.Unix()), idAttrs)
		}

		e.Gauge(metricUpdateAvailable, semconv.UnitDimensionless, "client update available (0/1)",
			boolToFloat(d.UpdateAvailable), idAttrs)

		counts[countKey{os: d.OS, authorized: d.Authorized, external: d.IsExternal}]++
	}

	for k, n := range counts {
		e.Gauge(metricDevicesCount, semconv.UnitDimensionless, "device count by os/authorized/external",
			float64(n), telemetry.Attrs{
				semconv.OSType: k.os,
				attrAuthorized: k.authorized,
				attrExternal:   k.external,
			})
	}

	return nil
}

// toMetas converts API devices to the cache's normalized DeviceMeta form,
// parsing each address and skipping any that fail to parse.
func toMetas(devs []tsclient.Device) []enrich.DeviceMeta {
	metas := make([]enrich.DeviceMeta, 0, len(devs))
	for i := range devs {
		d := &devs[i]
		addrs := make([]netip.Addr, 0, len(d.Addresses))
		for _, s := range d.Addresses {
			a, err := netip.ParseAddr(s)
			if err != nil {
				continue
			}
			addrs = append(addrs, a)
		}
		metas = append(metas, enrich.DeviceMeta{
			NodeID:    d.ID,
			Name:      d.Name,
			Hostname:  d.Hostname,
			OS:        d.OS,
			OSVersion: "", // not exposed by the pinned client
			User:      d.User,
			Tags:      d.Tags,
			Addrs:     addrs,
			External:  d.IsExternal,
		})
	}
	return metas
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
