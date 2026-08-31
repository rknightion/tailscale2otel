// Package nodemetrics implements a gated snapshot collector that scrapes a
// configured list of Prometheus-text /metrics endpoints (for example the
// per-node metrics tailscaled exposes) and re-emits every sample centrally
// through the shared telemetry.Emitter.
//
// It is a Prometheus-faithful drop-in for scraping individual Tailscale nodes:
// node identity is carried as LABELS (a "tailscale.node" attribute plus any
// configured passthrough labels), NOT as OTEL Resources, and a SINGLE
// MeterProvider (the one behind the injected Emitter) is used — there are no
// per-node providers. Metric names are forwarded VERBATIM with an empty unit so
// Grafana's OTLP->Prometheus normalization stays a near no-op and the re-emitted
// series keep their original names.
//
// Counters (and the cumulative _bucket/_sum/_count components of histogram and
// summary families) are converted to monotonic deltas across scrapes: the first
// observation of a series stores a baseline and emits nothing; subsequent scrapes
// emit the positive delta (or, on a detected counter reset, the current value).
// Gauges, untyped, and unknown-type families are forwarded as gauges carrying
// their current value.
package nodemetrics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/redact"
	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Compile-time assertion: *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

// noopAPITracer is the shared fallback for a nil Options.Tracer, so the
// nil-tracer path allocates no tracer per scrape, mirroring
// internal/tsapi/transport.go's retryTransport.startSpan pattern.
var noopAPITracer = tracenoop.NewTracerProvider().Tracer("")

const (
	defaultInterval          = 60 * time.Second
	defaultTimeout           = 10 * time.Second
	defaultDiscoveryInterval = 5 * time.Minute
	defaultMaxResponseBytes  = 4 * 1024 * 1024
	defaultMaxSamples        = 50000

	// defaultConcurrency bounds how many targets are scraped in parallel per
	// tick when Options.Concurrency is unset. It is combined with the per-tick
	// deadline (see Collect) so that neither a large target count nor a batch
	// of slow/unreachable targets can make a single Collect call run
	// unboundedly: targets fan out in bounded waves, and the whole tick is
	// additionally capped at the collector's own interval (#80).
	defaultConcurrency = 10

	// metricUp is the per-target scrape health gauge.
	metricUp = "tailscale.node.up"
	// attrInstance is the node-identity label attached to every emitted series.
	// It is deliberately NOT "instance": Grafana Cloud's OTLP->Prometheus
	// translation promotes the resource attribute service.instance.id to the
	// "instance" label, and that resource value WINS — clobbering a per-series
	// "instance" attribute to the collector host's name and collapsing
	// tailscale.node.up to a single series. The key "tailscale.node" normalizes to
	// the Prometheus label "tailscale_node", which does not collide with that
	// promotion, so per-node attribution survives.
	attrInstance = "tailscale.node"

	// attrInstancePromLabel is the Prometheus-normalized spelling of attrInstance.
	// Scraped labels are untrusted and may legally include this key; carrying both
	// keys would create a backend-dependent collision after OTLP->Prometheus
	// normalization, so this raw scraped/passthrough key is reserved and stripped.
	attrInstancePromLabel = "tailscale_node"

	// metricDiscoverySuccess and metricDiscoveredTargets are the discovery-health
	// gauges emitted every Collect when a Discoverer is configured.
	metricDiscoverySuccess  = "tailscale2otel.nodemetrics.discovery.success"
	metricDiscoveredTargets = "tailscale2otel.nodemetrics.discovery.targets"
	metricScrapeFailures    = "tailscale2otel.nodemetrics.scrape.failures"

	// staleGenerations is how many consecutive scrapes a counter series may go
	// unobserved before its delta baseline is evicted, bounding the prev map
	// against label churn while tolerating a transient target outage.
	staleGenerations = 5

	// prevHardCap bounds the delta-baseline map across all targets. Legitimate
	// fleets sit far below it (a tailscaled node emits a few hundred series); a
	// label-churning or malicious target hits maxSamples per scrape and would
	// otherwise hold maxSamples x staleGenerations baselines per target. At the
	// cap, NEW series stop acquiring baselines (their deltas are suppressed)
	// while existing series keep updating; pruneStale recovers space.
	prevHardCap = 250_000

	// defaultMaxDistinctMetrics is the process-lifetime cap on distinct forwarded
	// metric NAMES when Options.MaxDistinctMetrics is unset (0). It bounds the
	// number of OTEL instruments the raw verbatim forward can create over the
	// life of the process: with default-empty metric_allow/metric_deny, a scraped
	// node otherwise controls an unbounded set of metric names and each unseen
	// one creates a new, permanently-retained instrument (GHSA-gp33-6r5x-hw2f).
	// The per-scrape maxSamples and per-response maxResponseBytes budgets do NOT
	// bound this: they cap one response, not the distinct names accumulated
	// across many scrapes.
	defaultMaxDistinctMetrics = 2000

	// opScrapeNodeMetrics identifies this collector's own scrape probe for
	// apistate.Observe (#430/#524).
	//
	// Unlike every other collector's operation name, this is NOT an upstream
	// Tailscale OpenAPI operationId — nodemetrics never calls the tailnet
	// control-plane API at all. It scrapes each node's own tailscaled
	// client-metrics endpoint (:5252) directly, so there is no spec operation
	// to name and no tsapi.StatusError to classify; every failure here is a
	// plain error and lands on apistate.Classify's default branch
	// (transient_failure), which is the honest read for an unreachable node.
	// This string is a stable LOCAL probe identifier only — a future reader
	// must not go looking for it in spec/tailscale-api.json.
	opScrapeNodeMetrics = "scrapeNodeMetrics"
)

// scrapeFailureReason is deliberately closed: targets and their errors are
// operator-controlled or network-derived, so neither may become a metric
// attribute value. `other` preserves failure visibility when a new error shape
// appears without creating a series for its text.
type scrapeFailureReason string

const (
	scrapeFailureConnectionRefused scrapeFailureReason = "connection_refused"
	scrapeFailureTimeout           scrapeFailureReason = "timeout"
	scrapeFailureMissingEndpoint   scrapeFailureReason = "missing_endpoint"
	scrapeFailureHTTPError         scrapeFailureReason = "http_error"
	scrapeFailureOther             scrapeFailureReason = "other"
)

// scrapeFailureReasons fixes the externally documented vocabulary order. It
// also supplies the deterministic tiebreak for DominantScrapeFailure: a status
// hint must not flicker merely because concurrent workers completed in a
// different order.
var scrapeFailureReasons = [...]scrapeFailureReason{
	scrapeFailureConnectionRefused,
	scrapeFailureTimeout,
	scrapeFailureMissingEndpoint,
	scrapeFailureHTTPError,
	scrapeFailureOther,
}

