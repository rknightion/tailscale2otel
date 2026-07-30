package objectstore_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/s3"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// requestsByLabel totals the provider-request counter per "operation/outcome"
// pair. The pair is the whole label set on purpose: both attributes are closed
// two-value sets, so this map can never hold more than four keys.
func requestsByLabel(rec *telemetrytest.Recorder) map[string]float64 {
	out := map[string]float64{}
	for _, p := range rec.MetricPoints("tailscale2otel.objectstore.requests") {
		out[p.Attrs["operation"]+"/"+p.Attrs["outcome"]] += p.Value
	}
	return out
}

// A healthy cycle makes exactly two provider calls — one LIST of the day
// partition and one GET of the object it found — and each is counted once on the
// transport axis.
func TestCollect_CountsProviderRequestsByOperationAndOutcome(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	h.collect(t)

	got := requestsByLabel(h.rec)
	want := map[string]float64{"list/success": 1, "get/success": 1}
	if len(got) != len(want) {
		t.Fatalf("request series = %v, want exactly %v", got, want)
	}
	for label, n := range want {
		if got[label] != n {
			t.Errorf("requests[%s] = %v, want %v", label, got[label], n)
		}
	}
}

// A provider call that itself fails is the only thing that reaches
// outcome=error. The LIST case aborts the cycle; the GET case does not, so the
// same cycle also carries a successful list.
func TestCollect_CountsFailedProviderCallsAsTransportErrors(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		h := newHarness(t, nil)
		h.store.listErr = errors.New("provider refused the listing")

		if err := h.col.Collect(context.Background(), h.rec.Emitter()); err == nil {
			t.Fatal("Collect returned nil, want the listing failure surfaced")
		}

		got := requestsByLabel(h.rec)
		want := map[string]float64{"list/error": 1}
		if len(got) != len(want) || got["list/error"] != 1 {
			t.Fatalf("request series = %v, want exactly %v", got, want)
		}
	})

	t.Run("get", func(t *testing.T) {
		h := newHarness(t, nil)
		at := now.Add(-10 * time.Minute)
		key := keyAt(at, ".ndjson")
		h.store.put(key, []byte(record("n1", at)+"\n"))
		h.store.getErr[key] = errors.New("provider refused the fetch")

		h.collect(t)

		got := requestsByLabel(h.rec)
		want := map[string]float64{"list/success": 1, "get/error": 1}
		if len(got) != len(want) {
			t.Fatalf("request series = %v, want exactly %v", got, want)
		}
		for label, n := range want {
			if got[label] != n {
				t.Errorf("requests[%s] = %v, want %v", label, got[label], n)
			}
		}
	})
}

// slowStore delays every provider call, so the duration histogram observes a
// value a constant-zero implementation could not produce.
type slowStore struct {
	*fakeStore
	delay time.Duration
}

func (s *slowStore) List(ctx context.Context, prefix, startAfter string, limit int) (s3.ListResult, error) {
	time.Sleep(s.delay)
	return s.fakeStore.List(ctx, prefix, startAfter, limit)
}

func (s *slowStore) Get(ctx context.Context, identity string) (io.ReadCloser, error) {
	time.Sleep(s.delay)
	return s.fakeStore.Get(ctx, identity)
}

// requestDurationBuckets pins the declared bucket boundaries from outside the
// package. They reach 60s because an object-store GET is a network fetch of a
// multi-megabyte object, and a clipped top bucket would hide a stalling
// provider.
var requestDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

