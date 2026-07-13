package collector_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// runRecorderScheduler starts a Scheduler wired to the given Recorder's emitter
// with jitter disabled, a fixed clock, and an in-memory checkpoint store.
func runRecorderScheduler(t *testing.T, r *collector.Registry, rec *telemetrytest.Recorder, now time.Time, opts ...collector.SchedulerOption) {
	t.Helper()
	runRecorderSchedulerStore(t, r, rec, now, collector.NewMemoryStore(), opts...)
}

// runRecorderSchedulerStore is runRecorderScheduler with an injectable checkpoint
// store, so tests can drive the checkpoint-persist failure path.
func runRecorderSchedulerStore(t *testing.T, r *collector.Registry, rec *telemetrytest.Recorder, now time.Time, store collector.CheckpointStore, opts ...collector.SchedulerOption) {
	t.Helper()
	base := []collector.SchedulerOption{
		collector.WithStaggerWindow(0),
		collector.WithClock(func() time.Time { return now }),
	}
	s := collector.NewScheduler(rec.Emitter(), store, append(base, opts...)...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx, r); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// errSetStore forces a window to be computed (Get reports no checkpoint) and
// then fails to persist it, exercising the checkpoint-persist error path.
type errSetStore struct{}

func (errSetStore) Get(string) (time.Time, bool) { return time.Time{}, false }
func (errSetStore) Set(string, time.Time) error  { return errors.New("disk full") }
func (errSetStore) Keys() []string               { return nil }
func (errSetStore) Delete(string) error          { return nil }

// findPoint returns the first metric point for name carrying the given collector
// attribute, and whether one was found.
func findPoint(rec *telemetrytest.Recorder, name, coll string) (telemetrytest.MetricPoint, bool) {
	for _, p := range rec.MetricPoints(name) {
		if p.Attrs[semconv.AttrCollector] == coll {
			return p, true
		}
	}
	return telemetrytest.MetricPoint{}, false
}

func TestSelfObs_SuccessfulSnapshotEmitsScrapeMetrics(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "ok", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		return nil
	}}, time.Millisecond)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	waitFor(t, func() bool {
		_, ok := findPoint(rec, collector.MetricScrapeSuccess, "ok")
		return ok
	}, 2*time.Second)

	success, ok := findPoint(rec, collector.MetricScrapeSuccess, "ok")
	if !ok {
		t.Fatalf("%s not emitted for collector ok", collector.MetricScrapeSuccess)
	}
	if success.Kind != "gauge" || success.Value != 1 {
		t.Fatalf("success = %+v, want gauge value 1", success)
	}
	if success.Unit != semconv.UnitDimensionless {
		t.Fatalf("success unit = %q, want %q", success.Unit, semconv.UnitDimensionless)
	}

	dur, ok := findPoint(rec, collector.MetricScrapeDuration, "ok")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricScrapeDuration)
	}
	if dur.Kind != "gauge" || dur.Unit != semconv.UnitSeconds || dur.Value < 0 {
		t.Fatalf("duration = %+v, want non-negative gauge in seconds", dur)
	}

	ts, ok := findPoint(rec, collector.MetricScrapeLastTimestamp, "ok")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricScrapeLastTimestamp)
	}
	if ts.Kind != "gauge" || ts.Unit != semconv.UnitSeconds {
		t.Fatalf("last_timestamp = %+v, want gauge in seconds", ts)
	}
	if want := float64(now.Unix()); ts.Value != want {
		t.Fatalf("last_timestamp value = %v, want %v (clock unix)", ts.Value, want)
	}

	// A clean run must not record any scrape errors.
	if pts := rec.MetricPoints(collector.MetricScrapeErrors); len(pts) != 0 {
		t.Fatalf("scrape errors emitted on success: %+v", pts)
	}
}