// scrapeFailureError preserves a closed diagnostic classification while the
// error itself remains safe and useful to the caller/tracer. It must never
// expose transport error text as telemetry.
type scrapeFailureError struct {
	reason scrapeFailureReason
	err    error
}

func (e *scrapeFailureError) Error() string { return e.err.Error() }
func (e *scrapeFailureError) Unwrap() error { return e.err }

func newScrapeFailure(reason scrapeFailureReason, err error) error {
	return &scrapeFailureError{reason: reason, err: err}
}

func failureReason(err error) scrapeFailureReason {
	var scrapeErr *scrapeFailureError
	if errors.As(err, &scrapeErr) {
		return scrapeErr.reason
	}
	return scrapeFailureOther
}

func scrapeFailureReasonIndex(reason scrapeFailureReason) int {
	for i, candidate := range scrapeFailureReasons {
		if candidate == reason {
			return i
		}
	}
	return len(scrapeFailureReasons) - 1 // defensive fold to the closed `other` bucket
}

func classifyTransportFailure(err error) scrapeFailureReason {
	if errors.Is(err, context.DeadlineExceeded) {
		return scrapeFailureTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return scrapeFailureTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return scrapeFailureConnectionRefused
	}
	return scrapeFailureOther
}

// prevEntry is a tracked counter series' last cumulative value and the scrape
// generation in which it was last observed (for stale-baseline eviction).
type prevEntry struct {
	value float64
	gen   uint64
}

// Target is a single Prometheus-text endpoint to scrape. Instance overrides the
// default host:port value of the per-node "tailscale.node" identity label; Labels
// are passthrough attributes merged onto every sample from this target (parsed
// metric labels win on key conflict).
//
// The optional auth/TLS fields cover PROXIED/HTTPS targets; native tailscaled
// /metrics endpoints are plain HTTP with no auth/TLS, so leaving them unset keeps
// the scrape a plain GET with no added headers. BearerTokenFile, when set, is read
// fresh on every scrape (rotation-safe) and takes precedence over BearerToken; a
// non-nil TLS config builds a dedicated client (see New).
type Target struct {
	URL      string
	Instance string
	Labels   map[string]string

	BearerToken     string
	BearerTokenFile string
	Headers         map[string]string
	TLS             *TLSClientConfig
}

// TLSClientConfig is the optional per-target TLS trust/identity for HTTPS targets.
// A zero value (InsecureSkipVerify false, all paths empty) yields system defaults;
// InsecureSkipVerify defaults to false so an HTTPS target is verified unless the
// operator explicitly opts out (a deliberate footgun guard).
type TLSClientConfig struct {
	InsecureSkipVerify bool
	CAFile             string
	CertFile           string
	KeyFile            string
	ServerName         string
}

// Discoverer produces additional scrape Targets at runtime. The package stays
// Tailscale-agnostic: a Discoverer returns plain Targets and the concrete
// (e.g. Tailscale device-list) implementation lives in another package. Discover
// is called on the Collector's own discovery interval (see Options).
type Discoverer interface {
	Discover(ctx context.Context) ([]Target, error)
}

// Options configures a Collector.
//
// When Discoverer is set, the Collector periodically calls it (every
// DiscoveryInterval, defaulting to 5m when <= 0) and UNIONS the discovered
// targets with the static Targets (dedup by URL, static wins). When Discoverer
// is nil the Collector scrapes only the static Targets and DiscoveryInterval is
// ignored.
type Options struct {
	Targets           []Target
	Interval          time.Duration
	Timeout           time.Duration
	Client            *http.Client
	Now               func() time.Time
	Discoverer        Discoverer
	DiscoveryInterval time.Duration
	MaxResponseBytes  int64
	MaxSamples        int

	// Logger receives ERROR-level target-disable notices (a target's TLS config
	// failed to build, see tlsDisabled) and a one-time WARN when the distinct
	// forwarded metric-name budget (MaxDistinctMetrics) is first exhausted. A nil
	// Logger defaults to slog.Default().
	Logger *slog.Logger

	// MaxDistinctMetrics bounds the number of DISTINCT forwarded metric NAMES
	// admitted over the process lifetime (not per scrape), guarding the OTEL
	// instrument registry against an unbounded, node-controlled set of metric
	// names (GHSA-gp33-6r5x-hw2f). 0 selects defaultMaxDistinctMetrics (2000); a
	// negative value disables the budget entirely. Names encountered after the
	// budget is exhausted are dropped (never emitted, never create an
	// instrument) and counted via the metricMetricNamesDropped counter; the
	// tailscale.node.up-style health signals and curated tailscale.node.*
	// families are never subject to this budget — only the raw verbatim
	// forward is.
	MaxDistinctMetrics int

	// Concurrency bounds how many targets are scraped in parallel per tick
	// through a worker pool (never one goroutine per target). <= 0 defaults to
	// defaultConcurrency. Together with the per-tick deadline Collect derives
	// from Interval, this bounds a tick's total wall-clock duration
	// regardless of how many targets are slow or unreachable (#80).
	Concurrency int

	// Passthrough filters applied to forwarded samples only (never to
	// tailscale.node.up or the discovery.* gauges). MetricAllow/MetricDeny are
	// anchored against the metric NAME; DropLabels strips label keys (the
	// tailscale.node identity label is never dropped). Empty = no filtering.
	MetricAllow []string
	MetricDeny  []string
	DropLabels  []string

	// Tracer, when non-nil, emits one client span per target scrape (the GET of
	// the target's /metrics endpoint), mirroring
	// internal/tsapi/transport.go's retryTransport.startSpan. A nil Tracer is
	// replaced with a no-op at span-start (see noopAPITracer), so callers never
	// need a nil-guard. The span NAME is a single fixed class ("nodemetrics.scrape"),
	// never the per-target URL/host/instance — see fetchAndEmit's span-name
	// comment for why: node_metrics.discovery can add an unbounded, externally
	// discovered set of targets over the collector's lifetime, so anything
	// target-specific in the name would make span-name cardinality unbounded.
	// Per-target detail rides as span ATTRIBUTES instead.
	Tracer trace.Tracer

	// APIState, when non-nil, receives this collector's per-operation
	// availability for the admin status page and the capability matrix
	// (#430/#524). A nil tracker is a no-op.
	APIState *apistate.Tracker
}

// resolvedTarget pairs a Target with the *http.Client to scrape it through,
// keeping the two from ever desyncing when the active set changes at runtime.
// A nil client means "use the shared Collector.client". id is the target's stable
// effective identity (see targetIdentity): it deduplicates the runtime target set
// and namespaces this target's delta baselines so two distinct targets never share
// a baseline (#199).
//
// tlsDisabled marks a target whose TLS config was present but failed to build
// (e.g. an unreadable CAFile): the target is scraped NEVER, not merely with a
// fallback client. Before GHSA-2q4v-rrm9-966w, a failed TLS build left client nil
// so the target silently scraped over the shared/system-trust client while still
// attaching its bearer token / headers — an attacker who could present a
// system-trusted cert for the target's hostname would receive the credentials.
// tlsDisabled short-circuits scrapeTarget before any request is built, so a
// target's credentials are only ever sent over the client its own TLS trust
// policy produced.
type resolvedTarget struct {
	target      Target
	client      *http.Client // nil => use the shared Collector.client
	id          string       // stable effective identity (normalized URL + node label)
	tlsDisabled bool         // true => never scraped; reported as tailscale.node.up=0
}

