// Package enrich provides an in-memory cache that maps Tailscale addresses and
// node IDs to device metadata, used to enrich flow and audit records with
// human-readable device identity.
package enrich

import (
	"net/netip"
	"slices"
	"sync"
	"time"
)

// DeviceMeta is the normalized subset of a Tailscale device used for enrichment.
type DeviceMeta struct {
	// ID is the numeric/opaque device ID (tsapi.RichDevice.ID) — distinct from
	// NodeID, the control-plane node id used in flow logs. Node-metrics
	// discovery uses ID for the emitted HostID label (#85).
	ID        string
	NodeID    string
	Name      string // MagicDNS FQDN, e.g. "laptop.tail1a2b.ts.net"
	Hostname  string // short display name, e.g. "laptop"
	OS        string
	OSVersion string
	User      string
	Tags      []string
	Addrs     []netip.Addr
	External  bool // shared in from another tailnet
	// Online mirrors tsapi.RichDevice.ConnectedToControl. Node-metrics
	// discovery's online_only filter needs this when sourcing targets from the
	// cache instead of its own DevicesRich() call (#85).
	Online bool
	// Unverified marks an entry that came from the node identity embedded in a
	// flow log record rather than from the control plane's devices API. Tailscale
	// documents the embedded srcNode/dstNodes as node-produced, so a compromised
	// enrolled node can put anything it likes there; such an entry is a
	// best-effort hint only. Set by UpsertUnverified on the entries it stores;
	// callers never set it themselves, and it is always false on anything the
	// authoritative tier returns.
	Unverified bool
}

// Provenance says where a cache answer came from, so a consumer can decide
// whether to trust it and can label anything derived from it. See
// GHSA-pjfv-prc8-4fc9: flow-embedded identity is attacker-controllable, so an
// answer sourced from it must never be presented as control-plane truth.
type Provenance uint8

const (
	// ProvenanceNone means nothing was found; the returned value is a sentinel.
	ProvenanceNone Provenance = iota
	// ProvenanceAuthoritative means the answer came from the devices collector
	// (the Tailscale devices API) or the services collector.
	ProvenanceAuthoritative
	// ProvenanceUnverified means the answer came from identity a node embedded
	// in its own flow log records. Treat it as a hint, never as identity.
	ProvenanceUnverified
)

// String renders the provenance for logs and error messages.
func (p Provenance) String() string {
	switch p {
	case ProvenanceAuthoritative:
		return "authoritative"
	case ProvenanceUnverified:
		return "unverified"
	default:
		return "none"
	}
}

// UnverifiedPrefix marks a name that came from the unverified tier when it is
// used as a telemetry attribute VALUE (e.g. "unverified:laptop"). Provenance
// rides on the value rather than on a new attribute for two reasons: a separate
// dimension would multiply every flow series by the endpoints it labels, and a
// value that reads as a plain hostname on one series and a spoofable hint on the
// next is exactly the ambiguity this advisory is about. The value space it adds
// is bounded by MaxUnverifiedEntries.
const UnverifiedPrefix = "unverified:"

// Mark returns name tagged with the provenance p, so a spoofable hint is never
// indistinguishable from a control-plane name downstream. Authoritative names,
// sentinels and empty strings are returned unchanged.
func Mark(name string, p Provenance) string {
	if p != ProvenanceUnverified || name == "" {
		return name
	}
	return UnverifiedPrefix + name
}

// Bounds on the unverified tier. They are deliberately package constants, not
// configuration: they exist to cap what a compromised node can force this
// process to hold, and an operator lowering the ceiling is not a use case worth
// the attack surface of a knob.
const (
	// MaxUnverifiedEntries caps distinct flow-claimed node identities held at
	// once. Comfortably above any real tailnet's device count, small enough that
	// a node inventing an identity per record cannot grow the process.
	MaxUnverifiedEntries = 2048
	// maxUnverifiedAddrsPerEntry caps the addresses one claimed identity may
	// register. A real device has two (IPv4 + IPv6); the slack covers
	// multi-address nodes without letting one entry own an address range.
	maxUnverifiedAddrsPerEntry = 8
	// UnverifiedTTL is how long a flow-claimed identity is served for after it
	// was last seen. Flow logs for a live conversation arrive continuously, so a
	// hint that stops being refreshed is a hint that stopped being true.
	UnverifiedTTL = 30 * time.Minute
	// unverifiedEvictBatch is how many least-recently-seen entries one eviction
	// pass reclaims once the tier is full, so a flood pays one bounded sort per
	// batch rather than a scan per record.
	unverifiedEvictBatch = 256
	// unverifiedPruneInterval throttles the expiry sweep. UpsertUnverified runs
	// once per flow record — on a busy tailnet, thousands per second — and a
	// full sweep per call would be O(MaxUnverifiedEntries) on the ingest path.
	// Throttling costs nothing in correctness: every read re-checks the TTL, so
	// an expired entry is never served regardless of when it is swept, and the
	// memory ceiling is MaxUnverifiedEntries either way.
	unverifiedPruneInterval = time.Minute
)