func TestSelfObs_FailingSnapshotRecordsErrorAndZeroSuccess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "bad", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		return errors.New("boom")
	}}, time.Millisecond)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	waitFor(t, func() bool {
		p, ok := findPoint(rec, collector.MetricScrapeErrors, "bad")
		return ok && p.Value >= 1
	}, 2*time.Second)

	success, ok := findPoint(rec, collector.MetricScrapeSuccess, "bad")
	if !ok || success.Value != 0 {
		t.Fatalf("success = %+v (ok=%v), want gauge value 0", success, ok)
	}

	errPt, ok := findPoint(rec, collector.MetricScrapeErrors, "bad")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricScrapeErrors)
	}
	if errPt.Kind != "sum" || !errPt.Monotonic {
		t.Fatalf("errors = %+v, want monotonic sum (counter)", errPt)
	}
	if errPt.Unit != semconv.UnitDimensionless {
		t.Fatalf("errors unit = %q, want %q", errPt.Unit, semconv.UnitDimensionless)
	}
	if errPt.Attrs["error.type"] != "error" {
		t.Fatalf("error.type = %q, want \"error\"", errPt.Attrs["error.type"])
	}

	// Duration and last_timestamp must still be emitted on failure.
	if _, ok := findPoint(rec, collector.MetricScrapeDuration, "bad"); !ok {
		t.Fatalf("%s not emitted on failure", collector.MetricScrapeDuration)
	}
	if _, ok := findPoint(rec, collector.MetricScrapeLastTimestamp, "bad"); !ok {
		t.Fatalf("%s not emitted on failure", collector.MetricScrapeLastTimestamp)
	}
}

func TestSelfObs_TimeoutErrorIsClassifiedAsTimeout(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "slow", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		return context.DeadlineExceeded
	}}, time.Millisecond)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	waitFor(t, func() bool {
		p, ok := findPoint(rec, collector.MetricScrapeErrors, "slow")
		return ok && p.Attrs["error.type"] == "timeout"
	}, 2*time.Second)

	p, ok := findPoint(rec, collector.MetricScrapeErrors, "slow")
	if !ok || p.Attrs["error.type"] != "timeout" {
		t.Fatalf("error.type = %q (ok=%v), want \"timeout\"", p.Attrs["error.type"], ok)
	}
}

func TestSelfObs_PanickingSnapshotRecordsPanic(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "panicker", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		panic("kaboom")
	}}, time.Millisecond)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	waitFor(t, func() bool {
		p, ok := findPoint(rec, collector.MetricScrapeErrors, "panicker")
		return ok && p.Attrs["error.type"] == "panic"
	}, 2*time.Second)

	errPt, ok := findPoint(rec, collector.MetricScrapeErrors, "panicker")
	if !ok || errPt.Attrs["error.type"] != "panic" {
		t.Fatalf("error.type = %q (ok=%v), want \"panic\"", errPt.Attrs["error.type"], ok)
	}

	success, ok := findPoint(rec, collector.MetricScrapeSuccess, "panicker")
	if !ok || success.Value != 0 {
		t.Fatalf("success = %+v (ok=%v), want gauge value 0 on panic", success, ok)
	}
	// Duration and last_timestamp must still be emitted on panic.
	if _, ok := findPoint(rec, collector.MetricScrapeDuration, "panicker"); !ok {
		t.Fatalf("%s not emitted on panic", collector.MetricScrapeDuration)
	}
	if _, ok := findPoint(rec, collector.MetricScrapeLastTimestamp, "panicker"); !ok {
		t.Fatalf("%s not emitted on panic", collector.MetricScrapeLastTimestamp)
	}
}

func TestSelfObs_WindowCollectorEmitsScrapeMetrics(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.RegisterWindow(winFunc{name: "win", def: time.Millisecond, lag: time.Minute,
		fn: func(_ context.Context, from, to time.Time, _ telemetry.Emitter) (time.Time, error) {
			return to, nil
		}}, time.Millisecond, 5*time.Minute, time.Hour)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	waitFor(t, func() bool {
		_, ok := findPoint(rec, collector.MetricScrapeSuccess, "win")
		return ok
	}, 2*time.Second)

	success, ok := findPoint(rec, collector.MetricScrapeSuccess, "win")
	if !ok || success.Value != 1 {
		t.Fatalf("window success = %+v (ok=%v), want gauge value 1", success, ok)
	}
	if _, ok := findPoint(rec, collector.MetricScrapeDuration, "win"); !ok {
		t.Fatalf("%s not emitted for window collector", collector.MetricScrapeDuration)
	}
	if _, ok := findPoint(rec, collector.MetricScrapeLastTimestamp, "win"); !ok {
		t.Fatalf("%s not emitted for window collector", collector.MetricScrapeLastTimestamp)
	}
}

