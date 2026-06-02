package enrich_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/enrich"
)

func TestResolveName_KnownTailscaleAddress(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{{
		NodeID:   "nABC",
		Name:     "laptop.tail1a2b.ts.net",
		Hostname: "laptop",
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	if got := c.ResolveName("100.64.0.1:443"); got != "laptop" {
		t.Fatalf("ResolveName(%q) = %q, want %q", "100.64.0.1:443", got, "laptop")
	}
}

func TestResolveName_ExternalAddress(t *testing.T) {
	c := enrich.NewDeviceCache()
	// A public, non-Tailscale address (e.g. exit-node/subnet traffic to the internet).
	if got := c.ResolveName("8.8.8.8:53"); got != "external" {
		t.Fatalf("ResolveName(external) = %q, want %q", got, "external")
	}
}

func TestResolveName_UnknownTailscaleAddress(t *testing.T) {
	c := enrich.NewDeviceCache()
	// Within the Tailscale CGNAT range (100.64.0.0/10) but not in the cache.
	if got := c.ResolveName("100.100.100.100:443"); got != "unknown" {
		t.Fatalf("ResolveName(unknown tailscale) = %q, want %q", got, "unknown")
	}
}

func TestResolveName_UnknownTailscaleIPv6(t *testing.T) {
	c := enrich.NewDeviceCache()
	// Within the Tailscale IPv6 ULA range (fd7a:115c:a1e0::/48) but not in the cache.
	if got := c.ResolveName("[fd7a:115c:a1e0::1]:443"); got != "unknown" {
		t.Fatalf("ResolveName(unknown tailscale v6) = %q, want %q", got, "unknown")
	}
}

func TestLookupNode(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{{NodeID: "n1", Hostname: "h1", User: "alice@example.com"}})

	m, ok := c.LookupNode("n1")
	if !ok {
		t.Fatal("LookupNode(n1) not found, want found")
	}
	if m.User != "alice@example.com" {
		t.Fatalf("LookupNode(n1).User = %q, want %q", m.User, "alice@example.com")
	}
	if _, ok := c.LookupNode("missing"); ok {
		t.Fatal("LookupNode(missing) found, want not found")
	}
}

func TestLookupAddr(t *testing.T) {
	c := enrich.NewDeviceCache()
	a := netip.MustParseAddr("100.64.0.9")
	c.Replace([]enrich.DeviceMeta{{NodeID: "n1", Hostname: "h", OS: "linux", Addrs: []netip.Addr{a}}})

	m, ok := c.LookupAddr(a)
	if !ok {
		t.Fatal("LookupAddr not found, want found")
	}
	if m.OS != "linux" {
		t.Fatalf("LookupAddr.OS = %q, want %q", m.OS, "linux")
	}
}

func TestReplaceOverwritesPriorContents(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{{NodeID: "n1", Hostname: "old", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.5")}}})
	c.Replace([]enrich.DeviceMeta{{NodeID: "n2", Hostname: "new", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.6")}}})

	if got := c.ResolveName("100.64.0.5:1"); got != "unknown" {
		t.Fatalf("stale address still resolves: got %q, want unknown", got)
	}
	if got := c.ResolveName("100.64.0.6:1"); got != "new" {
		t.Fatalf("new address resolves to %q, want new", got)
	}
	if _, ok := c.LookupNode("n1"); ok {
		t.Fatal("stale node n1 still present after Replace")
	}
}

func TestLenReflectsDeviceCount(t *testing.T) {
	c := enrich.NewDeviceCache()
	if c.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", c.Len())
	}
	c.Replace([]enrich.DeviceMeta{{NodeID: "n1"}, {NodeID: "n2"}})
	if c.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", c.Len())
	}
}

func TestAgeUsesInjectedClock(t *testing.T) {
	now := time.Unix(1000, 0)
	c := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	c.Replace(nil) // records the update time as now=1000
	now = time.Unix(1042, 0)
	if got := c.Age(); got != 42*time.Second {
		t.Fatalf("Age() = %v, want 42s", got)
	}
}
