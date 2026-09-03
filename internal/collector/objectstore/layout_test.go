package objectstore_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// flatKeyAt builds a FLAT copied-export key: the self-contained legacy basename
// sitting directly under the configured prefix, with no YYYY/MM/DD partitions
// above it. This is the shape parseKey has always accepted and enumeration never
// listed (#293).
func flatKeyAt(at time.Time, ext string) string {
	return "flow/" + at.UTC().Format("2006-01-02-15-04-05") + ext
}

func TestCollect_FlatLayoutIngestsASelfContainedBasename(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.Layout = objectstore.LayoutFlat })
	at := now.Add(-10 * time.Minute)
	h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Fatalf("flow records = %d, want 1 — the flat object was never discovered", got)
	}
	if len(h.store.listCalls) != 1 {
		t.Fatalf("list calls = %+v, want exactly one listing of the base prefix", h.store.listCalls)
	}
	if got := h.store.listCalls[0].Prefix; got != "flow/" {
		t.Fatalf("listed prefix = %q, want the normalized base prefix %q", got, "flow/")
	}
}

// A flat prefix spans the WHOLE export, so it is never "committed ground" the way
// a mid-drain day partition is. The seen set is pruned 2*lookback behind the
// cursor, so an object too old to be inside the listing window is also too old to
// be remembered — if the walk kept treating those as candidates while a scan
// position was active, every wrap of the walk would re-fetch and re-emit the same
// history forever. The lower bound is therefore absolute in flat mode.
func TestCollect_FlatLayoutDoesNotReIngestHistoryOnEveryWrap(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Layout = objectstore.LayoutFlat
		o.MaxObjects = 2
		o.InitialLookback = 30 * time.Minute
		o.Lookback = 10 * time.Minute
	})
	// Ten objects far older than the cold-start bound, then two inside it. Ten is
	// more than one page (MaxObjects*4 = 8), so the walk is truncated and a durable
	// scan position is written — the state that made the history re-ingest.
	for i := 12; i >= 3; i-- {
		at := now.Add(-time.Duration(i) * time.Hour)
		h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("old", at)+"\n"))
	}
	for _, ago := range []time.Duration{20 * time.Minute, 10 * time.Minute} {
		at := now.Add(-ago)
		h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("new", at)+"\n"))
	}

	for range 8 {
		h.collect(t)
	}

	if got := h.flowRecords(); got != 2 {
		t.Errorf("flow records after eight cycles = %d, want the 2 objects inside the cold-start bound", got)
	}
	fetches := map[string]int{}
	for _, key := range h.store.fetched {
		fetches[key]++
	}
	for key, n := range fetches {
		if n != 1 {
			t.Errorf("object %q fetched %d times, want exactly once", key, n)
		}
	}
	if len(fetches) != 2 {
		t.Errorf("distinct objects fetched = %d (%v), want only the 2 inside the cold-start bound",
			len(fetches), h.store.fetched)
	}
}

// The defect this closes, pinned so it cannot come back: under the default layout
// a flat object is not merely unparseable, it is never LISTED at all — so it is
// invisible even in the skip counters. The parser accepting it bought nothing.
func TestCollect_PartitionedLayoutNeverListsAFlatObject(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 0 {
		t.Fatalf("flow records = %d, want 0 under the partitioned default", got)
	}
	if skips := skippedByReason(h.rec); len(skips) != 0 {
		t.Fatalf("skips = %v, want none — the object is never listed, so nothing counts it", skips)
	}
	for _, call := range h.store.listCalls {
		if !strings.HasPrefix(call.Prefix, "flow/20") {
			t.Fatalf("listed prefix %q, want only YYYY/MM/DD day partitions", call.Prefix)
		}
	}
}

// Listing the base prefix with no delimiter also returns everything beneath the
// day partitions, and parseKey handles four-or-more-component keys, so flat is a
// SUPERSET: one bucket holding both shapes is fully ingested.
func TestCollect_FlatLayoutIngestsMixedLayouts(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.Layout = objectstore.LayoutFlat })
	flatAt := now.Add(-12 * time.Minute)
	partitionedAt := now.Add(-10 * time.Minute)
	flatKey := flatKeyAt(flatAt, ".ndjson.zst")
	partitionedKey := officialKeyAt(partitionedAt, ".json")
	h.store.put(flatKey, zstdBytes(t, record("flat", flatAt)+"\n"))
	h.store.put(partitionedKey, []byte(record("partitioned", partitionedAt)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Fatalf("flow records = %d, want both the flat and the partitioned object", got)
	}
	fetched := map[string]bool{}
	for _, key := range h.store.fetched {
		fetched[key] = true
	}
	if !fetched[flatKey] || !fetched[partitionedKey] {
		t.Fatalf("fetched = %v, want both %q and %q", h.store.fetched, flatKey, partitionedKey)
	}
	if len(h.store.listCalls) != 1 {
		t.Fatalf("list calls = %+v, want one undelimited listing to cover both shapes", h.store.listCalls)
	}
}

