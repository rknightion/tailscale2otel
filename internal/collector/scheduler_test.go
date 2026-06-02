package collector_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/collector"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
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

func (noopEmitter) Counter(string, string, string, float64, telemetry.Attrs)       {}
func (noopEmitter) Gauge(string, string, string, float64, telemetry.Attrs)         {}
func (noopEmitter) UpDownCounter(string, string, string, float64, telemetry.Attrs) {}
func (noopEmitter) LogEvent(telemetry.Event)                                       {}

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
	s := collector.NewScheduler(noopEmitter{}, store, append([]collector.SchedulerOption{collector.WithJitter(0)}, opts...)...)
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
