package objectstore_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/s3"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// now is the fixed instant every test runs at, so cursor arithmetic never
// depends on the wall clock.
var now = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

// record is one flow-log record in the shape the export carries — the same shape
// the API returns, which is the point.
func record(nodeID string, at time.Time) string {
	b, _ := json.Marshal(map[string]any{
		"nodeId": nodeID,
		"start":  at.Format(time.RFC3339),
		"end":    at.Add(5 * time.Second).Format(time.RFC3339),
		"logged": at.Add(7 * time.Second).Format(time.RFC3339),
		"virtualTraffic": []map[string]any{{
			"proto": 6, "src": "100.64.0.1:41000", "dst": "100.64.0.2:443",
			"txBytes": 1000, "rxBytes": 800, "txPkts": 10, "rxPkts": 8,
		}},
	})
	return string(b)
}

// fakeStore serves a fixed set of objects, recording what was fetched.
type fakeStore struct {
	objects map[string][]byte // key -> body as stored (already compressed)
	sizes   map[string]int64
	fetched []string
	listErr error
	getErr  map[string]error
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string][]byte{}, sizes: map[string]int64{}, getErr: map[string]error{}}
}

func (f *fakeStore) put(key string, body []byte) {
	f.objects[key] = body
	f.sizes[key] = int64(len(body))
}

func (f *fakeStore) List(_ context.Context, prefix, _ string, limit int) ([]s3.Object, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []s3.Object
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, s3.Object{Key: k, Size: f.sizes[k]})
		}
	}
	// Real S3 lists lexicographically; matching that is what the cursor scheme
	// relies on.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].Key < out[i].Key {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if err, ok := f.getErr[key]; ok {
		return nil, err
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("no such key %q", key)
	}
	f.fetched = append(f.fetched, key)
	return io.NopCloser(bytes.NewReader(body)), nil
}

// keyAt builds an export key for a given instant.
func keyAt(at time.Time, ext string) string {
	return "flow/" + at.UTC().Format("2006/01/02") + "/" + at.UTC().Format("2006-01-02-15-04-05") + ext
}

func zstdBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := io.WriteString(w, s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// harness wires a collector to a fake store, a real processor and a real
// checkpoint store, so a test exercises the whole path.
type harness struct {
	store *fakeStore
	cp    collector.CheckpointStore
	col   *objectstore.Collector
	rec   *telemetrytest.Recorder
}

func newHarness(t *testing.T, tune func(*objectstore.Options)) *harness {
	t.Helper()
	h := &harness{store: newFakeStore(), cp: collector.NewMemoryStore(), rec: telemetrytest.New()}
	opts := objectstore.Options{
		Prefix: "flow",
		Now:    func() time.Time { return now },
		Logger: discardLogger(),
	}
	if tune != nil {
		tune(&opts)
	}
	h.col = objectstore.New(h.store, flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), h.cp, opts)
	return h
}

func (h *harness) collect(t *testing.T) {
	t.Helper()
	if err := h.col.Collect(context.Background(), h.rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
}

// flowRecords counts the flow log records the processor emitted, which is the
// evidence that objects reached the SAME path poll and stream use.
func (h *harness) flowRecords() int { return len(h.rec.LogRecords()) }

func TestCollect_IngestsObjectsThroughTheSharedProcessor(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"+record("n2", at)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Errorf("flow log records = %d, want 2 — the records did not reach the processor", got)
	}
	if len(h.store.fetched) != 1 {
		t.Errorf("fetched = %v, want the one object", h.store.fetched)
	}
}

// The export is zstd; gzip appears when an operator re-compresses while copying
// between buckets. Both, and plain, must decode.
func TestCollect_DecodesEveryCompression(t *testing.T) {
	for _, tc := range []struct {
		name string
		ext  string
		body func(*testing.T, string) []byte
	}{
		{"plain", ".ndjson", func(_ *testing.T, s string) []byte { return []byte(s) }},
		{"zstd", ".ndjson.zst", zstdBytes},
		{"zstd long extension", ".ndjson.zstd", zstdBytes},
		{"gzip", ".ndjson.gz", gzipBytes},
		{"gzip long extension", ".ndjson.gzip", gzipBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			at := now.Add(-10 * time.Minute)
			h.store.put(keyAt(at, tc.ext), tc.body(t, record("n1", at)+"\n"))

			h.collect(t)

			if got := h.flowRecords(); got != 1 {
				t.Errorf("records = %d, want 1 from a %s object", got, tc.name)
			}
		})
	}
}

