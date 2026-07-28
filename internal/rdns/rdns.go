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

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
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

	// StaleTTL is how long PAST a positive entry's TTL a resolved name may
	// still be served — while exactly one background refresh runs — before it
	// is finally treated as a miss (#297). Serving a bounded stale window
	// keeps a flow-metric label from flapping hostname -> external -> hostname
	// across every TTL expiry, which otherwise splits the series. <= 0
	// disables stale serving, reproducing the pre-#297 immediate-miss
	// behavior. Negative (failed-lookup) entries are NEVER served stale.
	//
	// Unlike the other fields above, New does NOT invent a default when this
	// is zero: the config layer (enrichment.reverse_dns.stale_ttl, default 1h)
	// supplies it, so a caller that forgets to set it gets today's behavior
	// rather than a silently-injected default.
	StaleTTL time.Duration

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
	hits, misses, negatives     int64
	staleHits                   int64
	querySuccess, queryFail     int64
	refreshSuccess, refreshFail int64
	evictExpired, evictPurged   int64
	evictStaleExpired           int64
	overflows                   int64
	lastPurge                   time.Time
}

// Stats is an absolute snapshot of the cache's counters and occupancy, for the
// admin status page. report() emits the same counters as OTEL metrics.
type Stats struct {
	Size, Capacity          int
	Hits, Misses, Negatives int64
	StaleHits               int64
	QuerySuccess, QueryFail int64
	RefreshSuccess          int64
	RefreshFail             int64
	EvictedExpired          int64
	EvictedPurged           int64
	EvictedStaleExpired     int64
	Overflows               int64
	TTL, NegativeTTL        time.Duration
	StaleTTL                time.Duration
	LastPurge               time.Time // zero when never purged
}

type entry struct {
	name    string // resolved PTR name; "" for a negative (failed) result
	expires time.Time
}

