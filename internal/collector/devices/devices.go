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
	"sort"
	"strings"
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
	metricAttribute        = "tailscale.device.attribute"
	metricAttributeInfo    = "tailscale.device.attribute.info"

	metricCacheAge  = "tailscale2otel.enrich.cache_age"
	metricCacheSize = "tailscale2otel.enrich.cache_size"

	eventPosture = "tailscale.device.posture"

	metricTailnetLockErrors    = "tailscale.tailnet_lock.errors"
	metricDerpRegionLatencyMin = "tailscale.derp.region.latency_min"
	metricDerpRegionDevices    = "tailscale.derp.region.devices"
	metricDerpRegionPreferred  = "tailscale.derp.region.preferred"

	eventTailnetLockError = "tailscale.device.tailnet_lock_error"
)

// Attribute keys specific to this collector.
const (
	attrAuthorized    = "tailscale.authorized"
	attrExternal      = "tailscale.external"
	attrDERPRegion    = "tailscale.derp.region"
	attrDERPPreferred = "tailscale.derp.preferred"

	// Posture attribute metric labels (tailscale.device.attribute{,.info}): the
	// full namespaced posture key, and (info gauge only) its string value.
	attrAttribute      = "attribute"
	attrAttributeValue = "value"
)

// Curated posture label keys carried by the posture info gauge. Each maps a
// colon-namespaced posture attribute (node:os, node:osVersion, …) to a short,
// analytics-friendly label name; a label is set only when its source key is
// present in the device's posture map.
const (
	attrPostureOS         = "os"
	attrPostureOSVersion  = "os_version"
	attrPostureTSVersion  = "ts_version"
	attrPostureAutoUpdate = "auto_update"
	attrPostureEncrypted  = "encrypted"
	attrPostureTrack      = "track"
)

// Posture LOG emission modes, controlling how often the posture log event fires
// (the posture info gauge metric is unaffected and always emitted per scrape).
const (
	// postureLogChanges emits the posture log for a device only when its posture
	// changed since the previous scrape (first-seen counts as changed). Default.
	postureLogChanges = "changes"
	// postureLogAlways emits the posture log every scrape (legacy behavior).
	postureLogAlways = "always"
	// postureLogOff never emits the posture log (the info gauge is still emitted).
	postureLogOff = "off"
)

// postureKeyToLabel maps the curated colon-namespaced posture attribute keys to
// their short metric label names. Posture keys not present here are not carried
// on the info gauge (they remain on the posture log's full attribute set).
var postureKeyToLabel = map[string]string{
	"node:os":               attrPostureOS,
	"node:osVersion":        attrPostureOSVersion,
	"node:tsVersion":        attrPostureTSVersion,
	"node:tsAutoUpdate":     attrPostureAutoUpdate,
	"node:tsStateEncrypted": attrPostureEncrypted,
	"node:tsReleaseTrack":   attrPostureTrack,
}

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
	derpRollup     bool
	postureLogMode string // "changes" (default) | "always" | "off"

	// attrNamespaces is the set of posture-attribute namespace prefixes (the part
	// before ":") promoted to the tailscale.device.attribute{,.info} metrics, and
	// attrNamespaceWildcard promotes every namespace present. Empty set +
	// non-wildcard disables the attribute metrics. Built by WithAttributeNamespaces.
	attrNamespaces        map[string]bool
	attrNamespaceWildcard bool

	// lastPosture remembers each device's last-emitted posture signature
	// (deviceID -> signature) so the posture LOG can fire on-change only. A
	// device absent from the map is first-seen and counts as changed.
	lastPosture map[string]string
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

// WithDerpRegionRollup controls whether the tailnet-wide per-DERP-region rollup
// gauges (latency_min, devices, preferred) are emitted (default true;
// cardinality.derp_region_rollup). The rollup is computed from the per-device
// DERP latency already fetched and is emitted independently of per_entity, so it
// is the low-cardinality DERP view that survives when device_per_entity is off.
func WithDerpRegionRollup(enabled bool) Option {
	return func(c *Collector) { c.derpRollup = enabled }
}

