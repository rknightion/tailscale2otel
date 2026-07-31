package objectstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/objectstore"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// availabilityStates returns, per operation, the single state whose gauge is 1.
// Copied from internal/collector/postureintegrations/postureintegrations_test.go
// (#420/#430/#524): every collector wired to apistate asserts availability the
// same way, so one operation being at two states simultaneously is always a bug.
func availabilityStates(t *testing.T, rec *telemetrytest.Recorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		op := p.Attrs["tailscale.api.operation"]
		st := p.Attrs["tailscale.api.state"]
		switch p.Value {
		case 1:
			if prev, dup := out[op]; dup {
				t.Fatalf("operation %q has two states at 1: %q and %q", op, prev, st)
			}
			out[op] = st
		case 0:
		default:
			t.Fatalf("availability gauge for %q/%q = %v, want 0 or 1", op, st, p.Value)
		}
	}
	return out
}

// A healthy cycle that lists and ingests successfully must report listObjects
// as supported on BOTH surfaces apistate.Observe drives: the OTLP availability
// metric (asserted here) and the in-process tracker the admin status page and
// capability matrix read (asserted via the same Observe call, so a supported
// gauge is sufficient evidence the tracker was updated too).
func TestCollect_ListSuccessReportsSupportedAvailability(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	h.collect(t)

	if got := availabilityStates(t, h.rec)["listObjects"]; got != string(apistate.StateSupported) {
		t.Errorf("listObjects availability = %q, want %q", got, apistate.StateSupported)
	}
}

// A LIST failure has no upstream HTTP status to classify — object-store
// backends don't surface one — so apistate.Classify's default branch reads it
// as transient_failure. That is a known, accepted imprecision (a permanently
// denied bucket also reads transient_failure rather than credential_rejected)
// documented on Options.APIState's call site; the point of this test is that
// Collect still returns the same list failure it always has, unaffected by
// the new Observe call sitting beside it.
func TestCollect_ListFailureReportsTransientFailureAvailability(t *testing.T) {
	h := newHarness(t, nil)
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))
	wantErr := errors.New("connection refused")
	h.store.listErr = wantErr

	err := h.col.Collect(context.Background(), h.rec.Emitter())
	if err == nil {
		t.Fatal("Collect returned nil, want the listing failure surfaced")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Collect error = %v, want it to wrap %v", err, wantErr)
	}

	if got := availabilityStates(t, h.rec)["listObjects"]; got != string(apistate.StateTransientFailure) {
		t.Errorf("listObjects availability = %q, want %q", got, apistate.StateTransientFailure)
	}
}

// A per-OBJECT failure (here, a GET that the fake backend refuses) is a
// completely different signal from a LIST failure: the bucket was reachable
// and listable, the one object just could not be fetched. That is already
// reported by tailscale2otel.objectstore.skipped and the gap machinery, so
// listObjects must still read supported — conflating the two would make an
// object-level hiccup look like the whole feed is down.
func TestCollect_ObjectLevelFailureStillReportsSupportedListAvailability(t *testing.T) {
	h := newHarness(t, nil)
	bad := now.Add(-20 * time.Minute)
	good := now.Add(-10 * time.Minute)
	h.store.put(keyAt(bad, ".ndjson"), nil)
	h.store.getErr[keyAt(bad, ".ndjson")] = errors.New("access denied")
	h.store.put(keyAt(good, ".ndjson"), []byte(record("n1", good)+"\n"))

	h.collect(t)

	if got := h.flowRecords(); got != 1 {
		t.Fatalf("records = %d, want the readable object ingested", got)
	}
	if got := skippedByReason(h.rec)["read_error"]; got != 1 {
		t.Fatalf("skipped = %v, want the object-level failure counted separately", skippedByReason(h.rec))
	}
	if got := availabilityStates(t, h.rec)["listObjects"]; got != string(apistate.StateSupported) {
		t.Errorf("listObjects availability = %q, want %q — a GET failure is not a LIST failure", got, apistate.StateSupported)
	}
}

// A tick that derives zero day partitions to list — here, a persisted cursor
// far ahead of a wildly earlier clock, which collapses the scan window so
// dayPrefixes returns none — must make ZERO Backend.List calls. A probe that
// never ran must stay unknown rather than claim any state, so no availability
// entry is emitted at all this cycle.
func TestCollect_NoListCallEmitsNoAvailabilityEntry(t *testing.T) {
	tracker := apistate.NewTracker()
	h := newHarness(t, func(o *objectstore.Options) { o.APIState = tracker })
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))
	h.collect(t)
	if got := availabilityStates(t, h.rec)["listObjects"]; got != string(apistate.StateSupported) {
		t.Fatalf("setup: listObjects availability = %q, want %q", got, apistate.StateSupported)
	}
	listCallsBefore := len(h.store.listCalls)

	// A fresh collector over the SAME checkpoint store (so the persisted cursor
	// carries over) but a clock ten hours BEFORE that cursor: from (cursor minus
	// the lookback) then sits after "now", so dayPrefixes derives no partitions
	// to list at all.
	past := now.Add(-10 * time.Hour)
	rec2 := telemetrytest.New()
	col2 := newFlowCollector(t, h.store,
		flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{}), h.cp,
		objectstore.Options{
			Prefix:   "flow",
			Now:      func() time.Time { return past },
			Logger:   discardLogger(),
			APIState: tracker,
		})
	if err := col2.Collect(context.Background(), rec2.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got := len(h.store.listCalls) - listCallsBefore; got != 0 {
		t.Fatalf("List calls made this tick = %d, want 0 (test setup invalid)", got)
	}
	if got := len(rec2.MetricPoints(apistate.MetricAvailability)); got != 0 {
		t.Fatalf("availability points emitted = %d, want none — a probe that never ran must not claim a state", got)
	}
}

// A nil Options.APIState must not panic, and the OTLP availability metric is
// still emitted regardless — EmitAvailability only needs a non-nil Emitter,
// and apistate.Record on a nil *Tracker is a documented no-op. The admin
// status page simply has nothing to read for this collector when it is wired
// without a tracker.
func TestCollect_NilAPIStateTrackerIsANoOp(t *testing.T) {
	h := newHarness(t, nil) // no o.APIState set: Options.APIState stays nil
	at := now.Add(-10 * time.Minute)
	h.store.put(keyAt(at, ".ndjson"), []byte(record("n1", at)+"\n"))

	h.collect(t)

	if got := availabilityStates(t, h.rec)["listObjects"]; got != string(apistate.StateSupported) {
		t.Errorf("listObjects availability = %q, want %q even with a nil tracker", got, apistate.StateSupported)
	}
}
