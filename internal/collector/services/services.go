// Package services is a snapshot collector for the tailnet's Tailscale Services
// (VIP services). It emits the service count plus, per service, the number of
// exposed port rules and (optionally) the backing-host count bucketed by
// approval and configuration state. Service addresses, comments and annotations
// are never fetched (see internal/tsapi) and so cannot be emitted.
package services

import (
	"context"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// Compile-time assertions.
var (
	_ collector.SnapshotCollector = (*Collector)(nil)
	_ api                         = (*tsapi.Client)(nil)
)

const defaultInterval = 600 * time.Second

const defaultSubrequestConcurrency = 1

const (
	metricCount    = "tailscale.services.count"
	metricByTag    = "tailscale.services.by_tag"
	metricPorts    = "tailscale.service.ports"
	metricHosts    = "tailscale.service.hosts"
	metricHostInfo = "tailscale.service.host.info"
)

const (
	attrName        = "tailscale.service.name"
	attrDisplayName = "tailscale.service.display_name"
	attrTag         = "tailscale.tag"
	attrNodeID      = semconv.AttrNodeID
	attrApproval    = "tailscale.service.approval"
	attrConfigured  = "tailscale.service.configured"
)

const (
	// tagOther is the bounded overflow value shared with the devices tag rollup.
	tagOther              = "__other__"
	defaultTagRollupLimit = 50
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
	api                   api
	interval              time.Duration
	perEntity             bool
	collectHosts          bool
	collectTags           bool
	tagLimit              int
	subrequestConcurrency int
	cache                 *enrich.DeviceCache
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

// WithSubrequestConcurrency bounds the number of backing-host requests that
// may be in flight at once. One is deliberately the default to preserve the
// historical sequential request pattern and avoid unexpectedly increasing API
// pressure for existing large-fleet deployments. Non-positive values retain
// that safe sequential default.
func WithSubrequestConcurrency(n int) Option {
	return func(c *Collector) {
		if n <= 0 {
			c.subrequestConcurrency = defaultSubrequestConcurrency
			return
		}
		c.subrequestConcurrency = n
	}
}

// WithTagRollup controls the tailscale.services.by_tag distribution gauge.
// enabled gates the rollup; limit caps the distinct tag series (the busiest
// services keep their own series and the rest fold into tagOther). A limit <= 0
// means unlimited, matching the devices collector's tag_rollup_limit behavior.
func WithTagRollup(enabled bool, limit int) Option {
	return func(c *Collector) {
		c.collectTags = enabled
		c.tagLimit = limit
	}
}

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
	c := &Collector{
		api:                   a,
		interval:              interval,
		perEntity:             true,
		collectTags:           true,
		tagLimit:              defaultTagRollupLimit,
		subrequestConcurrency: defaultSubrequestConcurrency,
		now:                   time.Now,
	}
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

type hostResult struct {
	hosts     []tsapi.ServiceHost
	err       error
	attempted bool
}

// fetchHosts runs the N+1 backing-host requests through a bounded worker pool.
// Results stay indexed by service so Collect can emit telemetry serially and
// preserve deterministic first-error behavior. complete is false when
// cancellation stops dispatch before every service has been assigned to a
// worker; in that case results are only a partial view and must not replace a
// complete host snapshot.
func (c *Collector) fetchHosts(ctx context.Context, svcs []tsapi.VIPService) ([]hostResult, bool) {
	if len(svcs) == 0 {
		return nil, true
	}
	workers := c.subrequestConcurrency
	if workers <= 0 {
		workers = defaultSubrequestConcurrency
	}
	if workers > len(svcs) {
		workers = len(svcs)
	}
	results := make([]hostResult, len(svcs))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i].attempted = true
				results[i].hosts, results[i].err = c.api.ServiceHosts(ctx, svcs[i].Name)
			}
		}()
	}

	for i := range svcs {
		if err := ctx.Err(); err != nil {
			close(jobs)
			wg.Wait()
			return results, false
		}
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, false
		}
	}
	close(jobs)
	wg.Wait()
	return results, true
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
	c.emitTagRollup(e, svcs)

	if !c.perEntity {
		return nil
	}

	// listServiceHosts is a distinct, N+1 per-service operation. It is only
	// "attempted" (and therefore only observed) when at least one
	// ServiceHosts call actually happens this tick — collect_hosts off, or no
	// services to iterate, must leave it unknown rather than claiming a state.
	var (
		hostsAttempted bool
		hostsComplete  bool
		hostsErr       error
		hostInfoPoints []telemetry.GaugePoint
	)
	var hostResults []hostResult
	if c.collectHosts {
		hostResults, hostsComplete = c.fetchHosts(ctx, svcs)
	}
	for i := range svcs {
		s := &svcs[i]
		e.Gauge(docPorts.Name, docPorts.Unit, docPorts.Description,
			float64(len(s.Ports)), serviceAttrs(*s))
		if c.collectHosts {
			result := hostResults[i]
			if result.attempted {
				hostsAttempted = true
				// First-error-wins in service order: these failures are systemic
				// (a scope or credential problem), not worth ranking against each
				// other.
				if result.err != nil {
					if hostsErr == nil {
						hostsErr = result.err
					}
				} else {
					c.emitHosts(e, *s, result.hosts, &hostInfoPoints)
				}
			}
		}
	}
	if c.collectHosts && hostsComplete && hostsErr == nil {
		e.GaugeSnapshot(docHostInfo.Name, docHostInfo.Unit, docHostInfo.Description, hostInfoPoints)
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

// emitHosts emits the backing-host counts for one service, bucketed by approval
// + configured state, and appends host identity points. Fetching happens in the
// bounded pool above; this method only mutates the emitter-owned state on the
// collector goroutine.
func (c *Collector) emitHosts(e telemetry.Emitter, service tsapi.VIPService, hosts []tsapi.ServiceHost, hostInfoPoints *[]telemetry.GaugePoint) {
	type bucket struct{ approval, configured string }
	counts := make(map[bucket]int, len(hosts))
	for _, h := range hosts {
		counts[bucket{h.ApprovalLevel, h.Configured}]++
	}
	for b, n := range counts {
		attrs := serviceAttrs(service)
		attrs[attrApproval] = b.approval
		attrs[attrConfigured] = b.configured
		e.Gauge(docHosts.Name, docHosts.Unit, docHosts.Description, float64(n), attrs)
	}
	for _, h := range hosts {
		*hostInfoPoints = append(*hostInfoPoints, c.hostInfoPoint(service, h))
	}
}

// emitTagRollup emits one bounded series per service tag. A service carrying
// multiple tags contributes once to each tag, just as a tagged device does in
// the devices rollup. The overflow bucket preserves the total number of
// service/tag memberships without allowing operator-authored tags to grow
// metric cardinality without bound.
func (c *Collector) emitTagRollup(e telemetry.Emitter, svcs []tsapi.VIPService) {
	if !c.collectTags {
		return
	}
	byTag := make(map[string]int)
	for i := range svcs {
		for _, tag := range svcs[i].Tags {
			if tag == "" {
				continue
			}
			byTag[tag]++
		}
	}
	if len(byTag) == 0 {
		return
	}
	emit := func(tag string, n int) {
		e.Gauge(docByTag.Name, docByTag.Unit, docByTag.Description, float64(n), telemetry.Attrs{attrTag: tag})
	}
	if c.tagLimit <= 0 || len(byTag) <= c.tagLimit {
		for tag, n := range byTag {
			emit(tag, n)
		}
		return
	}
	type tagCount struct {
		tag string
		n   int
	}
	tags := make([]tagCount, 0, len(byTag))
	for tag, n := range byTag {
		tags = append(tags, tagCount{tag: tag, n: n})
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].n != tags[j].n {
			return tags[i].n > tags[j].n
		}
		return tags[i].tag < tags[j].tag
	})
	other := 0
	for i, tc := range tags {
		if i < c.tagLimit {
			emit(tc.tag, tc.n)
		} else {
			other += tc.n
		}
	}
	if other > 0 {
		emit(tagOther, other)
	}
}