func TestCollect_RecordsProviderRequestDuration(t *testing.T) {
	const delay = 2 * time.Millisecond
	store := newFakeStore()
	at := now.Add(-10 * time.Minute)
	store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))
	rec := telemetrytest.New()
	col := newFlowCollector(
		t,
		&slowStore{fakeStore: store, delay: delay},
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}),
		collector.NewMemoryStore(),
		objectstore.Options{
			Prefix: "flow",
			Now:    func() time.Time { return now },
			Logger: discardLogger(),
		},
	)
	if err := col.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	points := map[string]telemetrytest.MetricPoint{}
	for _, p := range rec.MetricPoints("tailscale2otel.objectstore.request.duration") {
		points[p.Attrs["operation"]+"/"+p.Attrs["outcome"]] = p
	}
	if len(points) != 2 {
		t.Fatalf("duration series = %v, want exactly list/success and get/success", points)
	}
	for _, label := range []string{"list/success", "get/success"} {
		p, ok := points[label]
		if !ok {
			t.Fatalf("no duration recorded for %s (series: %v)", label, points)
		}
		if p.Kind != "histogram" {
			t.Errorf("%s kind = %q, want histogram", label, p.Kind)
		}
		if p.Unit != "s" {
			t.Errorf("%s unit = %q, want s", label, p.Unit)
		}
		if p.Count != 1 {
			t.Errorf("%s count = %d, want 1", label, p.Count)
		}
		if p.Value < delay.Seconds() {
			t.Errorf("%s sum = %v, want at least the %v the provider call took", label, p.Value, delay)
		}
		if !reflect.DeepEqual(p.Bounds, requestDurationBuckets) {
			t.Errorf("%s bounds = %v, want %v", label, p.Bounds, requestDurationBuckets)
		}
	}
}

// Transport health and ingestion health are separate axes. A row that fails to
// decode, and a body that fails mid-read AFTER the GET returned its reader, are
// both ingestion failures: they land on skipped and the gaps, and the request
// that carried them is still counted as a success. Nothing is counted twice on
// either axis.
func TestCollect_IngestionFailuresAreNotTransportErrors(t *testing.T) {
	t.Run("decode failure", func(t *testing.T) {
		h := newHarness(t, nil)
		at := now.Add(-10 * time.Minute)
		h.store.put(keyAt(at, ".ndjson"), []byte(
			record("n1", at)+"\n"+"{not json\n"+record("n2", at)+"\n"))

		h.collect(t)

		if got := skippedByReason(h.rec)["decode_error"]; got != 1 {
			t.Fatalf("skipped = %v, want the malformed row counted as decode_error", skippedByReason(h.rec))
		}
		got := requestsByLabel(h.rec)
		want := map[string]float64{"list/success": 1, "get/success": 1}
		if len(got) != len(want) {
			t.Fatalf("request series = %v, want exactly %v — a decode failure is not a transport error", got, want)
		}
		for label, n := range want {
			if got[label] != n {
				t.Errorf("requests[%s] = %v, want %v", label, got[label], n)
			}
		}
	})

	t.Run("mid-read body failure", func(t *testing.T) {
		h := newHarness(t, nil)
		at := now.Add(-10 * time.Minute)
		key := keyAt(at, ".ndjson")
		h.store.put(key, []byte(record("n1", at)+"\n"))
		h.store.readErr[key] = errors.New("connection reset while streaming the body")

		h.collect(t)

		if got := skippedByReason(h.rec)["read_error"]; got != 1 {
			t.Fatalf("skipped = %v, want the read failure counted as read_error", skippedByReason(h.rec))
		}
		if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 1 {
			t.Fatalf("gaps = %v, want the failed object retained as a gap", got)
		}
		got := requestsByLabel(h.rec)
		want := map[string]float64{"list/success": 1, "get/success": 1}
		if len(got) != len(want) {
			t.Fatalf("request series = %v, want exactly %v — the GET itself returned a reader", got, want)
		}
		for label, n := range want {
			if got[label] != n {
				t.Errorf("requests[%s] = %v, want %v", label, got[label], n)
			}
		}
	})
}

// counterTotal reads a nil-attribute counter's cumulative value, and reports -1
// when the series does not exist at all — "no data" and "zero" are different
// answers.
func counterTotal(rec *telemetrytest.Recorder, name string) float64 {
	pts := rec.MetricPoints(name)
	if len(pts) == 0 {
		return -1
	}
	var total float64
	for _, p := range pts {
		total += p.Value
	}
	return total
}

