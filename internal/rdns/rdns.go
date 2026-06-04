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

	// Lookup resolves an address to PTR names. Nil builds one from Server.
	Lookup func(ctx context.Context, addr netip.Addr) ([]string, error)
	// Now is the clock used for TTLs. Nil uses time.Now.
	Now func() time.Time
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

	mu       sync.Mutex
	entries  map[netip.Addr]entry
	inflight map[netip.Addr]struct{}

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
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	lookup := opts.Lookup
	if lookup == nil {
		lookup = resolverLookup(opts.Server)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache{
		lookup:   lookup,
		ttl:      opts.TTL,
		negTTL:   opts.NegativeTTL,
		timeout:  opts.Timeout,
		max:      opts.MaxEntries,
		now:      now,
		entries:  make(map[netip.Addr]entry),
		inflight: make(map[netip.Addr]struct{}),
		sem:      make(chan struct{}, opts.Concurrency),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// LookupName returns the cached PTR name for addr, or a miss. A miss schedules a
// background lookup (subject to the in-flight, worker, and capacity bounds) so a
// later sighting can be enriched. It never blocks on the network.
func (c *Cache) LookupName(addr netip.Addr) (string, bool) {
	c.mu.Lock()
	if e, ok := c.entries[addr]; ok && c.now().Before(e.expires) {
		name := e.name
		c.mu.Unlock()
		return name, name != ""
	}
	_, busy := c.inflight[addr]
	_, cached := c.entries[addr]
	// Skip when a lookup is already in flight, or when a brand-new address would
	// push the cache past its size bound (existing entries may still refresh).
	if busy || (!cached && len(c.entries) >= c.max) {
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
		c.entries[addr] = entry{expires: c.now().Add(c.negTTL)}
		return
	}
	c.entries[addr] = entry{name: strings.TrimSuffix(names[0], "."), expires: c.now().Add(c.ttl)}
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