// The whole point of the cursor and the seen set: a bucket listed twice must
// ingest once. Without it, every cycle re-reads the overlap window and every
// counter is inflated by however many cycles fit in it.
func TestCollect_ReListingDoesNotReIngest(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	h.collect(t)
	first := h.flowRecords()
	h.collect(t)

	if got := h.flowRecords(); got != first {
		t.Errorf("records after a second cycle = %d, want %d — the object was ingested twice", got, first)
	}
	if len(h.store.fetched) != 1 {
		t.Errorf("fetched = %v, want the object fetched once", h.store.fetched)
	}
}

// Objects can appear out of order relative to their embedded timestamp. Without
// the backwards overlap they land below the cursor and are never seen; with it,
// and only with the seen set, they are ingested exactly once.
func TestCollect_FindsAnObjectThatArrivesLate(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.Lookback = time.Hour })
	recent := now.Add(-10 * time.Minute)
	h.store.put(keyAt(recent, ".ndjson"), []byte(record("n1", recent)+"\n"))

	h.collect(t)
	if h.flowRecords() != 1 {
		t.Fatalf("setup: records = %d, want 1", h.flowRecords())
	}

	// An object stamped BEFORE the cursor, uploaded after it passed.
	late := now.Add(-30 * time.Minute)
	h.store.put(keyAt(late, ".ndjson"), []byte(record("nLate", late)+"\n"))

	h.collect(t)
	if got := h.flowRecords(); got != 2 {
		t.Errorf("records = %d, want the late object ingested too", got)
	}
	if len(h.store.fetched) != 2 {
		t.Errorf("fetched = %v, want each object once", h.store.fetched)
	}
}

// An object older than the overlap window is beyond recovery by design — the
// alternative is re-listing the whole bucket forever.
func TestCollect_IgnoresObjectsBeforeTheOverlapWindow(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.Lookback = 5 * time.Minute
		o.InitialLookback = 15 * time.Minute
	})
	old := now.Add(-2 * time.Hour)
	h.store.put(keyAt(old, ".ndjson"), []byte(record("nOld", old)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 0 {
		t.Errorf("records = %d, want none — the object predates the initial lookback", got)
	}
}

// A first run against a bucket holding months of exports must not try to ingest
// all of it.
func TestCollect_ColdStartIsBoundedByInitialLookback(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.InitialLookback = 30 * time.Minute })
	for _, ago := range []time.Duration{10 * time.Minute, 20 * time.Minute, 3 * time.Hour, 48 * time.Hour} {
		at := now.Add(-ago)
		h.store.put(keyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
	}

	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Errorf("records = %d, want only the two inside the initial lookback", got)
	}
}

// Exceeding the per-cycle budget is normal on a backlog, and must be reported
// rather than silently truncated — an operator whose bucket outruns the cap
// needs to know before the backlog becomes days.
func TestCollect_BudgetDefersRatherThanDropping(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.MaxObjects = 2 })
	for i := range 5 {
		at := now.Add(-time.Duration(i+1) * time.Minute)
		h.store.put(keyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
	}

	h.collect(t)
	if got := h.flowRecords(); got != 2 {
		t.Fatalf("records = %d, want the budget of 2", got)
	}
	skipped := skippedByReason(h.rec)
	if skipped["per_cycle_budget"] != 3 {
		t.Errorf("skipped = %v, want 3 deferred to the next cycle", skipped)
	}
	if backlog := lastGauge(h.rec, "tailscale2otel.objectstore.backlog"); backlog != 3 {
		t.Errorf("backlog gauge = %v, want 3", backlog)
	}

	// Deferred, not dropped: subsequent cycles drain the backlog two at a time.
	h.collect(t)
	if got := h.flowRecords(); got != 4 {
		t.Errorf("records after the second cycle = %d, want 4 (two more of the budget)", got)
	}
	h.collect(t)
	if got := h.flowRecords(); got != 5 {
		t.Errorf("records after the third cycle = %d, want all 5 eventually ingested", got)
	}
	if backlog := lastGauge(h.rec, "tailscale2otel.objectstore.backlog"); backlog != 0 {
		t.Errorf("backlog gauge = %v, want 0 once caught up", backlog)
	}
}

// The oldest objects go first, so the cursor only ever advances over ground
// actually covered. Ingesting newest-first would strand everything behind it.
func TestCollect_IngestsOldestFirst(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.MaxObjects = 2 })
	var want []string
	for i := 5; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Minute)
		k := keyAt(at, ".ndjson")
		h.store.put(k, []byte(record("n", at)+"\n"))
		want = append(want, k)
	}

	h.collect(t)

	if len(h.store.fetched) != 2 || h.store.fetched[0] != want[0] || h.store.fetched[1] != want[1] {
		t.Errorf("fetched = %v, want the two oldest first (%v)", h.store.fetched, want[:2])
	}
}