// Cache is an async, bounded reverse-DNS cache implementing Resolver.
type Cache struct {
	lookup   func(ctx context.Context, addr netip.Addr) ([]string, error)
	ttl      time.Duration
	negTTL   time.Duration
	staleTTL time.Duration
	timeout  time.Duration
	max      int
	now      func() time.Time

	emitter     telemetry.Emitter
	reportEvery time.Duration

	mu       sync.Mutex
	entries  map[netip.Addr]entry
	inflight map[netip.Addr]struct{}
	stats    stats
	reported stats // baseline for report()'s delta flush
	closed   bool  // set under mu by Close; guards wg.Add against Close's wg.Wait

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
		staleTTL:    opts.StaleTTL, // no invented default — see the Options.StaleTTL comment
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
	now := c.now()
	if e, ok := c.entries[addr]; ok {
		if now.Before(e.expires) {
			name := e.name
			if name != "" {
				c.stats.hits++
			} else {
				c.stats.negatives++
			}
			c.mu.Unlock()
			return name, name != ""
		}
		if c.servableLocked(e, now) {
			// Stale-but-servable positive entry (#297): its TTL has elapsed but
			// it is still within the configured stale window, so keep serving
			// the last-known name — never a negative entry, see the comment on
			// StaleTTL — while trying to kick off exactly one background
			// refresh. Without this, a flow-metric label would flap to
			// "external" for the whole time a refresh takes.
			c.stats.staleHits++
			name := e.name
			c.scheduleRefreshLocked(addr)
			c.mu.Unlock()
			return name, true
		}
	}
	// Any non-(fresh/stale)-cached sighting is a miss; it may or may not
	// schedule a background resolution depending on the bounds below.
	c.stats.misses++
	if c.closed {
		// Close has begun (or finished): never reserve a slot or call wg.Add
		// once Close may be inside (or about to enter) wg.Wait, or a
		// concurrent Add could race the WaitGroup's zero-counter transition.
		c.mu.Unlock()
		return "", false
	}
	// Skip when a lookup for this address is already in flight: it's neither a
	// new admission decision nor an overflow, just a duplicate sighting.
	if _, busy := c.inflight[addr]; busy {
		c.mu.Unlock()
		return "", false
	}
	_, cached := c.entries[addr]
	if !cached && len(c.entries)+len(c.inflight) >= c.max {
		// A brand-new address can't be admitted without exceeding the size
		// bound. Counting reserved (in-flight) slots alongside committed
		// entries closes the window where a burst of concurrent new
		// addresses could each pass a stale admission check before any of
		// their resolves land, overrunning max_entries.
		c.stats.overflows++
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
	// wg.Add happens while still holding mu, and Close sets closed=true while
	// holding mu before it ever calls wg.Wait — so every Add here is ordered
	// (via mu) to happen-before any concurrent Close's wg.Wait call, per
	// sync.WaitGroup's "Add must happen before Wait" contract. Once closed is
	// observed true (above), no further Add can occur.
	c.wg.Add(1)
	c.mu.Unlock()

	go c.resolve(addr)
	return "", false
}

// scheduleRefreshLocked attempts to start exactly one background refresh of a
// stale-but-servable address. Callers must hold mu. It reuses the same
// single-flight (inflight), worker (sem), and closing (closed) guards as the
// miss path in LookupName so the #118/#121 concurrency/capacity contracts
// apply uniformly — but it deliberately never checks or counts against the
// admission/overflow bound: addr is already a committed cache entry, not a
// new one. It never blocks: if no worker slot is free, resolve() is simply
// skipped and the stale name keeps being served on the next sighting.
func (c *Cache) scheduleRefreshLocked(addr netip.Addr) {
	if c.closed {
		return
	}
	if _, busy := c.inflight[addr]; busy {
		return
	}
	select {
	case c.sem <- struct{}{}:
	default:
		return
	}
	c.inflight[addr] = struct{}{}
	// Same wg.Add-under-mu ordering contract as LookupName's miss path — see
	// the comment there.
	c.wg.Add(1)

	go c.resolve(addr)
}

// resolve performs one background lookup and stores the (positive or
// negative) result, then releases the worker slot. It is used both for a
// first-time miss and for a stale-serving refresh; it tells the two apart by
// inspecting the entry already present under the lock (see isRefresh below)
// rather than threading a flag through, because the entry can legitimately
// change out from under an in-flight lookup.
func (c *Cache) resolve(addr netip.Addr) {
	defer c.wg.Done()
	defer func() { <-c.sem }()

	ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
	defer cancel()
	names, err := c.lookup(ctx, addr)

	name := pickPTRName(names)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, addr)

	// A positive entry present when this resolve lands can only have been
	// written by an earlier resolve() for the same addr (single-flight
	// prevents concurrent resolves). It is a refresh only while that name is
	// still SERVABLE, though — presence alone is not enough. sweep() runs on
	// the report interval, so an entry past expires+StaleTTL lingers in the
	// map for up to that long, and a sighting in that gap is a plain miss that
	// schedules an ordinary lookup. Treating it as a refresh would take the
	// keep-serving-stale early return below: the dead entry would never be
	// replaced by a negative one, so every subsequent sighting would query the
	// resolver again until the next sweep (#297).
	prev, hadEntry := c.entries[addr]
	isRefresh := hadEntry && prev.name != "" && c.servableLocked(prev, c.now())

	if err != nil || name == "" {
		c.stats.queryFail++
		if isRefresh {
			c.stats.refreshFail++
			// Do NOT downgrade a stale-but-servable positive to a negative
			// entry over one failed refresh (#297) — that would flap the
			// flow-metric label to "external" for the rest of the negative
			// TTL over a single transient resolver blip. Leave it exactly as
			// it was; it keeps serving stale until sweep() reclaims it at
			// expires+StaleTTL, or a later refresh succeeds.
			return
		}
		c.entries[addr] = entry{expires: c.now().Add(c.negTTL)}
		return
	}
	c.stats.querySuccess++
	if isRefresh {
		c.stats.refreshSuccess++
	}
	c.entries[addr] = entry{name: name, expires: c.now().Add(c.ttl)}
}

// pickPTRName returns a deterministic PTR name for an address: the lexicographic
// minimum of the (trailing-dot-trimmed, non-empty) names the resolver returned.
// LookupAddr's slice order is resolver-dependent — many resolvers rotate
// multi-PTR RRsets — so storing names[0] would let a multi-PTR IP's
// tailscale.src/dst.node flow-metric label flip between values across cache
// refreshes, splitting the series and breaking increase() continuity (#119).
// Returns "" when there is no usable name (caller then caches a negative).
func pickPTRName(names []string) string {
	var best string
	for _, n := range names {
		n = strings.TrimSuffix(n, ".")
		if n == "" {
			continue
		}
		if best == "" || n < best {
			best = n
		}
	}
	return best
}

