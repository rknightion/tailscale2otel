package objectstore_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v5/internal/dedup"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/ingest"
	storeapi "github.com/rknightion/tailscale2otel/v5/internal/objectstore"
	"github.com/rknightion/tailscale2otel/v5/internal/s3"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
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
	objects    map[string][]byte // key -> body as stored (already compressed)
	sizes      map[string]int64
	fetched    []string
	getCalls   []string
	readBytes  int64
	listCalls  []listCall
	listErr    error
	getErr     map[string]error
	readErr    map[string]error
	listHidden map[string]bool
}

type listCall struct {
	Prefix     string
	StartAfter string
	Limit      int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects:    map[string][]byte{},
		sizes:      map[string]int64{},
		getErr:     map[string]error{},
		readErr:    map[string]error{},
		listHidden: map[string]bool{},
	}
}

func (f *fakeStore) put(key string, body []byte) {
	f.objects[key] = body
	f.sizes[key] = int64(len(body))
}

func (f *fakeStore) List(_ context.Context, prefix, startAfter string, limit int) (s3.ListResult, error) {
	if f.listErr != nil {
		return s3.ListResult{}, f.listErr
	}
	f.listCalls = append(f.listCalls, listCall{Prefix: prefix, StartAfter: startAfter, Limit: limit})
	var out []s3.Object
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) && k > startAfter && !f.listHidden[k] {
			out = append(out, s3.Object{Identity: k, Key: k, Size: f.sizes[k]})
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
	truncated := limit > 0 && len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return s3.ListResult{Objects: out, Truncated: truncated}, nil
}

func (f *fakeStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.getCalls = append(f.getCalls, key)
	if err, ok := f.getErr[key]; ok {
		return nil, err
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("no such key %q", key)
	}
	f.fetched = append(f.fetched, key)
	if err := f.readErr[key]; err != nil {
		return &countedReadCloser{
			ReadCloser: &errorAfterReader{Reader: bytes.NewReader(body), err: err},
			n:          &f.readBytes,
		}, nil
	}
	return &countedReadCloser{
		ReadCloser: io.NopCloser(bytes.NewReader(body)),
		n:          &f.readBytes,
	}, nil
}

type countedReadCloser struct {
	io.ReadCloser
	n *int64
}

func (r *countedReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	*r.n += int64(n)
	return n, err
}

type errorAfterReader struct {
	*bytes.Reader
	err error
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return 0, r.err
	}
	return n, err
}

func (r *errorAfterReader) Close() error { return nil }

// keyAt builds an export key for a given instant.
func keyAt(at time.Time, ext string) string {
	return "flow/" + at.UTC().Format("2006/01/02") + "/" + at.UTC().Format("2006-01-02-15-04-05") + ext
}

