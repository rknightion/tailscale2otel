package enrich_test

import (
	"net/netip"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
)

// Flow log records embed the identity of both endpoints. Keeping that in a
// bounded unverified tier lets flow enrichment work without the devices
// collector — but the node wrote those fields itself, so it never becomes
// authoritative and never answers a plain lookup. The poisoning boundary itself
// is pinned in unverified_test.go; these cover the gap-filling behavior.

// With no devices collector, the record's own identity is all there is — and it
// is served, marked as unverified.
func TestUpsertUnverified_FillsEmptyCache(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertUnverified([]enrich.DeviceMeta{{
		NodeID:   "nA",
		Name:     "laptop.tail1a2b.ts.net",
		Hostname: "laptop",
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	name, prov := c.ResolveNameAny("100.64.0.1:443")
	if name != "laptop" || prov != enrich.ProvenanceUnverified {
		t.Errorf("ResolveNameAny = %q/%v, want %q/%v", name, prov, "laptop", enrich.ProvenanceUnverified)
	}
	if got := enrich.Mark(name, prov); got != "unverified:laptop" {
		t.Errorf("Mark = %q, want %q", got, "unverified:laptop")
	}
	m, prov, ok := c.LookupNodeAny("nA")
	if !ok {
		t.Fatal("LookupNodeAny(nA) missing after upsert")
	}
	if m.Hostname != "laptop" || prov != enrich.ProvenanceUnverified {
		t.Errorf("LookupNodeAny = %q/%v", m.Hostname, prov)
	}
	// The plain lookups stay control-plane-only.
	if got := c.ResolveName("100.64.0.1:443"); got != "unknown" {
		t.Errorf("ResolveName = %q, want %q", got, "unknown")
	}
	if _, ok := c.LookupNode("nA"); ok {
		t.Error("LookupNode returned an unverified entry")
	}
}

// The devices collector is authoritative. A flow record naming the same node
// must not clobber its richer entry, and must not even be stored — the
// authoritative tier answers for that node either way.
func TestUpsertUnverified_DoesNotClobberDevicesCollector(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.Replace([]enrich.DeviceMeta{{
		ID:        "dev-1",
		NodeID:    "nA",
		Hostname:  "laptop",
		OS:        "macOS",
		OSVersion: "15.2",
		Online:    true,
		Addrs:     []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	// Same node, poorer data, and a hostname that would be a regression.
	c.UpsertUnverified([]enrich.DeviceMeta{{
		NodeID:   "nA",
		Hostname: "laptop.tail1a2b.ts.net",
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	m, prov, ok := c.LookupNodeAny("nA")
	if !ok {
		t.Fatal("LookupNodeAny(nA) missing")
	}
	if prov != enrich.ProvenanceAuthoritative {
		t.Errorf("provenance = %v, want authoritative", prov)
	}
	if m.Hostname != "laptop" {
		t.Errorf("Hostname = %q, want the devices-collector value %q", m.Hostname, "laptop")
	}
	if m.OSVersion != "15.2" || m.ID != "dev-1" || !m.Online || m.Unverified {
		t.Errorf("devices-collector fields lost: %+v", m)
	}
	if n := c.LenUnverified(); n != 0 {
		t.Errorf("LenUnverified = %d, want 0 — a node the control plane knows needs no hint", n)
	}
}

// A later flow record may refine an earlier hint for the same node.
func TestUpsertUnverified_RefinesEarlierHint(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertUnverified([]enrich.DeviceMeta{{NodeID: "nA", Hostname: "old"}})
	c.UpsertUnverified([]enrich.DeviceMeta{{
		NodeID:   "nA",
		Hostname: "new",
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.9")},
	}})

	m, _, ok := c.LookupNodeAny("nA")
	if !ok {
		t.Fatal("LookupNodeAny(nA) missing")
	}
	if m.Hostname != "new" {
		t.Errorf("Hostname = %q, want %q", m.Hostname, "new")
	}
	if name, prov := c.ResolveNameAny("100.64.0.9"); name != "new" || prov != enrich.ProvenanceUnverified {
		t.Errorf("ResolveNameAny = %q/%v, want %q/unverified", name, prov, "new")
	}
	if n := c.LenUnverified(); n != 1 {
		t.Errorf("LenUnverified = %d, want 1 — refining must not add an entry", n)
	}
}

// A devices-collector Replace is a fresh control-plane view; it drops every
// hint, so a hint can never shadow a device the collector has since removed.
func TestUpsertUnverified_ReplaceClearsHints(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertUnverified([]enrich.DeviceMeta{{
		NodeID: "nGone", Hostname: "ghost",
		Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.5")},
	}})
	c.Replace([]enrich.DeviceMeta{{
		NodeID: "nA", Hostname: "laptop",
		Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	if _, _, ok := c.LookupNodeAny("nGone"); ok {
		t.Error("hint survived a devices-collector Replace")
	}
	if name, prov := c.ResolveNameAny("100.64.0.5"); prov != enrich.ProvenanceNone {
		t.Errorf("ResolveNameAny = %q/%v, want a miss", name, prov)
	}
}

// Entries with no node ID are unusable for lookup and must be skipped rather
// than creating an empty-keyed entry.
func TestUpsertUnverified_SkipsEntriesWithoutNodeID(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertUnverified([]enrich.DeviceMeta{{Hostname: "nameless"}})

	if _, _, ok := c.LookupNodeAny(""); ok {
		t.Error("entry with empty NodeID was cached")
	}
	if c.LenUnverified() != 0 {
		t.Errorf("LenUnverified = %d, want 0", c.LenUnverified())
	}
}

// One claimed identity must not be able to register an address range.
func TestUpsertUnverified_CapsAddressesPerEntry(t *testing.T) {
	c := enrich.NewDeviceCache()
	m := enrich.DeviceMeta{NodeID: "nEvil", Hostname: "evil"}
	for i := range 64 {
		m.Addrs = append(m.Addrs, netip.MustParseAddr("100.64.1."+itoa(i)))
	}
	c.UpsertUnverified([]enrich.DeviceMeta{m})

	var served int
	for i := range 64 {
		if _, prov := c.ResolveNameAny("100.64.1." + itoa(i)); prov == enrich.ProvenanceUnverified {
			served++
		}
	}
	if served > 8 {
		t.Errorf("%d addresses served for one claimed identity, want <= 8", served)
	}
	if served == 0 {
		t.Error("no addresses served at all — the cap must not drop the entry")
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
