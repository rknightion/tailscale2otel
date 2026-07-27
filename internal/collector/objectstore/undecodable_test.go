package objectstore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/collector"
	"github.com/rknightion/tailscale2otel/v3/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// framingMismatchBody is what an object looks like when the export is not
// newline-delimited bare records: one physical row that cannot decode into a
// record. Every row fails, so nothing is extracted — the signature of a framing
// mismatch rather than of one corrupt record.
const framingMismatchBody = `[{"nodeId":"n1"},{"nodeId":"n2"}]`

// An object that yields zero accepted records while at least one row failed is a
// failed object. Before this guard it was recorded as ingested: the cursor moved
// past it, no gap existed, gap.healthy stayed at 1, and the data was gone.
func TestCollect_EveryRowFailingToDecodeFailsTheObject(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(framingMismatchBody))

	h.collect(t)

	if got := h.flowRecords(); got != 0 {
		t.Fatalf("flow records = %d, want 0", got)
	}
	if got := metricTotal(h.rec, "tailscale2otel.objectstore.objects"); got != 0 {
		t.Errorf("objects = %v, want 0 — an object that decoded nothing was not ingested", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Errorf("gaps = %v, want 1 durable gap", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 0 {
		t.Errorf("gap healthy = %v, want 0", got)
	}

	var gap, seen, cursor bool
	for _, key := range h.cp.Keys() {
		gap = gap || strings.Contains(key, "/gap/")
		seen = seen || strings.Contains(key, "/seen/")
		cursor = cursor || strings.HasSuffix(key, "/cursor")
	}
	if !gap {
		t.Errorf("checkpoint keys = %v, want a durable gap row", h.cp.Keys())
	}
	if seen {
		t.Errorf("checkpoint keys = %v, want no seen identity for a failed object", h.cp.Keys())
	}
	if cursor {
		t.Errorf("checkpoint keys = %v, want the cursor left behind the failed object", h.cp.Keys())
	}
}

// A retry must come from the durable gap, not from re-listing, and it must
// succeed once the framing is understood.
func TestCollect_UndecodableObjectIsRetriedFromItsDurableGap(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Now = func() time.Time { return clock }
	})
	at := now.Add(-10 * time.Minute)
	key := keyAt(at, ".ndjson")
	h.store.put(key, []byte(framingMismatchBody))

	h.collect(t)
	if got := len(h.store.getCalls); got != 1 {
		t.Fatalf("GET calls = %d, want the first attempt", got)
	}

	// Repair the object's framing and hide it from listing, so only the durable
	// gap can drive the retry.
	h.store.put(key, []byte(record("n1", at)+"\n"))
	h.store.listHidden[key] = true
	clock = now.Add(2 * time.Minute)
	h.collect(t)

	if got := len(h.store.getCalls); got != 2 {
		t.Fatalf("GET calls = %d, want a retry driven by the gap alone", got)
	}
	if got := h.flowRecords(); got != 1 {
		t.Fatalf("flow records = %d, want the object recovered on retry", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 1 {
		t.Errorf("gap healthy = %v, want 1 once the gap resolved", got)
	}
	for _, key := range h.cp.Keys() {
		if strings.Contains(key, "/gap/") {
			t.Errorf("resolved gap row remains: %q", key)
		}
	}
}

// The new condition must be visible without becoming a cardinality or privacy
// problem: a closed reason value, no object key, bucket, or free-text error in
// any attribute, and a hashed digest rather than the key in local diagnostics.
func TestCollect_UndecodableObjectTelemetryIsBoundedAndKeyFree(t *testing.T) {
	var logs bytes.Buffer
	h := newHarness(t, func(o *objectstore.Options) {
		o.Prefix = "customer-private/flow"
		o.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	})
	at := now.Add(-10 * time.Minute)
	key := "customer-private/flow/" + at.UTC().Format("2006/01/02/15:04:05") + ".json"
	h.store.put(key, []byte(framingMismatchBody))

	h.collect(t)

	for _, name := range h.rec.MetricNames() {
		for _, point := range h.rec.MetricPoints(name) {
			for attr, value := range point.Attrs {
				if strings.Contains(value, key) || strings.Contains(value, "customer-private") {
					t.Errorf("metric %s attribute %s=%q exposes the object key", name, attr, value)
				}
			}
		}
	}
	// The reason label stays a closed set, so the new value cannot become an
	// unbounded dimension.
	closed := map[string]bool{
		"per_cycle_budget": true, "already_ingested": true, "unrecognized_key": true,
		"before_cursor": true, "future_timestamp": true, "decode_error": true,
		"semantic_invalid": true, "read_error": true, "undecodable_object": true,
	}
	for reason := range skippedByReason(h.rec) {
		if !closed[reason] {
			t.Errorf("skipped reason %q is outside the closed set", reason)
		}
	}
	if got := skippedByReason(h.rec)["undecodable_object"]; got != 1 {
		t.Errorf("skipped = %v, want one undecodable_object", skippedByReason(h.rec))
	}
	got := logs.String()
	if strings.Contains(got, key) || strings.Contains(got, "customer-private") {
		t.Fatalf("diagnostics leaked the raw object key: %s", got)
	}
	sum := sha256.Sum256([]byte(key))
	if wantDigest := hex.EncodeToString(sum[:6]); !strings.Contains(got, wantDigest) {
		t.Fatalf("diagnostics = %q, want the object digest %q", got, wantDigest)
	}
	if !strings.Contains(got, "stage=undecodable_object") {
		t.Errorf("diagnostics = %q, want the bounded failure stage", got)
	}
}

// invalidRecord is a decodable record that fails source validation, so the row
// takes the ErrRecordInvalid path rather than the decode path.
func invalidRecord(t *testing.T, nodeID string, at time.Time) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(record(nodeID, at)), &decoded); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	traffic, ok := decoded["virtualTraffic"].([]any)
	if !ok || len(traffic) == 0 {
		t.Fatalf("fixture has no virtualTraffic: %v", decoded)
	}
	conn, ok := traffic[0].(map[string]any)
	if !ok {
		t.Fatalf("fixture connection is not an object: %v", traffic[0])
	}
	conn["txBytes"] = float64(-1)
	line, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return string(line)
}

