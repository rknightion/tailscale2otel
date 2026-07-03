package collector_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// --- test doubles ---

type snapFunc struct {
	name string
	def  time.Duration
	fn   func(context.Context, telemetry.Emitter) error
}

func (s snapFunc) Name() string                                           { return s.name }
func (s snapFunc) DefaultInterval() time.Duration                         { return s.def }
func (s snapFunc) Collect(ctx context.Context, e telemetry.Emitter) error { return s.fn(ctx, e) }

type winFunc struct {
	name string
	def  time.Duration
	lag  time.Duration
	fn   func(context.Context, time.Time, time.Time, telemetry.Emitter) (time.Time, error)
}

func (w winFunc) Name() string                   { return w.name }
func (w winFunc) DefaultInterval() time.Duration { return w.def }
func (w winFunc) Lag() time.Duration             { return w.lag }
func (w winFunc) CollectWindow(ctx context.Context, from, to time.Time, e telemetry.Emitter) (time.Time, error) {
	return w.fn(ctx, from, to, e)
}

type noopEmitter struct{}

func (noopEmitter) Counter(string, string, string, float64, telemetry.Attrs)              {}
func (noopEmitter) Gauge(string, string, string, float64, telemetry.Attrs)                {}
func (noopEmitter) GaugeSnapshot(string, string, string, []telemetry.GaugePoint)          {}
func (noopEmitter) UpDownCounter(string, string, string, float64, telemetry.Attrs)        {}
func (noopEmitter) Histogram(string, string, string, float64, []float64, telemetry.Attrs) {}
func (noopEmitter) HistogramCtx(context.Context, string, string, string, float64, []float64, telemetry.Attrs) {
}
func (noopEmitter) LogEvent(telemetry.Event) {}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func runScheduler(t *testing.T, r *collector.Registry, store collector.CheckpointStore, opts ...collector.SchedulerOption) context.CancelFunc {
	t.Helper()
	s := collector.NewScheduler(noopEmitter{}, store, append([]collector.SchedulerOption{collector.WithStaggerWindow(0)}, opts...)...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx, r); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel
}

// --- tests ---

func TestScheduler_StatusTrackerRecordsOutcomes(t *testing.T) {
	tr := collector.NewStatusTracker()
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "ok", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		return nil
	}}, time.Millisecond)
	r.Register(snapFunc{name: "bad", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		return errors.New("nope")
	}}, time.Millisecond)
	r.Register(snapFunc{name: "boom", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		panic("kaboom")
	}}, time.Millisecond)

	runScheduler(t, r, collector.NewMemoryStore(), collector.WithStatusTracker(tr))

	waitFor(t, func() bool {
		s := tr.Snapshot()
		return s["ok"].Runs > 0 && s["bad"].Runs > 0 && s["boom"].Runs > 0
	}, 2*time.Second)

	s := tr.Snapshot()
	if !s["ok"].LastSuccess {
		t.Errorf("ok.LastSuccess = false, want true")
	}
	if s["bad"].LastSuccess || s["bad"].LastError != "nope" {
		t.Errorf("bad = %+v, want failure with error %q", s["bad"], "nope")
	}
	if s["boom"].LastSuccess || !strings.Contains(s["boom"].LastError, "kaboom") {
		t.Errorf("boom = %+v, want panic error containing %q", s["boom"], "kaboom")
	}
}

func TestScheduler_RunsFirstTickPromptly(t *testing.T) {
	var calls atomic.Int64
	r := collector.NewRegistry()
	// A long interval: the first tick must NOT wait a full interval. With the
	// old "stagger, then block on the first ticker tick" loop, the first run
	// wouldn't happen for an hour — fresh data must land promptly at startup.
	r.Register(snapFunc{name: "slow", def: time.Hour, fn: func(context.Context, telemetry.Emitter) error {
		calls.Add(1)
		return nil
	}}, time.Hour)

	runScheduler(t, r, collector.NewMemoryStore())
	waitFor(t, func() bool { return calls.Load() > 0 }, 2*time.Second)
}

func TestScheduler_InvokesSnapshotCollector(t *testing.T) {
	var calls atomic.Int64
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "snap", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		calls.Add(1)
		return nil
	}}, time.Millisecond)

	runScheduler(t, r, collector.NewMemoryStore())
	waitFor(t, func() bool { return calls.Load() > 0 }, 2*time.Second)
}