// unverifiedEntry is one flow-claimed identity plus when it was last seen.
type unverifiedEntry struct {
	meta DeviceMeta
	seen time.Time
}

// DeviceCache maps Tailscale addresses and node IDs to device metadata.
// It is safe for concurrent use by multiple goroutines.
//
// It holds two strictly separated tiers:
//
//   - The AUTHORITATIVE tier (byAddr/byNode), written only by Replace from the
//     devices collector's Tailscale API poll. Every plain lookup — LookupAddr,
//     LookupNode, ResolveName, Snapshot, Len — reads this tier and nothing else.
//   - The UNVERIFIED tier (unvNode/unvAddr), written only by UpsertUnverified
//     from identity a node embedded in its own flow log records. It is bounded
//     (MaxUnverifiedEntries) and expiring (UnverifiedTTL), it is never promoted
//     into the authoritative tier, and it is served only through the explicit
//     *Any / *Unverified methods, which report Provenance so the caller can mark
//     what it derives. See GHSA-pjfv-prc8-4fc9.
type DeviceCache struct {
	mu      sync.RWMutex
	byAddr  map[netip.Addr]*DeviceMeta
	byNode  map[string]*DeviceMeta
	unvNode map[string]*unverifiedEntry
	unvAddr map[netip.Addr]string // address -> claimed node ID, into unvNode
	// unvPruned is when the unverified tier was last swept for expiry; the sweep
	// is throttled to unverifiedPruneInterval because it runs on the ingest path.
	unvPruned time.Time
	updated   time.Time
	now       func() time.Time

	// byService maps a Tailscale Service (VIP service) backing address to the
	// service's name (e.g. "svc:argocd"), so flow-log peers destined for a
	// service VIP resolve to the service name instead of falling through to
	// "unknown". Populated by ReplaceServices; consulted by ResolveName.
	byService map[netip.Addr]string
}

// Option configures a DeviceCache.
type Option func(*DeviceCache)

// WithClock overrides the time source. Used in tests for deterministic Age().
func WithClock(now func() time.Time) Option {
	return func(c *DeviceCache) { c.now = now }
}