func officialKeyAt(at time.Time, ext string) string {
	return "flow/" + at.UTC().Format("2006/01/02/15:04:05") + ext
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

func newFlowCollector(
	t *testing.T,
	api storeapi.Backend,
	proc *flowlog.Processor,
	cp collector.CheckpointStore,
	opts objectstore.Options,
) *objectstore.Collector {
	t.Helper()
	if opts.Scope == (objectstore.CheckpointScope{}) {
		opts.Scope = objectstore.CheckpointScope{
			Tailnet:  "test.example",
			Provider: "s3",
			Signal:   semconv.IngestSignalFlow,
			Feed:     objectstore.FeedID("test", opts.Prefix),
		}
	}
	col, err := objectstore.New(api, objectstore.NewFlowSignal(proc), cp, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return col
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
	h.col = newFlowCollector(
		t,
		h.store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		h.cp,
		opts,
	)
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

// The metric is tailscale2otel.ingest.size, whose other emitters (stream,
// webhook) mean decoded/decompressed payload bytes. OnIngest must report the
// SAME meaning for objectstore, not the compressed bytes actually read off the
// wire (that figure is already covered, unchanged, by the object-store-specific
// tailscale2otel.objectstore.bytes counter). See #450.
func TestCollect_OnIngestReportsDecompressedNotWireBytes(t *testing.T) {
	var gotSource, gotSignal string
	var gotBytes, calls int
	h := newHarness(t, func(o *objectstore.Options) {
		o.OnIngest = func(source, signal string, records, bytes int) {
			calls++
			gotSource, gotSignal, gotBytes = source, signal, bytes
		}
	})
	at := now.Add(-10 * time.Minute)
	// Repeating one record compresses well, so the decompressed and compressed
	// sizes provably differ -- proof this test would fail if OnIngest reverted
	// to reporting the wire/compressed count.
	plain := strings.Repeat(record("n1", at)+"\n", 200)
	compressed := gzipBytes(t, plain)
	if len(compressed) >= len(plain) {
		t.Fatalf("fixture does not compress (compressed=%d, plain=%d bytes); the test needs the two sizes to differ", len(compressed), len(plain))
	}
	h.store.put(keyAt(at, ".json.gz"), compressed)

	h.collect(t)

	if calls != 1 {
		t.Fatalf("OnIngest calls = %d, want 1", calls)
	}
	if gotSource != semconv.IngestSourceObjectStore {
		t.Errorf("source = %q, want %q", gotSource, semconv.IngestSourceObjectStore)
	}
	if gotSignal != semconv.IngestSignalFlow {
		t.Errorf("signal = %q, want %q", gotSignal, semconv.IngestSignalFlow)
	}
	if gotBytes != len(plain) {
		t.Errorf("OnIngest bytes = %d, want the decompressed size %d (compressed wire size was %d)", gotBytes, len(plain), len(compressed))
	}
}

func TestCollect_ReportsAcceptedFreshnessAfterProcessorHandoff(t *testing.T) {
	var got []ingest.AcceptedEvent
	var h *harness
	h = newHarness(t, func(o *objectstore.Options) {
		o.OnAccepted = func(event ingest.AcceptedEvent) {
			if records := h.flowRecords(); records != 1 {
				t.Errorf("flow records at freshness handoff = %d, want 1", records)
			}
			got = append(got, event)
		}
	})
	keyTime := now.Add(-10 * time.Minute)
	eventTime := now.Add(-2 * time.Minute)
	h.store.put(keyAt(keyTime, ".ndjson"), []byte(record("n1", eventTime)+"\n"))

	h.collect(t)

	if len(got) != 1 {
		t.Fatalf("accepted events = %d, want 1", len(got))
	}
	event := got[0]
	if event.Source != semconv.IngestSourceObjectStore {
		t.Errorf("source = %q, want %q", event.Source, semconv.IngestSourceObjectStore)
	}
	if event.Signal != semconv.IngestSignalFlow {
		t.Errorf("signal = %q, want %q", event.Signal, semconv.IngestSignalFlow)
	}
	if !event.EventTime.Equal(eventTime.Add(5 * time.Second)) {
		t.Errorf("event time = %v, want record end %v", event.EventTime, eventTime.Add(5*time.Second))
	}
	if !event.CaptureTime.Equal(eventTime.Add(7 * time.Second)) {
		t.Errorf("capture time = %v, want record logged %v", event.CaptureTime, eventTime.Add(7*time.Second))
	}
	if event.EventTime.Equal(keyTime) || event.CaptureTime.Equal(keyTime) {
		t.Errorf("freshness timestamps = (%v, %v), must come from the record rather than object key %v", event.EventTime, event.CaptureTime, keyTime)
	}
	if event.AcceptedAt.IsZero() {
		t.Error("accepted at is zero")
	}
}

func TestCollect_AcceptedFreshnessSkipsRejectedAndReplayedRecords(t *testing.T) {
	var got []ingest.AcceptedEvent
	h := newHarness(t, func(o *objectstore.Options) {
		o.OnAccepted = func(event ingest.AcceptedEvent) { got = append(got, event) }
	})
	validAt := now.Add(-10 * time.Minute)
	h.store.put(keyAt(validAt, ".ndjson"), []byte(record("good", validAt)+"\n"))
	invalidAt := validAt.Add(time.Minute)
	var invalid map[string]any
	if err := json.Unmarshal([]byte(record("bad", invalidAt)), &invalid); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	invalid["virtualTraffic"].([]any)[0].(map[string]any)["txBytes"] = float64(-1)
	invalidLine, err := json.Marshal(invalid)
	if err != nil {
		t.Fatalf("encode invalid fixture: %v", err)
	}
	h.store.put(keyAt(invalidAt, ".ndjson"), append(invalidLine, '\n'))
	h.store.put(keyAt(now.Add(6*time.Minute), ".ndjson"), []byte(record("future", now)+"\n"))

	h.collect(t)
	h.collect(t)

	if len(got) != 1 {
		t.Fatalf("accepted events = %d, want 1; semantic rejection, future key, and replay must add none", len(got))
	}
}

func TestCollect_AcceptedFreshnessSurvivesProcessorCrossSourceDedup(t *testing.T) {
	store := newFakeStore()
	cp := collector.NewMemoryStore()
	rec := telemetrytest.New()
	proc := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{Dedup: dedup.New(8)})
	var got []ingest.AcceptedEvent
	col := newFlowCollector(t, store, proc, cp, objectstore.Options{
		Prefix: "flow",
		Now:    func() time.Time { return now },
		Logger: discardLogger(),
		OnAccepted: func(event ingest.AcceptedEvent) {
			got = append(got, event)
		},
	})
	at := now.Add(-10 * time.Minute)
	var fl flowlog.FlowLog
	if err := json.Unmarshal([]byte(record("n1", at)), &fl); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	proc.Process(fl, rec.Emitter()) // accepted previously by another source.
	store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if records := len(rec.LogRecords()); records != 1 {
		t.Fatalf("flow log records = %d, want 1; the object-store copy should be processor-deduped", records)
	}
	if len(got) != 1 {
		t.Fatalf("accepted events = %d, want 1 despite processor cross-source dedup", len(got))
	}
}

// The export is zstd; gzip appears when an operator re-compresses while copying
// between buckets. Both, and plain, must decode.
func TestCollect_DecodesEveryCompression(t *testing.T) {
	fixture, err := os.ReadFile("testdata/official-network-export.ndjson")
	if err != nil {
		t.Fatalf("read sanitized live-shape fixture: %v", err)
	}
	for _, tc := range []struct {
		name string
		ext  string
		body func(*testing.T, string) []byte
	}{
		{"plain", ".json", func(_ *testing.T, s string) []byte { return []byte(s) }},
		{"zstd", ".json.zst", zstdBytes},
		{"gzip", ".json.gz", gzipBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			at := time.Date(2026, 7, 24, 9, 5, 0, 0, time.UTC)
			key := "flow/" + at.Format("2006/01/02/15:04:05") + tc.ext
			h.store.put(key, tc.body(t, string(fixture)))

			h.collect(t)

			if got := h.flowRecords(); got != 1 {
				t.Errorf("records = %d, want 1 from a %s object", got, tc.name)
			}
		})
	}
}

func TestCollect_UnrecognizedOfficialLayoutIsVisible(t *testing.T) {
	h := newHarness(t, nil)
	h.store.put("flow/2026/07/24/not-a-time.json", []byte(record("n1", now.Add(-10*time.Minute))+"\n"))

	h.collect(t)

	if got := skippedByReason(h.rec)["unrecognized_key"]; got != 1 {
		t.Errorf("unrecognized-key skips = %v, want 1", got)
	}
	if len(h.store.fetched) != 0 {
		t.Errorf("fetched = %v, want an unknown layout rejected before GET", h.store.fetched)
	}
}

func TestCollect_QuarantinesSemanticallyInvalidLinesAndContinues(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	var invalid map[string]any
	if err := json.Unmarshal([]byte(record("bad", at)), &invalid); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	traffic := invalid["virtualTraffic"].([]any)
	traffic[0].(map[string]any)["txBytes"] = float64(-1)
	invalidLine, err := json.Marshal(invalid)
	if err != nil {
		t.Fatalf("encode invalid fixture: %v", err)
	}
	body := string(invalidLine) + "\n" + record("good", at) + "\n"
	h.store.put(keyAt(at, ".ndjson"), []byte(body))

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Fatalf("flow records = %d, want only the valid line", got)
	}
	if got := skippedByReason(h.rec)["semantic_invalid"]; got != 1 {
		t.Fatalf("semantic-invalid skipped = %v, want 1", got)
	}
	points := h.rec.MetricPoints(flowlog.MetricDataQuality)
	if len(points) != 1 {
		t.Fatalf("data-quality points = %d, want 1", len(points))
	}
	if got := points[0].Attrs["source"]; got != "objectstore" {
		t.Errorf("source = %q, want objectstore", got)
	}
	if got := points[0].Attrs["reason"]; got != string(flowlog.ViolationNegativeCounters) {
		t.Errorf("reason = %q, want %q", got, flowlog.ViolationNegativeCounters)
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

// TestCollect_SeenKeysTrimToConfiguredCapacity proves the durable seen set
// honors its configured bound. The oldest of three objects is trimmed at a
// capacity of two and is consequently eligible for re-ingestion on the next
// overlapping listing, which exercises the actual checkpoint path.
func TestCollect_SeenKeysTrimToConfiguredCapacity(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) {
		o.MaxSeenKeys = 2
		o.Lookback = time.Hour
	})
	for i := 1; i <= 3; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		h.store.put(keyAt(at, ".ndjson"), []byte(record(fmt.Sprintf("n%d", i), at)+"\n"))
	}

	h.collect(t)
	if got := len(seenCheckpointKeys(h.cp)); got != 2 {
		t.Fatalf("seen checkpoint keys after first cycle = %d, want 2", got)
	}
	if got := h.flowRecords(); got != 3 {
		t.Fatalf("flow records after first cycle = %d, want 3", got)
	}

	h.collect(t)
	if got := len(seenCheckpointKeys(h.cp)); got != 2 {
		t.Fatalf("seen checkpoint keys after second cycle = %d, want 2", got)
	}
	if got := h.flowRecords(); got != 4 {
		t.Fatalf("flow records after second cycle = %d, want 4 (one trimmed object re-ingested)", got)
	}
}

