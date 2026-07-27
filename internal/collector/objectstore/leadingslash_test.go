package objectstore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
)

// A configured prefix with a LEADING slash must keep its durable listing
// progress. It used to be erased on every cycle: the cycle listed
// "/flow/YYYY/MM/DD/" while the loader looked for a row under "flow/", so the
// scan position was classified stale and DELETED by the cycle that loaded it.
// Partitioned enumeration then restarted at the beginning of the day partition
// forever, and a backlog wider than one budget could never advance — with every
// health signal staying green (#498).
//
// This drives the whole collector across two cycles rather than the predicate in
// isolation, so it fails if ANY part of the chain stops agreeing on the prefix.
func TestCollect_LeadingSlashPrefixRetainsDurableListingProgress(t *testing.T) {
	const base = "/flow"
	h := newHarness(t, func(o *objectstore.Options) {
		o.Prefix = base
		// One object per cycle, so a scan position must be written and read back
		// for the second cycle to reach the second object at all.
		o.MaxObjects = 1
	})
	// Three objects in one day partition, oldest first.
	for _, ago := range []time.Duration{30 * time.Minute, 20 * time.Minute, 10 * time.Minute} {
		at := now.Add(-ago)
		key := base + "/" + at.UTC().Format("2006/01/02") + "/" +
			at.UTC().Format("2006-01-02-15-04-05") + ".ndjson"
		h.store.put(key, []byte(record("n", at)+"\n"))
	}

	h.collect(t)

	if len(h.store.listCalls) != 1 {
		t.Fatalf("list calls = %+v, want one", h.store.listCalls)
	}
	if got := h.store.listCalls[0].Prefix; !strings.HasPrefix(got, base+"/") {
		t.Fatalf("listed prefix = %q, want it under the configured %q", got, base)
	}
	if got := h.flowRecords(); got != 1 {
		t.Fatalf("records after cycle 1 = %d, want the one the budget allowed", got)
	}

	h.collect(t)

	if len(h.store.listCalls) != 2 {
		t.Fatalf("list calls = %+v, want two", h.store.listCalls)
	}
	if got := h.store.listCalls[1].StartAfter; got == "" {
		t.Fatalf("cycle 2 restarted from the beginning of the day partition: the durable scan "+
			"position for a leading-slash prefix was erased. calls = %+v", h.store.listCalls)
	}
	// Progress, not a repeat: the second cycle ingested the NEXT object rather
	// than re-reading the first.
	if got := h.flowRecords(); got != 2 {
		t.Errorf("records after cycle 2 = %d, want 2 — one new object, no repeat", got)
	}
	if len(h.store.fetched) != 2 || h.store.fetched[0] == h.store.fetched[1] {
		t.Errorf("fetched = %v, want two distinct objects", h.store.fetched)
	}
}