// NewDeviceCache returns an empty cache ready for use.
func NewDeviceCache(opts ...Option) *DeviceCache {
	c := &DeviceCache{
		byAddr:    map[netip.Addr]*DeviceMeta{},
		byNode:    map[string]*DeviceMeta{},
		unvNode:   map[string]*unverifiedEntry{},
		unvAddr:   map[netip.Addr]string{},
		byService: map[netip.Addr]string{},
		now:       time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	c.updated = c.now()
	return c
}

// Replace atomically swaps the AUTHORITATIVE cache contents for the given set
// of devices. Only the devices collector calls this; it is the sole way an
// entry becomes authoritative. It builds the new indexes before taking the
// write lock to keep the critical section tiny.
//
// It also drops the whole unverified tier: a fresh control-plane view is the
// moment stale flow-claimed hints stop being worth anything, and dropping them
// stops a hint shadowing a device the control plane has since removed. Live
// conversations re-seed their own hints from the next flow record.
func (c *DeviceCache) Replace(metas []DeviceMeta) {
	byAddr := make(map[netip.Addr]*DeviceMeta, len(metas))
	byNode := make(map[string]*DeviceMeta, len(metas))
	for i := range metas {
		m := metas[i]
		m.Unverified = false // authoritative by construction
		byNode[m.NodeID] = &m
		for _, a := range m.Addrs {
			byAddr[a] = &m
		}
	}
	now := c.now()
	c.mu.Lock()
	c.byAddr = byAddr
	c.byNode = byNode
	c.unvNode = map[string]*unverifiedEntry{}
	c.unvAddr = map[netip.Addr]string{}
	c.updated = now
	c.mu.Unlock()
}

// UpsertUnverified records device identity a node embedded in its own flow log
// records, so flow enrichment still says something useful when the devices
// collector is disabled, rate-limited, or has not yet completed its first poll.
//
// This data is NOT trustworthy. Tailscale documents the embedded
// srcNode/dstNodes fields as produced by the reporting node, so a compromised
// enrolled node can claim any identity for any address (GHSA-pjfv-prc8-4fc9).
// It therefore lands in a separate tier that:
//
//   - never touches the authoritative tier, and is never promoted into it;
//   - is invisible to LookupAddr/LookupNode/ResolveName/Snapshot/Len, so every
//     existing consumer keeps control-plane-only semantics;
//   - is served only through ResolveNameAny/LookupNodeAny, which report
//     ProvenanceUnverified so the caller marks whatever it derives;
//   - is capped at MaxUnverifiedEntries identities, each with at most
//     maxUnverifiedAddrsPerEntry addresses, expiring UnverifiedTTL after it was
//     last seen — a node cannot grow it or park a claim in it;
//   - does NOT refresh Age(), so flow traffic cannot make a stale authoritative
//     cache look fresh.
//
// Entries with no NodeID are skipped (unusable for lookup), as are identities
// the authoritative tier already knows (it wins every lookup anyway, so storing
// them would only spend the budget). A later record may refine an earlier hint
// for the same node.
func (c *DeviceCache) UpsertUnverified(metas []DeviceMeta) {
	if len(metas) == 0 {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneUnverifiedLocked(now)
	for i := range metas {
		m := metas[i]
		if m.NodeID == "" {
			continue
		}
		if _, ok := c.byNode[m.NodeID]; ok {
			continue // the control plane already knows this node
		}
		if _, refresh := c.unvNode[m.NodeID]; !refresh {
			// New identity: enforce the ceiling, evicting the least recently seen
			// claims if that is what it takes to make room.
			if len(c.unvNode) >= MaxUnverifiedEntries {
				c.evictOldestUnverifiedLocked(unverifiedEvictBatch)
			}
			if len(c.unvNode) >= MaxUnverifiedEntries {
				continue
			}
		}
		addrs := m.Addrs
		if len(addrs) > maxUnverifiedAddrsPerEntry {
			addrs = addrs[:maxUnverifiedAddrsPerEntry]
		}
		m.Addrs = addrs
		m.Unverified = true
		c.dropUnverifiedAddrsLocked(m.NodeID)
		c.unvNode[m.NodeID] = &unverifiedEntry{meta: m, seen: now}
		for _, a := range addrs {
			if _, ok := c.byAddr[a]; ok {
				continue // never shadow an authoritative address
			}
			c.unvAddr[a] = m.NodeID
		}
	}
	// Deliberately does NOT set c.updated: Age() reports authoritative staleness.
}

// pruneUnverifiedLocked drops every expired unverified entry, at most once per
// unverifiedPruneInterval. Callers hold the write lock.
func (c *DeviceCache) pruneUnverifiedLocked(now time.Time) {
	if now.Sub(c.unvPruned) < unverifiedPruneInterval {
		return
	}
	c.unvPruned = now
	for id, e := range c.unvNode {
		if now.Sub(e.seen) >= UnverifiedTTL {
			c.dropUnverifiedAddrsLocked(id)
			delete(c.unvNode, id)
		}
	}
}

// dropUnverifiedAddrsLocked removes the address index entries pointing at id.
// Callers hold the write lock.
func (c *DeviceCache) dropUnverifiedAddrsLocked(id string) {
	e, ok := c.unvNode[id]
	if !ok {
		return
	}
	for _, a := range e.meta.Addrs {
		if c.unvAddr[a] == id {
			delete(c.unvAddr, a)
		}
	}
}

// evictOldestUnverifiedLocked removes up to n least recently seen unverified
// entries. Callers hold the write lock. It runs only once the tier is full, and
// evicts a batch rather than a single entry so a node flooding fresh identities
// pays one bounded O(MaxUnverifiedEntries log n) pass per batch instead of one
// full scan per record.
func (c *DeviceCache) evictOldestUnverifiedLocked(n int) {
	if n <= 0 || len(c.unvNode) == 0 {
		return
	}
	type aged struct {
		id   string
		seen time.Time
	}
	all := make([]aged, 0, len(c.unvNode))
	for id, e := range c.unvNode {
		all = append(all, aged{id, e.seen})
	}
	slices.SortFunc(all, func(a, b aged) int { return a.seen.Compare(b.seen) })
	if n > len(all) {
		n = len(all)
	}
	for _, a := range all[:n] {
		c.dropUnverifiedAddrsLocked(a.id)
		delete(c.unvNode, a.id)
	}
}

// lookupUnverifiedLocked returns the live (unexpired) unverified entry for id.
// Callers hold at least the read lock; expired entries are reported as missing
// and swept by the next UpsertUnverified rather than mutating under a read lock.
func (c *DeviceCache) lookupUnverifiedLocked(id string, now time.Time) (*unverifiedEntry, bool) {
	e, ok := c.unvNode[id]
	if !ok || now.Sub(e.seen) >= UnverifiedTTL {
		return nil, false
	}
	return e, true
}

// ReplaceServices atomically swaps the cached Tailscale Service (VIP service)
// address map: backing address -> service name (e.g. "svc:argocd"). It builds
// the new map before taking the write lock, mirroring Replace. A nil or empty
// map clears the service map (e.g. the services collector is disabled or its
// last fetch failed and the caller chooses to drop stale entries); callers
// that want to keep the previous map on a transient fetch failure should
// simply not call ReplaceServices for that tick.
func (c *DeviceCache) ReplaceServices(byAddr map[netip.Addr]string) {
	cp := make(map[netip.Addr]string, len(byAddr))
	for a, name := range byAddr {
		cp[a] = name
	}
	c.mu.Lock()
	c.byService = cp
	c.mu.Unlock()
}

// LookupAddr returns the AUTHORITATIVE device owning the given address, if
// cached. Flow-claimed identity is never returned here; ask for it explicitly
// via ResolveNameAny or LookupNodeAny.
func (c *DeviceCache) LookupAddr(a netip.Addr) (*DeviceMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byAddr[a]
	return m, ok
}

// LookupNode returns the AUTHORITATIVE device with the given node ID, if
// cached. Flow-claimed identity is never returned here; see LookupNodeAny.
func (c *DeviceCache) LookupNode(id string) (*DeviceMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byNode[id]
	return m, ok
}

// LookupNodeAny returns the device for id from the authoritative tier, falling
// back to an unverified flow-claimed hint, and reports which it found. A caller
// using this is opting in to attacker-controllable data: anything derived from
// a ProvenanceUnverified result must be marked as such downstream.
func (c *DeviceCache) LookupNodeAny(id string) (*DeviceMeta, Provenance, bool) {
	now := c.now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.byNode[id]; ok {
		return m, ProvenanceAuthoritative, true
	}
	if e, ok := c.lookupUnverifiedLocked(id, now); ok {
		m := e.meta
		return &m, ProvenanceUnverified, true
	}
	return nil, ProvenanceNone, false
}

// LenUnverified returns the number of live (unexpired) flow-claimed identities
// held in the unverified tier. Never more than MaxUnverifiedEntries.
func (c *DeviceCache) LenUnverified() int {
	now := c.now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, e := range c.unvNode {
		if now.Sub(e.seen) < UnverifiedTTL {
			n++
		}
	}
	return n
}

// SnapshotUnverified returns a copy of every live flow-claimed identity, for a
// read-only view that presents them AS unverified (e.g. a separate admin status
// table). It must never be merged into the authoritative device list.
func (c *DeviceCache) SnapshotUnverified() []DeviceMeta {
	now := c.now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]DeviceMeta, 0, len(c.unvNode))
	for _, e := range c.unvNode {
		if now.Sub(e.seen) < UnverifiedTTL {
			out = append(out, e.meta)
		}
	}
	return out
}