// Validation failures count toward the guard exactly as decode failures do: an
// object whose every row is semantically invalid extracted nothing either. And
// because the decision precedes commit, not even the deferred data-quality
// observation of those rows may be emitted (#448).
func TestCollect_EveryRowFailingValidationFailsTheObjectWithoutCommitting(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(
		invalidRecord(t, "bad1", at)+"\n"+invalidRecord(t, "bad2", at)+"\n"))

	h.collect(t)

	if got := metricTotal(h.rec, "tailscale2otel.objectstore.objects"); got != 0 {
		t.Errorf("objects = %v, want 0", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 0 {
		t.Errorf("gap healthy = %v, want 0", got)
	}
	if got := skippedByReason(h.rec)["undecodable_object"]; got != 1 {
		t.Errorf("skipped = %v, want one undecodable_object", skippedByReason(h.rec))
	}
	if got := skippedByReason(h.rec)["semantic_invalid"]; got != 0 {
		t.Errorf("semantic_invalid = %v, want 0 — rows of a failed object get no row-local disposition", got)
	}
	if got := len(h.rec.MetricPoints(flowlog.MetricDataQuality)); got != 0 {
		t.Errorf("data-quality points = %d, want 0 — a failed object must commit nothing", got)
	}
}

// The failure is object-scoped, not cycle-scoped: an undecodable object must not
// take a healthy sibling down with it, and its own reason must be the one an
// operator sees.
func TestCollect_UndecodableObjectIsReportedWithoutStoppingHealthyObjects(t *testing.T) {
	h := newHarness(t, nil)
	badAt := now.Add(-20 * time.Minute)
	goodAt := now.Add(-10 * time.Minute)
	h.store.put(keyAt(badAt, ".ndjson"), []byte(framingMismatchBody))
	h.store.put(keyAt(goodAt, ".ndjson"), []byte(record("n1", goodAt)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Fatalf("flow records = %d, want the healthy object ingested", got)
	}
	if got := metricTotal(h.rec, "tailscale2otel.objectstore.objects"); got != 1 {
		t.Errorf("objects = %v, want only the healthy object counted", got)
	}
	reasons := skippedByReason(h.rec)
	if reasons["undecodable_object"] != 1 {
		t.Errorf("skipped = %v, want one undecodable_object", reasons)
	}
	if reasons["read_error"] != 0 {
		t.Errorf("skipped = %v, want no read_error — the object was read fine", reasons)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
		t.Errorf("gaps = %v, want the undecodable object retained", got)
	}
}

// Tailscale writes zero-length objects (tailscale/tailscale#16124). No rows and
// no failures is not a failure: the object completes, creates no gap, and leaves
// ingestion healthy. This is the crux of the guard — "no rows at all" must never
// be confused with "rows that all failed".
func TestCollect_EmptyObjectCompletesWithoutAGap(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(""))

	h.collect(t)

	if got := metricTotal(h.rec, "tailscale2otel.objectstore.objects"); got != 1 {
		t.Errorf("objects = %v, want the empty object completed", got)
	}
	if got := metricTotal(h.rec, "tailscale2otel.objectstore.records"); got != 0 {
		t.Errorf("records = %v, want 0", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Errorf("gaps = %v, want none for an empty object", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 1 {
		t.Errorf("gap healthy = %v, want 1", got)
	}
	if got := skippedByReason(h.rec)["undecodable_object"]; got != 0 {
		t.Errorf("skipped = %v, want no undecodable_object for an empty object", skippedByReason(h.rec))
	}
	h.collect(t)
	if got := len(h.store.getCalls); got != 1 {
		t.Errorf("GET calls = %d, want the empty object never retried", got)
	}
}

// Blank rows are skipped without being failures, so an object of nothing but
// blank rows is the empty case, not the all-failed case.
func TestCollect_BlankRowsAloneDoNotFailTheObject(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte("\n\n   \n\n"))

	h.collect(t)

	if got := metricTotal(h.rec, "tailscale2otel.objectstore.objects"); got != 1 {
		t.Errorf("objects = %v, want the blank object completed", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 1 {
		t.Errorf("gap healthy = %v, want 1", got)
	}
	if got := skippedByReason(h.rec)["undecodable_object"]; got != 0 {
		t.Errorf("skipped = %v, want no undecodable_object for blank rows", skippedByReason(h.rec))
	}
}

// Mixed objects keep today's row-local behavior exactly. Blank rows still count
// toward neither side of the guard, and one good row is enough to complete the
// object however many of its siblings failed.
func TestCollect_MixedObjectKeepsRowLocalBehavior(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(
		"{not json\n"+"\n"+invalidRecord(t, "bad", at)+"\n"+record("good", at)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Fatalf("flow records = %d, want the one good row", got)
	}
	if got := metricTotal(h.rec, "tailscale2otel.objectstore.objects"); got != 1 {
		t.Errorf("objects = %v, want the object completed", got)
	}
	reasons := skippedByReason(h.rec)
	if reasons["decode_error"] != 1 || reasons["semantic_invalid"] != 1 {
		t.Errorf("skipped = %v, want one decode_error and one semantic_invalid", reasons)
	}
	if reasons["undecodable_object"] != 0 {
		t.Errorf("skipped = %v, want no undecodable_object when a row was accepted", reasons)
	}
	if got := len(h.rec.MetricPoints(flowlog.MetricDataQuality)); got != 1 {
		t.Errorf("data-quality points = %d, want the invalid row still observed", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 1 {
		t.Errorf("gap healthy = %v, want 1 — a mixed object creates no gap", got)
	}
	for _, key := range h.cp.Keys() {
		if strings.Contains(key, "/gap/") {
			t.Errorf("mixed object created a gap: %q", key)
		}
	}
}

// The gap is durable state, so a restart must find it and retry it without
// re-listing the object.
func TestCollect_UndecodableGapSurvivesRestart(t *testing.T) {
	clock := now
	store := newFakeStore()
	at := now.Add(-10 * time.Minute)
	key := keyAt(at, ".ndjson")
	store.put(key, []byte(framingMismatchBody))

	checkpointPath := t.TempDir() + "/checkpoints.json"
	cp, err := collector.NewFileStore(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	newCollector := func(cp collector.CheckpointStore) *objectstore.Collector {
		return newFlowCollector(
			t,
			store,
			flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
			cp,
			objectstore.Options{
				Prefix: "flow",
				Now:    func() time.Time { return clock },
				Logger: discardLogger(),
			},
		)
	}
	rec := telemetrytest.New()
	if err := newCollector(cp).Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatal(err)
	}
	var gapRow string
	for _, checkpointKey := range cp.Keys() {
		if strings.Contains(checkpointKey, "/gap/") {
			gapRow = checkpointKey
		}
	}
	if gapRow == "" {
		t.Fatalf("checkpoint keys = %v, want a durable gap row", cp.Keys())
	}

	store.put(key, []byte(record("recovered", at)+"\n"))
	store.listHidden[key] = true
	clock = now.Add(2 * time.Minute)
	reopened, err := collector.NewFileStore(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := telemetrytest.New()
	if err := newCollector(reopened).Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatal(err)
	}
	if got := len(rec2.LogRecords()); got != 1 {
		t.Fatalf("records after restart = %d, want the gap retried from durable state alone", got)
	}
	if got := lastGauge(rec2, "tailscale2otel.objectstore.gap.healthy"); got != 1 {
		t.Errorf("gap healthy = %v, want 1 after recovery", got)
	}
	for _, checkpointKey := range reopened.Keys() {
		if strings.Contains(checkpointKey, "/gap/") {
			t.Errorf("resolved gap row remains: %q", checkpointKey)
		}
	}
}

// A framing mismatch keeps failing, so this pins what the EXISTING bounded retry
// policy does with it: capped exponential backoff, retried forever, never
// silently abandoned and never self-quarantined. Quarantine in this engine is a
// property of the failure classification, not of the attempt count, and this
// stage is deliberately not a quarantining one — so the object stays recoverable
// if a later release understands the framing.
func TestCollect_RepeatedUndecodableFailureStaysPendingAndKeepsRetrying(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Now = func() time.Time { return clock }
	})
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(framingMismatchBody))

	h.collect(t)
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
		h.collect(t)
	}

	if got := len(h.store.getCalls); got != 9 {
		t.Fatalf("GET calls = %d, want the initial attempt plus eight bounded retries", got)
	}
	var gapRows []string
	for _, key := range h.cp.Keys() {
		if strings.Contains(key, "/gap/") {
			gapRows = append(gapRows, key)
		}
	}
	if len(gapRows) != 1 {
		t.Fatalf("gap rows = %v, want exactly one", gapRows)
	}
	if !strings.Contains(gapRows[0], "/gap/pending/") {
		t.Errorf("gap row = %q, want a pending gap: repeated failure does not self-quarantine", gapRows[0])
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gap.healthy"); got != 0 {
		t.Errorf("gap healthy = %v, want a sustained 0 while the feed stays broken", got)
	}
}
