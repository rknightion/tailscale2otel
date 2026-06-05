// Package services is a snapshot collector for the tailnet's Tailscale Services
// (VIP services). It emits the service count plus, per service, the number of
// exposed port rules and (optionally) the backing-host count bucketed by
// approval and configuration state. Service addresses, comments and annotations
// are never fetched (see internal/tsapi) and so cannot be emitted.
package services

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/tsapi"
)

// Compile-time assertions.
var (
	_ collector.SnapshotCollector = (*Collector)(nil)
	_ api                         = (*tsapi.Client)(nil)
)

const defaultInterval = 600 * time.Second

const (
	metricCount = "tailscale.services.count"
	metricPorts = "tailscale.service.ports"
	metricHosts = "tailscale.service.hosts"
)

const (
	attrName       = "tailscale.service.name"
	attrApproval   = "tailscale.service.approval"
	attrConfigured = "tailscale.service.configured"
)

// api is the narrow slice of the Tailscale client this collector needs.
type api interface {
	Services(ctx context.Context) ([]tsapi.VIPService, error)
	ServiceHosts(ctx context.Context, name string) ([]tsapi.ServiceHost, error)
}

// Collector implements collector.SnapshotCollector for Tailscale Services.
type Collector struct {
	api          api
	interval     time.Duration
	perEntity    bool
	collectHosts bool
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-service gauges (ports, hosts) are
// emitted (default true); false (cardinality.service_per_entity) keeps only the
// aggregate services.count.
func WithPerEntity(enabled bool) Option { return func(c *Collector) { c.perEntity = enabled } }

// WithCollectHosts enables per-service backing-host detail, which makes one
// extra API call per service (N+1). Off by default.
func WithCollectHosts(enabled bool) Option { return func(c *Collector) { c.collectHosts = enabled } }

// New returns a services collector. A non-positive interval resolves to 600s.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: a, interval: interval, perEntity: true}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "services" }

// DefaultInterval returns the configured interval, or 600s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect lists Tailscale Services and emits the count plus (per-entity) the
// per-service port count and, when collect_hosts is on, backing-host buckets.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	svcs, err := c.api.Services(ctx)
	if err != nil {
		return err
	}
	e.Gauge(docCount.Name, docCount.Unit, docCount.Description, float64(len(svcs)), nil)

	if !c.perEntity {
		return nil
	}
	for i := range svcs {
		s := &svcs[i]
		e.Gauge(docPorts.Name, docPorts.Unit, docPorts.Description,
			float64(len(s.Ports)), telemetry.Attrs{attrName: s.Name})
		if c.collectHosts {
			c.emitHosts(ctx, e, s.Name)
		}
	}
	return nil
}

// emitHosts fetches and emits the backing-host counts for one service, bucketed
// by approval + configured state. A per-service host-call failure is non-fatal
// (the service's host series is skipped; collection continues).
func (c *Collector) emitHosts(ctx context.Context, e telemetry.Emitter, name string) {
	hosts, err := c.api.ServiceHosts(ctx, name)
	if err != nil {
		return
	}
	type bucket struct{ approval, configured string }
	counts := make(map[bucket]int, len(hosts))
	for _, h := range hosts {
		counts[bucket{h.ApprovalLevel, h.Configured}]++
	}
	for b, n := range counts {
		e.Gauge(docHosts.Name, docHosts.Unit, docHosts.Description, float64(n), telemetry.Attrs{
			attrName:       name,
			attrApproval:   b.approval,
			attrConfigured: b.configured,
		})
	}
}