// ResolveName maps an "addr:port" (or bare address) to a device's short name
// using AUTHORITATIVE data only. A Service (VIP service) backing address that
// isn't also a known device resolves to the service name (e.g. "svc:argocd").
// Unrecognized Tailscale-range addresses resolve to "unknown"; addresses
// outside Tailscale's ranges resolve to "external".
//
// Flow-claimed identity is deliberately invisible here. A caller that wants it
// must use ResolveNameAny and handle the provenance it returns.
func (c *DeviceCache) ResolveName(addrPort string) string {
	name, _ := c.resolve(addrPort, false)
	return name
}

// ResolveNameAny is ResolveName plus the unverified tier, consulted only after
// the authoritative tier and the service map both miss. It returns the name and
// where it came from. A ProvenanceUnverified name was chosen by whichever node
// wrote the flow record — the caller MUST mark anything it derives from it, so
// a spoofed name is never indistinguishable from a control-plane one.
func (c *DeviceCache) ResolveNameAny(addrPort string) (string, Provenance) {
	return c.resolve(addrPort, true)
}

// resolve is the shared body of ResolveName/ResolveNameAny. unverified selects
// whether the flow-claimed tier is consulted on an authoritative miss.
func (c *DeviceCache) resolve(addrPort string, unverified bool) (string, Provenance) {
	addr, ok := parseAddr(addrPort)
	if !ok {
		return "unknown", ProvenanceNone
	}
	now := c.now()
	c.mu.RLock()
	m, found := c.byAddr[addr]
	svc, svcFound := c.byService[addr]
	var hint string
	if unverified && !found && !svcFound {
		if id, ok := c.unvAddr[addr]; ok {
			if e, live := c.lookupUnverifiedLocked(id, now); live {
				hint = e.meta.Hostname
			}
		}
	}
	c.mu.RUnlock()
	if found {
		return m.Hostname, ProvenanceAuthoritative
	}
	if svcFound {
		return svc, ProvenanceAuthoritative
	}
	if hint != "" {
		return hint, ProvenanceUnverified
	}
	if IsTailscaleAddr(addr) {
		return "unknown", ProvenanceNone // a tailnet address we don't (yet) have cached
	}
	return "external", ProvenanceNone // non-Tailscale address (exit-node / subnet-router traffic)
}