func TestScheduler_IsolatesPanickingCollector(t *testing.T) {
	var healthy atomic.Int64
	r := collector.NewRegistry()
	r.Register(snapFunc{name: "bad", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		panic("boom")
	}}, time.Millisecond)
	r.Register(snapFunc{name: "good", def: time.Millisecond, fn: func(context.Context, telemetry.Emitter) error {
		healthy.Add(1)
		return nil
	}}, time.Millisecond)

	runScheduler(t, r, collector.NewMemoryStore())
	// The healthy collector must keep ticking despite the other panicking.
	waitFor(t, func() bool { return healthy.Load() > 3 }, 2*time.Second)
}

func TestScheduler_WindowAdvancesCheckpointOnSuccess(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	store := collector.NewMemoryStore()
	var calls atomic.Int64
	r := collector.NewRegistry()
	r.RegisterWindow(winFunc{name: "win", def: time.Millisecond, lag: time.Minute,
		fn: func(_ context.Context, from, to time.Time, _ telemetry.Emitter) (time.Time, error) {
			calls.Add(1)
			return to, nil
		}}, time.Millisecond, 5*time.Minute, time.Hour)

	runScheduler(t, r, store, collector.WithClock(func() time.Time { return now }))
	waitFor(t, func() bool {
		_, ok := store.Get("win")
		return ok
	}, 2*time.Second)

	got, _ := store.Get("win")
	if want := now.Add(-time.Minute); !got.Equal(want) {
		t.Fatalf("checkpoint = %v, want %v (to = now-lag)", got, want)
	}
}

func TestScheduler_NamespacesCheckpointKeys(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	store := collector.NewMemoryStore()
	mk := func() *collector.Registry {
		r := collector.NewRegistry()
		r.RegisterWindow(winFunc{name: "auditlogs", def: time.Millisecond, lag: time.Minute,
			fn: func(_ context.Context, _, to time.Time, _ telemetry.Emitter) (time.Time, error) {
				return to, nil
			}}, time.Millisecond, 5*time.Minute, time.Hour)
		return r
	}
	clock := collector.WithClock(func() time.Time { return now })
	runScheduler(t, mk(), store, clock, collector.WithCheckpointNamespace("acme"))
	runScheduler(t, mk(), store, clock, collector.WithCheckpointNamespace("beta"))

	waitFor(t, func() bool {
		_, a := store.Get("acme/auditlogs")
		_, b := store.Get("beta/auditlogs")
		return a && b
	}, 2*time.Second)

	if _, ok := store.Get("auditlogs"); ok {
		t.Error("bare key auditlogs should not be set when a namespace is configured")
	}
}

func TestScheduler_WindowDoesNotAdvanceCheckpointOnError(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	store := collector.NewMemoryStore()
	var calls atomic.Int64
	r := collector.NewRegistry()
	r.RegisterWindow(winFunc{name: "win", def: time.Millisecond, lag: time.Minute,
		fn: func(_ context.Context, from, to time.Time, _ telemetry.Emitter) (time.Time, error) {
			calls.Add(1)
			return time.Time{}, context.DeadlineExceeded
		}}, time.Millisecond, 5*time.Minute, time.Hour)

	runScheduler(t, r, store, collector.WithClock(func() time.Time { return now }))
	waitFor(t, func() bool { return calls.Load() > 2 }, 2*time.Second)

	if _, ok := store.Get("win"); ok {
		t.Fatal("checkpoint advanced despite collector error")
	}
}

// --- span test helpers ---

// fakeOK returns a SnapshotCollector that always succeeds, identified by name.
func fakeOK(name string) collector.SnapshotCollector {
	return snapFunc{name: name, def: time.Minute, fn: func(context.Context, telemetry.Emitter) error {
		return nil
	}}
}

// fakeErr returns a SnapshotCollector that always returns the given error.
func fakeErr(name string, err error) collector.SnapshotCollector {
	return snapFunc{name: name, def: time.Minute, fn: func(context.Context, telemetry.Emitter) error {
		return err
	}}
}

// spanNames extracts the Name() of each ended ReadOnlySpan for readable assertions.
func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name()
	}
	return names
}