// Collector implements collector.SnapshotCollector for node /metrics scraping.
type Collector struct {
	static            []resolvedTarget // built once in New from opts.Targets
	interval          time.Duration
	client            *http.Client
	now               func() time.Time
	discoverer        Discoverer
	discoveryInterval time.Duration
	timeout           time.Duration // resolved scrape timeout, for building discovered clients
	maxResponseBytes  int64
	maxSamples        int
	concurrency       int // resolved worker-pool size for scrapeAll (#80)
	logger            *slog.Logger
	tracer            trace.Tracer // never nil; resolved in New (see noopAPITracer)

	// tracker records this collector's per-operation availability for the
	// admin status page and the capability matrix (#430/#524). A nil
	// *apistate.Tracker is a no-op.
	tracker *apistate.Tracker

	metricAllow []*regexp.Regexp    // anchored name allowlist; empty => allow all
	metricDeny  []*regexp.Regexp    // anchored name denylist; applied after allow
	dropLabels  map[string]struct{} // label keys stripped from forwarded series (never the tailscale.node label)

	// maxDistinctMetrics is the resolved distinct-forwarded-metric-name budget
	// (see Options.MaxDistinctMetrics); -1 means the budget is disabled.
	// metricNames is the process-lifetime admitted-name set guarded by
	// metricNamesMu (checked from scrapeAll's concurrent workers, see
	// admitMetricName); metricBudgetWarned gates the one-time WARN log.
	maxDistinctMetrics int
	metricNamesMu      sync.Mutex
	metricNames        map[string]struct{}
	metricBudgetWarned bool

	mu          sync.Mutex
	active      []resolvedTarget     // current scrape set: static ∪ discovered (guarded by mu)
	lastDiscACK time.Time            // last SUCCESSFUL discovery time (guarded by mu)
	lastDiscOK  bool                 // outcome of the last discovery attempt (guarded by mu)
	prev        map[string]prevEntry // series key -> last cumulative value + generation
	gen         uint64               // scrape generation, bumped once per Collect

	// gsb accumulates the per-target tailscale.node.up gauge and flushes it as an
	// observable-gauge snapshot each Collect, so a target dropped from the active
	// set (removed from discovery, or a static target deleted) drops its node.up
	// series out of the export instead of ghosting at its last value (#55). Only
	// Collect's single goroutine touches it — scrapeAll's concurrent workers
	// return their (instance, up) results, which Collect Adds after they join, so
	// the builder needs no synchronization. The forwarded passthrough samples
	// (emitSample) are intentionally NOT snapshotted: their metric names are
	// dynamic and include monotonic counters, a different concern from #55.
	gsb *telemetry.GaugeSnapshotBuilder

	// curatedGauges accumulates the churning curated GAUGE families
	// (health_messages, derp.home_region, peer_relay.endpoints) and flushes them
	// as observable-gauge snapshots each Collect, so a node that leaves the fleet
	// drops its curated series out instead of ghosting (#55) — the same reason
	// node.up uses a snapshot. Unlike node.up, curated gauges are emitted from
	// deep inside the concurrent scrapeAll workers (curateGauge), so Adds are
	// guarded by curatedMu; Flush runs on Collect's goroutine after the workers
	// join. Curated COUNTERS need no builder: they ride the shared delta pipeline
	// and are emitted directly (the Emitter is concurrency-safe).
	curatedMu     sync.Mutex
	curatedGauges *telemetry.GaugeSnapshotBuilder

	// scrapeFailureCounts is a process-lifetime diagnostic for the admin status
	// page. Unlike the emitted counter it is read locally, so it keeps only the
	// fixed closed reason vocabulary and needs no per-target state.
	scrapeFailureCounts [len(scrapeFailureReasons)]atomic.Uint64
}

// New returns a nodemetrics Collector. A zero Interval defaults to 60s and a
// zero Timeout defaults to 10s; an explicit Client (if non-nil) is used as-is for
// targets without a TLS config.
//
// Each target carrying a non-nil TLS config gets a DEDICATED client whose
// transport is a clone of http.DefaultTransport with a TLS config built from the
// target's CA/cert/key (mirroring telemetry.tlsConfig). Targets without a TLS
// config share the default client, so an injected opts.Client still applies to
// them. A TLS config that fails to build (e.g. unreadable CAFile) DISABLES that
// target entirely (see resolveTargets) — it is never scraped over any client,
// so its credentials can never leak to a fallback client; the failure is logged
// at ERROR and the target reports tailscale.node.up=0 every tick.
func New(opts Options) *Collector {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: timeout, CheckRedirect: noRedirect}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	discoveryInterval := opts.DiscoveryInterval
	if opts.Discoverer != nil && discoveryInterval <= 0 {
		discoveryInterval = defaultDiscoveryInterval
	}
	maxResponseBytes := opts.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	maxSamples := opts.MaxSamples
	if maxSamples <= 0 {
		maxSamples = defaultMaxSamples
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	tracer := opts.Tracer
	if tracer == nil {
		tracer = noopAPITracer
	}
	maxDistinctMetrics := opts.MaxDistinctMetrics
	switch {
	case maxDistinctMetrics == 0:
		maxDistinctMetrics = defaultMaxDistinctMetrics
	case maxDistinctMetrics < 0:
		maxDistinctMetrics = -1 // any negative value collapses to the disabled sentinel
	}
	static := dedupTargets(resolveTargets(opts.Targets, timeout, logger))
	return &Collector{
		static:             static,
		interval:           opts.Interval,
		client:             client,
		now:                now,
		discoverer:         opts.Discoverer,
		discoveryInterval:  discoveryInterval,
		timeout:            timeout,
		maxResponseBytes:   maxResponseBytes,
		maxSamples:         maxSamples,
		concurrency:        concurrency,
		logger:             logger,
		tracer:             tracer,
		tracker:            opts.APIState,
		metricAllow:        compileAnchored(opts.MetricAllow),
		metricDeny:         compileAnchored(opts.MetricDeny),
		dropLabels:         toSet(opts.DropLabels),
		maxDistinctMetrics: maxDistinctMetrics,
		metricNames:        make(map[string]struct{}),
		active:             static,
		prev:               make(map[string]prevEntry),
		gsb:                telemetry.NewGaugeSnapshotBuilder(),
		curatedGauges:      telemetry.NewGaugeSnapshotBuilder(),
	}
}