// The retry concept is OBJECT-level, from the durable gap mechanism: the first
// attempt on a newly listed object is not a retry, and every later attempt on
// its gap row is. The provider layer has no retry of its own to count.
func TestCollect_CountsObjectLevelRetriesNotFirstAttempts(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.Now = func() time.Time { return clock }
	})
	at := now.Add(-10 * time.Minute)
	key := keyAt(at, ".ndjson")
	h.store.put(key, []byte(record("n1", at)+"\n"))
	h.store.getErr[key] = errors.New("provider refused the fetch")

	h.collect(t)
	if got := counterTotal(h.rec, "tailscale2otel.objectstore.retries"); got != 0 {
		t.Fatalf("retries after the first failure = %v, want 0 emitted (a first attempt is not a retry)", got)
	}

	// Past the first backoff: the gap row is attempted again and fails again.
	clock = now.Add(90 * time.Second)
	h.collect(t)
	if got := counterTotal(h.rec, "tailscale2otel.objectstore.retries"); got != 1 {
		t.Fatalf("retries after the second cycle = %v, want 1", got)
	}

	// Past the second backoff, with the provider healthy: one more retry, which
	// resolves the gap.
	delete(h.store.getErr, key)
	clock = now.Add(240 * time.Second)
	h.collect(t)
	if got := counterTotal(h.rec, "tailscale2otel.objectstore.retries"); got != 2 {
		t.Fatalf("retries after the recovery cycle = %v, want 2", got)
	}
	if got := lastGauge(h.rec, "tailscale2otel.objectstore.gaps"); got != 0 {
		t.Fatalf("gaps = %v, want the retried object resolved", got)
	}
	if got := h.flowRecords(); got != 1 {
		t.Fatalf("flow records = %d, want the retried object ingested once", got)
	}

	for _, p := range h.rec.MetricPoints("tailscale2otel.objectstore.retries") {
		if len(p.Attrs) != 0 {
			t.Errorf("retries attributes = %v, want none", p.Attrs)
		}
	}
}

