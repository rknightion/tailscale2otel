package enrich_test

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
)

// GHSA-pjfv-prc8-4fc9. The srcNode/dstNodes identity embedded in a network flow
// log is produced by the reporting node, not by the control plane, so a
// compromised enrolled node can put anything it likes in it. Folding that into
// the shared device cache let it decide what later records, the node-metrics
// scrape target list and the admin device table say about a tailnet.
//
// These tests pin the boundary: embedded flow identity lives in a separate,
// bounded, TTL'd tier that never becomes authoritative and that every consumer
// must ask for by name.

// spoof is the identity a compromised node claims for itself.
func spoof(nodeID, hostname string, addrs ...string) enrich.DeviceMeta {
	m := enrich.DeviceMeta{NodeID: nodeID, Hostname: hostname, Name: hostname + ".tail1a2b.ts.net"}
	for _, a := range addrs {
		m.Addrs = append(m.Addrs, netip.MustParseAddr(a))
	}
	return m
}

// The authoritative answer for an address or node must be the devices
// collector's, and only the devices collector's — whether the spoof arrives
// before or after the authoritative refresh.
func TestUnverified_NeverChangesAuthoritativeLookup(t *testing.T) {
	for _, order := range []string{"spoof-first", "spoof-last"} {
		t.Run(order, func(t *testing.T) {
			c := enrich.NewDeviceCache()
			authoritative := []enrich.DeviceMeta{{
				ID: "dev-1", NodeID: "nA", Hostname: "laptop", Name: "laptop.tail1a2b.ts.net",
				OS: "macOS", OSVersion: "15.2", Online: true,
				Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
			}}
			poison := []enrich.DeviceMeta{
				spoof("nA", "evil", "100.64.0.1"),      // same node, lying about itself
				spoof("nEvil", "evil", "100.64.0.1"),   // different node, claiming A's address
				spoof("nGhost", "ghost", "100.64.0.9"), // a node that does not exist
			}

			if order == "spoof-first" {
				c.UpsertUnverified(poison)
				c.Replace(authoritative)
			} else {
				c.Replace(authoritative)
				c.UpsertUnverified(poison)
			}

			m, ok := c.LookupNode("nA")
			if !ok {
				t.Fatal("LookupNode(nA) missing")
			}
			if m.Hostname != "laptop" || m.OSVersion != "15.2" || m.ID != "dev-1" || !m.Online {
				t.Errorf("authoritative entry poisoned: %+v", m)
			}
			if got := c.ResolveName("100.64.0.1:443"); got != "laptop" {
				t.Errorf("ResolveName = %q, want the authoritative %q", got, "laptop")
			}
			if got, ok := c.LookupNode("nEvil"); ok {
				t.Errorf("LookupNode(nEvil) returned an unverified entry %+v — authoritative lookups must never see it", got)
			}
			if got, ok := c.LookupNode("nGhost"); ok {
				t.Errorf("LookupNode(nGhost) returned an unverified entry %+v", got)
			}
			if got, ok := c.LookupAddr(netip.MustParseAddr("100.64.0.9")); ok {
				t.Errorf("LookupAddr(100.64.0.9) returned an unverified entry %+v", got)
			}
			if got := c.ResolveName("100.64.0.9:443"); got != "unknown" {
				t.Errorf("ResolveName(unverified addr) = %q, want %q", got, "unknown")
			}
			if c.Len() != 1 {
				t.Errorf("Len = %d, want 1 — unverified entries must not count as devices", c.Len())
			}
		})
	}
}

// Snapshot feeds the admin status device table AND the node-metrics scrape
// target list. A node that can add rows to either picks what the operator sees
// and what the exporter connects out to.
func TestUnverified_NeverAppearsInSnapshot(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{{NodeID: "nA", Hostname: "laptop"}})
	c.UpsertUnverified([]enrich.DeviceMeta{spoof("nEvil", "evil", "100.64.0.9")})

	snap := c.Snapshot()
	if len(snap) != 1 || snap[0].NodeID != "nA" {
		t.Fatalf("Snapshot = %+v, want only the authoritative device", snap)
	}
}