// compileAnchored compiles each pattern in its ANCHORED form `^(?:pat)$` so a
// metric-name filter matches the whole name, never a substring. A pattern that
// fails to compile is SKIPPED defensively (config.Validate already guaranteed
// every pattern compiles, so this is unreachable in practice).
func compileAnchored(patterns []string) []*regexp.Regexp {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(fmt.Sprintf("^(?:%s)$", p))
		if err != nil {
			continue // defensive: Validate already rejected bad patterns
		}
		out = append(out, re)
	}
	return out
}

// toSet builds a lookup set from keys, or nil when there are none.
func toSet(keys []string) map[string]struct{} {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		out[k] = struct{}{}
	}
	return out
}

// resolveTargets pairs each Target with its dedicated *http.Client, or nil to
// fall back to the shared Collector.client. A target carrying a non-nil TLS
// config that builds successfully gets a DEDICATED client whose transport is a
// clone of http.DefaultTransport with that TLS config.
//
// A target whose TLS config is present but FAILS to build (e.g. an unreadable
// CAFile) is marked tlsDisabled and left on a nil client: it is NEVER scraped
// (see scrapeTarget), not silently scraped over the shared/system-trust client.
// Before this fix (GHSA-2q4v-rrm9-966w) a failed TLS build fell through to the
// shared client while the target's bearer token / headers were still attached
// at scrape time, so an attacker able to present a system-trusted certificate
// for the target's original hostname would receive its credentials. One bad
// target's TLS config must not take the whole process down, so the failure is
// logged at ERROR (naming the target and the error) and the target reports
// tailscale.node.up=0 every tick, same as any other scrape failure — it is a
// disabled target, not a process-fatal one.
func resolveTargets(ts []Target, timeout time.Duration, logger *slog.Logger) []resolvedTarget {
	out := make([]resolvedTarget, len(ts))
	for i := range ts {
		out[i].target = ts[i]
		instance := effectiveInstance(&ts[i])
		out[i].id = targetIdentity(ts[i].URL, instance)
		tlsCfg := ts[i].TLS
		if tlsCfg == nil {
			continue
		}
		tc, err := buildTLSConfig(tlsCfg)
		if err != nil {
			out[i].tlsDisabled = true
			logger.Error("nodemetrics: target TLS configuration failed to load; target disabled and will not be scraped (no fallback to a shared/untrusted client, so credentials are never sent)",
				"instance", instance, "url", redact.URLOrigin(ts[i].URL), "error", err)
			continue
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = tc
		out[i].client = &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: noRedirect}
	}
	return out
}

// dedupTargets removes runtime targets that share an effective identity (normalized
// URL + instance, see targetIdentity), keeping the FIRST occurrence so the result is
// deterministic and order-stable. Targets that differ only by URL, or only by
// instance, are kept — this collapses ONLY true duplicates (same endpoint, same node
// label). Config validation already rejects colliding STATIC targets (validate.go);
// this is the runtime backstop so a duplicate that reaches New (e.g. via the
// programmatic Options API) or emerges from the discovery union can never
// double-scrape as one identity or let two targets corrupt a shared delta baseline
// (#199).
func dedupTargets(rts []resolvedTarget) []resolvedTarget {
	out := make([]resolvedTarget, 0, len(rts))
	seen := make(map[string]struct{}, len(rts))
	for i := range rts {
		if _, ok := seen[rts[i].id]; ok {
			continue
		}
		seen[rts[i].id] = struct{}{}
		out = append(out, rts[i])
	}
	return out
}

// effectiveInstance is the node-identity label a target resolves to: its explicit
// Instance when set, else the host:port derived from its URL. It mirrors the value
// scrapeTarget attaches as the tailscale.node attribute, so the two never diverge.
func effectiveInstance(t *Target) string {
	if t.Instance != "" {
		return t.Instance
	}
	return hostPort(t.URL)
}

// targetIdentity returns a target's stable EFFECTIVE identity: its normalized URL
// plus its effective node-identity label (its explicit Instance, else the host:port
// derived from the URL). Two entries sharing this identity are the same target — a
// true duplicate — so it dedups the runtime target set (dedupTargets) and is
// rejected by config validation. It also namespaces each target's delta baselines so
// two DISTINCT targets never share a baseline, even when they carry the same node
// label or scrape byte-identical series (#199). The instance component is what keeps
// two same-URL/different-config targets (e.g. one verify-on and one skip-verify HTTPS
// scrape of the same endpoint) as separate targets with separate baselines; the
// discovery UNION keeps its own, separate endpoint-level dedup (see unionTargets).
func targetIdentity(rawURL, instance string) string {
	return normalizeTargetURL(rawURL) + "\x00" + instance
}

// normalizeTargetURL canonicalizes a target URL for identity comparison: the
// scheme and host are lowercased (both are case-insensitive per RFC 3986) while
// the path/query are left byte-exact (a metrics server may treat them as
// significant). A URL that fails to parse falls back to its raw string, so an
// unparseable value still keys deterministically.
func normalizeTargetURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String()
}

// noRedirect is the CheckRedirect for every scrape client: it stops the HTTP
// client from following 3xx responses (SSRF-via-redirect guard). Returning
// http.ErrUseLastResponse makes Do() return the 3xx response itself instead of
// an error; fetchAndEmit then treats the non-2xx status as a failed scrape
// (tailscale.node.up=0), so a redirect target's body is never fetched or
// re-emitted. A compromised scrape target therefore cannot bounce the scraper at
// internal URLs (cloud metadata, loopback admin ports).
func noRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// buildTLSConfig builds a *tls.Config from a per-target TLSClientConfig, mirroring
// telemetry.tlsConfig: RootCAs from CAFile, a client certificate from
// CertFile+KeyFile, plus InsecureSkipVerify and ServerName passthrough.
func buildTLSConfig(t *TLSClientConfig) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: t.InsecureSkipVerify, //nolint:gosec // operator opt-in for proxied/self-signed targets; defaults false
		ServerName:         t.ServerName,
	}
	if t.CAFile != "" {
		pem, err := safefile.ReadRegular(t.CAFile, safefile.MaxPEMBytes, safefile.AllowSymlink)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in CA file %s", t.CAFile)
		}
		cfg.RootCAs = pool
	}
	if t.CertFile != "" && t.KeyFile != "" {
		cert, err := safefile.LoadX509KeyPair(t.CertFile, t.KeyFile, safefile.MaxPEMBytes)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "nodemetrics" }

