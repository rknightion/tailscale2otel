// Package dns is a snapshot collector for the tailnet DNS configuration. It
// emits counts of global nameservers, search paths, and split-DNS zones, plus
// the MagicDNS flag.
package dns

import (
	"context"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

const defaultInterval = 600 * time.Second

// Metric names emitted by this collector.
const (
	metricNameserversCount = "tailscale.dns.nameservers.count"
	metricSearchPathsCount = "tailscale.dns.search_paths.count"
	metricSplitZonesCount  = "tailscale.dns.split_zones.count"
	metricMagicDNS         = "tailscale.dns.magic_dns"
)

// api is the narrow slice of the Tailscale client this collector needs. It is
// satisfied by *tsapi.Client.
type api interface {
	DNSNameservers(ctx context.Context) ([]string, error)
	DNSSearchPaths(ctx context.Context) ([]string, error)
	DNSSplitDNS(ctx context.Context) (tsclient.SplitDNSResponse, error)
	DNSPreferences(ctx context.Context) (*tsclient.DNSPreferences, error)
}

// Collector implements collector.SnapshotCollector for DNS configuration.
type Collector struct {
	api      api
	interval time.Duration
}

// New returns a DNS collector. A non-positive interval resolves to the default
// (600s) via DefaultInterval.
func New(a api, interval time.Duration) *Collector {
	return &Collector{api: a, interval: interval}
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

// Collect fetches DNS configuration and emits nameserver/search-path/split-zone
// counts and the MagicDNS flag.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	nameservers, err := c.api.DNSNameservers(ctx)
	if err != nil {
		return err
	}
	searchPaths, err := c.api.DNSSearchPaths(ctx)
	if err != nil {
		return err
	}
	split, err := c.api.DNSSplitDNS(ctx)
	if err != nil {
		return err
	}
	prefs, err := c.api.DNSPreferences(ctx)
	if err != nil {
		return err
	}

	e.Gauge(metricNameserversCount, semconv.UnitDimensionless, "number of global DNS nameservers",
		float64(len(nameservers)), nil)
	e.Gauge(metricSearchPathsCount, semconv.UnitDimensionless, "number of DNS search paths",
		float64(len(searchPaths)), nil)
	e.Gauge(metricSplitZonesCount, semconv.UnitDimensionless, "number of split-DNS zones",
		float64(len(split)), nil)
	e.Gauge(metricMagicDNS, semconv.UnitDimensionless, "MagicDNS enabled (1) or disabled (0)",
		boolValue(prefs.MagicDNS), nil)

	return nil
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
