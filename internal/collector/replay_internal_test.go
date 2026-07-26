package collector

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

type replayWindow struct {
	name      string
	overlap   time.Duration
	from      time.Time
	to        time.Time
	returnHWM time.Time
}

func (c *replayWindow) Name() string                   { return c.name }
func (c *replayWindow) DefaultInterval() time.Duration { return time.Minute }
func (c *replayWindow) Lag() time.Duration             { return 0 }
func (c *replayWindow) ReplayOverlap() time.Duration   { return c.overlap }

func (c *replayWindow) CollectWindow(
	_ context.Context,
	from, to time.Time,
	_ telemetry.Emitter,
) (time.Time, error) {
	c.from, c.to = from, to
	return c.returnHWM, nil
}

func TestRunWindow_ReplaysOnlyWarmWindowsAndPersistsForwardHWM(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	last := now.Add(-10 * time.Minute)
	store := NewMemoryStore()
	if err := store.Set("flowlogs", last); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	c := &replayWindow{
		name:      "flowlogs",
		overlap:   5 * time.Minute,
		returnHWM: last.Add(-time.Minute),
	}
	s := NewScheduler(telemetrytest.New().Emitter(), store, WithClock(func() time.Time { return now }))

	if err := s.runWindow(context.Background(), c, Entry{
		Collector:       c,
		InitialLookback: time.Hour,
		MaxWindow:       time.Hour,
	}); err != nil {
		t.Fatalf("runWindow: %v", err)
	}
	if want := last.Add(-5 * time.Minute); !c.from.Equal(want) {
		t.Errorf("warm replay from = %v, want %v", c.from, want)
	}
	if !c.to.Equal(now) {
		t.Errorf("warm replay to = %v, want %v", c.to, now)
	}
	if got, ok := store.Get("flowlogs"); !ok || !got.Equal(now) {
		t.Errorf("persisted checkpoint = %v/%v, want forward HWM %v", got, ok, now)
	}

	coldStore := NewMemoryStore()
	cold := &replayWindow{name: "flowlogs", overlap: 5 * time.Minute}
	coldScheduler := NewScheduler(
		telemetrytest.New().Emitter(),
		coldStore,
		WithClock(func() time.Time { return now }),
	)
	if err := coldScheduler.runWindow(context.Background(), cold, Entry{
		Collector:       cold,
		InitialLookback: time.Hour,
		MaxWindow:       time.Hour,
	}); err != nil {
		t.Fatalf("cold runWindow: %v", err)
	}
	if want := now.Add(-time.Hour); !cold.from.Equal(want) {
		t.Errorf("cold replay from = %v, want initial lookback %v without extra overlap", cold.from, want)
	}
}