// DefaultInterval returns the configured interval, or 60s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Collect scrapes every target and re-emits its samples. It returns nil in the
// normal case (including partial failure) and a non-nil error only when EVERY
// target failed, so the scheduler's scrape.success reflects total failure. Empty
// Targets returns nil immediately.
func (c *Collector) Collect(ctx context.Context, e telemetry.Emitter) error {
	c.maybeDiscover(ctx)

	// Snapshot the active set (and bump/capture the generation) under the lock so
	// a concurrent discovery swapping c.active can't desync the scrape loop. The
	// snapshot is a slice-header copy; resolvedTarget elements are never mutated
	// in place, so iterating the snapshot without the lock is safe.
	c.mu.Lock()
	targets := c.active
	c.gen++
	gen := c.gen
	discOK := c.lastDiscOK
	c.mu.Unlock()

	// Discovery-health gauges (only when discovery is enabled): emitted every
	// Collect from stored state so the series stays continuous between the
	// (slower) discovery refreshes, even when the active set is empty.
	if c.discoverer != nil {
		success := 0.0
		if discOK {
			success = 1
		}
		e.Gauge(docDiscoverySuccess.Name, docDiscoverySuccess.Unit, docDiscoverySuccess.Description, success, nil)
		e.Gauge(docDiscoveredTargets.Name, docDiscoveredTargets.Unit, docDiscoveredTargets.Description, float64(len(targets)), nil)
	}

	if len(targets) == 0 {
		// Flush empty node.up + curated-gauge snapshots so any series from a prior
		// tick with targets is cleared rather than ghosting once discovery drops
		// them all.
		c.gsb.Flush(e)
		c.flushCuratedGauges(e)
		return nil
	}

	// Bound the whole tick to at most the collector's own interval: a
	// context.WithTimeout wraps every target scrape below, so a batch of
	// slow/unreachable targets can never push a single Collect call past its
	// next scheduled tick. Without this, the scheduler's single-slot
	// time.Ticker would drop/collapse the next tick(s) rather than queue them,
	// degrading the collector's actual cadence as the slow/unreachable
	// population grows (#80). Targets still in flight when the deadline fires
	// are aborted (their in-flight HTTP request is canceled) and still emit
	// tailscale.node.up=0, same as any other scrape failure.
	tickCtx, cancel := context.WithTimeout(ctx, c.DefaultInterval())
	defer cancel()

	successes := c.scrapeAll(tickCtx, targets, e)
	failures := len(targets) - successes
	c.pruneStale(gen)
	// Emit the node.up + curated-gauge snapshots on every path (including
	// all-failed, where node.up is 0 for every point): a target absent this tick
	// drops out instead of ghosting (#55).
	c.gsb.Flush(e)
	c.flushCuratedGauges(e)

	// Observe this collector's own probe (#430/#524): with at least one
	// success the collector is working (an offline node is already reported
	// per-target by tailscale.node.up, so it must not also flip this
	// collector-level row and duplicate that signal); with every target
	// failed, observe the SAME error returned below rather than inventing a
	// second one — apistate.Classify has no tsapi.StatusError to work with
	// here, so this reads as transient_failure, the honest answer for an
	// unreachable node.
	var scrapeErr error
	if failures == len(targets) {
		scrapeErr = fmt.Errorf("nodemetrics: all %d target(s) failed", failures)
	}
	apistate.Observe(e, c.tracker, c.Name(), opScrapeNodeMetrics, apistate.Disposition{}, scrapeErr, c.now())
	return scrapeErr
}

// scrapeAll scrapes every target through a bounded worker pool (at most
// c.concurrency scrapes in flight at once) and returns the number that
// succeeded. Every target is still attempted exactly once and always emits
// its tailscale.node.up gauge via scrapeTarget — even one whose scrape starts
// after ctx has already expired, since that fails (and reports up=0) almost
// immediately rather than blocking. Combined with the caller's per-tick
// context.WithTimeout, this bounds a tick's total duration to roughly the
// configured interval regardless of target count or health (#80).
func (c *Collector) scrapeAll(ctx context.Context, targets []resolvedTarget, e telemetry.Emitter) int {
	sem := make(chan struct{}, c.concurrency)
	var wg sync.WaitGroup
	var successes atomic.Int64
	// Each worker records its target's node.up point at its own index (disjoint
	// writes, so no synchronization is needed); Collect feeds them into the
	// single-goroutine builder after the workers join.
	ups := make([]telemetry.GaugePoint, len(targets))
	for i := range targets {
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, rt *resolvedTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			instance, err := c.scrapeTarget(ctx, rt, c.clientOf(rt), e)
			up := 0.0
			if err == nil {
				successes.Add(1)
				up = 1
			} else {
				reason := failureReason(err)
				c.recordScrapeFailure(reason)
				e.Counter(docScrapeFailures.Name, docScrapeFailures.Unit, docScrapeFailures.Description, 1,
					telemetry.Attrs{attrReason: string(reason)})
			}
			ups[idx] = telemetry.GaugePoint{Value: up, Attrs: telemetry.Attrs{attrInstance: instance}}
		}(i, &targets[i])
	}
	wg.Wait()
	// Back on Collect's goroutine: accumulate node.up for the flush. A target no
	// longer in `targets` (dropped from discovery) contributes no point, so the
	// builder's next Flush clears its prior series (#55).
	for i := range ups {
		c.gsb.Add(docNodeUp.Name, docNodeUp.Unit, docNodeUp.Description, ups[i].Value, ups[i].Attrs)
	}
	return int(successes.Load())
}

func (c *Collector) recordScrapeFailure(reason scrapeFailureReason) {
	c.scrapeFailureCounts[scrapeFailureReasonIndex(reason)].Add(1)
}

// DominantScrapeFailure returns the most common failed-scrape reason observed
// for this collector process and its count. Before the first failure it returns
// an empty reason and zero. Equal counts resolve in the declared closed
// vocabulary order, keeping the status-page hint stable under concurrency.
func (c *Collector) DominantScrapeFailure() (string, uint64) {
	var dominant int
	var max uint64
	for i := range c.scrapeFailureCounts {
		count := c.scrapeFailureCounts[i].Load()
		if count > max {
			dominant, max = i, count
		}
	}
	if max == 0 {
		return "", 0
	}
	return string(scrapeFailureReasons[dominant]), max
}

// maybeDiscover runs the Discoverer when due (every discoveryInterval since the
// last SUCCESSFUL discovery; immediately when never run). On success it rebuilds
// the active set as static ∪ discovered (dedup by URL, static wins) and records
// the discovery time. On error it leaves the active set and lastDiscACK
// UNCHANGED, so the prior targets keep being scraped and discovery retries on the
// next tick (a flaky Discoverer never empties the scrape set).
func (c *Collector) maybeDiscover(ctx context.Context) {
	if c.discoverer == nil {
		return
	}
	now := c.now()
	c.mu.Lock()
	due := c.lastDiscACK.IsZero() || now.Sub(c.lastDiscACK) >= c.discoveryInterval
	c.mu.Unlock()
	if !due {
		return
	}
	discovered, err := c.discoverer.Discover(ctx)
	if err != nil {
		c.mu.Lock()
		c.lastDiscOK = false
		c.mu.Unlock()
		return // keep prior active set and lastDiscACK; retry next tick
	}
	union := dedupTargets(unionTargets(c.static, resolveTargets(discovered, c.timeout, c.logger)))
	c.mu.Lock()
	c.active = union
	c.lastDiscACK = now
	c.lastDiscOK = true
	c.mu.Unlock()
}