func seenCheckpointKeys(cp collector.CheckpointStore) []string {
	var keys []string
	for _, key := range cp.Keys() {
		if strings.Contains(key, "/seen/") {
			keys = append(keys, key)
		}
	}
	return keys
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

func TestCollect_DrainsBusyPrefixAcrossCyclesAndRestart(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.MaxObjects = 2 })
	for i := 10; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Minute)
		h.store.put(officialKeyAt(at, ".json"), []byte(record("n", at)+"\n"))
	}

	for cycle := range 5 {
		h.collect(t)
		if cycle == 0 {
			if got := lastGauge(h.rec, "tailscale2otel.objectstore.scan.truncated"); got != 1 {
				t.Fatalf("scan truncated after first cycle = %v, want 1", got)
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
		if call.StartAfter != "" {
			resumed = true
			break
		}
	}
	if !resumed {
		t.Fatal("no list call resumed from a durable start-after position")
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.scan.truncated"); got != 0 {
		t.Fatalf("scan truncated after drain = %v, want 0", got)
	}
}

func TestCollect_LateKeyBeforeScanPositionIsFoundAfterWrap(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.MaxObjects = 2 })
	for i := 10; i >= 1; i-- {
		at := now.Add(-time.Duration(i) * time.Minute)
		h.store.put(officialKeyAt(at, ".json"), []byte(record("n", at)+"\n"))
	}

	h.collect(t)
	lateAt := now.Add(-10*time.Minute - 30*time.Second)
	lateKey := officialKeyAt(lateAt, ".json")
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