// The official basename carries only a TIME; its date comes from the three
// directory components above it. Directly under a flat root there are none, so
// there is no timestamp to place against the cursor. Guessing one would either
// ingest it and never look again, or skip it forever — so it stays a visible skip.
func TestCollect_FlatLayoutOfficialBasenameAtTheRootIsUnrecognized(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.Layout = objectstore.LayoutFlat })
	at := now.Add(-10 * time.Minute)
	h.store.put("flow/"+at.UTC().Format("15:04:05")+".json", []byte(record("n1", at)+"\n"))

	h.collect(t)

	if got := skippedByReason(h.rec)["unrecognized_key"]; got != 1 {
		t.Fatalf("unrecognized-key skips = %v, want 1", got)
	}
	if got := h.flowRecords(); got != 0 {
		t.Fatalf("flow records = %d, want none from a key with no derivable date", got)
	}
	if len(h.store.fetched) != 0 {
		t.Fatalf("fetched = %v, want the undatable key rejected before GET", h.store.fetched)
	}
}

// #285's mechanism must hold identically for the single flat prefix: a prefix that
// is fully handled has its scan position CLEARED, so the next cycle re-lists it
// from the start and an object whose key sorts lexicographically BEFORE the last
// handled key is still discovered.
func TestCollect_FlatLayoutLateKeyBeforeScanPositionIsFoundAfterWrap(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Layout = objectstore.LayoutFlat
		o.MaxObjects = 2
	})
	for i := 10; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Minute)
		h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
	}

	h.collect(t)
	lateAt := now.Add(-10*time.Minute - 30*time.Second)
	lateKey := flatKeyAt(lateAt, ".ndjson")
	if lateKey >= flatKeyAt(now.Add(-10*time.Minute), ".ndjson") {
		t.Fatalf("fixture is not a late key: %q must sort before the first handled key", lateKey)
	}
	h.store.put(lateKey, []byte(record("late", lateAt)+"\n"))

	for range 5 {
		h.collect(t)
	}

	if got := h.flowRecords(); got != 11 {
		t.Fatalf("flow records = %d, want the ten original objects plus the late key", got)
	}
	var fetched int
	for _, key := range h.store.fetched {
		if key == lateKey {
			fetched++
		}
	}
	if fetched != 1 {
		t.Fatalf("late key fetches = %d, want exactly one after the scan wrapped", fetched)
	}
}

// The same durable startAfter machinery, over one prefix instead of many: a
// backlog larger than one page is drained across cycles, survives a restart, and
// never costs more than one LIST per cycle.
func TestCollect_FlatLayoutDrainsAcrossCyclesAndRestart(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Layout = objectstore.LayoutFlat
		o.MaxObjects = 2
	})
	for i := 10; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Minute)
		h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
	}

	for cycle := range 5 {
		h.collect(t)
		if cycle == 0 {
			if got := lastGauge(h.rec, "tailscale2otel.objectstore.scan.truncated"); got != 1 {
				t.Fatalf("scan truncated after the first cycle = %v, want 1", got)
			}
		}
		if cycle == 1 {
			h.col = newFlowCollector(
				t,
				h.store,
				flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
				h.cp,
				objectstore.Options{
					Prefix:     "flow",
					Layout:     objectstore.LayoutFlat,
					MaxObjects: 2,
					Now:        func() time.Time { return now },
					Logger:     discardLogger(),
				},
			)
		}
	}

	if got := h.flowRecords(); got != 10 {
		t.Fatalf("flow records = %d, want all 10", got)
	}
	var resumed bool
	for _, call := range h.store.listCalls {
		if call.Prefix != "flow/" {
			t.Fatalf("list call %+v, want only the one flat prefix", call)
		}
		if call.StartAfter != "" {
			resumed = true
		}
	}
	if !resumed {
		t.Fatal("no list call resumed from a durable start-after position")
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.scan.truncated"); got != 0 {
		t.Fatalf("scan truncated after drain = %v, want 0", got)
	}
}

