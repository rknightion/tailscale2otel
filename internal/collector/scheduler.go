package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// noopSchedulerTracer is the shared fallback for a nil Scheduler.tracer, so span
// creation in runTick never allocates a fresh no-op provider on every tick.
var noopSchedulerTracer = tracenoop.NewTracerProvider().Tracer("")

// spanAttrErrorType is the OTEL semantic-convention key for a bounded error class.
// internal/semconv carries no constant for it (the scrape.errors counter and its
// catalog entry both use the literal), so it is declared once here rather than
// repeated at the call sites.
const spanAttrErrorType = "error.type"

// Scheduler runs each registered collector on its own goroutine and ticker,
// isolating failures so one collector cannot stop the others.
type Scheduler struct {
	emitter telemetry.Emitter
	store   CheckpointStore
	now     func() time.Time
	// staggerWindow bounds the random startup delay applied to each collector's
	// first tick, to de-synchronize polls (see WithStaggerWindow). Zero disables it.
	staggerWindow time.Duration
	logger        *slog.Logger
	selfObs       bool
	status        *StatusTracker // optional; records per-collector run outcomes for the status page
	tracer        trace.Tracer   // optional; emits one root span per scrape cycle
	// namespace, when non-empty, prefixes every checkpoint store key with
	// namespace+"/" so multiple schedulers (one per tailnet) sharing a
	// CheckpointStore don't collide. Empty keeps the bare collector name.
	namespace string
}

// SchedulerOption configures a Scheduler.
type SchedulerOption func(*Scheduler)

// WithStaggerWindow sets the upper bound on the random startup delay applied to
// each collector's first tick, used to de-synchronize collectors so they don't
// hit the Tailscale API in lock-step. The delay is an absolute duration,
// independent of the collector's Interval: the first tick always fires within
// this window (then every Interval thereafter). Zero makes every first tick
// fire immediately. Defaults to defaultStaggerWindow.
func WithStaggerWindow(d time.Duration) SchedulerOption {
	return func(s *Scheduler) { s.staggerWindow = d }
}

// WithClock overrides the time source (used in tests).
func WithClock(now func() time.Time) SchedulerOption { return func(s *Scheduler) { s.now = now } }

// WithLogger sets the logger used for collector errors.
func WithLogger(l *slog.Logger) SchedulerOption { return func(s *Scheduler) { s.logger = l } }

// WithSelfObs toggles per-collector self-observability scrape metrics
// (tailscale2otel.scrape.*). It defaults to enabled; passing false suppresses
// all scrape metric emission, leaving behavior identical to having no self-obs.
func WithSelfObs(enabled bool) SchedulerOption { return func(s *Scheduler) { s.selfObs = enabled } }

// WithStatusTracker records each collector's latest run outcome into t for
// in-process introspection (e.g. the admin status page). Recording happens on
// every tick regardless of WithSelfObs, so the status page works even when
// scrape metrics are suppressed.
func WithStatusTracker(t *StatusTracker) SchedulerOption { return func(s *Scheduler) { s.status = t } }

// WithTracer sets the tracer used to emit one root span per scrape cycle. A nil
// tracer (the default) disables span emission via a package-level no-op tracer,
// so callers and the tick path never need a nil check.
func WithTracer(tr trace.Tracer) SchedulerOption { return func(s *Scheduler) { s.tracer = tr } }

// WithCheckpointNamespace prefixes every checkpoint key with ns+"/" so multiple
// schedulers (one per tailnet) sharing a CheckpointStore don't collide. Empty
// (the default) keeps the bare collector name for single-tailnet continuity.
func WithCheckpointNamespace(ns string) SchedulerOption {
	return func(s *Scheduler) { s.namespace = ns }
}

// checkpointKey returns the store key for a collector, applying the optional
// tailnet namespace.
func (s *Scheduler) checkpointKey(name string) string {
	if s.namespace == "" {
		return name
	}
	return s.namespace + "/" + name
}

