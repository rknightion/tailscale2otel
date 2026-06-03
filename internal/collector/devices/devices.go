// Package devices implements the "devices" snapshot collector. On each tick it
// lists every device in the tailnet via a single GET /devices?fields=all (the
// rich field set), emits per-device and aggregate metrics, and repopulates the
// shared enrich.DeviceCache so flow and audit records can be resolved to
// human-readable device identity.
//
// The rich device record carries a real connected-to-control flag, per-region
// DERP latency, inline advertised/enabled routes, and OS distribution details
// (os.version) with no per-device fan-out. The optional route gauges read the
// inline route slices (no extra API calls); posture collection, when enabled,
// makes one additional API call per device and is therefore off by default.
package devices

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// Compile-time assertions: *Collector is a SnapshotCollector and *tsapi.Client
// satisfies the narrow api surface this collector depends on.
var (
	_ collector.SnapshotCollector = (*Collector)(nil)
	_ api                         = (*tsapi.Client)(nil)
)

// Metric and event names emitted by this collector.
const (
	metricOnline           = "tailscale.device.online"
	metricLastSeen         = "tailscale.device.last_seen"
	metricKeyExpiry        = "tailscale.device.key.expiry"
	metricUpdateAvailable  = "tailscale.device.update_available"
	metricDERPLatency      = "tailscale.device.derp.latency"
	metricRoutesAdvertised = "tailscale.device.routes.advertised"
	metricRoutesEnabled    = "tailscale.device.routes.enabled"
	metricDevicesCount     = "tailscale.devices.count"

	metricCacheAge  = "tailscale2otel.enrich.cache_age"
	metricCacheSize = "tailscale2otel.enrich.cache_size"

	eventPosture = "tailscale.device.posture"
)

// Attribute keys specific to this collector.
const (
	attrAuthorized    = "tailscale.authorized"
	attrExternal      = "tailscale.external"
	attrDERPRegion    = "tailscale.derp.region"
	attrDERPPreferred = "tailscale.derp.preferred"
)

const defaultInterval = 60 * time.Second

// api is the subset of the Tailscale API this collector needs. It is satisfied
// by *tsapi.Client.
type api interface {
	DevicesRich(ctx context.Context) ([]tsapi.RichDevice, error)
	DevicePostureAttributes(ctx context.Context, deviceID string) (map[string]any, error)
}