// One unreadable object must not wedge ingestion behind it forever.
func TestCollect_OneBadObjectDoesNotStopTheRest(t *testing.T) {
	h := newHarness(t, nil)
	bad := now.Add(-20 * time.Minute)
	good := now.Add(-10 * time.Minute)
	h.store.put(keyAt(bad, ".ndjson"), nil)
	h.store.getErr[keyAt(bad, ".ndjson")] = errors.New("access denied")
	h.store.put(keyAt(good, ".ndjson"), []byte(record("n1", good)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Errorf("records = %d, want the readable object ingested", got)
	}
	if skippedByReason(h.rec)["read_error"] != 1 {
		t.Errorf("skipped = %v, want the failure counted", skippedByReason(h.rec))
	}
}

// One malformed line costs one record. The export is line-delimited precisely so
// that is true, and abandoning the object would lose thousands of good records
// to one bad one.
func TestCollect_MalformedLineCostsOneRecord(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(
		record("n1", at)+"\n"+"{not json\n"+"\n"+record("n2", at)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Errorf("records = %d, want the two good ones", got)
	}
	if skippedByReason(h.rec)["decode_error"] != 1 {
		t.Errorf("skipped = %v, want the malformed line counted", skippedByReason(h.rec))
	}
}

// Flow records for a busy node are large. bufio.Scanner's 64 KiB default fails
// on them with "token too long", which reads as corruption rather than a limit —
// so the buffer is raised explicitly.
func TestCollect_HandlesAVeryLongLine(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)

	conns := make([]map[string]any, 4000)
	for i := range conns {
		conns[i] = map[string]any{
			"proto": 6, "src": fmt.Sprintf("100.64.0.1:%d", 40000+i), "dst": "100.64.0.2:443",
			"txBytes": 1, "rxBytes": 1,
		}
	}
	big, _ := json.Marshal(map[string]any{
		"nodeId": "nBig", "start": at.Format(time.RFC3339), "end": at.Format(time.RFC3339),
		"logged": at.Format(time.RFC3339), "virtualTraffic": conns,
	})
	if len(big) < 128<<10 {
		t.Fatalf("fixture is only %d bytes; it must exceed the scanner default to be a test", len(big))
	}
	h.store.put(keyAt(at, ".ndjson"), append(big, '\n'))

	h.collect(t)

	if got := h.flowRecords(); got == 0 {
		t.Error("a record longer than the scanner default was dropped")
	}
}

// A listing failure must leave the cursor alone, so the next cycle retries the
// same ground rather than skipping past it.
func TestCollect_ListFailureDoesNotAdvanceTheCursor(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))
	h.store.listErr = errors.New("connection refused")

	if err := h.col.Collect(context.Background(), h.rec.Emitter()); err == nil {
		t.Fatal("Collect succeeded despite a list failure")
	}

	h.store.listErr = nil
	h.collect(t)
	if got := h.flowRecords(); got != 1 {
		t.Errorf("records = %d, want the object ingested once the listing recovered", got)
	}
}

// The cursor is durable, so a restart resumes rather than re-reading the
// overlap window.
func TestCollect_CursorSurvivesARestart(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))
	h.collect(t)

	// A fresh collector over the SAME checkpoint store and bucket: this is what a
	// process restart looks like.
	rec2 := telemetrytest.New()
	restarted := objectstore.New(h.store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), h.cp,
		objectstore.Options{Prefix: "flow", Now: func() time.Time { return now }, Logger: discardLogger()})
	if err := restarted.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatal(err)
	}

	if got := len(rec2.LogRecords()); got != 0 {
		t.Errorf("records after a restart = %d, want none — the object was already ingested", got)
	}
}

// A key that carries no timestamp cannot be placed against the cursor, so it is
// reported rather than guessed at: assuming "now" ingests it and never looks
// again, assuming zero skips it forever.
func TestCollect_UnrecognizedKeysAreReportedNotGuessed(t *testing.T) {
	h := newHarness(t, nil)
	h.store.put("flow/2026/07/24/manifest.txt", []byte("not a flow log"))
	h.store.put("flow/2026/07/24/README", []byte("hello"))

	h.collect(t)

	if got := h.flowRecords(); got != 0 {
		t.Errorf("records = %d, want none", got)
	}
	if skippedByReason(h.rec)["unrecognized_key"] != 2 {
		t.Errorf("skipped = %v, want both unrecognized keys counted", skippedByReason(h.rec))
	}
	if len(h.store.fetched) != 0 {
		t.Errorf("fetched = %v, want nothing fetched", h.store.fetched)
	}
}
