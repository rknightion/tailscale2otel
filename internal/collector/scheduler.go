package collector

import (
	"context"
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

// NewScheduler returns a Scheduler that drives collectors with the given
// emitter and checkpoint store.
func NewScheduler(e telemetry.Emitter, store CheckpointStore, opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{
		emitter: e,
		store:   store,
		now:     time.Now,
		jitter:  0.1,
		logger:  slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run launches one goroutine per registered collector and blocks until ctx is
// cancelled, then drains. Returns ctx.Err().
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
	return time.Duration(rand.Float64() * s.jitter * float64(interval))
}

// runTick executes one collection, recovering from panics so a single bad
// collector run never crashes the scheduler.
func (s *Scheduler) runTick(ctx context.Context, e Entry) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("collector panicked", "collector", e.Collector.Name(), "panic", r)
		}
	}()
	switch c := e.Collector.(type) {
	case WindowCollector:
		s.runWindow(ctx, c, e)
	case SnapshotCollector:
		if err := c.Collect(ctx, s.emitter); err != nil {
			s.logger.Warn("collector failed", "collector", c.Name(), "error", err)
		}
	default:
		s.logger.Warn("collector implements neither SnapshotCollector nor WindowCollector",
			"collector", c.Name())
	}
}

func (s *Scheduler) runWindow(ctx context.Context, c WindowCollector, e Entry) {
	last, hasLast := s.store.Get(c.Name())
	from, to, ok := nextWindow(last, hasLast, s.now(), c.Lag(), e.InitialLookback, e.MaxWindow)
	if !ok {
		return
	}
	hwm, err := c.CollectWindow(ctx, from, to, s.emitter)
	if err != nil {
		// Do not advance the checkpoint: the next tick retries the same window
		// (at-least-once, no gaps).
		s.logger.Warn("window collector failed", "collector", c.Name(), "error", err)
		return
	}
	if hwm.IsZero() {
		hwm = to
	}
	if err := s.store.Set(c.Name(), hwm); err != nil {
		s.logger.Warn("checkpoint persist failed", "collector", c.Name(), "error", err)
	}
}