// emitHostInfo emits one identity point per service host. The raw NodeID is
// retained as the join key, and the shared authoritative device cache supplies
// the human/device dimensions when the devices collector has refreshed it.
// Missing cache entries are not errors: the service-host API remains useful and
// the node ID still identifies the host for a later join.
func (c *Collector) hostInfoPoint(service tsapi.VIPService, h tsapi.ServiceHost) telemetry.GaugePoint {
	attrs := serviceAttrs(service)
	attrs[attrApproval] = h.ApprovalLevel
	attrs[attrConfigured] = h.Configured
	if h.NodeID != "" {
		attrs[attrNodeID] = h.NodeID
	}
	if c.cache != nil && h.NodeID != "" {
		if d, ok := c.cache.LookupNode(h.NodeID); ok {
			if d.Hostname != "" {
				attrs[semconv.HostName] = d.Hostname
			}
			if d.ID != "" {
				attrs[semconv.HostID] = d.ID
			}
			if d.OS != "" {
				attrs[semconv.OSType] = d.OS
			}
			if d.OSVersion != "" {
				attrs[semconv.OSVersion] = d.OSVersion
			}
			if d.User != "" {
				attrs[semconv.AttrUser] = d.User
			}
			if len(d.Tags) > 0 {
				attrs[semconv.AttrTags] = strings.Join(d.Tags, ",")
			}
		}
	}
	return telemetry.GaugePoint{Value: 1, Attrs: attrs}
}

// serviceAttrs returns the bounded identity carried by a per-service signal.
// displayName is optional in the API and is omitted when absent; the stable
// service name remains the series identity in either case.
func serviceAttrs(s tsapi.VIPService) telemetry.Attrs {
	attrs := telemetry.Attrs{attrName: s.Name}
	if s.DisplayName != "" {
		attrs[attrDisplayName] = s.DisplayName
	}
	return attrs
}