func TestSelfObs_FailingWindowRecordsError(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	r := collector.NewRegistry()
	r.RegisterWindow(winFunc{name: "win", def: time.Millisecond, lag: time.Minute,
		fn: func(_ context.Context, from, to time.Time, _ telemetry.Emitter) (time.Time, error) {
			return time.Time{}, context.DeadlineExceeded
		}}, time.Millisecond, 5*time.Minute, time.Hour)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now)

	waitFor(t, func() bool {
		p, ok := findPoint(rec, collector.MetricScrapeErrors, "win")
		return ok && p.Attrs["error.type"] == "timeout"
	}, 2*time.Second)

	success, ok := findPoint(rec, collector.MetricScrapeSuccess, "win")
	if !ok || success.Value != 0 {
		t.Fatalf("window success = %+v (ok=%v), want gauge value 0 on error", success, ok)
	}
}

func TestSelfObs_CheckpointPersistErrorRecorded(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	r := collector.NewRegistry()
	// The collect succeeds (returns a valid high-water mark), so the failure is
	// isolated to the checkpoint Set call — not the scrape.
	r.RegisterWindow(winFunc{name: "win", def: time.Millisecond, lag: time.Minute,
		fn: func(_ context.Context, _, to time.Time, _ telemetry.Emitter) (time.Time, error) {
			return to, nil
		}}, time.Millisecond, 5*time.Minute, time.Hour)

	rec := telemetrytest.New()
	runRecorderSchedulerStore(t, r, rec, now, errSetStore{})

	waitFor(t, func() bool {
		p, ok := findPoint(rec, collector.MetricCheckpointPersistErrors, "win")
		return ok && p.Value >= 1
	}, 2*time.Second)

	p, ok := findPoint(rec, collector.MetricCheckpointPersistErrors, "win")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricCheckpointPersistErrors)
	}
	if p.Kind != "sum" || !p.Monotonic {
		t.Fatalf("checkpoint persist errors = %+v, want a monotonic sum (counter)", p)
	}
	if p.Unit != semconv.UnitDimensionless {
		t.Fatalf("unit = %q, want %q", p.Unit, semconv.UnitDimensionless)
	}
	// The collect succeeded, so no scrape error should be recorded.
	if pts := rec.MetricPoints(collector.MetricScrapeErrors); len(pts) != 0 {
		t.Fatalf("scrape errors emitted despite a successful collect: %+v", pts)
	}
}

func TestSelfObs_CheckpointPersistErrorSuppressedWhenDisabled(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	ran := make(chan struct{}, 1)
	r := collector.NewRegistry()
	r.RegisterWindow(winFunc{name: "win", def: time.Millisecond, lag: time.Minute,
		fn: func(_ context.Context, _, to time.Time, _ telemetry.Emitter) (time.Time, error) {
			select {
			case ran <- struct{}{}:
			default:
			}
			return to, nil
		}}, time.Millisecond, 5*time.Minute, time.Hour)

	rec := telemetrytest.New()
	runRecorderSchedulerStore(t, r, rec, now, errSetStore{}, collector.WithSelfObs(false))

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("window collector never ran")
	}
	time.Sleep(20 * time.Millisecond)
	if pts := rec.MetricPoints(collector.MetricCheckpointPersistErrors); len(pts) != 0 {
		t.Fatalf("WithSelfObs(false): checkpoint persist errors emitted %d points, want 0", len(pts))
	}
}

func TestSelfObs_DisabledEmitsNoScrapeMetrics(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	var calls = make(chan struct{}, 1)
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "ok", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		select {
		case calls <- struct{}{}:
		default:
		}
		return nil
	}}, time.Millisecond)

	rec := telemetrytest.New()
	runRecorderScheduler(t, r, rec, now, collector.WithSelfObs(false))

	// Wait until the collector has actually run at least once.
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("collector never ran")
	}
	// Give a couple more ticks a chance to emit anything erroneously.
	time.Sleep(20 * time.Millisecond)

	for _, name := range []string{
		collector.MetricScrapeDuration,
		collector.MetricScrapeSuccess,
		collector.MetricScrapeErrors,
		collector.MetricScrapeLastTimestamp,
		collector.MetricScrapeStaleness,
		collector.MetricScrapeBudget,
	} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Fatalf("WithSelfObs(false): %s emitted %d points, want 0", name, len(pts))
		}
	}
}