// One prefix per cycle regardless of how wide the listing window is. In flat mode
// the maxDayPrefixes span cap therefore cannot misfire: the window plays no part
// in choosing what to list, and one prefix can never exceed the cap.
func TestCollect_FlatLayoutListsOnePrefixRegardlessOfTheWindow(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Layout = objectstore.LayoutFlat
		// Wide enough that the partitioned layout would enumerate the full
		// maxDayPrefixes span of day partitions.
		o.InitialLookback = 30 * 24 * time.Hour
		o.Lookback = 30 * 24 * time.Hour
	})
	at := now.Add(-10 * time.Minute)
	h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	h.collect(t)
	h.collect(t)

	if len(h.store.listCalls) != 2 {
		t.Fatalf("list calls = %+v, want exactly one per cycle", h.store.listCalls)
	}
}

// Switching layout on a live deployment must not wedge on the position rows the
// other layout wrote. They are recognized as stale on the first cycle under the
// new layout and deleted there, in both directions.
func TestCollect_LayoutSwitchPrunesTheOtherLayoutsScanRows(t *testing.T) {
	dayRow := scanRowFragment("flow/" + now.UTC().Format("2006/01/02") + "/")
	flatRow := scanRowFragment("flow/")

	t.Run("partitioned to flat", func(t *testing.T) {
		h := newHarness(t, func(o *objectstore.Options) { o.MaxObjects = 2 })
		for i := 10; i >= 1; i-- {
			at := now.Add(-time.Duration(i) * time.Minute)
			h.store.put(officialKeyAt(at, ".json"), []byte(record("n", at)+"\n"))
			h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("f", at)+"\n"))
		}
		h.collect(t)
		if !hasCheckpointFragment(t, h, dayRow) {
			t.Fatalf("setup: no day-partition scan row was persisted: %v", h.cp.Keys())
		}

		h.col = newFlowCollector(t, h.store,
			flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), h.cp,
			objectstore.Options{
				Prefix: "flow", Layout: objectstore.LayoutFlat, MaxObjects: 2,
				Now: func() time.Time { return now }, Logger: discardLogger(),
			})
		h.collect(t)

		if hasCheckpointFragment(t, h, dayRow) {
			t.Fatalf("day-partition scan row survived the switch to flat: %v", h.cp.Keys())
		}
		if !hasCheckpointFragment(t, h, flatRow) {
			t.Fatalf("flat scan row was not written after the switch: %v", h.cp.Keys())
		}
	})

	t.Run("flat to partitioned", func(t *testing.T) {
		h := newHarness(t, func(o *objectstore.Options) {
			o.Layout = objectstore.LayoutFlat
			o.MaxObjects = 2
		})
		for i := 10; i >= 1; i-- {
			at := now.Add(-time.Duration(i) * time.Minute)
			h.store.put(officialKeyAt(at, ".json"), []byte(record("n", at)+"\n"))
			h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("f", at)+"\n"))
		}
		h.collect(t)
		if !hasCheckpointFragment(t, h, flatRow) {
			t.Fatalf("setup: no flat scan row was persisted: %v", h.cp.Keys())
		}

		h.col = newFlowCollector(t, h.store,
			flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), h.cp,
			objectstore.Options{
				Prefix: "flow", MaxObjects: 2,
				Now: func() time.Time { return now }, Logger: discardLogger(),
			})
		h.collect(t)

		if hasCheckpointFragment(t, h, flatRow) {
			t.Fatalf("flat scan row survived the switch to partitioned: %v", h.cp.Keys())
		}
	})
}

// A flat root with no configured prefix lists the whole bucket, so its scan row
// carries an EMPTY prefix. That row must round-trip: the checkpoint reader once
// rejected an empty encoded prefix outright, which would have failed every cycle
// as corrupt state the moment a position was written.
func TestCollect_FlatLayoutAtTheBucketRootPersistsItsScanPosition(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Prefix = ""
		o.Layout = objectstore.LayoutFlat
		o.MaxObjects = 2
	})
	for i := 10; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Minute)
		h.store.put(at.UTC().Format("2006-01-02-15-04-05")+".ndjson", []byte(record("n", at)+"\n"))
	}

	h.collect(t)
	if !hasCheckpointFragment(t, h, scanRowFragment("")) {
		t.Fatalf("no scan row for the bucket-root prefix: %v", h.cp.Keys())
	}
	for range 5 {
		h.collect(t)
	}

	if got := h.flowRecords(); got != 10 {
		t.Fatalf("flow records = %d, want all 10 drained from the bucket root", got)
	}
	for _, call := range h.store.listCalls {
		if call.Prefix != "" {
			t.Fatalf("list call %+v, want the empty bucket-root prefix", call)
		}
	}
}