// Anything the unverified tier does answer must say so, so a consumer cannot
// mistake it for control-plane truth.
func TestUnverified_LookupsCarryProvenance(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{{
		NodeID: "nA", Hostname: "laptop",
		Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})
	c.UpsertUnverified([]enrich.DeviceMeta{spoof("nEvil", "evil", "100.64.0.9")})

	if _, prov := c.ResolveNameAny("100.64.0.1:443"); prov != enrich.ProvenanceAuthoritative {
		t.Errorf("authoritative provenance = %v, want %v", prov, enrich.ProvenanceAuthoritative)
	}
	name, prov := c.ResolveNameAny("100.64.0.9:443")
	if name != "evil" || prov != enrich.ProvenanceUnverified {
		t.Errorf("ResolveNameAny(unverified) = %q/%v, want %q/%v", name, prov, "evil", enrich.ProvenanceUnverified)
	}
	if _, prov := c.ResolveNameAny("8.8.8.8:53"); prov != enrich.ProvenanceNone {
		t.Errorf("miss provenance = %v, want %v", prov, enrich.ProvenanceNone)
	}

	if _, prov, ok := c.LookupNodeAny("nA"); !ok || prov != enrich.ProvenanceAuthoritative {
		t.Errorf("LookupNodeAny(nA) = %v/%v, want authoritative", prov, ok)
	}
	m, prov, ok := c.LookupNodeAny("nEvil")
	if !ok || prov != enrich.ProvenanceUnverified || m.Hostname != "evil" {
		t.Errorf("LookupNodeAny(nEvil) = %+v/%v/%v, want the unverified hint", m, prov, ok)
	}
	if !m.Unverified {
		t.Error("returned DeviceMeta.Unverified = false, want true")
	}
}

// A compromised node emitting a fresh identity per record must not be able to
// grow process memory without limit. The devices collector's Replace is the
// only thing that used to clear these, and it never runs when the collector is
// disabled — which is exactly when the unverified tier is used at all.
func TestUnverified_TierIsBounded(t *testing.T) {
	c := enrich.NewDeviceCache()
	for i := range 50000 {
		s := strconv.Itoa(i)
		c.UpsertUnverified([]enrich.DeviceMeta{spoof("n"+s, "h"+s, "100.64."+strconv.Itoa(i%200)+"."+strconv.Itoa(i%250))})
	}
	if n := c.LenUnverified(); n > enrich.MaxUnverifiedEntries {
		t.Errorf("LenUnverified = %d, want <= %d", n, enrich.MaxUnverifiedEntries)
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0 — nothing authoritative was ever added", c.Len())
	}
}

// A spoofed hint must age out on its own, not linger until some other event
// happens to clear it.
func TestUnverified_ExpiresAfterTTL(t *testing.T) {
	now := time.Now()
	c := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	c.UpsertUnverified([]enrich.DeviceMeta{spoof("nEvil", "evil", "100.64.0.9")})

	if _, prov := c.ResolveNameAny("100.64.0.9"); prov != enrich.ProvenanceUnverified {
		t.Fatalf("hint not stored: provenance = %v", prov)
	}
	now = now.Add(enrich.UnverifiedTTL + time.Second)
	if name, prov := c.ResolveNameAny("100.64.0.9"); prov != enrich.ProvenanceNone {
		t.Errorf("expired hint still served: %q/%v", name, prov)
	}
	if _, _, ok := c.LookupNodeAny("nEvil"); ok {
		t.Error("expired hint still returned by LookupNodeAny")
	}
	if n := c.LenUnverified(); n != 0 {
		t.Errorf("LenUnverified = %d after expiry, want 0", n)
	}
}

// enrich.cache_age is the staleness signal for the AUTHORITATIVE cache. If flow
// traffic refreshes it, a node can keep the cache looking fresh while the
// devices collector is broken.
func TestUnverified_DoesNotRefreshCacheAge(t *testing.T) {
	now := time.Now()
	c := enrich.NewDeviceCache(enrich.WithClock(func() time.Time { return now }))
	c.Replace([]enrich.DeviceMeta{{NodeID: "nA", Hostname: "laptop"}})

	now = now.Add(time.Hour)
	c.UpsertUnverified([]enrich.DeviceMeta{spoof("nEvil", "evil", "100.64.0.9")})

	if age := c.Age(); age < time.Hour {
		t.Errorf("Age = %v, want >= 1h — an unverified upsert must not mask a stale authoritative cache", age)
	}
}