// DiscoveryStatus is a snapshot of the scraper's current targets and the
// outcome of the most recent dynamic discovery refresh.
type DiscoveryStatus struct {
	Enabled       bool         // dynamic discovery configured (a Discoverer is set)
	LastDiscovery time.Time    // time of the last SUCCESSFUL discovery (zero if none yet)
	LastOK        bool         // outcome of the last discovery attempt
	Static        int          // number of static (configured) targets
	Active        int          // number of active targets (static ∪ discovered)
	Targets       []TargetInfo // the active scrape set
}

// TargetInfo identifies one active scrape target and whether it was statically
// configured or dynamically discovered.
type TargetInfo struct {
	Instance string
	URL      string
	Source   string // "static" | "discovered"
}

// Snapshot returns the current scrape targets and last-discovery health for an
// admin status page. Each active target's Source is "static" when its URL is one
// of the configured static targets, else "discovered" (a discovered target whose
// URL collides with a static one is reported as static, mirroring the union's
// static-wins dedup). The returned slice is freshly allocated, so the caller may
// retain it safely.
func (c *Collector) Snapshot() DiscoveryStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	staticURLs := make(map[string]struct{}, len(c.static))
	for i := range c.static {
		staticURLs[c.static[i].target.URL] = struct{}{}
	}
	targets := make([]TargetInfo, 0, len(c.active))
	for i := range c.active {
		t := &c.active[i].target
		source := "discovered"
		if _, ok := staticURLs[t.URL]; ok {
			source = "static"
		}
		targets = append(targets, TargetInfo{
			Instance: t.Instance,
			URL:      t.URL,
			Source:   source,
		})
	}
	return DiscoveryStatus{
		Enabled:       c.discoverer != nil,
		LastDiscovery: c.lastDiscACK,
		LastOK:        c.lastDiscOK,
		Static:        len(c.static),
		Active:        len(c.active),
		Targets:       targets,
	}
}

// unionTargets returns the static targets first, then each discovered target whose
// URL is not already present. Dedup here is ENDPOINT-level (by Target.URL) and STATIC
// WINS, so an operator's explicit static target (its labels/auth/TLS) is never
// overridden by a discovered duplicate, and a discovered node that is also statically
// configured is not scraped twice. This is deliberately distinct from the effective
// target IDENTITY used for baselines and validation (see targetIdentity): two STATIC
// entries for the same URL with different instances/configs are legitimately separate
// targets and are BOTH kept here, whereas a DISCOVERED duplicate of a static URL is
// dropped. Order is stable; the caller collapses any residual same-identity entries
// via dedupTargets.
func unionTargets(static, discovered []resolvedTarget) []resolvedTarget {
	out := make([]resolvedTarget, 0, len(static)+len(discovered))
	seen := make(map[string]struct{}, len(static)+len(discovered))
	for i := range static {
		out = append(out, static[i])
		seen[static[i].target.URL] = struct{}{}
	}
	for i := range discovered {
		u := discovered[i].target.URL
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, discovered[i])
	}
	return out
}

// pruneStale evicts counter baselines not observed within the last
// staleGenerations scrapes, bounding the prev map against series/label churn. The
// generation is passed in (the snapshot captured by Collect) so it stays stable
// even if a concurrent Collect were to bump c.gen.
func (c *Collector) pruneStale(gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, pe := range c.prev {
		if gen-pe.gen >= staleGenerations {
			delete(c.prev, k)
		}
	}
}

// clientOf returns the resolved target's dedicated client, falling back to the
// shared default client when the target has no (or a failed) TLS config.
func (c *Collector) clientOf(rt *resolvedTarget) *http.Client {
	if rt.client != nil {
		return rt.client
	}
	return c.client
}

// errResponseTooLarge is returned when a target response exceeds the configured
// per-scrape byte budget.
var errResponseTooLarge = fmt.Errorf("nodemetrics: response exceeds max_response_bytes")

type maxBytesReadCloser struct {
	r         io.ReadCloser
	remaining int64
}

func limitedReadCloser(r io.ReadCloser, max int64) io.Reader {
	if max <= 0 {
		return r
	}
	return &maxBytesReadCloser{r: r, remaining: max}
}

func (r *maxBytesReadCloser) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var one [1]byte
		n, err := r.r.Read(one[:])
		if n > 0 {
			return 0, errResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	return n, err
}

// scrapeTarget fetches and re-emits one target's forwarded samples, returning
// the resolved instance label and a non-nil error on any GET/read/parse
// failure. The per-target tailscale.node.up health gauge is NOT emitted here:
// scrapeTarget runs on a worker-pool goroutine (#80), and node.up now flows
// through the (single-goroutine) GaugeSnapshotBuilder so departed targets drop
// out instead of ghosting (#55). scrapeAll derives up (1 on nil error, else 0)
// from the returned error and feeds node.up into the builder after the workers
// join. The returned instance is always non-empty for a valid target URL.
func (c *Collector) scrapeTarget(ctx context.Context, rt *resolvedTarget, client *http.Client, e telemetry.Emitter) (string, error) {
	t := &rt.target
	instance := effectiveInstance(t)
	if rt.tlsDisabled {
		// Never build or send a request for a target whose TLS trust policy
		// failed to construct: no client (shared, default, or otherwise) is
		// used, so credentials configured on this target can never be sent
		// over an unintended connection (GHSA-2q4v-rrm9-966w). The ERROR log
		// was already emitted once, at target resolution (resolveTargets).
		return instance, newScrapeFailure(scrapeFailureOther,
			fmt.Errorf("nodemetrics: target %s disabled: TLS configuration failed to load", instance))
	}
	return instance, c.fetchAndEmit(ctx, t, rt.id, client, instance, e)
}

// fetchAndEmit performs the GET, parses the body, and emits every sample. It
// returns an error on any transport, status, read, or parse failure. targetID is
// the scrape target's stable identity, threaded down to key delta baselines (#199).
func (c *Collector) fetchAndEmit(ctx context.Context, t *Target, targetID string, client *http.Client, instance string, e telemetry.Emitter) error {
	resp, err := c.scrapeHTTP(ctx, t, client, instance)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	samples, err := parse(limitedReadCloser(resp.Body, c.maxResponseBytes), c.maxSamples)
	if err != nil {
		return newScrapeFailure(scrapeFailureOther, err)
	}
	for i := range samples {
		c.emitSample(&samples[i], t, targetID, instance, e)
	}
	return nil
}

