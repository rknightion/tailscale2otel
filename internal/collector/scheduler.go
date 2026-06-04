package collector

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Scheduler runs each registered collector on its own goroutine and ticker,
// isolating failures so one collector cannot stop the others.
type Scheduler struct {
	emitter telemetry.Emitter
	store   CheckpointStore
	now     func() time.Time
	jitter  float64
	logger  *slog.Logger
	selfObs bool
	status  *StatusTracker // optional; records per-collector run outcomes for the status page
}

// SchedulerOption configures a Scheduler.
type SchedulerOption func(*Scheduler)

// WithJitter sets the fractional jitter (0..1) applied to the initial tick of
// each collector, to de-synchronize polls. Zero disables jitter.
func WithJitter(f float64) SchedulerOption { return func(s *Scheduler) { s.jitter = f } }

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

// NewScheduler returns a Scheduler that drives collectors with the given
// emitter and checkpoint store.
func NewScheduler(e telemetry.Emitter, store CheckpointStore, opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{
		emitter: e,
		store:   store,
		now:     time.Now,
		jitter:  0.1,
		logger:  slog.Default(),
		selfObs: true,
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

func (s *Scheduler) runLoop(ctx context.Context, e Entry) {
	if d := s.initialDelay(e.Interval); d > 0 {
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	ticker := time.NewTicker(e.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runTick(ctx, e)
		}
	}
}

func (s *Scheduler) initialDelay(interval time.Duration) time.Duration {
	if s.jitter <= 0 {
		return 0
	}
	return time.Duration(rand.Float64() * s.jitter * float64(interval)) //nolint:gosec // G404: scheduling jitter is not security-sensitive
}

// runTick executes one collection, recovering from panics so a single bad
// collector run never crashes the scheduler. The whole run is timed and, when
// self-obs is enabled, the per-collector scrape.* metrics are emitted — even on
// the panic-recovery path, which records success=0 plus an errors{panic} count.
func (s *Scheduler) runTick(ctx context.Context, e Entry) {
	started := time.Now()  // monotonic: used for duration only
	startedWall := s.now() // wall-clock: for the status page's LastStarted
	var runErr error
	panicked := false
	var panicVal any
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			panicVal = r
			s.logger.Error("collector panicked", "collector", e.Collector.Name(), "panic", r)
		}
		duration := time.Since(started)
		finishedWall := s.now()
		if s.selfObs {
			emitScrapeMetrics(s.emitter, scrapeResult{
				collector:  e.Collector.Name(),
				duration:   duration,
				finishedAt: finishedWall,
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
		if runErr != nil {
			s.logger.Warn("collector failed", "collector", c.Name(), "error", runErr)
		}
	default:
		s.logger.Warn("collector implements neither SnapshotCollector nor WindowCollector",
			"collector", c.Name())
	}
}

// runWindow polls a window collector's next [from, to] range and advances the
// checkpoint on success. It returns the collector's error (nil on success or
// when there is no new window to poll) so the caller can record scrape metrics.
func (s *Scheduler) runWindow(ctx context.Context, c WindowCollector, e Entry) error {
	last, hasLast := s.store.Get(c.Name())
	from, to, ok := nextWindow(last, hasLast, s.now(), c.Lag(), e.InitialLookback, e.MaxWindow)
	if !ok {
		return nil
	}
	hwm, err := c.CollectWindow(ctx, from, to, s.emitter)
	if err != nil {
		// Do not advance the checkpoint: the next tick retries the same window
		// (at-least-once, no gaps).
		s.logger.Warn("window collector failed", "collector", c.Name(), "error", err)
		return err
	}
	if hwm.IsZero() {
		hwm = to
	}
	if err := s.store.Set(c.Name(), hwm); err != nil {
		s.logger.Warn("checkpoint persist failed", "collector", c.Name(), "error", err)
	}
	return nil
}