func TestSelfObs_StalenessGrowsWhileFailing(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	var nowNs atomic.Int64
	nowNs.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, nowNs.Load()).UTC() }

	r := collector.NewRegistry()
	r.Register(snapFunc{name: "bad", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		nowNs.Add(int64(time.Minute)) // advance the wall clock one minute per run
		return errors.New("boom")
	}}, time.Millisecond)

	rec := telemetrytest.New()
	s := collector.NewScheduler(rec.Emitter(), collector.NewMemoryStore(),
		collector.WithStaggerWindow(0), collector.WithClock(clock))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx, r); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, func() bool {
		p, ok := findPoint(rec, collector.MetricScrapeStaleness, "bad")
		return ok && p.Value >= 60
	}, 2*time.Second)

	p, ok := findPoint(rec, collector.MetricScrapeStaleness, "bad")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricScrapeStaleness)
	}
	if p.Kind != "gauge" || p.Unit != semconv.UnitSeconds {
		t.Fatalf("staleness = %+v, want gauge in seconds", p)
	}
	if p.Value < 60 {
		t.Fatalf("staleness value = %v, want >= 60 (>= one advance since the never-reached last success)", p.Value)
	}
}

func TestSelfObs_BudgetReflectsDurationOverInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const (
			interval = 100 * time.Millisecond
			work     = 25 * time.Millisecond
		)
		r := collector.NewRegistry()
		r.Register(snapFunc{name: "slow", def: interval, fn: func(context.Context, telemetry.Emitter) error {
			time.Sleep(work) // fake-clock sleep → the measured duration is exactly `work`
			return nil
		}}, interval)

		rec := telemetrytest.New()
		s := collector.NewScheduler(rec.Emitter(), collector.NewMemoryStore(),
			collector.WithStaggerWindow(0))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = s.Run(ctx, r) }()

		synctest.Wait()  // barrier: scheduler goroutine has reached time.Sleep(work) inside the collector (no emit yet)
		time.Sleep(work) // advance fake clock past the collector's sleep
		synctest.Wait()  // first tick completes; goroutines block on the ticker

		p, ok := findPoint(rec, collector.MetricScrapeBudget, "slow")
		if !ok {
			t.Fatalf("%s not emitted", collector.MetricScrapeBudget)
		}
		if p.Kind != "gauge" || p.Unit != semconv.UnitDimensionless {
			t.Fatalf("budget = %+v, want a dimensionless gauge", p)
		}
		want := work.Seconds() / interval.Seconds() // 0.25
		if math.Abs(p.Value-want) > 1e-9 {
			t.Fatalf("budget value = %v, want %v (duration/interval)", p.Value, want)
		}
	})
}

func TestSelfObs_StalenessZeroWhileSucceeding(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	var nowNs atomic.Int64
	nowNs.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, nowNs.Load()).UTC() }

	r := collector.NewRegistry()
	r.Register(snapFunc{name: "ok", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		nowNs.Add(int64(time.Minute)) // clock marches on, but the run succeeds
		return nil
	}}, time.Millisecond)

	rec := telemetrytest.New()
	s := collector.NewScheduler(rec.Emitter(), collector.NewMemoryStore(),
		collector.WithStaggerWindow(0), collector.WithClock(clock))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx, r); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, func() bool {
		_, ok := findPoint(rec, collector.MetricScrapeStaleness, "ok")
		return ok
	}, 2*time.Second)

	p, ok := findPoint(rec, collector.MetricScrapeStaleness, "ok")
	if !ok {
		t.Fatalf("%s not emitted", collector.MetricScrapeStaleness)
	}
	if p.Value != 0 {
		t.Fatalf("staleness value = %v, want 0 (resets on every success even as the clock advances)", p.Value)
	}
}
