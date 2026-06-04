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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Compile-time assertion: *Collector is a SnapshotCollector.
var _ collector.SnapshotCollector = (*Collector)(nil)

const (
	defaultInterval          = 60 * time.Second
	defaultTimeout           = 10 * time.Second
	defaultDiscoveryInterval = 5 * time.Minute
	defaultMaxResponseBytes  = 4 * 1024 * 1024
	defaultMaxSamples        = 50000

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

	// metricDiscoverySuccess and metricDiscoveredTargets are the discovery-health
	// gauges emitted every Collect when a Discoverer is configured.
	metricDiscoverySuccess  = "tailscale2otel.nodemetrics.discovery.success"
	metricDiscoveredTargets = "tailscale2otel.nodemetrics.discovery.targets"

	// staleGenerations is how many consecutive scrapes a counter series may go
	// unobserved before its delta baseline is evicted, bounding the prev map
	// against label churn while tolerating a transient target outage.
	staleGenerations = 5
)

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

	// Passthrough filters applied to forwarded samples only (never to
	// tailscale.node.up or the discovery.* gauges). MetricAllow/MetricDeny are
	// anchored against the metric NAME; DropLabels strips label keys (the
	// tailscale.node identity label is never dropped). Empty = no filtering.
	MetricAllow []string
	MetricDeny  []string
	DropLabels  []string
}

// resolvedTarget pairs a Target with the *http.Client to scrape it through,
// keeping the two from ever desyncing when the active set changes at runtime.
// A nil client means "use the shared Collector.client".
type resolvedTarget struct {
	target Target
	client *http.Client // nil => use the shared Collector.client
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

	metricAllow []*regexp.Regexp    // anchored name allowlist; empty => allow all
	metricDeny  []*regexp.Regexp    // anchored name denylist; applied after allow
	dropLabels  map[string]struct{} // label keys stripped from forwarded series (never the tailscale.node label)

	mu          sync.Mutex
	active      []resolvedTarget     // current scrape set: static ∪ discovered (guarded by mu)
	lastDiscACK time.Time            // last SUCCESSFUL discovery time (guarded by mu)
	lastDiscOK  bool                 // outcome of the last discovery attempt (guarded by mu)
	prev        map[string]prevEntry // series key -> last cumulative value + generation
	gen         uint64               // scrape generation, bumped once per Collect
}

