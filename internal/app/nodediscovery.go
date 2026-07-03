package app

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/rknightion/tailscale2otel/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// nodeDiscoveryAPI is the subset of the Tailscale API the node-metrics
// discoverer needs. It is satisfied by *tsapi.Client and faked in tests.
type nodeDiscoveryAPI interface {
	DevicesRich(ctx context.Context) ([]tsapi.RichDevice, error)
}

var _ nodeDiscoveryAPI = (*tsapi.Client)(nil)

// deviceCacheReader is the subset of *enrich.DeviceCache the discoverer needs
// to source targets from the devices collector's shared cache instead of
// issuing its own DevicesRich() poll (#85). Narrowed to an interface so it can
// be faked in tests without a real cache.
type deviceCacheReader interface {
	Snapshot() []enrich.DeviceMeta
}

var _ deviceCacheReader = (*enrich.DeviceCache)(nil)

// nodeDiscovererOption configures a nodeDiscoverer at construction.
type nodeDiscovererOption func(*nodeDiscoverer)

// withDeviceCache makes the discoverer prefer cache over its own DevicesRich()
// call when the cache currently holds at least one device (#85). The devices
// collector populates this same cache (see internal/collector/devices) on its
// own, typically shorter, interval — reusing it avoids a duplicate full-
// inventory fetch against the heaviest, most rate-limit-sensitive Tailscale
// API endpoint. An empty cache (devices collector disabled, or not yet ticked)
// makes Discover fall back to the API poll on that call, so a slow-starting or
// disabled devices collector never silently starves node-metrics discovery of
// targets — see collectDevices.
func withDeviceCache(cache deviceCacheReader) nodeDiscovererOption {
	return func(d *nodeDiscoverer) { d.cache = cache }
}

// nodeDiscoverer turns the Tailscale device inventory into node-metrics scrape
// targets, applying the configured online/external/tag filters and the
// metrics-endpoint shape (scheme/port/path). It satisfies nodemetrics.Discoverer.
type nodeDiscoverer struct {
	api   nodeDiscoveryAPI
	cache deviceCacheReader // nil unless withDeviceCache is passed (#85)
	cfg   config.NodeMetricsDiscovery
	log   *slog.Logger
}

var _ nodemetrics.Discoverer = (*nodeDiscoverer)(nil)

func newNodeDiscoverer(api nodeDiscoveryAPI, cfg config.NodeMetricsDiscovery, log *slog.Logger, opts ...nodeDiscovererOption) *nodeDiscoverer {
	if log == nil {
		log = slog.Default()
	}
	d := &nodeDiscoverer{api: api, cfg: cfg, log: log}
	for _, o := range opts {
		o(d)
	}
	return d
}

// discoveryDevice is the minimal per-device view the match/toTarget pipeline
// needs, populated from either tsapi.RichDevice (API-poll path) or
// enrich.DeviceMeta (shared-cache path, #85) so the rest of Discover doesn't
// care which source produced it.
type discoveryDevice struct {
	id                 string
	name               string
	hostname           string
	tags               []string
	addresses          []string
	connectedToControl bool
	isExternal         bool
}

func discoveryDeviceFromRich(d *tsapi.RichDevice) discoveryDevice {
	return discoveryDevice{
		id:                 d.ID,
		name:               d.Name,
		hostname:           d.Hostname,
		tags:               d.Tags,
		addresses:          d.Addresses,
		connectedToControl: d.ConnectedToControl,
		isExternal:         d.IsExternal,
	}
}

func discoveryDeviceFromMeta(m *enrich.DeviceMeta) discoveryDevice {
	addrs := make([]string, len(m.Addrs))
	for i, a := range m.Addrs {
		addrs[i] = a.String()
	}
	return discoveryDevice{
		id:                 m.ID,
		name:               m.Name,
		hostname:           m.Hostname,
		tags:               m.Tags,
		addresses:          addrs,
		connectedToControl: m.Online,
		isExternal:         m.External,
	}
}