// Everything this collector emits must be declared, and the two attributed
// signals must stay inside their closed sets. Four series per instrument is the
// whole cardinality budget for the transport axis, whatever the bucket contains.
func TestCollect_TransportTelemetryIsDeclaredAndBounded(t *testing.T) {
	clock := now
	h := newHarness(t, func(o *objectstore.Options) {
		o.MaxObjects = 2
		o.Now = func() time.Time { return clock }
	})
	// One object with a bad row (ingestion failure), one whose GET fails
	// (transport failure, then a retry), one deferred by the budget.
	partial := now.Add(-30 * time.Minute)
	h.store.put(keyAt(partial, ".ndjson"), []byte(record("good", partial)+"\n{not json\n"))
	failing := now.Add(-20 * time.Minute)
	failingKey := keyAt(failing, ".ndjson")
	h.store.put(failingKey, []byte(record("n", failing)+"\n"))
	h.store.getErr[failingKey] = errors.New("provider refused the fetch")
	deferred := now.Add(-10 * time.Minute)
	h.store.put(keyAt(deferred, ".ndjson"), []byte(record("n", deferred)+"\n"))

	h.collect(t)
	clock = now.Add(90 * time.Second)
	h.collect(t)
	h.store.listErr = errors.New("provider refused the listing")
	if err := h.col.Collect(context.Background(), h.rec.Emitter()); err == nil {
		t.Fatal("Collect returned nil, want the listing failure surfaced")
	}

	got := requestsByLabel(h.rec)
	for _, label := range []string{"list/success", "list/error", "get/success", "get/error"} {
		if got[label] == 0 {
			t.Fatalf("request series = %v, want every closed-set combination exercised", got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("request series = %v, want exactly the 4 closed-set combinations", got)
	}

	operations := map[string]bool{"list": true, "get": true}
	outcomes := map[string]bool{"success": true, "error": true}
	for _, name := range []string{
		"tailscale2otel.objectstore.requests",
		"tailscale2otel.objectstore.request.duration",
	} {
		points := h.rec.MetricPoints(name)
		if len(points) > 4 {
			t.Errorf("%s = %d series, want at most 4", name, len(points))
		}
		for _, p := range points {
			if len(p.Attrs) != 2 {
				t.Errorf("%s attributes = %v, want exactly operation and outcome", name, p.Attrs)
			}
			if !operations[p.Attrs["operation"]] {
				t.Errorf("%s operation = %q, outside the closed set", name, p.Attrs["operation"])
			}
			if !outcomes[p.Attrs["outcome"]] {
				t.Errorf("%s outcome = %q, outside the closed set", name, p.Attrs["outcome"])
			}
		}
	}

	for _, name := range []string{
		"tailscale2otel.objectstore.retries",
		"tailscale2otel.objectstore.cursor.age",
		"tailscale2otel.objectstore.discovered.newest.age",
		"tailscale2otel.objectstore.pending.oldest.age",
	} {
		points := h.rec.MetricPoints(name)
		if len(points) != 1 {
			t.Errorf("%s = %d series, want exactly 1 unattributed series", name, len(points))
		}
		for _, p := range points {
			if len(p.Attrs) != 0 {
				t.Errorf("%s attributes = %v, want none", name, p.Attrs)
			}
		}
	}

	// Declared-vs-emitted drift: a signal emitted without a descriptor is missing
	// from docs/metrics.md, and an attribute emitted without being declared is the
	// drift class AssertCatalogAttrs exists to catch.
	declared := map[string]metricdoc.Metric{}
	for _, m := range objectstore.Catalog() {
		declared[m.Name] = m
	}
	for _, name := range h.rec.MetricNames() {
		if !strings.HasPrefix(name, "tailscale2otel.objectstore.") {
			continue
		}
		m, ok := declared[name]
		if !ok {
			t.Errorf("emitted metric %q is not declared in Catalog()", name)
			continue
		}
		for _, p := range h.rec.MetricPoints(name) {
			if p.Unit != m.Unit {
				t.Errorf("%s unit = %q, declared %q", name, p.Unit, m.Unit)
			}
			if p.Description != m.Description {
				t.Errorf("%s description drifted from its descriptor", name)
			}
		}
	}
	telemetrytest.AssertCatalogAttrs(t, h.rec, objectstore.Catalog(), nil)

	// The six additions are declared with the instrument and unit the frozen seam
	// specifies, so the generated Prometheus names cannot drift.
	for _, want := range []metricdoc.Metric{
		{Name: "tailscale2otel.objectstore.requests", Instrument: metricdoc.Counter, Unit: "1"},
		{Name: "tailscale2otel.objectstore.request.duration", Instrument: metricdoc.Histogram, Unit: "s"},
		{Name: "tailscale2otel.objectstore.retries", Instrument: metricdoc.Counter, Unit: "1"},
		{Name: "tailscale2otel.objectstore.cursor.age", Instrument: metricdoc.Gauge, Unit: "s"},
		{Name: "tailscale2otel.objectstore.discovered.newest.age", Instrument: metricdoc.Gauge, Unit: "s"},
		{Name: "tailscale2otel.objectstore.pending.oldest.age", Instrument: metricdoc.Gauge, Unit: "s"},
	} {
		m, ok := declared[want.Name]
		if !ok {
			t.Errorf("Catalog() is missing %q", want.Name)
			continue
		}
		if m.Instrument != want.Instrument || m.Unit != want.Unit {
			t.Errorf("%s declared as %s/%q, want %s/%q", want.Name, m.Instrument, m.Unit, want.Instrument, want.Unit)
		}
	}
}
