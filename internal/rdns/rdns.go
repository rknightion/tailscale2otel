// Package rdns provides best-effort, non-blocking reverse-DNS (PTR) enrichment
// for external IP addresses seen in flow logs. Lookups run in the background and
// populate a bounded cache with positive and negative TTLs; the hot path never
// blocks on the network — a cache miss returns immediately and the resolved name
// is available on the next sighting.
package rdns

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Resolver is the narrow, fakeable interface the flow processor depends on.
// LookupName never performs a synchronous network lookup: it returns a cached
// PTR name when one is available and otherwise reports a miss.
type Resolver interface {
	LookupName(addr netip.Addr) (string, bool)
}

// Options configures a Cache. Zero values select sensible defaults; Lookup and
// Now are injectable so tests stay hermetic (no real DNS, deterministic clock).
type Options struct {
	Server      string        // resolver "ip" or "ip:port"; empty = system resolver
	Timeout     time.Duration // per-lookup timeout (default 2s)
	TTL         time.Duration // positive-result cache TTL (default 1h)
	NegativeTTL time.Duration // failed-lookup cache TTL (default 5m)
	MaxEntries  int           // cache size bound (default 4096)
	Concurrency int           // max in-flight background lookups (default 8)

	// ReportInterval is how often expired entries are swept and (when Emitter is
	// set) metrics are flushed. Nil/zero uses the default 30s.
	ReportInterval time.Duration
	// Emitter, when non-nil, receives the cache's self-observability metrics on
	// each report tick. Nil disables emission (the cache still sweeps and tracks
	// Stats); wired only when self_observability.enabled.
	Emitter telemetry.Emitter

	// Lookup resolves an address to PTR names. Nil builds one from Server.
	Lookup func(ctx context.Context, addr netip.Addr) ([]string, error)
	// Now is the clock used for TTLs. Nil uses time.Now.
	Now func() time.Time
}

// defaultReportInterval is the sweep/report cadence when Options.ReportInterval
// is unset. 30s keeps the entries gauge fresh and reclaims expired slots well
// inside the default negative TTL.
const defaultReportInterval = 30 * time.Second

// stats holds the cumulative counters surfaced via Stats() and flushed as OTEL
// counter deltas by report(). All fields are guarded by Cache.mu.
type stats struct {
	hits, misses, negatives   int64
	querySuccess, queryFail   int64
	evictExpired, evictPurged int64
	overflows                 int64
	lastPurge                 time.Time
}

// Stats is an absolute snapshot of the cache's counters and occupancy, for the
// admin status page. report() emits the same counters as OTEL metrics.
type Stats struct {
	Size, Capacity          int
	Hits, Misses, Negatives int64
	QuerySuccess, QueryFail int64
	EvictedExpired          int64
	EvictedPurged           int64
	Overflows               int64
	TTL, NegativeTTL        time.Duration
	LastPurge               time.Time // zero when never purged
}

type entry struct {
	name    string // resolved PTR name; "" for a negative (failed) result
	expires time.Time
}

// Cache is an async, bounded reverse-DNS cache implementing Resolver.
type Cache struct {
	lookup  func(ctx context.Context, addr netip.Addr) ([]string, error)
	ttl     time.Duration
	negTTL  time.Duration
	timeout time.Duration
	max     int
	now     func() time.Time

	emitter     telemetry.Emitter
	reportEvery time.Duration

	mu       sync.Mutex
	entries  map[netip.Addr]entry
	inflight map[netip.Addr]struct{}
	stats    stats
	reported stats // baseline for report()'s delta flush

	sem    chan struct{} // bounds concurrent background lookups
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns a started Cache. Call Close to drain outstanding lookups.
func New(opts Options) *Cache {
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.TTL <= 0 {
		opts.TTL = time.Hour
	}
	if opts.NegativeTTL <= 0 {
		opts.NegativeTTL = 5 * time.Minute
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = 4096
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.ReportInterval <= 0 {
		opts.ReportInterval = defaultReportInterval
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	lookup := opts.Lookup
	if lookup == nil {
		lookup = resolverLookup(opts.Server)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Cache{
		lookup:      lookup,
		ttl:         opts.TTL,
		negTTL:      opts.NegativeTTL,
		timeout:     opts.Timeout,
		max:         opts.MaxEntries,
		now:         now,
		emitter:     opts.Emitter,
		reportEvery: opts.ReportInterval,
		entries:     make(map[netip.Addr]entry),
		inflight:    make(map[netip.Addr]struct{}),
		sem:         make(chan struct{}, opts.Concurrency),
		ctx:         ctx,
		cancel:      cancel,
	}
	c.wg.Add(1)
	go c.run()
	return c
}

// run sweeps expired entries and flushes metrics on the report interval until
// the cache is closed. It always sweeps; emission is a no-op when no Emitter is
// configured.
func (c *Cache) run() {
	defer c.wg.Done()
	t := time.NewTicker(c.reportEvery)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			c.sweep()
			c.report()
		}
	}
}

// LookupName returns the cached PTR name for addr, or a miss. A miss schedules a
// background lookup (subject to the in-flight, worker, and capacity bounds) so a
// later sighting can be enriched. It never blocks on the network.
func (c *Cache) LookupName(addr netip.Addr) (string, bool) {
	c.mu.Lock()
	if e, ok := c.entries[addr]; ok && c.now().Before(e.expires) {
		name := e.name
		if name != "" {
			c.stats.hits++
		} else {
			c.stats.negatives++
		}
		c.mu.Unlock()
		return name, name != ""
	}
	// Any non-(positive/negative)-cached sighting is a miss; it may or may not
	// schedule a background resolution depending on the bounds below.
	c.stats.misses++
	_, busy := c.inflight[addr]
	_, cached := c.entries[addr]
	if !cached && len(c.entries) >= c.max {
		// A brand-new address can't be admitted without exceeding the size bound,
		// so it goes un-enriched: surface it so the cache can be sized up.
		c.stats.overflows++
		c.mu.Unlock()
		return "", false
	}
	// Skip when a lookup for this address is already in flight.
	if busy {
		c.mu.Unlock()
		return "", false
	}
	// Reserve a worker slot without blocking; if all are busy, try again later.
	select {
	case c.sem <- struct{}{}:
	default:
		c.mu.Unlock()
		return "", false
	}
	c.inflight[addr] = struct{}{}
	c.mu.Unlock()

	c.wg.Add(1)
	go c.resolve(addr)
	return "", false
}

// resolve performs one background lookup and stores the (positive or negative)
// result, then releases the worker slot.
func (c *Cache) resolve(addr netip.Addr) {
	defer c.wg.Done()
	defer func() { <-c.sem }()

	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()
	names, err := c.lookup(ctx, addr)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, addr)
	if err != nil || len(names) == 0 || names[0] == "" {
		c.stats.queryFail++
		c.entries[addr] = entry{expires: c.now().Add(c.negTTL)}
		return
	}
	c.stats.querySuccess++
	c.entries[addr] = entry{name: strings.TrimSuffix(names[0], "."), expires: c.now().Add(c.ttl)}
}

// sweep deletes entries whose TTL has elapsed, reclaiming their slots, and
// counts them under evictExpired.
func (c *Cache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for a, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, a)
			c.stats.evictExpired++
		}
	}
}

