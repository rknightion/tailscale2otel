package enrich_test

import (
	"net/netip"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
)

// Flow log records embed the identity of both endpoints. Folding that into the
// cache lets flow enrichment work without the devices collector — but it must
// never degrade what the devices collector already knows, since flow records
// carry a strict subset of the fields (no OS version, no online state, no
// device ID).
func TestUpsertFromFlow_FillsEmptyCache(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertFromFlow([]enrich.DeviceMeta{{
		NodeID:   "nA",
		Name:     "laptop.tail1a2b.ts.net",
		Hostname: "laptop",
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	if got := c.ResolveName("100.64.0.1:443"); got != "laptop" {
		t.Errorf("ResolveName = %q, want %q", got, "laptop")
	}
	m, ok := c.LookupNode("nA")
	if !ok {
		t.Fatal("LookupNode(nA) missing after upsert")
	}
	if m.Hostname != "laptop" {
		t.Errorf("Hostname = %q, want %q", m.Hostname, "laptop")
	}
}

// The devices collector is authoritative. A flow record naming the same node
// must not clobber its richer entry.
func TestUpsertFromFlow_DoesNotClobberDevicesCollector(t *testing.T) {
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
	c.UpsertFromFlow([]enrich.DeviceMeta{{
		NodeID:   "nA",
		Hostname: "laptop.tail1a2b.ts.net",
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	m, ok := c.LookupNode("nA")
	if !ok {
		t.Fatal("LookupNode(nA) missing")
	}
	if m.Hostname != "laptop" {
		t.Errorf("Hostname = %q, want the devices-collector value %q", m.Hostname, "laptop")
	}
	if m.OSVersion != "15.2" || m.ID != "dev-1" || !m.Online {
		t.Errorf("devices-collector fields lost: %+v", m)
	}
}

// A later flow record may refine an earlier flow-derived entry — only
// devices-collector entries are protected.
func TestUpsertFromFlow_RefinesEarlierFlowEntry(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertFromFlow([]enrich.DeviceMeta{{NodeID: "nA", Hostname: "old"}})
	c.UpsertFromFlow([]enrich.DeviceMeta{{
		NodeID:   "nA",
		Hostname: "new",
		Addrs:    []netip.Addr{netip.MustParseAddr("100.64.0.9")},
	}})

	m, ok := c.LookupNode("nA")
	if !ok {
		t.Fatal("LookupNode(nA) missing")
	}
	if m.Hostname != "new" {
		t.Errorf("Hostname = %q, want %q", m.Hostname, "new")
	}
	if got := c.ResolveName("100.64.0.9"); got != "new" {
		t.Errorf("ResolveName = %q, want %q", got, "new")
	}
}

// A devices-collector Replace is a full swap and legitimately drops
// flow-derived entries; the next flow record re-adds them. What must NOT happen
// is a flow-derived entry surviving as a stale shadow of a device the collector
// has since dropped.
func TestUpsertFromFlow_ReplaceClearsFlowEntries(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertFromFlow([]enrich.DeviceMeta{{
		NodeID: "nGone", Hostname: "ghost",
		Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.5")},
	}})
	c.Replace([]enrich.DeviceMeta{{
		NodeID: "nA", Hostname: "laptop",
		Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
	}})

	if _, ok := c.LookupNode("nGone"); ok {
		t.Error("flow-derived entry survived a devices-collector Replace")
	}
}

// Entries with no node ID are unusable for lookup and must be skipped rather
// than creating an empty-keyed entry.
func TestUpsertFromFlow_SkipsEntriesWithoutNodeID(t *testing.T) {
	c := enrich.NewDeviceCache()
	c.UpsertFromFlow([]enrich.DeviceMeta{{Hostname: "nameless"}})

	if _, ok := c.LookupNode(""); ok {
		t.Error("entry with empty NodeID was cached")
	}
	if c.Len() != 0 {
		t.Errorf("Len = %d, want 0", c.Len())
	}
}