// The absolute lower bound in flat mode is what stops the walk re-ingesting
// history, and it is safe precisely because it is not the retry mechanism: an
// object that failed while it was inside the window keeps its own durable gap row
// and is retried from that, with no listing involved. Proven by hiding it from
// every subsequent listing.
func TestCollect_FlatLayoutFailedObjectIsRetriedFromItsGapAfterFallingOutOfTheWindow(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Layout = objectstore.LayoutFlat
		o.InitialLookback = 3 * time.Hour
		o.Lookback = 5 * time.Minute
		o.Now = func() time.Time { return clock }
	})
	badAt := now.Add(-2 * time.Hour)
	goodAt := now.Add(-10 * time.Minute)
	badKey := flatKeyAt(badAt, ".ndjson")
	h.store.put(badKey, []byte(record("recovered", badAt)+"\n"))
	h.store.getErr[badKey] = errors.New("temporary object-store failure")
	h.store.put(flatKeyAt(goodAt, ".ndjson"), []byte(record("newer", goodAt)+"\n"))

	h.collect(t)
	if got := h.flowRecords(); got != 1 {
		t.Fatalf("first-cycle records = %d, want the readable object", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Fatalf("gaps = %v, want the failure retained as one gap", got)
	}

	// The cursor now sits at the newer object, so the failed object is older than
	// from = cursor - lookback and enumeration will skip it from here on.
	delete(h.store.getErr, badKey)
	h.store.listHidden[badKey] = true
	clock = now.Add(2 * time.Minute)
	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Fatalf("records = %d, want the failed object recovered from its gap row", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Fatalf("gaps = %v, want the gap resolved", got)
	}
}

func TestNew_RejectsAnUnsupportedLayout(t *testing.T) {
	_, err := objectstore.New(
		newFakeStore(),
		objectstore.NewFlowSignal(flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{})),
		collector.NewMemoryStore(),
		objectstore.Options{
			Prefix: "flow",
			Layout: "auto",
			Scope: objectstore.CheckpointScope{
				Tailnet: "test.example", Provider: "s3",
				Signal: semconv.IngestSignalFlow, Feed: objectstore.FeedID("test", "flow"),
			},
		},
	)
	if err == nil {
		t.Fatal("New accepted an unsupported layout")
	}
	for _, want := range []string{"auto", "partitioned", "flat"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// scanRowFragment is the substring a scan checkpoint row for prefix contains. The
// row encoding is internal, so a test outside the package matches on the encoded
// prefix segment rather than reaching for it.
func scanRowFragment(prefix string) string {
	return "/scan/" + base64.RawURLEncoding.EncodeToString([]byte(prefix)) + "/"
}

func hasCheckpointFragment(t *testing.T, h *harness, fragment string) bool {
	t.Helper()
	for _, key := range h.cp.Keys() {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

// The discovery and backlog ages are derived from ENUMERATION, and flat mode
// enumerates one undelimited prefix rather than day partitions — so both gauges
// need pinning under it, not just under the partitioned default the rest of these
// tests use.
func TestCollect_FlatLayoutReportsDiscoveryAndBacklogAges(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Layout = objectstore.LayoutFlat
		o.MaxObjects = 1
		o.InitialLookback = 6 * time.Hour
	})
	for _, ago := range []time.Duration{30 * time.Minute, 20 * time.Minute, 10 * time.Minute} {
		at := now.Add(-ago)
		h.store.put(flatKeyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
	}

	h.collect(t)

	if len(h.store.listCalls) != 1 || h.store.listCalls[0].Prefix != "flow/" {
		t.Fatalf("list calls = %+v, want one listing of the normalized base prefix", h.store.listCalls)
	}
	if got := requestsByLabel(h.rec)["list/success"]; got != 1 {
		t.Errorf("list requests = %v, want the one flat listing counted", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.discovered.newest.age"); got != 600 {
		t.Errorf("newest discovered age = %v, want 600 — the newest object under the flat prefix", got)
	}
	// The budget of one ingested the oldest object, so the backlog now starts at
	// the middle one.
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.pending.oldest.age"); got != 1200 {
		t.Errorf("pending oldest age = %v, want 1200", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.cursor.age"); got != 1800 {
		t.Errorf("cursor age = %v, want 1800 — the one object ingested", got)
	}

	// A resumed flat walk starts after the durable scan position, and still
	// discovers the newest object ahead of it.
	h.collect(t)
	if got := h.store.listCalls[1].StartAfter; got == "" {
		t.Fatalf("second flat listing did not resume from a durable scan position: %+v", h.store.listCalls)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.discovered.newest.age"); got != 600 {
		t.Errorf("newest discovered age on the resumed walk = %v, want 600", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.pending.oldest.age"); got != 600 {
		t.Errorf("pending oldest age on the resumed walk = %v, want 600", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.cursor.age"); got != 1200 {
		t.Errorf("cursor age on the resumed walk = %v, want 1200", got)
	}
}