// scrapeHTTP performs the GET of one target's /metrics endpoint inside a
// single client span, and returns the response on a 2xx status (caller closes
// the body). The span covers only the HTTP round trip and status check — like
// internal/tsapi/transport.go's retryTransport, it ends BEFORE the body is
// parsed, so sample decoding is never attributed to network latency.
//
// The span NAME is the fixed class "nodemetrics.scrape", never t.URL or
// instance: node_metrics.discovery (see internal/collector/CLAUDE.md) can add
// an unbounded, externally discovered set of targets over the collector's
// lifetime, so any per-target text in the span NAME would make span-name
// cardinality unbounded. Per-target detail (instance, url.full) rides as span
// ATTRIBUTES instead — the same tradeoff tsapi's transport makes with
// url.full (a bounded endpoint-class name, an unbounded-but-attribute URL).
func (c *Collector) scrapeHTTP(ctx context.Context, t *Target, client *http.Client, instance string) (*http.Response, error) {
	spanCtx, span := c.tracer.Start(ctx, "nodemetrics.scrape", trace.WithSpanKind(trace.SpanKindClient))

	req, err := http.NewRequestWithContext(spanCtx, http.MethodGet, t.URL, nil)
	if err != nil {
		safeErr := errors.New("nodemetrics: invalid target URL")
		c.observeScrape(span, instance, t.URL, 0, safeErr)
		return nil, safeErr
	}
	if err := applyAuthHeaders(req, t); err != nil {
		c.observeScrape(span, instance, t.URL, 0, err)
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		safeErr := errors.New("nodemetrics: scrape transport failed")
		c.observeScrape(span, instance, t.URL, 0, safeErr)
		return nil, newScrapeFailure(classifyTransportFailure(err), safeErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusErr := fmt.Errorf("nodemetrics: GET %s: status %d", redact.URLOrigin(t.URL), resp.StatusCode)
		c.observeScrape(span, instance, t.URL, resp.StatusCode, statusErr)
		_ = resp.Body.Close()
		reason := scrapeFailureHTTPError
		if resp.StatusCode == http.StatusNotFound {
			reason = scrapeFailureMissingEndpoint
		}
		return nil, newScrapeFailure(reason, statusErr)
	}
	c.observeScrape(span, instance, t.URL, resp.StatusCode, nil)
	return resp, nil
}

// observeScrape finalizes one scrapeHTTP span: status/error attributes, span
// status, and End. status is 0 when no HTTP response was received.
func (c *Collector) observeScrape(span trace.Span, instance, url string, status int, err error) {
	if span.IsRecording() {
		span.SetAttributes(
			attribute.String(attrInstance, instance),
			attribute.String("http.request.method", http.MethodGet),
			attribute.String("url.full", redact.URLOrigin(url)),
		)
		if status != 0 {
			span.SetAttributes(attribute.Int("http.response.status_code", status))
		}
		switch {
		case err != nil:
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		case status >= 400:
			span.SetStatus(codes.Error, http.StatusText(status))
		}
	}
	span.End()
}

// applyAuthHeaders sets the target's custom headers and bearer Authorization on
// the request. A BearerTokenFile is read fresh on every call (rotation-safe) and
// takes precedence over a static BearerToken; a file read error fails the scrape.
// With no auth fields set the request is left unchanged (plain GET).
func applyAuthHeaders(req *http.Request, t *Target) error {
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	switch {
	case t.BearerTokenFile != "":
		b, err := safefile.ReadRegular(t.BearerTokenFile, safefile.MaxSecretBytes, safefile.AllowSymlink)
		if err != nil {
			return fmt.Errorf("nodemetrics: read bearer token file %s: %w", t.BearerTokenFile, err)
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(b)))
	case t.BearerToken != "":
		req.Header.Set("Authorization", "Bearer "+t.BearerToken)
	}
	return nil
}

// emitSample emits one parsed sample: cumulative series (counters and
// histogram/summary _bucket/_sum/_count) become monotonic deltas; everything
// else becomes a gauge of the current value.
//
// This is the SINGLE choke point for forwarded samples, so the passthrough
// filters (metric_allow/metric_deny on the name; drop_labels on the attrs) are
// applied here and nowhere else: tailscale.node.up and the discovery.* gauges,
// emitted elsewhere, are never filtered. Curation is ADDITIVE and runs
// alongside the raw forward: when a sample's family matches a curated mapping
// (see curated.go), the derived tailscale.node.* series is emitted IN ADDITION
// to the verbatim forward, and curation deliberately BYPASSES the passthrough
// filters (curated metrics are catalog metrics, not passthrough).
func (c *Collector) emitSample(s *sample, t *Target, targetID, instance string, e telemetry.Emitter) {
	allowed := c.allowMetric(s.name)
	if s.cumulative {
		c.emitCumulative(s, t, targetID, instance, allowed, e)
		return
	}
	if allowed {
		if c.admitMetricName(s.name) {
			attrs := mergeAttrs(t.Labels, s.labels, instance)
			c.applyDropLabels(attrs)
			e.Gauge(s.name, "", s.help, s.value, attrs)
		} else {
			c.recordMetricNameDrop(e)
		}
	}
	c.curateGauge(s, instance)
}

// emitCumulative handles a cumulative sample: it computes the series delta ONCE
// (the shared baseline pipeline, see delta) and feeds it to both the verbatim
// raw forward (when the name passes the passthrough filters) and any curated
// counter the family maps to. The single delta means a curated counter never
// maintains its own baseline. When the sample is neither forwarded nor curated
// it returns immediately WITHOUT touching the baseline map, preserving the prior
// behavior where a filtered-out non-curated counter is not tracked.
//
// The baseline is keyed off the FULL pre-drop source-series identity (metric name
// + every raw scraped label + the stable target identity, see baselineKey), NOT
// the emitted post-drop attributes. So when several source series collapse onto one
// emitted series — because drop_labels removed a distinguishing label, or the
// curated mapping folds a raw label — each keeps its OWN baseline (and its own
// first-observation suppression / reset detection), and their independently-correct
// deltas SUM on the merged emitted series via the counter's own accumulation (#199).
func (c *Collector) emitCumulative(s *sample, t *Target, targetID, instance string, allowed bool, e telemetry.Emitter) {
	if isNaN(s.value) {
		return
	}
	cc, curated := curatedCounters[s.name]
	if !allowed && !curated {
		return
	}
	delta, ok := c.delta(baselineKey(targetID, s.name, s.labels), s.value)
	if !ok || delta <= 0 {
		return
	}
	if allowed {
		if c.admitMetricName(s.name) {
			attrs := mergeAttrs(t.Labels, s.labels, instance)
			c.applyDropLabels(attrs)
			e.Counter(s.name, "", s.help, delta, attrs)
		} else {
			c.recordMetricNameDrop(e)
		}
	}
	if curated {
		out := cc.attrs(s.labels)
		out[attrInstance] = instance
		e.Counter(cc.name, cc.unit, cc.desc, delta, out)
	}
}