// WithPostureLogMode controls how often the per-device posture LOG event fires
// (it does not affect the posture info-gauge metric, which is emitted every
// scrape when collect_posture is on):
//
//   - "changes" (default): emit the posture log for a device only when its
//     posture changed since the previous scrape; a first-seen device counts as
//     changed, so process start dumps a full baseline, then only deltas.
//   - "always": emit the posture log every scrape (the legacy behavior).
//   - "off": never emit the posture log (the info-gauge metric is still emitted).
//
// An empty or unrecognized mode falls back to "changes".
func WithPostureLogMode(mode string) Option {
	return func(c *Collector) { c.postureLogMode = normalizePostureLogMode(mode) }
}

// normalizePostureLogMode maps an arbitrary mode string to a known mode,
// defaulting unknown/empty values to "changes".
func normalizePostureLogMode(mode string) string {
	switch mode {
	case postureLogAlways, postureLogOff, postureLogChanges:
		return mode
	default:
		return postureLogChanges
	}
}

// WithAttributeNamespaces sets the posture-attribute namespace allow-list: each
// entry is a namespace prefix (the part before ":" in a posture key, e.g.
// "intune", "ip") whose attributes are promoted to the
// tailscale.device.attribute{,.info} metrics. The sentinel "*" promotes every
// namespace present (including node and custom). An empty list (the default)
// disables the attribute metrics; the posture info-gauge and posture log are
// unaffected. Requires collect_posture (which fetches the attributes) — no extra
// API calls are made, the already-fetched attribute map is reused.
func WithAttributeNamespaces(ns []string) Option {
	return func(c *Collector) {
		c.attrNamespaces = make(map[string]bool, len(ns))
		c.attrNamespaceWildcard = false
		for _, n := range ns {
			if n == "*" {
				c.attrNamespaceWildcard = true
				continue
			}
			if n = strings.TrimSpace(n); n != "" {
				c.attrNamespaces[n] = true
			}
		}
	}
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
		derpRollup:     true,
		postureLogMode: postureLogChanges,
		lastPosture:    make(map[string]string),
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
	lockErrors := 0

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
			// tailscale.tags: comma-joined ACL tags (matches the node-metrics
			// label in nodediscovery.go). Omitted for untagged devices, like
			// os.version above. Functionally dependent on the device, so it adds
			// no new series — just a richer label on the existing per-device ones.
			if len(d.Tags) > 0 {
				idAttrs[semconv.AttrTags] = strings.Join(d.Tags, ",")
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

		if d.TailnetLockError != "" {
			lockErrors++
			e.LogEvent(telemetry.Event{
				Name:     docTailnetLockError.Name,
				Severity: telemetry.SeverityError,
				Body:     d.TailnetLockError,
				Attrs:    telemetry.Attrs{semconv.HostName: d.Hostname, semconv.HostID: d.ID},
			})
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

	e.Gauge(docTailnetLockErrors.Name, docTailnetLockErrors.Unit, docTailnetLockErrors.Description,
		float64(lockErrors), nil)

	if c.derpRollup {
		c.emitDERPRollup(e, devs)
	}

	return nil
}

// emitDERPRollup aggregates the per-device DERP latency already fetched into
// tailnet-wide per-region gauges: the best (min) latency to each region, the
// number of devices reporting it, and how many prefer it.
func (c *Collector) emitDERPRollup(e telemetry.Emitter, devs []tsapi.RichDevice) {
	type agg struct {
		minMs     float64
		haveMin   bool
		devices   int
		preferred int
	}
	byRegion := map[string]*agg{}
	for i := range devs {
		for region, derp := range devs[i].DERPLatency {
			a := byRegion[region]
			if a == nil {
				a = &agg{}
				byRegion[region] = a
			}
			a.devices++
			if derp.Preferred {
				a.preferred++
			}
			if !a.haveMin || derp.LatencyMs < a.minMs {
				a.minMs = derp.LatencyMs
				a.haveMin = true
			}
		}
	}
	for region, a := range byRegion {
		attrs := telemetry.Attrs{attrDERPRegion: region}
		e.Gauge(docDerpRegionLatencyMin.Name, docDerpRegionLatencyMin.Unit, docDerpRegionLatencyMin.Description,
			a.minMs/1000, attrs)
		e.Gauge(docDerpRegionDevices.Name, docDerpRegionDevices.Unit, docDerpRegionDevices.Description,
			float64(a.devices), attrs)
		e.Gauge(docDerpRegionPreferred.Name, docDerpRegionPreferred.Unit, docDerpRegionPreferred.Description,
			float64(a.preferred), attrs)
	}
}

// emitPosture fetches the posture attributes for one device, always emits the
// posture info-gauge metric (constant 1, curated labels), and conditionally
// emits the full posture LOG event depending on the configured posture log mode.
// Per-device errors are non-fatal: the device is skipped and collection
// continues.
func (c *Collector) emitPosture(ctx context.Context, e telemetry.Emitter, d *tsapi.RichDevice) {
	attrs, err := c.api.DevicePostureAttributes(ctx, d.ID)
	if err != nil {
		return
	}

	// Info-gauge metric: always emitted (independent of log mode). Constant 1,
	// carrying the curated posture subset plus device identity as labels.
	metricAttrs := telemetry.Attrs{
		semconv.HostName: d.Hostname,
		semconv.HostID:   d.ID,
	}
	for srcKey, label := range postureKeyToLabel {
		if v, ok := attrs[srcKey]; ok {
			metricAttrs[label] = fmt.Sprint(v)
		}
	}
	e.Gauge(docPostureInfo.Name, docPostureInfo.Unit, docPostureInfo.Description, 1, metricAttrs)

	// Promote the allow-listed posture attributes to queryable metrics (hybrid
	// model), reusing the already-fetched attribute map — no extra API call.
	if c.attrNamespaceWildcard || len(c.attrNamespaces) > 0 {
		c.emitAttributes(e, d, attrs)
	}

	// Decide whether to emit the LOG. The signature is computed over the FULL
	// posture map so any posture change (not just curated keys) fires the log.
	sig := postureSignature(attrs)
	emitLog := false
	switch c.postureLogMode {
	case postureLogOff:
		emitLog = false
	case postureLogAlways:
		emitLog = true
	default: // postureLogChanges
		prev, seen := c.lastPosture[d.ID]
		emitLog = !seen || prev != sig
	}
	c.lastPosture[d.ID] = sig

	if !emitLog {
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

// emitAttributes promotes the allow-listed posture attributes to metrics
// (hybrid model): boolean and numeric values become the tailscale.device.attribute
// gauge (where the value carries meaning — 0/1 for booleans, the number itself
// otherwise); string/enum values become the tailscale.device.attribute.info gauge
// (constant 1, the string carried as the `value` label). Attributes whose
// namespace (the part before ":") is not allow-listed are skipped, as are
// non-scalar values (posture values are documented as string|number|bool).
func (c *Collector) emitAttributes(e telemetry.Emitter, d *tsapi.RichDevice, attrs map[string]any) {
	for key, v := range attrs {
		ns, _, ok := strings.Cut(key, ":")
		if !ok {
			continue // Tailscale posture keys are always namespaced.
		}
		if !c.attrNamespaceWildcard && !c.attrNamespaces[ns] {
			continue
		}
		labels := telemetry.Attrs{
			semconv.HostName: d.Hostname,
			semconv.HostID:   d.ID,
			attrAttribute:    key,
		}
		switch val := v.(type) {
		case bool:
			e.Gauge(docAttribute.Name, docAttribute.Unit, docAttribute.Description, boolToFloat(val), labels)
		case float64:
			e.Gauge(docAttribute.Name, docAttribute.Unit, docAttribute.Description, val, labels)
		case string:
			labels[attrAttributeValue] = val
			e.Gauge(docAttributeInfo.Name, docAttributeInfo.Unit, docAttributeInfo.Description, 1, labels)
		default:
			// Skip anything that isn't a scalar string/number/bool.
		}
	}
}

// postureSignature returns a stable string fingerprint of a posture map: each
// entry rendered as key=value, sorted by key and joined, so logically-equal
// maps produce equal signatures regardless of Go map iteration order.
func postureSignature(attrs map[string]any) string {
	if len(attrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attrs))
	for k, v := range attrs {
		parts = append(parts, k+"="+fmt.Sprint(v))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x1f")
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