// TestRunTick_EmitsScrapeSpan verifies that a single runTick call emits exactly
// one root span named "scrape <collector>", and that a failing collector's span
// carries Error status.
func TestRunTick_EmitsScrapeSpan(t *testing.T) {
	t.Run("ok collector emits Unset-status span", func(t *testing.T) {
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

		s := collector.NewScheduler(noopEmitter{}, collector.NewMemoryStore(),
			collector.WithTracer(tp.Tracer("test")),
			collector.WithStaggerWindow(0),
			collector.WithSelfObs(false), // suppress metric emission; only span matters here
		)
		last := time.Now()
		s.RunTick(context.Background(),
			collector.Entry{Collector: fakeOK("dev"), Interval: time.Minute},
			&last)

		spans := sr.Ended()
		if len(spans) != 1 {
			t.Fatalf("got %d spans %v, want exactly 1", len(spans), spanNames(spans))
		}
		if got := spans[0].Name(); got != "scrape dev" {
			t.Errorf("span name = %q, want %q", got, "scrape dev")
		}
		// A successful run must not set Error status.
		if code := spans[0].Status().Code; code != codes.Unset {
			t.Errorf("ok run span status code = %v, want Unset", code)
		}
		// Confirm the span kind is Internal.
		if spans[0].SpanKind() != trace.SpanKindInternal {
			t.Errorf("span kind = %v, want Internal", spans[0].SpanKind())
		}
	})

	t.Run("failing collector marks span Error", func(t *testing.T) {
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

		boom := errors.New("api unavailable")
		s := collector.NewScheduler(noopEmitter{}, collector.NewMemoryStore(),
			collector.WithTracer(tp.Tracer("test")),
			collector.WithStaggerWindow(0),
			collector.WithSelfObs(false),
		)
		last := time.Now()
		s.RunTick(context.Background(),
			collector.Entry{Collector: fakeErr("keys", boom), Interval: time.Minute},
			&last)

		spans := sr.Ended()
		if len(spans) != 1 {
			t.Fatalf("got %d spans %v, want exactly 1", len(spans), spanNames(spans))
		}
		if got := spans[0].Name(); got != "scrape keys" {
			t.Errorf("span name = %q, want %q", got, "scrape keys")
		}
		// A failing collector must set Error status on the span.
		if code := spans[0].Status().Code; code != codes.Error {
			t.Errorf("span status code = %v, want Error", code)
		}
		if desc := spans[0].Status().Description; !strings.Contains(desc, "api unavailable") {
			t.Errorf("span status description = %q, want it to contain %q", desc, "api unavailable")
		}
	})
}

// --- shutdown-cancellation classification (#93) ---
//
// A collector tick that fails purely because the scheduler's own context was
// canceled (process shutdown) mid-run must NOT be recorded as a scrape
// failure: no scrape.* metrics, no StatusTracker entry, no "collector
// failed"/"window collector failed" WARN log. A genuine context.Canceled (or
// context.DeadlineExceeded) that happens while the scheduler's context is
// still live is unrelated to shutdown and must still count as a real failure.

// allScrapeMetricNames lists every metric emitScrapeMetrics can produce, used
// to assert none of them fired for a shutdown-canceled tick.
var allScrapeMetricNames = []string{
	collector.MetricScrapeDuration,
	collector.MetricScrapeSuccess,
	collector.MetricScrapeErrors,
	collector.MetricScrapeLastTimestamp,
	collector.MetricScrapeStaleness,
	collector.MetricScrapeBudget,
}

func TestRunTick_ShutdownCancellationOfSnapshotCollectorNotRecordedAsFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rec := telemetrytest.New()
	tracker := collector.NewStatusTracker()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	entry := collector.Entry{
		Collector: snapFunc{name: "shutdown", def: time.Minute, fn: func(ctx context.Context, _ telemetry.Emitter) error {
			close(started)
			<-ctx.Done() // block until the scheduler's own context is canceled mid-tick
			return fmt.Errorf("collect: %w", ctx.Err())
		}},
		Interval: time.Minute,
	}

	s := collector.NewScheduler(rec.Emitter(), collector.NewMemoryStore(),
		collector.WithStaggerWindow(0),
		collector.WithClock(func() time.Time { return now }),
		collector.WithStatusTracker(tracker),
		collector.WithLogger(logger))

	last := now
	done := make(chan struct{})
	go func() {
		s.RunTick(ctx, entry, &last)
		close(done)
	}()
	<-started
	cancel() // simulate shutdown while the collector is mid-run
	<-done

	for _, name := range allScrapeMetricNames {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("shutdown-canceled tick emitted %s: %+v, want none", name, pts)
		}
	}
	if run, ok := tracker.Snapshot()["shutdown"]; ok || run.Runs != 0 {
		t.Errorf("StatusTracker recorded a run for a shutdown-canceled tick: ok=%v run=%+v", ok, run)
	}
	if strings.Contains(logBuf.String(), "collector failed") {
		t.Errorf("WARN log emitted for a shutdown-canceled tick: %s", logBuf.String())
	}
}