// collectDevices returns the current device view for Discover, preferring the
// shared devices-collector cache over another DevicesRich() poll (#85). It
// falls back to the API when no cache was configured (withDeviceCache not
// passed) or the cache is currently empty — a transiently or permanently
// empty cache (devices collector disabled, or enabled but not yet past its
// first tick) must never silently starve discovery of targets.
func (d *nodeDiscoverer) collectDevices(ctx context.Context) ([]discoveryDevice, error) {
	if d.cache != nil {
		if snap := d.cache.Snapshot(); len(snap) > 0 {
			out := make([]discoveryDevice, len(snap))
			for i := range snap {
				out[i] = discoveryDeviceFromMeta(&snap[i])
			}
			return out, nil
		}
	}
	devs, err := d.api.DevicesRich(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]discoveryDevice, len(devs))
	for i := range devs {
		out[i] = discoveryDeviceFromRich(&devs[i])
	}
	return out, nil
}

// Discover lists the tailnet devices and converts the matching ones into scrape
// targets (one per device, using the configured scheme/port/path). Reachability
// is NOT pre-checked: a node the scraper cannot reach simply reports
// tailscale.node.up=0 at scrape time, so no ACL/grant evaluation is needed.
func (d *nodeDiscoverer) Discover(ctx context.Context) ([]nodemetrics.Target, error) {
	devs, err := d.collectDevices(ctx)
	if err != nil {
		return nil, err
	}
	capHint := len(devs)
	if d.cfg.MaxTargets > 0 && capHint > d.cfg.MaxTargets {
		capHint = d.cfg.MaxTargets
	}
	out := make([]nodemetrics.Target, 0, capHint)
	for i := range devs {
		dev := &devs[i]
		if !d.match(dev) {
			continue
		}
		addr, ok := pickAddress(dev.addresses, d.cfg.AddressOrder)
		if !ok {
			continue // no usable Tailscale address
		}
		out = append(out, d.toTarget(dev, addr))
		if d.cfg.MaxTargets > 0 && len(out) >= d.cfg.MaxTargets {
			break
		}
	}
	d.disambiguateInstances(out)
	return out, nil
}

// disambiguateInstances guarantees every non-empty instance label is unique
// across the discovered set. Non-unique sources (instance_source: hostname — many
// devices report the same OS hostname, classically "localhost") would otherwise
// collapse those devices onto a single tailscale.node label, silently merging
// their metrics and colliding the scraper's per-series delta tracking. Colliding
// labels are suffixed with the node address so each device stays distinct, and a
// WARN is logged so the operator can switch to instance_source: name (MagicDNS,
// unique) or address. The "address" source uses an empty instance (the collector
// derives a unique host:port from the URL) and so never collides here.
func (d *nodeDiscoverer) disambiguateInstances(targets []nodemetrics.Target) {
	// instance_source: name/address are unique by construction. Only "hostname" can
	// collide, so scope disambiguation to it. The OLD logic suffixed only labels that
	// collided WITHIN THE CURRENT batch, so a device's tailscale.node label flapped
	// between bare and address-suffixed as its colliding sibling went on/offline
	// between discovery refreshes (resetting the scraper's per-series delta baseline
	// each flip), and it never disambiguated against static targets (#98). For the
	// non-unique source we now UNCONDITIONALLY suffix every instance with its node
	// address: the label becomes a pure function of the device itself — stable across
	// batches AND distinct from any static/bare instance — instead of batch-relative.
	if d.cfg.InstanceSource != "hostname" {
		return
	}
	counts := make(map[string]int, len(targets))
	for i := range targets {
		if inst := targets[i].Instance; inst != "" {
			counts[inst]++
		}
	}
	for i := range targets {
		inst := targets[i].Instance
		if inst == "" {
			continue
		}
		if u, err := url.Parse(targets[i].URL); err == nil && u.Hostname() != "" {
			targets[i].Instance = inst + "@" + u.Hostname()
		}
	}
	// Still WARN when the batch actually had duplicate hostnames, so an operator
	// knows why labels carry the @address suffix and can switch to instance_source: name.
	for inst, n := range counts {
		if n > 1 {
			d.log.Warn("node-metrics discovery: non-unique instance label (hostname source); "+
				"labels are address-suffixed for uniqueness — prefer instance_source: name (MagicDNS)",
				"instance", inst, "devices", n, "instance_source", d.cfg.InstanceSource)
		}
	}
}