// NewScheduler returns a Scheduler that drives collectors with the given
// emitter and checkpoint store.
func NewScheduler(e telemetry.Emitter, store CheckpointStore, opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{
		emitter:       e,
		store:         store,
		now:           time.Now,
		staggerWindow: defaultStaggerWindow,
		logger:        slog.Default(),
		selfObs:       true,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run launches one goroutine per registered collector and blocks until ctx is
// canceled, then drains. Returns ctx.Err().
func (s *Scheduler) Run(ctx context.Context, r *Registry) error {
	var wg sync.WaitGroup
	for _, entry := range r.Entries() {
		wg.Add(1)
		go func(e Entry) {
			defer wg.Done()
			s.runLoop(ctx, e)
		}(entry)
	}
	wg.Wait()
	return ctx.Err()
}

// defaultStaggerWindow bounds the random startup delay applied to each
// collector's first tick. It de-synchronizes collectors (so they don't poll the
// Tailscale API in lock-step) without delaying the first poll by a fraction of a
// potentially long interval: a 600s collector still produces data within seconds
// of startup, not 10 minutes later.
const defaultStaggerWindow = 3 * time.Second

func (s *Scheduler) runLoop(ctx context.Context, e Entry) {
	// Baseline for scrape.staleness: until the first successful run, staleness
	// counts up from here (loop start), so a collector that never succeeds shows
	// a growing — and therefore alertable — value.
	lastSuccess := s.now()
	if d := s.initialDelay(); d > 0 {
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	// Run the first tick immediately after the stagger so fresh data lands
	// within seconds of startup rather than one full Interval later.
	select {
	case <-ctx.Done():
		return
	default:
	}
	s.runTick(ctx, e, &lastSuccess)
	ticker := time.NewTicker(e.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runTick(ctx, e, &lastSuccess)
		}
	}
}

func (s *Scheduler) initialDelay() time.Duration {
	if s.staggerWindow <= 0 {
		return 0
	}
	return time.Duration(rand.Float64() * float64(s.staggerWindow)) //nolint:gosec // G404: scheduling jitter is not security-sensitive
}

// isShutdownCancellation reports whether err represents a collector run being
// interrupted by ctx's own cancellation (e.g. process shutdown) rather than a
// genuine collection failure. It requires BOTH that ctx itself is done — so an
// unrelated, request-scoped cancellation/timeout elsewhere is never
// misclassified — and that err is context.Canceled or context.DeadlineExceeded,
// the two sentinel errors an interrupted in-flight operation can surface
// depending on exactly when it observed ctx being done (#93).
func isShutdownCancellation(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// runTick executes one collection, recovering from panics so a single bad
// collector run never crashes the scheduler. The whole run is timed and, when
// self-obs is enabled, the per-collector scrape.* metrics are emitted — even on
// the panic-recovery path, which records success=0 plus an errors{panic} count.
// A run interrupted purely by shutdown cancellation (see isShutdownCancellation)
// is treated as neither success nor failure: it is skipped entirely from
// scrape.* metrics, the StatusTracker, and WARN logging, so a routine shutdown
// never leaves behind a false "collector failed" final sample (#93).
func (s *Scheduler) runTick(ctx context.Context, e Entry, lastSuccess *time.Time) {
	started := time.Now()  // monotonic: used for duration only
	startedWall := s.now() // wall-clock: for the status page's LastStarted

	// Start a root span for this scrape cycle. API child spans (added in Phase 3)
	// nest under this automatically because the span-bearing ctx is threaded down
	// through Collect / runWindow → tsapi. When tracing is disabled, tr resolves
	// to the package-level no-op tracer — zero allocation cost per tick.
	tr := s.tracer
	if tr == nil {
		tr = noopSchedulerTracer
	}
	ctx, span := tr.Start(ctx, "scrape "+e.Collector.Name(),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String(semconv.AttrCollector, e.Collector.Name())))

	var runErr error
	panicked := false
	var panicVal any
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			panicVal = r
			s.logger.Error("collector panicked", "collector", e.Collector.Name(), "panic", r)
		}
		// Finalize the scrape span before emitting metrics so the span is ended
		// (and therefore visible to any span processor) prior to the scrape metrics.
		//
		// Both branches also stamp the BOUNDED failure class (#473). The status
		// description below is free text — a collector error string or a recovered
		// panic value — so internal/telemetry's PII span exporter replaces it
		// wholesale once free_text_details is disabled. error.type is the same
		// three-value enum the scrape.errors counter carries (panic|timeout|error),
		// so a redacted span still says WHAT KIND of failure this was. It is set
		// only on failure, keeping "attribute present" equivalent to "this scrape
		// failed", and its value set is closed — no cardinality risk on the span or
		// on any series a metrics-generator derives from it.
		switch {
		case panicked:
			span.SetAttributes(attribute.String(spanAttrErrorType, scrapeErrorType(nil, true)))
			// Record the panic as an exception event too, so the panic text lands on
			// the governed free-text surface (exception.message) rather than living
			// only in the ungoverned status description.
			span.RecordError(fmt.Errorf("panic: %v", panicVal))
			span.SetStatus(codes.Error, fmt.Sprintf("panic: %v", panicVal))
		case runErr != nil:
			span.SetAttributes(attribute.String(spanAttrErrorType, scrapeErrorType(runErr, false)))
			span.RecordError(runErr)
			span.SetStatus(codes.Error, runErr.Error())
		}
		span.End()

		if !panicked && isShutdownCancellation(ctx, runErr) {
			// Shutdown, not a real failure: skip scrape metrics, status
			// tracking, and WARN logging entirely (the tracing span above is
			// still recorded, since that's a per-run trace rather than a
			// persisted last-sample metric).
			return
		}

		duration := time.Since(started)
		finishedWall := s.now()
		failed := panicked || runErr != nil
		if !failed {
			*lastSuccess = finishedWall
		}
		staleness := finishedWall.Sub(*lastSuccess)
		if staleness < 0 { // guard a backward wall-clock jump (NTP)
			staleness = 0
		}
		if s.selfObs {
			emitScrapeMetrics(s.emitter, scrapeResult{
				collector:  e.Collector.Name(),
				duration:   duration,
				interval:   e.Interval,
				finishedAt: finishedWall,
				staleness:  staleness,
				err:        runErr,
				panicked:   panicked,
			})
		}
		if s.status != nil {
			errStr := ""
			switch {
			case panicked:
				errStr = fmt.Sprintf("panic: %v", panicVal)
			case runErr != nil:
				errStr = runErr.Error()
			}
			s.status.record(e.Collector.Name(), startedWall, finishedWall, duration, errStr)
		}
	}()
	switch c := e.Collector.(type) {
	case WindowCollector:
		runErr = s.runWindow(ctx, c, e)
	case SnapshotCollector:
		runErr = c.Collect(ctx, s.emitter)
		if runErr != nil && !isShutdownCancellation(ctx, runErr) {
			s.logger.Warn("collector failed", "collector", c.Name(), "error", runErr)
		}
	default:
		// Defensive-only: Register requires SnapshotCollector and RegisterWindow
		// requires WindowCollector, both compile-time (#58), so a registered
		// collector always matches a case above. This branch can only be reached by
		// a future code path that appends to Registry.entries directly.
		s.logger.Warn("collector implements neither SnapshotCollector nor WindowCollector",
			"collector", c.Name())
	}
}

// runWindow polls a window collector's next [from, to] range and advances the
// checkpoint on success. It returns the collector's error (nil on success or
// when there is no new window to poll) so the caller can record scrape metrics.
func (s *Scheduler) runWindow(ctx context.Context, c WindowCollector, e Entry) error {
	last, hasLast := s.store.Get(s.checkpointKey(c.Name()))
	from, to, ok := nextWindow(last, hasLast, s.now(), c.Lag(), e.InitialLookback, e.MaxWindow)
	if !ok {
		return nil
	}
	hwm, err := c.CollectWindow(ctx, from, to, s.emitter)
	if err != nil {
		// Do not advance the checkpoint: the next tick retries the same window
		// (at-least-once, no gaps).
		if !isShutdownCancellation(ctx, err) {
			s.logger.Warn("window collector failed", "collector", c.Name(), "error", err)
		}
		return err
	}
	if hwm.IsZero() {
		hwm = to
	}
	if err := s.store.Set(s.checkpointKey(c.Name()), hwm); err != nil {
		s.logger.Warn("checkpoint persist failed", "collector", c.Name(), "error", err)
		if s.selfObs {
			emitCheckpointPersistError(s.emitter, c.Name())
		}
	}
	return nil
}
