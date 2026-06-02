// Package enrich provides an in-memory cache that maps Tailscale addresses and
// node IDs to device metadata, used to enrich flow and audit records with
// human-readable device identity.
package enrich

import (
	"net/netip"
	"sync"
	"time"
)

// DeviceMeta is the normalized subset of a Tailscale device used for enrichment.
type DeviceMeta struct {
	NodeID    string
	Name      string // MagicDNS FQDN, e.g. "laptop.tail1a2b.ts.net"
	Hostname  string // short display name, e.g. "laptop"
	OS        string
	OSVersion string
	User      string
	Tags      []string
	Addrs     []netip.Addr
	External  bool // shared in from another tailnet
}

// DeviceCache maps Tailscale addresses and node IDs to device metadata.
// It is safe for concurrent use by multiple goroutines.
type DeviceCache struct {
	mu      sync.RWMutex
	byAddr  map[netip.Addr]*DeviceMeta
	byNode  map[string]*DeviceMeta
	updated time.Time
	now     func() time.Time
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
		byAddr: map[netip.Addr]*DeviceMeta{},
		byNode: map[string]*DeviceMeta{},
		now:    time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	c.updated = c.now()
	return c
}

// Replace atomically swaps the cache contents for the given set of devices.
// It builds the new indexes before taking the write lock to keep the critical
// section tiny.
func (c *DeviceCache) Replace(metas []DeviceMeta) {
	byAddr := make(map[netip.Addr]*DeviceMeta, len(metas))
	byNode := make(map[string]*DeviceMeta, len(metas))
	for i := range metas {
		m := metas[i]
		byNode[m.NodeID] = &m
		for _, a := range m.Addrs {
			byAddr[a] = &m
		}
	}
	now := c.now()
	c.mu.Lock()
	c.byAddr = byAddr
	c.byNode = byNode
	c.updated = now
	c.mu.Unlock()
}

// LookupAddr returns the device owning the given address, if cached.
func (c *DeviceCache) LookupAddr(a netip.Addr) (*DeviceMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byAddr[a]
	return m, ok
}

// LookupNode returns the device with the given node ID, if cached.
func (c *DeviceCache) LookupNode(id string) (*DeviceMeta, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.byNode[id]
	return m, ok
}

// ResolveName maps an "addr:port" (or bare address) to a device's short name.
// Unrecognized Tailscale-range addresses resolve to "unknown"; addresses
// outside Tailscale's ranges resolve to "external".
func (c *DeviceCache) ResolveName(addrPort string) string {
	addr, ok := parseAddr(addrPort)
	if !ok {
		return "unknown"
	}
	c.mu.RLock()
	m, found := c.byAddr[addr]
	c.mu.RUnlock()
	if found {
		return m.Hostname
	}
	if isTailscaleAddr(addr) {
		return "unknown" // a tailnet address we don't (yet) have cached
	}
	return "external" // non-Tailscale address (exit-node / subnet-router traffic)
}

// Len returns the number of cached devices.
func (c *DeviceCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byNode)
}

// Age returns how long ago the cache was last replaced.
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

// isTailscaleAddr reports whether a falls within Tailscale's address ranges.
func isTailscaleAddr(a netip.Addr) bool {
	return tsCGNAT.Contains(a) || tsULA.Contains(a)
}