// Collector implements collector.SnapshotCollector for the device inventory.
type Collector struct {
	api            api
	cache          *enrich.DeviceCache
	interval       time.Duration
	collectRoutes  bool
	collectPosture bool
	perEntity      bool
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-device gauges (online, last_seen,
// key.expiry, update_available, DERP latency, routes) are emitted. The default
// is true; false (cardinality.device_per_entity) emits only the aggregate
// tailscale.devices.count rollup, dropping the per-device series.
func WithPerEntity(enabled bool) Option {
	return func(c *Collector) { c.perEntity = enabled }
}

// New returns a devices Collector that lists via the rich devices endpoint,
// repopulates cache, and uses interval as its poll cadence (a non-positive
// interval defaults to 60s). When collectRoutes is true the per-device route
// gauges are emitted (read from the inline route slices, no extra API call).
// When collectPosture is true the collector additionally calls
// DevicePostureAttributes once per device (N API calls per tick) and emits a
// posture log event per device; it is off by default. Options (e.g.
// WithPerEntity) tune cardinality; per-entity gauges are emitted by default.
func New(api api, cache *enrich.DeviceCache, interval time.Duration, collectRoutes, collectPosture bool, opts ...Option) *Collector {
	c := &Collector{
		api:            api,
		cache:          cache,
		interval:       interval,
		collectRoutes:  collectRoutes,
		collectPosture: collectPosture,
		perEntity:      true,
	}
	for _, o := range opts {
		o(c)
	}
	return c
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

// Collect lists devices, repopulates the cache, and emits metrics (and, when
// enabled, route gauges and posture log events).
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	devs, err := c.api.DevicesRich(ctx)
	if err != nil {
		return err
	}

	c.cache.Replace(toMetas(devs))
	e.Gauge(docCacheAge.Name, docCacheAge.Unit, docCacheAge.Description,
		c.cache.Age().Seconds(), nil)
	e.Gauge(docCacheSize.Name, docCacheSize.Unit, docCacheSize.Description,
		float64(c.cache.Len()), nil)

	type countKey struct {
		os         string
		authorized bool
		external   bool
	}
	counts := make(map[countKey]int, len(devs))

	for i := range devs {
		d := &devs[i]

		// Per-device gauges (one series per device) are gated by
		// cardinality.device_per_entity; when off, only the aggregate
		// devices.count rollup below is emitted.
		if c.perEntity {
			idAttrs := telemetry.Attrs{
				semconv.HostName: d.Hostname,
				semconv.HostID:   d.ID,
				semconv.OSType:   d.OS,
				semconv.AttrUser: d.User,
			}
			if d.Distro.Version != "" {
				idAttrs[semconv.OSVersion] = d.Distro.Version
			}

			e.Gauge(docOnline.Name, docOnline.Unit, docOnline.Description,
				boolToFloat(d.ConnectedToControl), idAttrs)

			if !d.LastSeen.IsZero() {
				e.Gauge(docLastSeen.Name, docLastSeen.Unit, docLastSeen.Description,
					float64(d.LastSeen.Unix()), idAttrs)
			}

			if !d.KeyExpiryDisabled && !d.Expires.IsZero() {
				e.Gauge(docKeyExpiry.Name, docKeyExpiry.Unit, docKeyExpiry.Description,
					float64(d.Expires.Unix()), idAttrs)
			}

			e.Gauge(docUpdateAvailable.Name, docUpdateAvailable.Unit, docUpdateAvailable.Description,
				boolToFloat(d.UpdateAvailable), idAttrs)

			for region, derp := range d.DERPLatency {
				e.Gauge(docDERPLatency.Name, docDERPLatency.Unit, docDERPLatency.Description,
					derp.LatencyMs/1000, telemetry.Attrs{
						semconv.HostName:  d.Hostname,
						semconv.HostID:    d.ID,
						attrDERPRegion:    region,
						attrDERPPreferred: derp.Preferred,
					})
			}

			if c.collectRoutes {
				routeAttrs := telemetry.Attrs{
					semconv.HostName: d.Hostname,
					semconv.HostID:   d.ID,
				}
				e.Gauge(docRoutesAdvertised.Name, docRoutesAdvertised.Unit, docRoutesAdvertised.Description,
					float64(len(d.AdvertisedRoutes)), routeAttrs)
				e.Gauge(docRoutesEnabled.Name, docRoutesEnabled.Unit, docRoutesEnabled.Description,
					float64(len(d.EnabledRoutes)), routeAttrs)
			}
		}

		if c.collectPosture {
			c.emitPosture(ctx, e, d)
		}

		counts[countKey{os: d.OS, authorized: d.Authorized, external: d.IsExternal}]++
	}

	for k, n := range counts {
		e.Gauge(docDevicesCount.Name, docDevicesCount.Unit, docDevicesCount.Description,
			float64(n), telemetry.Attrs{
				semconv.OSType: k.os,
				attrAuthorized: k.authorized,
				attrExternal:   k.external,
			})
	}

	return nil
}

// emitPosture fetches the posture attributes for one device and emits a single
// posture log event. Per-device errors are non-fatal: the device is skipped and
// collection continues.
func (c *Collector) emitPosture(ctx context.Context, e telemetry.Emitter, d *tsapi.RichDevice) {
	attrs, err := c.api.DevicePostureAttributes(ctx, d.ID)
	if err != nil {
		return
	}
	evAttrs := telemetry.Attrs{
		semconv.HostName: d.Hostname,
		semconv.HostID:   d.ID,
	}
	for k, v := range attrs {
		evAttrs[k] = fmt.Sprint(v)
	}
	e.LogEvent(telemetry.Event{
		Name:     docPosture.Name,
		Severity: telemetry.SeverityInfo,
		Body:     fmt.Sprintf("device %q has %d posture attribute(s)", d.Hostname, len(attrs)),
		Attrs:    evAttrs,
	})
}

// toMetas converts rich API devices to the cache's normalized DeviceMeta form,
// parsing each address and skipping any that fail to parse. NodeID is set to the
// control-plane node id (used in flow logs), not the numeric device ID.
func toMetas(devs []tsapi.RichDevice) []enrich.DeviceMeta {
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
			NodeID:    d.NodeID,
			Name:      d.Name,
			Hostname:  d.Hostname,
			OS:        d.OS,
			OSVersion: d.Distro.Version,
			User:      d.User,
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