// match reports whether a device passes the online/external/tag filters.
// ExcludeTags wins over IncludeTags; an empty IncludeTags matches every device.
func (d *nodeDiscoverer) match(dev *discoveryDevice) bool {
	if d.cfg.OnlineOnly && !dev.connectedToControl {
		return false
	}
	if d.cfg.ExcludeExternal && dev.isExternal {
		return false
	}
	for _, ex := range d.cfg.ExcludeTags {
		if slices.Contains(dev.tags, ex) {
			return false
		}
	}
	if len(d.cfg.IncludeTags) > 0 {
		matched := false
		for _, in := range d.cfg.IncludeTags {
			if slices.Contains(dev.tags, in) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// pickAddress chooses a device address, preferring the configured family
// ("ipv6" else "ipv4") and falling back to the other. It skips addresses
// outside Tailscale's ranges (CGNAT 100.64.0.0/10 or ULA fd7a:115c:a1e0::/48)
// so that a control-plane compromise or API quirk cannot turn the scraper into
// an SSRF client against metadata endpoints, loopback admin ports, or RFC1918
// services. It returns ok=false when the device has no usable Tailscale address.
func pickAddress(addrs []string, order string) (netip.Addr, bool) {
	var v4, v6 netip.Addr
	var hasV4, hasV6 bool
	for _, s := range addrs {
		a, err := netip.ParseAddr(s)
		if err != nil || !enrich.IsTailscaleAddr(a) {
			// Only Tailscale-range addresses may become scrape targets: a non-CGNAT/
			// non-ULA value here (control-plane compromise, API quirk) must not turn
			// the scraper into an SSRF client against metadata/loopback services.
			continue
		}
		if a.Is4() {
			if !hasV4 {
				v4, hasV4 = a, true
			}
		} else if !hasV6 {
			v6, hasV6 = a, true
		}
	}
	preferV6 := order == "ipv6"
	switch {
	case preferV6 && hasV6:
		return v6, true
	case preferV6 && hasV4:
		return v4, true
	case !preferV6 && hasV4:
		return v4, true
	case !preferV6 && hasV6:
		return v6, true
	}
	return netip.Addr{}, false
}

// toTarget builds the scrape Target for one device at the chosen address.
func (d *nodeDiscoverer) toTarget(dev *discoveryDevice, addr netip.Addr) nodemetrics.Target {
	u := url.URL{
		Scheme: d.cfg.Scheme,
		Host:   net.JoinHostPort(addr.String(), strconv.Itoa(d.cfg.Port)), // JoinHostPort brackets IPv6
		Path:   d.cfg.Path,
	}
	t := nodemetrics.Target{URL: u.String()}

	switch d.cfg.InstanceSource {
	case "name":
		t.Instance = magicDNSShort(dev.name)
	case "hostname":
		t.Instance = dev.hostname
	default: // "address": leave empty so the collector derives host:port from the URL
	}

	wantTags := d.cfg.IncludeTagsLabel && len(dev.tags) > 0
	if d.cfg.IncludeHostLabels || wantTags {
		t.Labels = make(map[string]string, 3)
		if d.cfg.IncludeHostLabels {
			t.Labels[semconv.HostName] = dev.hostname
			t.Labels[semconv.HostID] = dev.id
		}
		if wantTags {
			t.Labels[semconv.AttrTags] = strings.Join(dev.tags, ",")
		}
	}
	return t
}

// magicDNSShort returns the first DNS label of a MagicDNS name, e.g.
// "laptop.example.ts.net" -> "laptop" — the friendly short identity, which is
// still unique within a tailnet (Tailscale dedupes device names). It returns the
// input unchanged when there is no dot (already short, or empty).
func magicDNSShort(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}