// sweep deletes entries whose TTL (and, for a servable positive entry, its
// stale window too) has elapsed, reclaiming their slots. A positive entry
// with StaleTTL configured is retained until expires+StaleTTL and counted
// under evictStaleExpired rather than evictExpired when it finally goes;
// negative entries, and positive entries when StaleTTL<=0, are unaffected and
// keep counting evictExpired at plain expiry.
func (c *Cache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for a, e := range c.entries {
		if now.Before(e.expires) || c.servableLocked(e, now) {
			continue
		}
		delete(c.entries, a)
		if e.name != "" && c.staleTTL > 0 {
			c.stats.evictStaleExpired++
		} else {
			c.stats.evictExpired++
		}
	}
}

// servableLocked reports whether an entry may still be handed to the hot path
// past its TTL, under the configured stale window. It is the ONE definition of
// that window: LookupName's stale band, resolve's is-this-a-refresh test and
// sweep's eviction deadline have to agree, or an entry gets reclaimed while it
// is still being served, or served after it was reclaimed. Callers must hold
// mu. Negative entries are never servable stale, and neither is anything when
// StaleTTL is disabled.
func (c *Cache) servableLocked(e entry, now time.Time) bool {
	return e.name != "" && c.staleTTL > 0 && now.Before(e.expires.Add(c.staleTTL))
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
		Size:                len(c.entries),
		Capacity:            c.max,
		Hits:                c.stats.hits,
		Misses:              c.stats.misses,
		Negatives:           c.stats.negatives,
		StaleHits:           c.stats.staleHits,
		QuerySuccess:        c.stats.querySuccess,
		QueryFail:           c.stats.queryFail,
		RefreshSuccess:      c.stats.refreshSuccess,
		RefreshFail:         c.stats.refreshFail,
		EvictedExpired:      c.stats.evictExpired,
		EvictedPurged:       c.stats.evictPurged,
		EvictedStaleExpired: c.stats.evictStaleExpired,
		Overflows:           c.stats.overflows,
		TTL:                 c.ttl,
		NegativeTTL:         c.negTTL,
		StaleTTL:            c.staleTTL,
		LastPurge:           c.stats.lastPurge,
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
	emitDelta(MetricLookups, docLookups.Unit, docLookups.Description, attrResult, resultStale, cur.staleHits, prev.staleHits)
	emitDelta(MetricQueries, docQueries.Unit, docQueries.Description, attrResult, resultSuccess, cur.querySuccess, prev.querySuccess)
	emitDelta(MetricQueries, docQueries.Unit, docQueries.Description, attrResult, resultFailure, cur.queryFail, prev.queryFail)
	emitDelta(MetricRefreshes, docRefreshes.Unit, docRefreshes.Description, attrResult, resultSuccess, cur.refreshSuccess, prev.refreshSuccess)
	emitDelta(MetricRefreshes, docRefreshes.Unit, docRefreshes.Description, attrResult, resultFailure, cur.refreshFail, prev.refreshFail)
	emitDelta(MetricEvictions, docEvictions.Unit, docEvictions.Description, attrReason, reasonExpired, cur.evictExpired, prev.evictExpired)
	emitDelta(MetricEvictions, docEvictions.Unit, docEvictions.Description, attrReason, reasonPurge, cur.evictPurged, prev.evictPurged)
	emitDelta(MetricEvictions, docEvictions.Unit, docEvictions.Description, attrReason, reasonStaleExpired, cur.evictStaleExpired, prev.evictStaleExpired)
	if d := cur.overflows - prev.overflows; d > 0 {
		c.emitter.Counter(MetricOverflows, docOverflows.Unit, docOverflows.Description, float64(d), nil)
	}
	c.emitter.Gauge(MetricEntries, docEntries.Unit, docEntries.Description, size, nil)
	c.emitter.Gauge(MetricCapacity, docCapacity.Unit, docCapacity.Description, capacity, nil)
}

// Close cancels outstanding lookups and waits for the background workers to
// exit. It first marks the cache closed (under mu) so that any LookupName
// call it happens-before via mu will observe closed and skip wg.Add, and any
// LookupName call that already completed its own wg.Add happens-before this
// call via the same mutex — so wg.Wait below never races a concurrent Add.
func (c *Cache) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
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
