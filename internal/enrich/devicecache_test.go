package enrich_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
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

func TestDeviceCache_Snapshot(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{
		{
			ID:       "12345",
			NodeID:   "n1",
			Name:     "laptop.tail1a2b.ts.net",
			Hostname: "laptop",
			OS:       "linux",
			User:     "alice@example.com",
			Tags:     []string{"tag:server"},
			Online:   true,
			Addrs: []netip.Addr{
				netip.MustParseAddr("100.64.0.1"),
				netip.MustParseAddr("fd7a:115c:a1e0::1"),
			},
		},
		{
			NodeID:   "n2",
			Hostname: "phone",
			OS:       "ios",
			Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.2")},
		},
	})

	snap := c.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot() returned %d devices, want 2", len(snap))
	}

	// Each device must appear exactly once (keyed by node ID), with expected fields.
	byNode := map[string]enrich.DeviceMeta{}
	for _, m := range snap {
		if _, dup := byNode[m.NodeID]; dup {
			t.Fatalf("Snapshot() returned node %q more than once", m.NodeID)
		}
		byNode[m.NodeID] = m
	}

	n1, ok := byNode["n1"]
	if !ok {
		t.Fatal("Snapshot() missing node n1")
	}
	if n1.Hostname != "laptop" || n1.OS != "linux" || n1.User != "alice@example.com" {
		t.Fatalf("Snapshot() n1 = %+v, want laptop/linux/alice", n1)
	}
	if n1.ID != "12345" {
		t.Fatalf("Snapshot() n1.ID = %q, want 12345", n1.ID)
	}
	if !n1.Online {
		t.Fatal("Snapshot() n1.Online = false, want true")
	}
	if len(n1.Addrs) != 2 {
		t.Fatalf("Snapshot() n1 has %d addrs, want 2", len(n1.Addrs))
	}
	if _, ok := byNode["n2"]; !ok {
		t.Fatal("Snapshot() missing node n2")
	}

	// Mutating the returned slice/entries must not affect the cache (copies).
	snap[0].Hostname = "MUTATED"
	if m, _ := c.LookupNode(snap[0].NodeID); m == nil || m.Hostname == "MUTATED" {
		t.Fatal("mutating Snapshot() entry affected the cache; want isolated copies")
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

func TestIsTailscaleAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"100.64.0.1", true},         // CGNAT low edge
		{"100.127.255.254", true},    // CGNAT high edge
		{"100.63.255.255", false},    // just below CGNAT
		{"100.128.0.0", false},       // just above CGNAT
		{"fd7a:115c:a1e0::1", true},  // Tailscale ULA
		{"fd7a:115c:a1e1::1", false}, // adjacent ULA /48
		{"169.254.169.254", false},   // cloud metadata
		{"127.0.0.1", false},
		{"10.0.0.1", false},
	}
	for _, tc := range cases {
		if got := enrich.IsTailscaleAddr(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("IsTailscaleAddr(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestResolveName_ServiceVIP(t *testing.T) {
	c := enrich.NewDeviceCache()
	vip := netip.MustParseAddr("100.124.43.64")
	c.ReplaceServices(map[netip.Addr]string{vip: "svc:argocd"})

	if got := c.ResolveName("100.124.43.64:443"); got != "svc:argocd" {
		t.Fatalf("ResolveName(service VIP) = %q, want %q", got, "svc:argocd")
	}
	// Bare address (no port) resolves the same way.
	if got := c.ResolveName("100.124.43.64"); got != "svc:argocd" {
		t.Fatalf("ResolveName(bare service VIP) = %q, want %q", got, "svc:argocd")
	}
}

func TestResolveName_DeviceWinsOverServiceOnAddressCollision(t *testing.T) {
	// A device hit takes priority over a service-VIP hit at the same address
	// (shouldn't normally collide, but the device index is authoritative).
	c := enrich.NewDeviceCache()
	addr := netip.MustParseAddr("100.64.0.1")
	c.Replace([]enrich.DeviceMeta{{NodeID: "n1", Hostname: "laptop", Addrs: []netip.Addr{addr}}})
	c.ReplaceServices(map[netip.Addr]string{addr: "svc:argocd"})

	if got := c.ResolveName("100.64.0.1:443"); got != "laptop" {
		t.Fatalf("ResolveName(collision) = %q, want device hostname %q", got, "laptop")
	}
}

func TestResolveName_UnknownWhenNoServiceCached(t *testing.T) {
	c := enrich.NewDeviceCache()
	// A CGNAT address with no device and no service entry still falls through
	// to "unknown", not a stale/zero-value service name.
	if got := c.ResolveName("100.100.100.100:443"); got != "unknown" {
		t.Fatalf("ResolveName(no service) = %q, want unknown", got)
	}
}

func TestReplaceServicesOverwritesPriorContents(t *testing.T) {
	c := enrich.NewDeviceCache()
	a := netip.MustParseAddr("100.124.43.64")
	b := netip.MustParseAddr("100.69.161.118")
	c.ReplaceServices(map[netip.Addr]string{a: "svc:argocd"})
	c.ReplaceServices(map[netip.Addr]string{b: "svc:grpc"})

	if got := c.ResolveName("100.124.43.64:1"); got != "unknown" {
		t.Fatalf("stale service address still resolves: got %q, want unknown", got)
	}
	if got := c.ResolveName("100.69.161.118:1"); got != "svc:grpc" {
		t.Fatalf("new service address resolves to %q, want svc:grpc", got)
	}
}

func TestReplaceServicesIsIndependentOfDeviceReplace(t *testing.T) {
	// Replace() (devices) must not clear the service map, and vice versa —
	// they are separate indexes populated by separate collectors.
	c := enrich.NewDeviceCache()
	svcAddr := netip.MustParseAddr("100.124.43.64")
	devAddr := netip.MustParseAddr("100.64.0.1")
	c.ReplaceServices(map[netip.Addr]string{svcAddr: "svc:argocd"})
	c.Replace([]enrich.DeviceMeta{{NodeID: "n1", Hostname: "laptop", Addrs: []netip.Addr{devAddr}}})

	if got := c.ResolveName("100.124.43.64:1"); got != "svc:argocd" {
		t.Fatalf("service entry cleared by device Replace: got %q, want svc:argocd", got)
	}
	if got := c.ResolveName("100.64.0.1:1"); got != "laptop" {
		t.Fatalf("device entry missing: got %q, want laptop", got)
	}

	c.ReplaceServices(nil)
	if got := c.ResolveName("100.64.0.1:1"); got != "laptop" {
		t.Fatalf("device entry cleared by service ReplaceServices(nil): got %q, want laptop", got)
	}
}