func TestCollect_FailureAtPrefixStartKeepsScanGroundActive(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.InitialLookback = 3 * time.Hour
		o.Lookback = 5 * time.Minute
		o.Now = func() time.Time { return clock }
	})
	badAt := now.Add(-2 * time.Hour)
	goodAt := now.Add(-10 * time.Minute)
	badKey := officialKeyAt(badAt, ".json")
	h.store.put(badKey, []byte(record("bad", badAt)+"\n"))
	h.store.getErr[badKey] = errors.New("temporary read failure")
	h.store.put(officialKeyAt(goodAt, ".json"), []byte(record("good", goodAt)+"\n"))

	h.collect(t)
	delete(h.store.getErr, badKey)
	clock = now.Add(time.Minute)
	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Fatalf("flow records = %d, want the later success and recovered failed object", got)
	}
	var attempts int
	for _, key := range h.store.getCalls {
		if key == badKey {
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("failed-object attempts = %d, want an immediate retry from active scan ground", attempts)
	}
}

func TestCollect_FailedGapSurvivesCursorAdvanceAndRestart(t *testing.T) {
	clock := now
	store := newFakeStore()
	badAt := now.Add(-2 * time.Hour)
	goodAt := now.Add(-10 * time.Minute)
	badKey := officialKeyAt(badAt, ".json")
	store.put(badKey, []byte(record("recovered", badAt)+"\n"))
	store.getErr[badKey] = errors.New("temporary object-store failure")
	store.put(officialKeyAt(goodAt, ".json"), []byte(record("newer", goodAt)+"\n"))

	checkpointPath := t.TempDir() + "/checkpoints.json"
	cp, err := collector.NewFileStore(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	rec := telemetrytest.New()
	col := newFlowCollector(
		t,
		store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		cp,
		objectstore.Options{
			Prefix:          "flow",
			InitialLookback: 3 * time.Hour,
			Lookback:        5 * time.Minute,
			Now:             func() time.Time { return clock },
			Logger:          discardLogger(),
		},
	)
	if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	if got := len(rec.LogRecords()); got != 1 {
		t.Fatalf("first-cycle records = %d, want the newer object", got)
	}
	var hasGap bool
	for _, key := range cp.Keys() {
		if strings.Contains(key, "/gap/") {
			hasGap = true
			break
		}
	}
	if !hasGap {
		t.Fatal("failed object was not persisted independently as a gap")
	}

	delete(store.getErr, badKey)
	store.listHidden[badKey] = true
	clock = now.Add(2 * time.Minute)
	reopened, err := collector.NewFileStore(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := telemetrytest.New()
	restarted := newFlowCollector(
		t,
		store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		reopened,
		objectstore.Options{
			Prefix:          "flow",
			InitialLookback: 3 * time.Hour,
			Lookback:        5 * time.Minute,
			Now:             func() time.Time { return clock },
			Logger:          discardLogger(),
		},
	)
	if err := restarted.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatal(err)
	}
	if got := len(rec2.LogRecords()); got != 1 {
		t.Fatalf("restart records = %d, want the gap retried without rediscovery by listing", got)
	}
	for _, key := range reopened.Keys() {
		if strings.Contains(key, "/gap/") {
			t.Fatalf("resolved gap row remains: %q", key)
		}
	}
}

func TestCollect_QuarantinedGapCanBeManuallyAcknowledged(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Now = func() time.Time { return clock }
	})
	at := now.Add(-10 * time.Minute)
	key := officialKeyAt(at, ".json.gz")
	h.store.put(key, []byte("not a gzip stream"))

	h.collect(t)
	if got := len(h.store.getCalls); got != 1 {
		t.Fatalf("GET calls = %d, want one failed decompression attempt", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Fatalf("gap count = %v, want one quarantined gap", got)
	}
	clock = now.Add(24 * time.Hour)
	h.collect(t)
	if got := len(h.store.getCalls); got != 1 {
		t.Fatalf("GET calls after quarantine = %d, want no automatic retry", got)
	}
	for _, checkpointKey := range h.cp.Keys() {
		if strings.Contains(checkpointKey, "/gap/") {
			if err := h.cp.Delete(checkpointKey); err != nil {
				t.Fatal(err)
			}
		}
	}

	clock = clock.Add(time.Minute)
	h.collect(t)
	if got := len(h.store.getCalls); got != 1 {
		t.Fatalf("GET calls after manual acknowledgement = %d, want no retry", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 1 {
		t.Fatalf("gap healthy = %v, want 1 after acknowledgement", got)
	}
}

func TestCollect_GapRetryBackoffIsBoundedButDoesNotAbandon(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Now = func() time.Time { return clock }
	})
	at := now.Add(-10 * time.Minute)
	key := officialKeyAt(at, ".json")
	h.store.put(key, []byte(record("eventual", at)+"\n"))
	h.store.getErr[key] = errors.New("temporary object-store failure")

	restartAndCollect := func() {
		h.col = newFlowCollector(
			t,
			h.store,
			flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
			h.cp,
			objectstore.Options{
				Prefix: "flow",
				Now:    func() time.Time { return clock },
				Logger: discardLogger(),
			},
		)
		h.collect(t)
	}

	restartAndCollect()
	clock = now.Add(time.Minute - time.Second)
	restartAndCollect()
	if got := len(h.store.getCalls); got != 1 {
		t.Fatalf("GET calls before first retry boundary = %d, want 1", got)
	}

	clock = now
	for _, delay := range []time.Duration{
		time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		16 * time.Minute,
		32 * time.Minute,
		time.Hour,
		time.Hour,
	} {
		clock = clock.Add(delay)
		restartAndCollect()
	}
	if got := len(h.store.getCalls); got != 9 {
		t.Fatalf("GET calls = %d, want initial attempt plus eight durable retries", got)
	}
	var hasGap bool
	for _, checkpointKey := range h.cp.Keys() {
		if strings.Contains(checkpointKey, "/gap/") {
			hasGap = true
			break
		}
	}
	if !hasGap {
		t.Fatal("transient gap was abandoned after repeated capped retries")
	}
}

