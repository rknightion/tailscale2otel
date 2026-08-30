// Package dns is a snapshot collector for the tailnet DNS configuration. It
// reads the unified GET /dns/configuration in one call and emits counts of
// global nameservers, search paths, and split-DNS zones; the MagicDNS and
// override-local flags; a count of exit-node-eligible resolvers; and a
// per-resolver info gauge labeled by address/kind/domain/use_with_exit_node.
package dns

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/snapshot"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

const defaultInterval = 600 * time.Second

const (
	defaultSnapshotHeartbeat = 24 * time.Hour
	defaultSnapshotBodyBytes = 32 * 1024
	// EventDNSSnapshot is the opt-in JSON DNS configuration snapshot event.
	EventDNSSnapshot = "tailscale.dns.snapshot"
)

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

// opGetDNSConfiguration is the upstream operationId of the DNS configuration
// fetch call.
const opGetDNSConfiguration = "getDnsConfiguration"

// dnsDisposition is the DEFAULT disposition (#420/#524): 403 stays
// scope_denied. DNS configuration is available on every plan, so upstream has
// no reason to answer 403 as a feature gate here — a 403 on this path means
// the credential is missing the DNS read scope, and reading it as "disabled"
// would hide exactly that.
var dnsDisposition = apistate.Disposition{}

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
	// tracker records this collector's per-operation availability for the admin
	// status page and the capability matrix (#430/#524). A nil tracker is a no-op.
	tracker *apistate.Tracker
	// now is the clock, injectable from tests.
	now func() time.Time

	// snapshot is opt-in because the complete DNS configuration includes
	// resolver addresses and search domains. The emitter is initialized on the
	// first Collect because telemetry.Emitter is scoped to that call.
	snapshotEnabled   bool
	snapshotHeartbeat time.Duration
	snapshotBodyBytes int
	snapshotEmitter   *snapshot.Emitter
}

// Option configures optional Collector behavior.
type Option func(*Collector)

// WithAPIState wires the shared per-operation availability tracker (#420).
// Availability METRICS are emitted regardless; the tracker is the in-process
// introspection copy the admin status page reads. A nil tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option { return func(c *Collector) { c.tracker = t } }

// WithClock overrides the collector's clock (for deterministic last-probe
// timestamp tests); the default is time.Now.
func WithClock(now func() time.Time) Option {
	return func(c *Collector) { c.now = now }
}

// WithSnapshot enables the JSON DNS configuration snapshot. The optional body
// limit should match otlp.limits.log_body_bytes; when omitted or non-positive,
// the telemetry default (32 KiB) is used. The snapshot is emitted on the first
// observation, on content changes, and on a daily heartbeat when unchanged.
func WithSnapshot(enabled bool, maxBodyBytes ...int) Option {
	return func(c *Collector) {
		c.snapshotEnabled = enabled
		if len(maxBodyBytes) > 0 && maxBodyBytes[0] > 0 {
			c.snapshotBodyBytes = maxBodyBytes[0]
		}
	}
}

// WithSnapshotHeartbeat overrides the default daily heartbeat. It exists for
// deterministic tests and leaves the public configuration surface unchanged.
func WithSnapshotHeartbeat(heartbeat time.Duration) Option {
	return func(c *Collector) {
		c.snapshotHeartbeat = heartbeat
	}
}

// New returns a DNS collector. A non-positive interval resolves to the default
// (600s) via DefaultInterval.
func New(a api, interval time.Duration, opts ...Option) *Collector {
	c := &Collector{
		api:               a,
		interval:          interval,
		gsb:               telemetry.NewGaugeSnapshotBuilder(),
		now:               time.Now,
		snapshotHeartbeat: defaultSnapshotHeartbeat,
		snapshotBodyBytes: defaultSnapshotBodyBytes,
	}
	for _, o := range opts {
		o(c)
	}
	return c
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
	apistate.Observe(e, c.tracker, c.Name(), opGetDNSConfiguration, dnsDisposition, err, c.now())
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

	if c.snapshotEnabled {
		body, err := marshalSnapshot(cfg)
		if err != nil {
			return err
		}
		c.emitSnapshot(e, body)
	}

	return nil
}

// snapshotResolver and snapshotPreferences retain the wire names from the DNS
// endpoint. tsapi.DNSConfig is intentionally a normalized type without JSON
// tags, so marshaling it directly would expose Go field names rather than the
// API response shape.
type snapshotResolver struct {
	Address         string `json:"address"`
	UseWithExitNode bool   `json:"useWithExitNode"`
}

type snapshotPreferences struct {
	OverrideLocalDNS bool `json:"overrideLocalDNS"`
	MagicDNS         bool `json:"magicDNS"`
}

type snapshotConfig struct {
	Nameservers []snapshotResolver            `json:"nameservers"`
	SplitDNS    map[string][]snapshotResolver `json:"splitDNS"`
	SearchPaths []string                      `json:"searchPaths"`
	Preferences snapshotPreferences           `json:"preferences"`
}

func marshalSnapshot(cfg *tsapi.DNSConfig) (string, error) {
	nameservers := make([]snapshotResolver, len(cfg.Nameservers))
	for i, resolver := range cfg.Nameservers {
		nameservers[i] = snapshotResolver{
			Address:         resolver.Address,
			UseWithExitNode: resolver.UseWithExitNode,
		}
	}

	splitDNS := make(map[string][]snapshotResolver, len(cfg.SplitDNS))
	for domain, resolvers := range cfg.SplitDNS {
		out := make([]snapshotResolver, len(resolvers))
		for i, resolver := range resolvers {
			out[i] = snapshotResolver{
				Address:         resolver.Address,
				UseWithExitNode: resolver.UseWithExitNode,
			}
		}
		splitDNS[domain] = out
	}

	body, err := json.Marshal(snapshotConfig{
		Nameservers: nameservers,
		SplitDNS:    splitDNS,
		SearchPaths: append([]string(nil), cfg.SearchPaths...),
		Preferences: snapshotPreferences{
			OverrideLocalDNS: cfg.OverrideLocalDNS,
			MagicDNS:         cfg.MagicDNS,
		},
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Collector) emitSnapshot(e telemetry.Emitter, body string) {
	if c.snapshotEmitter == nil {
		emitter, err := snapshot.New(snapshot.Config{
			Emitter:      e,
			EventName:    EventDNSSnapshot,
			Kind:         snapshot.KindDNS,
			Heartbeat:    c.snapshotHeartbeat,
			MaxBodyBytes: c.snapshotBodyBytes,
		})
		if err != nil {
			return
		}
		c.snapshotEmitter = emitter
	}
	c.snapshotEmitter.Observe(c.now(), "", body, nil)
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