func TestRunTick_ShutdownCancellationOfWindowCollectorNotRecordedAsFailure(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	rec := telemetrytest.New()
	tracker := collector.NewStatusTracker()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	entry := collector.Entry{
		Collector: winFunc{name: "winshutdown", def: time.Minute, lag: time.Minute,
			fn: func(ctx context.Context, _, _ time.Time, _ telemetry.Emitter) (time.Time, error) {
				close(started)
				<-ctx.Done()
				return time.Time{}, fmt.Errorf("collect window: %w", ctx.Err())
			}},
		Interval:        time.Minute,
		InitialLookback: 5 * time.Minute,
		MaxWindow:       time.Hour,
	}

	s := collector.NewScheduler(rec.Emitter(), collector.NewMemoryStore(),
		collector.WithStaggerWindow(0),
		collector.WithClock(func() time.Time { return now }),
		collector.WithStatusTracker(tracker),
		collector.WithLogger(logger))

	last := now
	done := make(chan struct{})
	go func() {
		s.RunTick(ctx, entry, &last)
		close(done)
	}()
	<-started
	cancel()
	<-done

	for _, name := range allScrapeMetricNames {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("shutdown-canceled window tick emitted %s: %+v, want none", name, pts)
		}
	}
	if run, ok := tracker.Snapshot()["winshutdown"]; ok || run.Runs != 0 {
		t.Errorf("StatusTracker recorded a run for a shutdown-canceled window tick: ok=%v run=%+v", ok, run)
	}
	if strings.Contains(logBuf.String(), "window collector failed") {
		t.Errorf("WARN log emitted for a shutdown-canceled window tick: %s", logBuf.String())
	}
}

// TestRunTick_NonShutdownCancellationStillCountsAsFailure guards against an
// overly broad fix: a context.Canceled/DeadlineExceeded error that occurs
// while the scheduler's own context (ctx passed to RunTick) is still live —
// i.e. NOT caused by shutdown — must still be recorded as a genuine scrape
// failure (e.g. a collector's own internal timeout/cancellation).
func TestRunTick_NonShutdownCancellationStillCountsAsFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	rec := telemetrytest.New()
	tracker := collector.NewStatusTracker()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	entry := collector.Entry{
		Collector: snapFunc{name: "notshutdown", def: time.Minute, fn: func(context.Context, telemetry.Emitter) error {
			return fmt.Errorf("internal timeout: %w", context.DeadlineExceeded)
		}},
		Interval: time.Minute,
	}

	s := collector.NewScheduler(rec.Emitter(), collector.NewMemoryStore(),
		collector.WithStaggerWindow(0),
		collector.WithClock(func() time.Time { return now }),
		collector.WithStatusTracker(tracker),
		collector.WithLogger(logger))

	last := now
	// The scheduler's own context is NOT canceled: this is a live, ongoing run.
	s.RunTick(context.Background(), entry, &last)

	success, ok := findPoint(rec, collector.MetricScrapeSuccess, "notshutdown")
	if !ok || success.Value != 0 {
		t.Fatalf("success = %+v (ok=%v), want gauge value 0 for a genuine (non-shutdown) failure", success, ok)
	}
	errPt, ok := findPoint(rec, collector.MetricScrapeErrors, "notshutdown")
	if !ok {
		t.Fatalf("%s not emitted for a genuine (non-shutdown) failure", collector.MetricScrapeErrors)
	}
	if errPt.Attrs["error.type"] != "timeout" {
		t.Fatalf("error.type = %q, want \"timeout\"", errPt.Attrs["error.type"])
	}
	if run, ok := tracker.Snapshot()["notshutdown"]; !ok || run.Runs != 1 || run.LastSuccess {
		t.Fatalf("StatusTracker run = %+v (ok=%v), want a recorded failure", run, ok)
	}
	if !strings.Contains(logBuf.String(), "collector failed") {
		t.Errorf("expected a \"collector failed\" WARN log for a genuine (non-shutdown) failure, got: %s", logBuf.String())
	}
}