// New returns a nodemetrics Collector. A zero Interval defaults to 60s and a
// zero Timeout defaults to 10s; an explicit Client (if non-nil) is used as-is for
// targets without a TLS config.
//
// Each target carrying a non-nil TLS config gets a DEDICATED client whose
// transport is a clone of http.DefaultTransport with a TLS config built from the
// target's CA/cert/key (mirroring telemetry.tlsConfig). Targets without a TLS
// config share the default client, so an injected opts.Client still applies to
// them. A TLS config that fails to build (e.g. unreadable CAFile) leaves that
// target on the default client, deferring the failure to scrape time where it is
// surfaced as tailscale.node.up=0.
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
	static := resolveTargets(opts.Targets, timeout)
	return &Collector{
		static:            static,
		interval:          opts.Interval,
		client:            client,
		now:               now,
		discoverer:        opts.Discoverer,
		discoveryInterval: discoveryInterval,
		timeout:           timeout,
		maxResponseBytes:  maxResponseBytes,
		maxSamples:        maxSamples,
		metricAllow:       compileAnchored(opts.MetricAllow),
		metricDeny:        compileAnchored(opts.MetricDeny),
		dropLabels:        toSet(opts.DropLabels),
		active:            static,
		prev:              make(map[string]prevEntry),
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
// clone of http.DefaultTransport with that TLS config; a target with no TLS (or
// a TLS config that fails to build, e.g. an unreadable CAFile) leaves client nil
// so the failure is deferred to scrape time and surfaced as tailscale.node.up=0.
func resolveTargets(ts []Target, timeout time.Duration) []resolvedTarget {
	out := make([]resolvedTarget, len(ts))
	for i := range ts {
		out[i].target = ts[i]
		tls := ts[i].TLS
		if tls == nil {
			continue
		}
		tc, err := buildTLSConfig(tls)
		if err != nil {
			continue // fall back to shared client; scrape will fail and report up=0
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = tc
		out[i].client = &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: noRedirect}
	}
	return out
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
		pem, err := os.ReadFile(t.CAFile)
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
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
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
		return nil
	}
	var failures int
	for i := range targets {
		if err := c.scrapeTarget(ctx, &targets[i].target, c.clientOf(&targets[i]), e); err != nil {
			failures++
		}
	}
	c.pruneStale(gen)
	if failures == len(targets) {
		return fmt.Errorf("nodemetrics: all %d target(s) failed", failures)
	}
	return nil
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
	union := unionTargets(c.static, resolveTargets(discovered, c.timeout))
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

// unionTargets returns the static targets first, then each discovered target
// whose URL is not already present. Dedup is by Target.URL and STATIC WINS, so an
// operator's explicit static target (its labels/auth/TLS) is never overridden by
// a discovered duplicate. Order is stable.
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

// scrapeTarget fetches and re-emits one target. It always emits the per-target
// tailscale.node.up health gauge (1 on success, 0 on any GET/read/parse error)
// and returns a non-nil error on failure so Collect can count it.
func (c *Collector) scrapeTarget(ctx context.Context, t *Target, client *http.Client, e telemetry.Emitter) error {
	instance := t.Instance
	if instance == "" {
		instance = hostPort(t.URL)
	}
	if err := c.fetchAndEmit(ctx, t, client, instance, e); err != nil {
		e.Gauge(docNodeUp.Name, docNodeUp.Unit, docNodeUp.Description, 0, telemetry.Attrs{attrInstance: instance})
		return err
	}
	e.Gauge(docNodeUp.Name, docNodeUp.Unit, docNodeUp.Description, 1, telemetry.Attrs{attrInstance: instance})
	return nil
}

// fetchAndEmit performs the GET, parses the body, and emits every sample. It
// returns an error on any transport, status, read, or parse failure.
func (c *Collector) fetchAndEmit(ctx context.Context, t *Target, client *http.Client, instance string, e telemetry.Emitter) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return err
	}
	if err := applyAuthHeaders(req, t); err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("nodemetrics: GET %s: status %d", t.URL, resp.StatusCode)
	}

	samples, err := parse(limitedReadCloser(resp.Body, c.maxResponseBytes), c.maxSamples)
	if err != nil {
		return err
	}
	for i := range samples {
		c.emitSample(&samples[i], t, instance, e)
	}
	return nil
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
		b, err := os.ReadFile(t.BearerTokenFile)
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
// emitted elsewhere, are never filtered.
func (c *Collector) emitSample(s *sample, t *Target, instance string, e telemetry.Emitter) {
	if !c.allowMetric(s.name) {
		return
	}
	attrs := mergeAttrs(t.Labels, s.labels, instance)
	c.applyDropLabels(attrs)
	if s.cumulative {
		c.emitDelta(s, attrs, e)
		return
	}
	e.Gauge(s.name, "", s.help, s.value, attrs)
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

// emitDelta applies the per-series delta logic. NaN samples are skipped (a
// counter must not be NaN). The first observation of a series stores a baseline
// and emits nothing; subsequent scrapes emit a positive delta (or, on reset, the
// current value).
func (c *Collector) emitDelta(s *sample, attrs telemetry.Attrs, e telemetry.Emitter) {
	if isNaN(s.value) {
		return
	}
	key := seriesKey(s.name, attrs)

	c.mu.Lock()
	pe, seen := c.prev[key]
	c.prev[key] = prevEntry{value: s.value, gen: c.gen}
	c.mu.Unlock()

	if !seen {
		return // baseline only on first observation
	}
	delta := s.value - pe.value
	if s.value < pe.value {
		// Counter reset: the new series started from zero, so the current value
		// is the increment.
		delta = s.value
	}
	if delta > 0 {
		e.Counter(s.name, "", s.help, delta, attrs)
	}
}

// mergeAttrs builds the per-sample attribute set: target passthrough labels
// first, then parsed metric labels (which win on conflict), then the
// tailscale.node identity label (which always wins). All values are strings.
func mergeAttrs(targetLabels, metricLabels map[string]string, instance string) telemetry.Attrs {
	out := make(telemetry.Attrs, len(targetLabels)+len(metricLabels)+1)
	for k, v := range targetLabels {
		out[k] = v
	}
	for k, v := range metricLabels {
		out[k] = v
	}
	out[attrInstance] = instance
	return out
}

// seriesKey is the stable, injective key used for delta tracking:
// name + "\x00" + sorted "k=<quoted v>" over ALL attrs (incl tailscale.node), joined by
// ",". The value is rendered with strconv.Quote so that a value containing "="
// or "," (both legal in Prometheus label values) cannot be confused with the
// key/value or part separators — keys are [a-zA-Z0-9_] so they need no quoting.
func seriesKey(name string, attrs telemetry.Attrs) string {
	parts := make([]string, 0, len(attrs))
	for k, v := range attrs {
		parts = append(parts, k+"="+strconv.Quote(attrString(v)))
	}
	sort.Strings(parts)
	return name + "\x00" + strings.Join(parts, ",")
}

// attrString renders an attr value as a string for series keying. Values here
// are always strings in practice; the fallback keeps keying total.
func attrString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
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
