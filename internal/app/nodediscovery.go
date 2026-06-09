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

// nodeDiscoverer turns the Tailscale device inventory into node-metrics scrape
// targets, applying the configured online/external/tag filters and the
// metrics-endpoint shape (scheme/port/path). It satisfies nodemetrics.Discoverer.
type nodeDiscoverer struct {
	api nodeDiscoveryAPI
	cfg config.NodeMetricsDiscovery
	log *slog.Logger
}

var _ nodemetrics.Discoverer = (*nodeDiscoverer)(nil)

func newNodeDiscoverer(api nodeDiscoveryAPI, cfg config.NodeMetricsDiscovery, log *slog.Logger) *nodeDiscoverer {
	if log == nil {
		log = slog.Default()
	}
	return &nodeDiscoverer{api: api, cfg: cfg, log: log}
}

// Discover lists the tailnet devices and converts the matching ones into scrape
// targets (one per device, using the configured scheme/port/path). Reachability
// is NOT pre-checked: a node the scraper cannot reach simply reports
// tailscale.node.up=0 at scrape time, so no ACL/grant evaluation is needed.
func (d *nodeDiscoverer) Discover(ctx context.Context) ([]nodemetrics.Target, error) {
	devs, err := d.api.DevicesRich(ctx)
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
		addr, ok := pickAddress(dev.Addresses, d.cfg.AddressOrder)
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
	counts := make(map[string]int, len(targets))
	for i := range targets {
		if inst := targets[i].Instance; inst != "" {
			counts[inst]++
		}
	}
	for inst, n := range counts {
		if n > 1 {
			d.log.Warn("node-metrics discovery: non-unique instance label; disambiguating by address",
				"instance", inst, "devices", n, "instance_source", d.cfg.InstanceSource)
		}
	}
	for i := range targets {
		inst := targets[i].Instance
		if inst == "" || counts[inst] < 2 {
			continue
		}
		if u, err := url.Parse(targets[i].URL); err == nil && u.Hostname() != "" {
			targets[i].Instance = inst + "@" + u.Hostname()
		}
	}
}

// match reports whether a device passes the online/external/tag filters.
// ExcludeTags wins over IncludeTags; an empty IncludeTags matches every device.
func (d *nodeDiscoverer) match(dev *tsapi.RichDevice) bool {
	if d.cfg.OnlineOnly && !dev.ConnectedToControl {
		return false
	}
	if d.cfg.ExcludeExternal && dev.IsExternal {
		return false
	}
	for _, ex := range d.cfg.ExcludeTags {
		if slices.Contains(dev.Tags, ex) {
			return false
		}
	}
	if len(d.cfg.IncludeTags) > 0 {
		matched := false
		for _, in := range d.cfg.IncludeTags {
			if slices.Contains(dev.Tags, in) {
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
func (d *nodeDiscoverer) toTarget(dev *tsapi.RichDevice, addr netip.Addr) nodemetrics.Target {
	u := url.URL{
		Scheme: d.cfg.Scheme,
		Host:   net.JoinHostPort(addr.String(), strconv.Itoa(d.cfg.Port)), // JoinHostPort brackets IPv6
		Path:   d.cfg.Path,
	}
	t := nodemetrics.Target{URL: u.String()}

	switch d.cfg.InstanceSource {
	case "name":
		t.Instance = magicDNSShort(dev.Name)
	case "hostname":
		t.Instance = dev.Hostname
	default: // "address": leave empty so the collector derives host:port from the URL
	}

	wantTags := d.cfg.IncludeTagsLabel && len(dev.Tags) > 0
	if d.cfg.IncludeHostLabels || wantTags {
		t.Labels = make(map[string]string, 3)
		if d.cfg.IncludeHostLabels {
			t.Labels[semconv.HostName] = dev.Hostname
			t.Labels[semconv.HostID] = dev.ID
		}
		if wantTags {
			t.Labels[semconv.AttrTags] = strings.Join(dev.Tags, ",")
		}
	}
	return t
}

// magicDNSShort returns the first DNS label of a MagicDNS name, e.g.
// "jules.saga-turtle.ts.net" -> "jules" — the friendly short identity, which is
// still unique within a tailnet (Tailscale dedupes device names). It returns the
// input unchanged when there is no dot (already short, or empty).
func magicDNSShort(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}
