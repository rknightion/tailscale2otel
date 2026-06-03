// Package nodemetrics implements a gated snapshot collector that scrapes a
// configured list of Prometheus-text /metrics endpoints (for example the
// per-node metrics tailscaled exposes) and re-emits every sample centrally
// through the shared telemetry.Emitter.
//
// It is a Prometheus-faithful drop-in for scraping individual Tailscale nodes:
// node identity is carried as LABELS (an "instance" attribute plus any
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
	"fmt"
	"net/http"
	"net/url"
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
	defaultInterval = 60 * time.Second
	defaultTimeout  = 10 * time.Second

	// metricUp is the per-target scrape health gauge.
	metricUp = "tailscale.node.up"
	// attrInstance is the node-identity label attached to every emitted series.
	attrInstance = "instance"

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
// default host:port "instance" label; Labels are passthrough attributes merged
// onto every sample from this target (parsed metric labels win on key conflict).
type Target struct {
	URL      string
	Instance string
	Labels   map[string]string
}

// Options configures a Collector.
type Options struct {
	Targets  []Target
	Interval time.Duration
	Timeout  time.Duration
	Client   *http.Client
	Now      func() time.Time
}

// Collector implements collector.SnapshotCollector for node /metrics scraping.
type Collector struct {
	targets  []Target
	interval time.Duration
	client   *http.Client
	now      func() time.Time

	mu   sync.Mutex
	prev map[string]prevEntry // series key -> last cumulative value + generation
	gen  uint64               // scrape generation, bumped once per Collect
}

// New returns a nodemetrics Collector. A zero Interval defaults to 60s and a
// zero Timeout defaults to 10s; an explicit Client (if non-nil) is used as-is.
func New(opts Options) *Collector {
	client := opts.Client
	if client == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Collector{
		targets:  opts.Targets,
		interval: opts.Interval,
		client:   client,
		now:      now,
		prev:     make(map[string]prevEntry),
	}
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
	if len(c.targets) == 0 {
		return nil
	}
	// Collect is driven by a single per-collector goroutine (never concurrent with
	// itself), so the generation counter needs no lock; prev map access is still
	// guarded by c.mu in case of any future sharing.
	c.gen++
	var failures int
	for i := range c.targets {
		if err := c.scrapeTarget(ctx, &c.targets[i], e); err != nil {
			failures++
		}
	}
	c.pruneStale()
	if failures == len(c.targets) {
		return fmt.Errorf("nodemetrics: all %d target(s) failed", failures)
	}
	return nil
}

// pruneStale evicts counter baselines not observed within the last
// staleGenerations scrapes, bounding the prev map against series/label churn.
func (c *Collector) pruneStale() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, pe := range c.prev {
		if c.gen-pe.gen >= staleGenerations {
			delete(c.prev, k)
		}
	}
}

// scrapeTarget fetches and re-emits one target. It always emits the per-target
// tailscale.node.up health gauge (1 on success, 0 on any GET/read/parse error)
// and returns a non-nil error on failure so Collect can count it.
func (c *Collector) scrapeTarget(ctx context.Context, t *Target, e telemetry.Emitter) error {
	instance := t.Instance
	if instance == "" {
		instance = hostPort(t.URL)
	}
	if err := c.fetchAndEmit(ctx, t, instance, e); err != nil {
		e.Gauge(metricUp, "1", "node metrics scrape up (1) or down (0)", 0, telemetry.Attrs{attrInstance: instance})
		return err
	}
	e.Gauge(metricUp, "1", "node metrics scrape up (1) or down (0)", 1, telemetry.Attrs{attrInstance: instance})
	return nil
}

// fetchAndEmit performs the GET, parses the body, and emits every sample. It
// returns an error on any transport, status, read, or parse failure.
func (c *Collector) fetchAndEmit(ctx context.Context, t *Target, instance string, e telemetry.Emitter) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("nodemetrics: GET %s: status %d", t.URL, resp.StatusCode)
	}

	samples, err := parse(resp.Body)
	if err != nil {
		return err
	}
	for i := range samples {
		c.emitSample(&samples[i], t, instance, e)
	}
	return nil
}

// emitSample emits one parsed sample: cumulative series (counters and
// histogram/summary _bucket/_sum/_count) become monotonic deltas; everything
// else becomes a gauge of the current value.
func (c *Collector) emitSample(s *sample, t *Target, instance string, e telemetry.Emitter) {
	attrs := mergeAttrs(t.Labels, s.labels, instance)
	if s.cumulative {
		c.emitDelta(s, attrs, e)
		return
	}
	e.Gauge(s.name, "", s.help, s.value, attrs)
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
// first, then parsed metric labels (which win on conflict), then the instance
// label (which always wins). All values are strings.
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
// name + "\x00" + sorted "k=<quoted v>" over ALL attrs (incl instance), joined by
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

// hostPort extracts host:port from a target URL for the default instance label,
// falling back to the raw URL when it cannot be parsed.
func hostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

func isNaN(f float64) bool { return f != f }