// Purge removes every cached entry and returns the number removed. The cleared
// entries count under evictPurged and LastPurge records when. In-flight lookups
// are left to complete and repopulate naturally.
func (c *Cache) Purge() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(c.entries)
	c.entries = make(map[netip.Addr]entry)
	c.stats.evictPurged += int64(n)
	c.stats.lastPurge = c.now()
	return n
}

// Stats returns an absolute snapshot of the cache counters and occupancy for the
// admin status page.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		Size:           len(c.entries),
		Capacity:       c.max,
		Hits:           c.stats.hits,
		Misses:         c.stats.misses,
		Negatives:      c.stats.negatives,
		QuerySuccess:   c.stats.querySuccess,
		QueryFail:      c.stats.queryFail,
		EvictedExpired: c.stats.evictExpired,
		EvictedPurged:  c.stats.evictPurged,
		Overflows:      c.stats.overflows,
		TTL:            c.ttl,
		NegativeTTL:    c.negTTL,
		LastPurge:      c.stats.lastPurge,
	}
}

// report flushes the cumulative counters as OTEL counter deltas (since the last
// report) plus the current occupancy/capacity gauges. It is a no-op when no
// Emitter is configured. Only the single run() goroutine calls report() in
// production, so the delta baseline has a single writer.
func (c *Cache) report() {
	if c.emitter == nil {
		return
	}
	c.mu.Lock()
	cur := c.stats
	prev := c.reported
	c.reported = cur
	size := float64(len(c.entries))
	capacity := float64(c.max)
	c.mu.Unlock()

	emitDelta := func(metric, unit, desc, key, val string, now, before int64) {
		if d := now - before; d > 0 {
			c.emitter.Counter(metric, unit, desc, float64(d), telemetry.Attrs{key: val})
		}
	}
	emitDelta(MetricLookups, docLookups.Unit, docLookups.Description, attrResult, resultHit, cur.hits, prev.hits)
	emitDelta(MetricLookups, docLookups.Unit, docLookups.Description, attrResult, resultMiss, cur.misses, prev.misses)
	emitDelta(MetricLookups, docLookups.Unit, docLookups.Description, attrResult, resultNegative, cur.negatives, prev.negatives)
	emitDelta(MetricQueries, docQueries.Unit, docQueries.Description, attrResult, resultSuccess, cur.querySuccess, prev.querySuccess)
	emitDelta(MetricQueries, docQueries.Unit, docQueries.Description, attrResult, resultFailure, cur.queryFail, prev.queryFail)
	emitDelta(MetricEvictions, docEvictions.Unit, docEvictions.Description, attrReason, reasonExpired, cur.evictExpired, prev.evictExpired)
	emitDelta(MetricEvictions, docEvictions.Unit, docEvictions.Description, attrReason, reasonPurge, cur.evictPurged, prev.evictPurged)
	if d := cur.overflows - prev.overflows; d > 0 {
		c.emitter.Counter(MetricOverflows, docOverflows.Unit, docOverflows.Description, float64(d), nil)
	}
	c.emitter.Gauge(MetricEntries, docEntries.Unit, docEntries.Description, size, nil)
	c.emitter.Gauge(MetricCapacity, docCapacity.Unit, docCapacity.Description, capacity, nil)
}

// Close cancels outstanding lookups and waits for the background workers to exit.
func (c *Cache) Close() {
	c.cancel()
	c.wg.Wait()
}

// resolverLookup returns a lookup func bound to the given DNS server (empty =
// the system resolver). A non-empty server forces the pure-Go resolver so the
// custom Dial target is honored.
func resolverLookup(server string) func(context.Context, netip.Addr) ([]string, error) {
	r := net.DefaultResolver
	if dialAddr := normalizeServer(server); dialAddr != "" {
		r = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, dialAddr)
			},
		}
	}
	return func(ctx context.Context, a netip.Addr) ([]string, error) {
		return r.LookupAddr(ctx, a.String())
	}
}

// normalizeServer turns a configured resolver address into a dial target. An
// empty value yields "" (use the system resolver); a bare IP gets the default
// DNS port 53; an "ip:port" is used as-is.
func normalizeServer(server string) string {
	if server == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(server, "53")
}
