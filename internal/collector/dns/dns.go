// Package dns is a snapshot collector for the tailnet DNS configuration. It
// reads the unified GET /dns/configuration in one call and emits counts of
// global nameservers, search paths, and split-DNS zones; the MagicDNS and
// override-local flags; a count of exit-node-eligible resolvers; and a
// per-resolver info gauge labeled by address/kind/domain/use_with_exit_node.
package dns

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

const defaultInterval = 600 * time.Second

// Metric names emitted by this collector.
const (
	metricNameserversCount = "tailscale.dns.nameservers.count"
	metricSearchPathsCount = "tailscale.dns.search_paths.count"
	metricSplitZonesCount  = "tailscale.dns.split_zones.count"
	metricMagicDNS         = "tailscale.dns.magic_dns"
	metricOverrideLocal    = "tailscale.dns.override_local"
	metricUseWithExitNode  = "tailscale.dns.resolvers.use_with_exit_node"
	metricResolver         = "tailscale.dns.resolver"
	metricSearchPath       = "tailscale.dns.search_path"
)

// Attribute keys for the per-resolver info gauge. Package-local (mirrors the
// keys collector's attr constants); address/domain are intentionally emitted
// (DNS PII fence relaxed for this exporter, per A3).
const (
	attrAddress         = "tailscale.dns.resolver.address"
	attrKind            = "tailscale.dns.resolver.kind"
	attrDomain          = "tailscale.dns.resolver.domain"
	attrUseWithExitNode = "tailscale.dns.resolver.use_with_exit_node"

	// attrSearchPathDomain is the identity attribute for the per-search-path info
	// gauge. NOTE: this key must be registered in internal/telemetry/pii/registry.go
	// under CatNetworkTopology and added to identityKeys (the wiring pass handles this).
	attrSearchPathDomain = "tailscale.dns.search_path.domain"

	resolverKindGlobal = "global"
	resolverKindSplit  = "split"
)

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	DNSConfiguration(ctx context.Context) (*tsapi.DNSConfig, error)
}

// Collector implements collector.SnapshotCollector for DNS configuration.
type Collector struct {
	api      api
	interval time.Duration
	// gsb accumulates the churning per-resolver / per-search-path info gauges
	// each tick and flushes them via an observable gauge, so a resolver or
	// search path that goes away drops its series instead of ghosting (#55). It
	// persists across Collect calls for the collector's lifetime.
	gsb *telemetry.GaugeSnapshotBuilder
}

// New returns a DNS collector. A non-positive interval resolves to the default
// (600s) via DefaultInterval.
func New(a api, interval time.Duration) *Collector {
	return &Collector{api: a, interval: interval, gsb: telemetry.NewGaugeSnapshotBuilder()}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "dns" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect fetches the unified DNS configuration in one call and emits the
// nameserver/search-path/split-zone counts, the MagicDNS and override-local
// flags, the count of exit-node-eligible resolvers, and a per-resolver info
// gauge labeled by address/kind/domain/use_with_exit_node.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	cfg, err := c.api.DNSConfiguration(ctx)
	if err != nil {
		return err
	}

	// Aggregate counts — identical values to the four former split GETs.
	e.Gauge(docNameserversCount.Name, docNameserversCount.Unit, docNameserversCount.Description,
		float64(len(cfg.Nameservers)), nil)
	e.Gauge(docSearchPathsCount.Name, docSearchPathsCount.Unit, docSearchPathsCount.Description,
		float64(len(cfg.SearchPaths)), nil)
	e.Gauge(docSplitZonesCount.Name, docSplitZonesCount.Unit, docSplitZonesCount.Description,
		float64(len(cfg.SplitDNS)), nil)
	e.Gauge(docMagicDNS.Name, docMagicDNS.Unit, docMagicDNS.Description,
		boolValue(cfg.MagicDNS), nil)
	e.Gauge(docOverrideLocal.Name, docOverrideLocal.Unit, docOverrideLocal.Description,
		boolValue(cfg.OverrideLocalDNS), nil)

	// Per-resolver info gauge + exit-node-eligible count (global + split).
	exitCount := 0
	for _, r := range cfg.Nameservers {
		if r.UseWithExitNode {
			exitCount++
		}
		c.gsb.Add(docResolver.Name, docResolver.Unit, docResolver.Description, 1, telemetry.Attrs{
			attrAddress:         r.Address,
			attrKind:            resolverKindGlobal,
			attrDomain:          "",
			attrUseWithExitNode: boolString(r.UseWithExitNode),
		})
	}
	for domain, resolvers := range cfg.SplitDNS {
		if len(resolvers) == 0 {
			// #63: a split-DNS domain with a null/empty resolver list (a
			// legitimate Tailscale feature for excluding a subdomain from a
			// broader override) is still counted in split_zones.count above,
			// but the loop below never runs for it — leaving no series to
			// identify which counted domain has no resolvers. Emit a single
			// point with an empty address so it stays identifiable; the
			// address="" + a non-empty split domain combination cannot occur
			// for any real resolver (a global resolver always has domain=""
			// instead), so this synthetic point is unambiguous.
			c.gsb.Add(docResolver.Name, docResolver.Unit, docResolver.Description, 1, telemetry.Attrs{
				attrAddress:         "",
				attrKind:            resolverKindSplit,
				attrDomain:          domain,
				attrUseWithExitNode: boolString(false),
			})
			continue
		}
		for _, r := range resolvers {
			if r.UseWithExitNode {
				exitCount++
			}
			c.gsb.Add(docResolver.Name, docResolver.Unit, docResolver.Description, 1, telemetry.Attrs{
				attrAddress:         r.Address,
				attrKind:            resolverKindSplit,
				attrDomain:          domain,
				attrUseWithExitNode: boolString(r.UseWithExitNode),
			})
		}
	}
	e.Gauge(docUseWithExitNode.Name, docUseWithExitNode.Unit, docUseWithExitNode.Description,
		float64(exitCount), nil)

	// Per-search-path info gauge: one datapoint per domain, value always 1.
	for _, sp := range cfg.SearchPaths {
		c.gsb.Add(docSearchPath.Name, docSearchPath.Unit, docSearchPath.Description, 1, telemetry.Attrs{
			attrSearchPathDomain: sp,
		})
	}

	// Flush the churning info gauges via observable snapshots so resolvers /
	// search paths that departed since the last tick drop out instead of
	// ghosting (#55). Only reached on the success path (an API error returned
	// above, before any Add).
	c.gsb.Flush(e)

	return nil
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