func TestCollect_FailureDiagnosticsUseDigestNotRawKey(t *testing.T) {
	var logs bytes.Buffer
	h := newHarness(t, func(o *objectstore.Options) {
		o.Prefix = "customer-private/flow"
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	})
	at := now.Add(-10 * time.Minute)
	key := "customer-private/flow/" + at.UTC().Format("2006/01/02/15:04:05") + ".json"
	h.store.put(key, []byte(record("n", at)+"\n"))
	h.store.getErr[key] = fmt.Errorf("backend could not fetch %s", key)

	h.collect(t)

	got := logs.String()
	if strings.Contains(got, key) || strings.Contains(got, "customer-private") {
		t.Fatalf("failure diagnostics leaked the raw object key: %s", got)
	}
	sum := sha256.Sum256([]byte(key))
	wantDigest := hex.EncodeToString(sum[:6])
	if !strings.Contains(got, wantDigest) {
		t.Fatalf("failure diagnostics = %q, want digest %q", got, wantDigest)
	}
}

func TestCollect_GapHealthTelemetryHasNoObjectAttributes(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Now = func() time.Time { return clock }
	})
	at := now.Add(-10 * time.Minute)
	key := officialKeyAt(at, ".json")
	h.store.put(key, []byte(record("recovered", at)+"\n"))
	h.store.getErr[key] = errors.New("temporary read failure")

	h.collect(t)
	clock = now.Add(30 * time.Second)
	h.collect(t)
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Fatalf("gap count = %v, want 1", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.oldest.age"); got != 30 {
		t.Fatalf("oldest gap age = %v, want 30 seconds", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 0 {
		t.Fatalf("gap healthy = %v, want 0", got)
	}
	for _, name := range []string{
		"tailscale2otel.objectstore.gaps",
		"tailscale2otel.objectstore.gap.oldest.age",
		"tailscale2otel.objectstore.gap.healthy",
	} {
		for _, point := range h.rec.MetricPoints(name) {
			if len(point.Attrs) != 0 {
				t.Fatalf("%s attributes = %v, want none", name, point.Attrs)
			}
		}
	}

	delete(h.store.getErr, key)
	clock = now.Add(time.Minute)
	h.collect(t)
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Fatalf("resolved gap count = %v, want 0", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.oldest.age"); got != 0 {
		t.Fatalf("resolved oldest gap age = %v, want 0", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 1 {
		t.Fatalf("resolved gap healthy = %v, want 1", got)
	}
}

func TestCollect_LateScannerErrorEntersDurableGapPath(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Now = func() time.Time { return clock }
	})
	at := now.Add(-10 * time.Minute)
	key := officialKeyAt(at, ".json")
	h.store.put(key, []byte(record("partially-emitted", at)+"\n"))
	h.store.readErr[key] = errors.New("late object read failure")

	h.collect(t)
	if got := h.flowRecords(); got != 0 {
		t.Fatalf("partial records = %d, want 0 before the staged object reaches clean EOF", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Fatalf("gap count = %v, want the scanner failure retained", got)
	}

	delete(h.store.readErr, key)
	clock = now.Add(time.Minute)
	rec2 := telemetrytest.New()
	h.col = newFlowCollector(
		t,
		h.store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		h.cp,
		objectstore.Options{
			Prefix: "flow",
			Now:    func() time.Time { return clock },
			Logger: discardLogger(),
		},
	)
	if err := h.col.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatal(err)
	}
	if got := len(rec2.LogRecords()); got != 1 {
		t.Fatalf("retry records = %d, want the object replayed after restart", got)
	}
	if got := lastGauge(rec2, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Fatalf("gap count after scanner recovery = %v, want 0", got)
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
	h.col = newFlowCollector(
		t,
		h.store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		h.cp,
		objectstore.Options{
			Prefix: "flow",
			Now:    func() time.Time { return now },
			Logger: discardLogger(),
		},
	)
	h.collect(t)
	if got := h.flowRecords(); got != 2 {
		t.Fatalf("records after restart = %d, want no object replay for one malformed row", got)
	}
	for _, key := range h.cp.Keys() {
		if strings.Contains(key, "/gap/") {
			t.Fatalf("record-level malformed JSON created an object gap: %q", key)
		}
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
	restarted := newFlowCollector(t, h.store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), h.cp,
		objectstore.Options{Prefix: "flow", Now: func() time.Time { return now }, Logger: discardLogger()})
	if err := restarted.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatal(err)
	}

	if got := len(rec2.LogRecords()); got != 0 {
		t.Errorf("records after a restart = %d, want none — the object was already ingested", got)
	}
}

// A basename stamped in the future must never reach the cursor. The cursor is
// the listing lower bound, so one object named three hours ahead moves the
// window past the wall clock and the NEXT cycle derives no day prefixes at all —
// ingestion stops until the clock catches up.
func TestCollect_FutureDatedKeyCannotAdvanceTheCursor(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.Lookback = time.Hour })
	current := now.Add(-10 * time.Minute)
	future := now.Add(3 * time.Hour)
	h.store.put(keyAt(current, ".ndjson"), []byte(record("n1", current)+"\n"))
	h.store.put(keyAt(future, ".ndjson"), []byte(record("nFuture", future)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Errorf("records = %d, want only the current object", got)
	}
	if skippedByReason(h.rec)["future_timestamp"] != 1 {
		t.Errorf("skipped = %v, want the future-dated key counted", skippedByReason(h.rec))
	}
	if len(h.store.fetched) != 1 || h.store.fetched[0] != keyAt(current, ".ndjson") {
		t.Errorf("fetched = %v, want only the current object", h.store.fetched)
	}

	// The cursor is unpoisoned, which is only observable through the next cycle
	// still covering ground: an object landing after cycle one must be found.
	later := now.Add(-5 * time.Minute)
	h.store.put(keyAt(later, ".ndjson"), []byte(record("nLater", later)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 2 {
		t.Errorf("records after the second cycle = %d, want the newly arrived object ingested — the cursor was pushed into the future", got)
	}
}

// The allowance is a fixed five minutes, and it is a real boundary: an export
// written by a host a few minutes fast is legitimate data, one named hours ahead
// is not.
func TestCollect_ClockSkewAllowanceBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		at         time.Time
		wantRecord int
	}{
		{"a second inside the allowance", now.Add(5*time.Minute - time.Second), 1},
		{"exactly at the allowance", now.Add(5 * time.Minute), 1},
		{"a second beyond the allowance", now.Add(5*time.Minute + time.Second), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			// Keep the record timestamp valid so this test isolates object-key
			// clock-skew admission at the exact allowance boundary.
			h.store.put(keyAt(tc.at, ".ndjson"), []byte(record("n1", now)+"\n"))

			h.collect(t)

			if got := h.flowRecords(); got != tc.wantRecord {
				t.Errorf("records = %d, want %d for a key at %s", got, tc.wantRecord, tc.at.Format(time.RFC3339))
			}
			wantSkips := float64(1 - tc.wantRecord)
			if got := skippedByReason(h.rec)["future_timestamp"]; got != wantSkips {
				t.Errorf("future_timestamp skips = %v, want %v", got, wantSkips)
			}
		})
	}
}