// Len returns the number of AUTHORITATIVE cached devices. Flow-claimed
// identities are counted by LenUnverified, so the enrich.cache_size gauge
// cannot be inflated by traffic.
func (c *DeviceCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byNode)
}

// Snapshot returns a copy of every AUTHORITATIVE cached device exactly once,
// suitable for
// rendering a read-only device table (e.g. an admin status page). It iterates
// byNode (one entry per device) rather than byAddr, so multi-address devices are
// not duplicated. Each element is a value copy of the cached *DeviceMeta;
// callers may freely read or replace top-level fields without affecting the
// cache. The copy is shallow: the Tags and Addrs slices are shared with the
// cached entry, which is acceptable for a read-only view.
func (c *DeviceCache) Snapshot() []DeviceMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]DeviceMeta, 0, len(c.byNode))
	for _, m := range c.byNode {
		out = append(out, *m)
	}
	return out
}

// Age returns how long ago the AUTHORITATIVE cache was last replaced. It is the
// staleness signal behind enrich.cache_age, so unverified upserts deliberately
// do not touch it: flow traffic must not be able to make a stalled devices
// collector look healthy.
func (c *DeviceCache) Age() time.Duration {
	c.mu.RLock()
	updated := c.updated
	c.mu.RUnlock()
	return c.now().Sub(updated)
}

// parseAddr accepts either "ip:port" or a bare "ip" and returns the address.
func parseAddr(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a, true
	}
	return netip.Addr{}, false
}

// Tailscale's address ranges: the IPv4 CGNAT block and the IPv6 ULA block.
var (
	tsCGNAT = netip.MustParsePrefix("100.64.0.0/10")
	tsULA   = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// IsTailscaleAddr reports whether a falls within Tailscale's address ranges
// (the IPv4 CGNAT block 100.64.0.0/10 and the ULA block fd7a:115c:a1e0::/48).
// Headscale's defaults match; custom Headscale prefixes outside these ranges
// are not recognized.
func IsTailscaleAddr(a netip.Addr) bool {
	return tsCGNAT.Contains(a) || tsULA.Contains(a)
}
