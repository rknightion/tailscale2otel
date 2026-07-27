package objectstore_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
)

// dayOffsetsFetched maps each fetched object back to how many whole days before
// `now` its key is stamped, so a test can assert HOW FAR BACK ingestion reached
// rather than merely how many objects it read.
func dayOffsetsFetched(t *testing.T, keys []string) map[int]bool {
	t.Helper()
	const stamp = "2006-01-02-15-04-05"
	out := map[int]bool{}
	for _, k := range keys {
		if len(k) < len(stamp)+len(".ndjson") {
			t.Fatalf("unexpected key %q", k)
		}
		at, err := time.Parse(stamp, k[len(k)-len(stamp)-len(".ndjson"):len(k)-len(".ndjson")])
		if err != nil {
			t.Fatalf("parse %q: %v", k, err)
		}
		out[int(now.Sub(at).Hours()/24)] = true
	}
	return out
}

// putOneObjectPerDay writes one object per day for the last days days, using the
// key shape the given layout enumerates.
func putOneObjectPerDay(h *harness, layout objectstore.Layout, days int) {
	for d := 1; d <= days; d++ {
		at := now.Add(-time.Duration(d) * 24 * time.Hour)
		key := keyAt(at, ".ndjson")
		if layout == objectstore.LayoutFlat {
			key = "flow/" + at.UTC().Format("2006-01-02-15-04-05") + ".ndjson"
		}
		h.store.put(key, []byte(record("n", at)+"\n"))
	}
}

// THE MAXIMUM EFFECTIVE BACKFILL UNDER THE PARTITIONED LAYOUT IS maxDayPrefixes
// DAY PARTITIONS — 14, i.e. today plus the previous 13 days — no matter how large
// initial_lookback is set.
//
// This is the claim README and docs/configuration.md now state, and it is a
// PERMANENT ceiling rather than a per-cycle one: dayPrefixes walks backwards from
// the newest day so a capped span keeps the RECENT days, and the cursor only ever
// moves forward, so the days beyond the cap are never enumerated on any later
// cycle either. An operator who sets initial_lookback: 720h expecting 30 days of
// history silently gets 14 and no error, no warning from the engine, and no gap
// metric — which is exactly why config.Warnings advises against it (#463).
//
// Twelve cycles are driven so the test would fail if the reach grew over time.
func TestCollect_PartitionedBackfillIsCappedAtFourteenDayPartitions(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.InitialLookback = 30 * 24 * time.Hour
		o.MaxObjects = 1000
	})
	putOneObjectPerDay(h, objectstore.LayoutPartitioned, 30)

	for range 12 {
		h.collect(t)
	}

	days := dayOffsetsFetched(t, h.store.fetched)
	// Day offsets 1..13 are reachable; offset 0 (today) holds no object in this
	// fixture, so 13 of the 14 enumerated partitions carry one.
	if len(days) != 13 {
		t.Fatalf("reached %d distinct days, want 13 (the 14-partition cap less today): %v", len(days), days)
	}
	for d := 1; d <= 13; d++ {
		if !days[d] {
			t.Errorf("day offset %d was not ingested; the cap must keep the RECENT days", d)
		}
	}
	for d := 14; d <= 30; d++ {
		if days[d] {
			t.Errorf("day offset %d was ingested; the documented ceiling of 14 partitions is wrong", d)
		}
	}
}

// The flat layout has no day partitions to cap, so it reaches arbitrarily far
// back. That is the documented way to backfill more than two weeks, and it is why
// the flat advisory describes a cost (more LIST requests, higher discovery
// latency) rather than a limitation.
func TestCollect_FlatBackfillReachesBeyondTheDayPartitionCap(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Layout = objectstore.LayoutFlat
		o.InitialLookback = 30 * 24 * time.Hour
		o.MaxObjects = 1000
	})
	putOneObjectPerDay(h, objectstore.LayoutFlat, 30)

	for range 12 {
		h.collect(t)
	}

	days := dayOffsetsFetched(t, h.store.fetched)
	if len(days) != 30 {
		t.Fatalf("reached %d distinct days, want all 30 — flat has no partition cap: %v", len(days), days)
	}
	if !days[30] {
		t.Error("the oldest day was not reached; flat backfill is not bounded by maxDayPrefixes")
	}
}
