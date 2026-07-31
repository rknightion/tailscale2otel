// Package services is a snapshot collector for the tailnet's Tailscale Services
// (VIP services). It emits the service count plus, per service, the number of
// exposed port rules and (optionally) the backing-host count bucketed by
// approval and configuration state. Service addresses, comments and annotations
// are never fetched (see internal/tsapi) and so cannot be emitted.
package services

import (
	"context"
	"net/netip"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
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

// opListServices is the upstream OpenAPI operationId (spec/tailscale-api.json)
// of GET /api/v2/tailnet/{tailnet}/services. Both Services() and ServiceAddrs()
// hit this exact same endpoint (see internal/tsapi/services.go: both build
// their URL via servicesURL()) — ServiceAddrs merely decodes an extra field off
// the same response shape — so they share this one operation name.
const opListServices = "listServices"

// opListServiceHosts is the upstream OpenAPI operationId of the per-service
// backing-host list call, GET /api/v2/tailnet/{tailnet}/services/{serviceName}/devices.
const opListServiceHosts = "listServiceHosts"

// servicesDisposition is the DEFAULT disposition (#420/#524) for both
// operations this collector observes: an ambiguous 403 stays scope_denied.
// Neither the tailnet services list nor the per-service hosts endpoint is
// documented upstream as 403-gated (unlike flowlogs, the one operation in this
// repo that earns a DisabledOn opt-out), so a 403 here means the credential is
// missing the services scope, not that the feature is off.
var servicesDisposition = apistate.Disposition{}

// api is the narrow slice of the Tailscale client this collector needs.
type api interface {
	Services(ctx context.Context) ([]tsapi.VIPService, error)
	ServiceHosts(ctx context.Context, name string) ([]tsapi.ServiceHost, error)
	ServiceAddrs(ctx context.Context) ([]tsapi.ServiceAddr, error)
}

// Collector implements collector.SnapshotCollector for Tailscale Services.
type Collector struct {
	api          api
	interval     time.Duration
	perEntity    bool
	collectHosts bool
	cache        *enrich.DeviceCache
	// tracker records this collector's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithPerEntity controls whether the per-service gauges (ports, hosts) are
// emitted (default true); false (cardinality.per_entity.service) keeps only the
// aggregate services.count.
func WithPerEntity(enabled bool) Option { return func(c *Collector) { c.perEntity = enabled } }

// WithCollectHosts enables per-service backing-host detail, which makes one
// extra API call per service (N+1). Off by default.
func WithCollectHosts(enabled bool) Option { return func(c *Collector) { c.collectHosts = enabled } }

// WithEnrichCache supplies the shared enrich.DeviceCache so flow-log peers
// destined for a Service VIP resolve to the service name instead of falling
// through to "unknown" (#166). Off (nil, the default) when not supplied. When
// set, every Collect fetches each service's backing addresses via the
// carve-out tsapi.ServiceAddrs and repopulates the cache's service-VIP map —
// unconditionally, independent of WithPerEntity/WithCollectHosts, since it's a
// second call already shaped like Services() and cheap relative to the
// inventory metrics above it.
func WithEnrichCache(cache *enrich.DeviceCache) Option {
	return func(c *Collector) { c.cache = cache }
}

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads. A nil tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option { return func(c *Collector) { c.tracker = t } }

// WithClock overrides the collector's clock (for deterministic availability-
// probe tests); the default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) { c.now = now }
}

// New returns a services collector. A non-positive interval resolves to 600s.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{api: a, interval: interval, perEntity: true, now: time.Now}
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
//
// Two of this collector's calls resolve to ONE upstream operation, listServices
// (Services() and ServiceAddrs() both hit GET .../services — see
// opListServices above), and the availability tracker holds one entry per
// (collector, operation): observing it twice in one tick would have the second
// call silently overwrite the first's gauge value. So listServices is observed
// EXACTLY ONCE per tick — immediately once every call that actually runs this
// tick has returned, using the first non-nil error among them (#524).
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	svcs, err := c.api.Services(ctx)
	if err != nil {
		// ServiceAddrs is never attempted this tick (we return below), so it
		// cannot contribute an error — Services()'s own error is representative.
		apistate.Observe(e, c.tracker, c.Name(), opListServices, servicesDisposition, err, c.now())
		return err
	}

	var addrsErr error
	if c.cache != nil {
		addrsErr = c.populateEnrichCache(ctx)
	}
	apistate.Observe(e, c.tracker, c.Name(), opListServices, servicesDisposition, addrsErr, c.now())

	e.Gauge(docCount.Name, docCount.Unit, docCount.Description, float64(len(svcs)), nil)

	if !c.perEntity {
		return nil
	}

	// listServiceHosts is a distinct, N+1 per-service operation. It is only
	// "attempted" (and therefore only observed) when at least one
	// ServiceHosts call actually happens this tick — collect_hosts off, or no
	// services to iterate, must leave it unknown rather than claiming a state.
	var (
		hostsAttempted bool
		hostsErr       error
	)
	for i := range svcs {
		s := &svcs[i]
		e.Gauge(docPorts.Name, docPorts.Unit, docPorts.Description,
			float64(len(s.Ports)), telemetry.Attrs{attrName: s.Name})
		if c.collectHosts {
			hostsAttempted = true
			// First-error-wins: these failures are systemic (a scope or
			// credential problem), not worth ranking against each other.
			if hErr := c.emitHosts(ctx, e, s.Name); hErr != nil && hostsErr == nil {
				hostsErr = hErr
			}
		}
	}
	if hostsAttempted {
		apistate.Observe(e, c.tracker, c.Name(), opListServiceHosts, servicesDisposition, hostsErr, c.now())
	}
	return nil
}

// populateEnrichCache fetches each service's backing addresses via the
// carve-out tsapi.ServiceAddrs and repopulates the shared enrich cache's
// service-VIP map, so flow-log peers destined for a Service resolve to the
// service name (#166). A fetch failure is non-fatal: the inventory metrics
// emitted above are unaffected, and the cache's previous service map (if any)
// is left in place rather than being cleared. The decoded addresses are used
// ONLY as map keys here — they are never attached to an emitted attribute. The
// error is returned (rather than swallowed) purely so Collect can fold it into
// the shared listServices availability observation; it is never propagated as
// a Collect failure.
func (c *Collector) populateEnrichCache(ctx context.Context) error {
	addrs, err := c.api.ServiceAddrs(ctx)
	if err != nil {
		return err
	}
	byAddr := make(map[netip.Addr]string, len(addrs)*2)
	for _, s := range addrs {
		for _, raw := range s.Addrs {
			a, err := netip.ParseAddr(raw)
			if err != nil {
				continue
			}
			byAddr[a] = s.Name
		}
	}
	c.cache.ReplaceServices(byAddr)
	return nil
}

// emitHosts fetches and emits the backing-host counts for one service, bucketed
// by approval + configured state. A per-service host-call failure is non-fatal
// (the service's host series is skipped; collection continues); the error is
// returned so Collect can aggregate it into the single listServiceHosts
// availability observation for the tick.
func (c *Collector) emitHosts(ctx context.Context, e telemetry.Emitter, name string) error {
	hosts, err := c.api.ServiceHosts(ctx, name)
	if err != nil {
		return err
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
	return nil
}