// A near-future object is ordinary data — hosts drift — so it is ingested and
// then treated exactly like any other ingested object on the next cycle.
func TestCollect_NearFutureObjectIsIngestedAndNotReIngested(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(2 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("nSkew", at)+"\n"))

	h.collect(t)
	if got := h.flowRecords(); got != 1 {
		t.Fatalf("records = %d, want the near-future object ingested", got)
	}

	h.collect(t)
	if got := h.flowRecords(); got != 1 {
		t.Errorf("records after a second cycle = %d, want 1 — it was ingested twice", got)
	}
}

// Skipping a future key is not permanent: once the wall clock reaches it, the
// object is picked up like any other. Rejection must not enter the seen set.
func TestCollect_FutureKeyIsPickedUpOnceTheClockCatchesUp(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) { o.Now = func() time.Time { return clock } })
	at := now.Add(30 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("nAhead", at)+"\n"))

	h.collect(t)
	if got := h.flowRecords(); got != 0 {
		t.Fatalf("records = %d, want the future key skipped", got)
	}

	clock = now.Add(time.Hour)
	h.collect(t)
	if got := h.flowRecords(); got != 1 {
		t.Errorf("records = %d, want the object ingested once its timestamp is in the past", got)
	}
}

// The cursor is durable, so a future key that reached it would keep ingestion
// wedged across a restart too. A fresh process over the same checkpoint store
// must resume normally.
func TestCollect_FutureKeyDoesNotPoisonTheCursorAcrossARestart(t *testing.T) {
	h := newHarness(t, func(o *objectstore.Options) { o.Lookback = time.Hour })
	current := now.Add(-10 * time.Minute)
	future := now.Add(3 * time.Hour)
	h.store.put(keyAt(current, ".ndjson"), []byte(record("n1", current)+"\n"))
	h.store.put(keyAt(future, ".ndjson"), []byte(record("nFuture", future)+"\n"))
	h.collect(t)

	// A fresh collector over the SAME checkpoint store: a process restart.
	later := now.Add(-5 * time.Minute)
	h.store.put(keyAt(later, ".ndjson"), []byte(record("nLater", later)+"\n"))
	rec2 := telemetrytest.New()
	restarted := newFlowCollector(t, h.store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), h.cp,
		objectstore.Options{
			Prefix: "flow", Lookback: time.Hour,
			Now: func() time.Time { return now }, Logger: discardLogger(),
		})
	if err := restarted.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatal(err)
	}

	if got := len(rec2.LogRecords()); got != 1 {
		t.Errorf("records after a restart = %d, want the newly arrived object — the persisted cursor was in the future", got)
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

// The cursor is the lower bound of the next cycle's listing window, so its age
// is end-to-end ingestion lag. It is never absent: a cold start with no
// persisted cursor is genuinely the initial lookback behind, and reporting zero
// there would claim the feed was caught up when it is hours behind.
func TestCollect_ReportsCursorAge(t *testing.T) {
	t.Run("cold start with nothing to ingest", func(t *testing.T) {
		h := newHarness(t, func(o *objectstore.Options) { o.InitialLookback = 30 * time.Minute })

		h.collect(t)

		if got := lastGauge(h.rec, "tailscale2otel.objectstore.cursor.age"); got != 1800 {
			t.Fatalf("cursor age = %v, want the 30m initial lookback in seconds", got)
		}
	})

	t.Run("advances with the newest ingested object", func(t *testing.T) {
		h := newHarness(t, func(o *objectstore.Options) { o.InitialLookback = 30 * time.Minute })
		for _, ago := range []time.Duration{20 * time.Minute, 10 * time.Minute} {
			at := now.Add(-ago)
			h.store.put(keyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
		}

		h.collect(t)

		if got := lastGauge(h.rec, "tailscale2otel.objectstore.cursor.age"); got != 600 {
			t.Fatalf("cursor age = %v, want 600 — the newest ingested object, not the oldest", got)
		}

		// A quiet cycle does not move the cursor, so the age holds rather than
		// resetting.
		h.collect(t)
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.cursor.age"); got != 600 {
			t.Fatalf("cursor age after a quiet cycle = %v, want it held at 600", got)
		}
	})

	t.Run("carries no attributes", func(t *testing.T) {
		h := newHarness(t, nil)
		h.collect(t)
		for _, p := range h.rec.MetricPoints("tailscale2otel.objectstore.cursor.age") {
			if len(p.Attrs) != 0 {
				t.Errorf("cursor age attributes = %v, want none", p.Attrs)
			}
		}
	})
}

// Discovery freshness answers "is the exporter still writing?", which is a
// different question from "did we ingest anything". Already-ingested objects
// therefore still count, and a cycle that listed nothing usable reports -1 rather
// than a zero that would read as the freshest possible object.
func TestCollect_ReportsNewestDiscoveredObjectAge(t *testing.T) {
	const metric = "tailscale2otel.objectstore.discovered.newest.age"

	t.Run("newest listed object", func(t *testing.T) {
		h := newHarness(t, nil)
		for _, ago := range []time.Duration{30 * time.Minute, 10 * time.Minute} {
			at := now.Add(-ago)
			h.store.put(keyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
		}

		h.collect(t)
		if got := lastGauge(h.rec, metric); got != 600 {
			t.Fatalf("newest discovered age = %v, want 600 (the newest key, not the oldest)", got)
		}

		// A caught-up feed keeps reporting: the objects are listed again and
		// skipped as already ingested, which is still discovery.
		h.collect(t)
		if got := skippedByReason(h.rec)["already_ingested"]; got == 0 {
			t.Fatalf("skipped = %v, want the second cycle to re-list the ingested objects", skippedByReason(h.rec))
		}
		if got := lastGauge(h.rec, metric); got != 600 {
			t.Fatalf("newest discovered age on a caught-up cycle = %v, want it still 600", got)
		}
	})

	t.Run("nothing listed reports the sentinel", func(t *testing.T) {
		h := newHarness(t, nil)

		h.collect(t)

		// Asserted as a PRESENT series holding the sentinel, not merely as a
		// non-zero reading: an absent gauge would satisfy a bare value check while
		// leaving a dashboard with a stale last value re-exported forever.
		if got := len(h.rec.MetricPoints(metric)); got != 1 {
			t.Fatalf("newest discovered age series = %d points, want exactly 1 carrying the sentinel", got)
		}
		if got := lastGauge(h.rec, metric); got != -1 {
			t.Fatalf("newest discovered age over an empty prefix = %v, want -1 — zero would read as a brand-new object", got)
		}
	})

	t.Run("future-stamped objects are not discoveries", func(t *testing.T) {
		h := newHarness(t, nil)
		at := now.Add(10 * time.Minute)
		h.store.put(keyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))

		h.collect(t)

		if got := skippedByReason(h.rec)["future_timestamp"]; got != 1 {
			t.Fatalf("skipped = %v, want the future-stamped object skipped", skippedByReason(h.rec))
		}
		if got := len(h.rec.MetricPoints(metric)); got != 1 {
			t.Fatalf("newest discovered age series = %d points, want exactly 1 carrying the sentinel", got)
		}
		if got := lastGauge(h.rec, metric); got != -1 {
			t.Fatalf("newest discovered age = %v, want -1 — a broken exporter clock must not pin this at zero", got)
		}
	})

	t.Run("carries no attributes", func(t *testing.T) {
		h := newHarness(t, nil)
		h.collect(t)
		for _, p := range h.rec.MetricPoints(metric) {
			if len(p.Attrs) != 0 {
				t.Errorf("newest discovered age attributes = %v, want none", p.Attrs)
			}
		}
	})
}

// pending.oldest.age and gap.oldest.age are different signals and both are kept.
// Pending is BACKLOG latency: the oldest HEALTHY object listed but not yet
// processed. A gap is FAILURE age: the oldest object that failed and is awaiting
// retry or acknowledgement. An object that fails leaves the pending population
// for the gap population, so conflating the two would report the wrong object.
func TestCollect_PendingOldestAgeIsBacklogLatencyNotFailureAge(t *testing.T) {
	const pendingMetric = "tailscale2otel.objectstore.pending.oldest.age"

	t.Run("deferred by the per-cycle budget", func(t *testing.T) {
		h := newHarness(t, func(o *objectstore.Options) { o.MaxObjects = 2 })
		for i := range 5 {
			at := now.Add(-time.Duration(i+1) * time.Minute)
			h.store.put(keyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))
		}

		h.collect(t)

		if got := lastGauge(h.rec, "tailscale2otel.objectstore.backlog"); got != 3 {
			t.Fatalf("backlog = %v, want 3 deferred", got)
		}
		// Oldest REMAINING, not oldest overall (300) and not newest remaining (60):
		// the two oldest were ingested, so the backlog now starts three minutes back.
		if got := lastGauge(h.rec, pendingMetric); got != 180 {
			t.Fatalf("pending oldest age = %v, want 180 — the oldest object still waiting", got)
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
			t.Fatalf("gaps = %v, want 0 — a deferred healthy object is not a failure", got)
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.oldest.age"); got != 0 {
			t.Fatalf("gap oldest age = %v, want 0 — nothing has failed", got)
		}
	})

	t.Run("a failed object leaves the pending population", func(t *testing.T) {
		clock := now
		h := newHarness(t, func(o *objectstore.Options) {
			o.MaxObjects = 1
			o.Now = func() time.Time { return clock }
		})
		failing := now.Add(-20 * time.Minute)
		failingKey := keyAt(failing, ".ndjson")
		h.store.put(failingKey, []byte(record("fails", failing)+"\n"))
		h.store.getErr[failingKey] = errors.New("provider refused the fetch")
		for _, ago := range []time.Duration{15 * time.Minute, 5 * time.Minute} {
			at := now.Add(-ago)
			h.store.put(keyAt(at, ".ndjson"), []byte(record("healthy", at)+"\n"))
		}

		h.collect(t)
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
			t.Fatalf("gaps = %v, want the failed object retained", got)
		}

		// Past the first backoff, the whole budget goes to retrying the gap, so
		// both healthy objects are still waiting.
		clock = now.Add(120 * time.Second)
		h.collect(t)

		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
			t.Fatalf("gaps = %v, want the failed object still unresolved", got)
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.oldest.age"); got != 120 {
			t.Fatalf("gap oldest age = %v, want 120 — the failed object's age", got)
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.backlog"); got != 2 {
			t.Fatalf("backlog = %v, want the 2 healthy objects still waiting", got)
		}
		// 1020 = (now+120s) - (now-15m): the oldest HEALTHY object. Conflating the
		// two populations would report 1320, the failed object's key age.
		if got := lastGauge(h.rec, pendingMetric); got != 1020 {
			t.Fatalf("pending oldest age = %v, want 1020 — the oldest healthy object, not the failed one", got)
		}
	})

	// The failed object is re-LISTED here (its prefix caught up, so no scan
	// position hides it) and is still not pending: the gap row is what excludes
	// it. This is the leg that fails if the two populations are ever merged —
	// pending would report the failed object's 22-minute key age instead of zero.
	t.Run("a gapped object is never counted as pending", func(t *testing.T) {
		clock := now
		h := newHarness(t, func(o *objectstore.Options) {
			o.MaxObjects = 1
			o.Now = func() time.Time { return clock }
		})
		failing := now.Add(-20 * time.Minute)
		failingKey := keyAt(failing, ".ndjson")
		h.store.put(failingKey, []byte(record("fails", failing)+"\n"))
		h.store.getErr[failingKey] = errors.New("provider refused the fetch")

		h.collect(t)
		clock = now.Add(120 * time.Second)
		h.collect(t)

		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
			t.Fatalf("gaps = %v, want the failed object unresolved", got)
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.oldest.age"); got != 120 {
			t.Fatalf("gap oldest age = %v, want 120 — the failure has a real age", got)
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.backlog"); got != 0 {
			t.Errorf("backlog = %v, want 0 — the only object is a gap, not a backlog", got)
		}
		if got := lastGauge(h.rec, pendingMetric); got != 0 {
			t.Fatalf("pending oldest age = %v, want 0 — a failed object's age belongs to gap.oldest.age", got)
		}
		if got := counterTotal(h.rec, "tailscale2otel.objectstore.retries"); got != 1 {
			t.Fatalf("retries = %v, want the one gap retry", got)
		}
	})

	t.Run("nothing pending reports a present zero", func(t *testing.T) {
		h := newHarness(t, nil)
		at := now.Add(-10 * time.Minute)
		h.store.put(keyAt(at, ".ndjson"), []byte(record("n", at)+"\n"))

		h.collect(t)

		if got := len(h.rec.MetricPoints(pendingMetric)); got != 1 {
			t.Fatalf("pending oldest age series = %d points, want exactly 1", got)
		}
		if got := lastGauge(h.rec, pendingMetric); got != 0 {
			t.Fatalf("pending oldest age = %v, want 0 alongside a zero backlog", got)
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.backlog"); got != 0 {
			t.Fatalf("backlog = %v, want 0", got)
		}
		for _, p := range h.rec.MetricPoints(pendingMetric) {
			if len(p.Attrs) != 0 {
				t.Errorf("pending oldest age attributes = %v, want none", p.Attrs)
			}
		}
	})
}