// curateGauge emits the curated GAUGE derived from a non-cumulative sample, if
// its family maps to one (see curated.go). Curated gauges churn per node, so
// they accumulate into the churn-safe curatedGauges snapshot builder (guarded by
// curatedMu — curateGauge runs on the concurrent scrapeAll workers) and are
// flushed once per Collect. Filters are bypassed (curated metrics are catalog
// metrics, not passthrough).
func (c *Collector) curateGauge(s *sample, instance string) {
	cg, ok := curatedGaugeSpecs[s.name]
	if !ok {
		return
	}
	out := cg.attrs(s.labels)
	out[attrInstance] = instance
	c.curatedMu.Lock()
	c.curatedGauges.Add(cg.name, cg.unit, cg.desc, s.value, out)
	c.curatedMu.Unlock()
}

// flushCuratedGauges flushes the curated-gauge snapshot builder under curatedMu.
// It runs on Collect's goroutine after the scrapeAll workers have joined, so the
// lock is uncontended here; it guards against a late worker only in principle.
func (c *Collector) flushCuratedGauges(e telemetry.Emitter) {
	c.curatedMu.Lock()
	c.curatedGauges.Flush(e)
	c.curatedMu.Unlock()
}

// allowMetric reports whether a forwarded metric NAME passes the passthrough
// filters: when metricAllow is non-empty the name must match at least one
// allow pattern, then any metricDeny match drops it (deny wins). Both pattern
// sets are anchored (see compileAnchored).
func (c *Collector) allowMetric(name string) bool {
	if len(c.metricAllow) > 0 {
		matched := false
		for _, re := range c.metricAllow {
			if re.MatchString(name) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, re := range c.metricDeny {
		if re.MatchString(name) {
			return false
		}
	}
	return true
}

// admitMetricName reports whether name may be forwarded: either it was already
// admitted, or the process-lifetime distinct-name budget (maxDistinctMetrics)
// still has room. It is the enforcement point for GHSA-gp33-6r5x-hw2f — a
// scraped target fully controls the set of metric names it exposes, and with
// default-empty metric_allow/metric_deny every unseen name would otherwise
// create a new OTEL instrument retained for the process lifetime. A negative
// maxDistinctMetrics disables the budget (always admits). Safe for concurrent
// callers (scrapeAll's worker pool scrapes multiple targets at once); logs a
// single WARN the first time the budget is exhausted, never per dropped sample.
func (c *Collector) admitMetricName(name string) bool {
	if c.maxDistinctMetrics < 0 {
		return true
	}
	c.metricNamesMu.Lock()
	defer c.metricNamesMu.Unlock()
	if _, ok := c.metricNames[name]; ok {
		return true
	}
	if len(c.metricNames) >= c.maxDistinctMetrics {
		if !c.metricBudgetWarned {
			c.metricBudgetWarned = true
			c.logger.Warn("nodemetrics: distinct forwarded metric-name budget exhausted; new metric names are now dropped",
				"max_distinct_metrics", c.maxDistinctMetrics)
		}
		return false
	}
	c.metricNames[name] = struct{}{}
	return true
}

// recordMetricNameDrop emits the counted, observable signal for a sample
// dropped by admitMetricName, so a bitten budget shows up in telemetry rather
// than silently vanishing (the accompanying WARN log fires only once; this
// counter increments on every subsequent drop).
func (c *Collector) recordMetricNameDrop(e telemetry.Emitter) {
	e.Counter(metricMetricNamesDropped, "", descMetricNamesDropped, 1, telemetry.Attrs{attrReason: reasonMetricNameBudget})
}

// applyDropLabels deletes every dropLabels key from attrs, EXCEPT the
// tailscale.node identity label which is never dropped (node identity must survive).
func (c *Collector) applyDropLabels(attrs telemetry.Attrs) {
	for k := range c.dropLabels {
		if k == attrInstance {
			continue
		}
		delete(attrs, k)
	}
}

// delta reads and updates the per-series counter baseline for the given source
// key, returning the monotonic increment to emit and whether a prior baseline
// existed (ok=false on the FIRST observation of a series, which only stores the
// baseline and emits nothing). It is the SINGLE baseline read/update per source
// series: the raw forward and any curated counter both consume the returned delta,
// so a curated metric never maintains its own baseline. On a detected counter reset
// (current value below the baseline) the delta is the current value (the new series
// started from zero). The baseline map is bounded by prevHardCap: at the cap a
// brand-new series is not tracked (its delta is suppressed) while existing series
// keep updating. The key must uniquely identify the SOURCE series (see baselineKey)
// so merged emitted series never share a baseline.
func (c *Collector) delta(key string, value float64) (float64, bool) {
	c.mu.Lock()
	pe, seen := c.prev[key]
	if seen || len(c.prev) < prevHardCap {
		c.prev[key] = prevEntry{value: value, gen: c.gen}
	}
	c.mu.Unlock()

	if !seen {
		return 0, false
	}
	d := value - pe.value
	if value < pe.value {
		d = value // counter reset: current value is the increment
	}
	return d, true
}

// mergeAttrs builds the per-sample attribute set: target passthrough labels
// first, then parsed metric labels (which win on conflict), then the
// tailscale.node identity label (which always wins). The Prometheus-normalized
// identity spelling (tailscale_node) is reserved and stripped from scraped and
// passthrough labels to prevent normalized-key collisions in OTLP backends. All
// values are strings.
func mergeAttrs(targetLabels, metricLabels map[string]string, instance string) telemetry.Attrs {
	out := make(telemetry.Attrs, len(targetLabels)+len(metricLabels)+1)
	for k, v := range targetLabels {
		if k == attrInstancePromLabel {
			continue
		}
		out[k] = v
	}
	for k, v := range metricLabels {
		if k == attrInstancePromLabel {
			continue
		}
		out[k] = v
	}
	out[attrInstance] = instance
	return out
}

// baselineKey is the stable, injective key used for delta tracking. It identifies
// one SOURCE series — NOT the (possibly merged) emitted series — so that two source
// series which collapse onto one emitted series keep separate baselines (#199). It
// is the target identity, then the metric name, then the sorted "k=<quoted v>" over
// EVERY raw scraped label (BEFORE drop_labels and BEFORE any curated folding),
// joined by "," and separated by "\x00". The value is rendered with strconv.Quote
// so a value containing "=" or "," (both legal in Prometheus label values) cannot be
// confused with the key/value or part separators; label keys are [a-zA-Z0-9_] so
// they need no quoting, and neither the target identity nor a metric name contains a
// literal NUL, keeping the "\x00" separators unambiguous.
func baselineKey(targetID, name string, labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+strconv.Quote(v))
	}
	sort.Strings(parts)
	return targetID + "\x00" + name + "\x00" + strings.Join(parts, ",")
}

// hostPort extracts host:port from a target URL for the default tailscale.node
// label value, falling back to the raw URL when it cannot be parsed.
func hostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

func isNaN(f float64) bool { return f != f }
