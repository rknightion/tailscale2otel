package collector

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/jsonbudget"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

type budgetWindow struct {
	replayWindow
	maxSpan  time.Duration
	requests [][2]time.Time
	err      error
}

func (c *budgetWindow) CollectWindow(_ context.Context, from, to time.Time, _ telemetry.Emitter) (time.Time, error) {
	c.requests = append(c.requests, [2]time.Time{from, to})
	if c.err != nil {
		return time.Time{}, c.err
	}
	if to.Sub(from) > c.maxSpan {
		return time.Time{}, fmt.Errorf("response: %w", jsonbudget.ErrTooLarge)
	}
	return to, nil
}

func TestRunWindow_ShrinksAfterBudgetError(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	initial := now.Add(-65 * time.Minute)
	store := NewMemoryStore()
	if err := store.Set("flowlogs", initial); err != nil {
		t.Fatal(err)
	}
	c := &budgetWindow{replayWindow: replayWindow{name: "flowlogs", overlap: 5 * time.Minute}, maxSpan: 2 * time.Minute}
	s := NewScheduler(telemetrytest.New().Emitter(), store, WithClock(func() time.Time { return now }))
	entry := Entry{Collector: c, InitialLookback: time.Hour, MaxWindow: time.Hour}
	var failedSpan time.Duration
	for tick := 0; tick < 30; tick++ {
		before, _ := store.Get("flowlogs")
		start := len(c.requests)
		err := s.runWindow(context.Background(), c, entry)
		if len(c.requests)-start > 8 {
			t.Fatal("more than eight windows in tick")
		}
		if failedSpan > 0 {
			request := c.requests[start]
			if span := request[1].Sub(request[0]); span > max(failedSpan/2, 10*time.Second) {
				t.Fatalf("window did not shrink: %v after %v", span, failedSpan)
			}
		}
		after, _ := store.Get("flowlogs")
		if after.Before(before) {
			t.Fatal("cursor moved backward")
		}
		if tick > 0 {
			for _, request := range c.requests[start:] {
				if request[0].Before(before) {
					t.Fatal("limited window replayed before cursor")
				}
			}
		}
		failedSpan = 0
		if err != nil {
			if !errors.Is(err, jsonbudget.ErrTooLarge) {
				t.Fatal(err)
			}
			last := c.requests[len(c.requests)-1]
			failedSpan = last[1].Sub(last[0])
		}
		if now.Sub(after) <= time.Minute {
			return
		}
		now = now.Add(time.Minute)
	}
	t.Fatal("did not catch up within 30 ticks")
}

func TestRunWindow_OnlyByteBudgetErrorsShrink(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	for _, failure := range []error{nil, errors.New("transient"), jsonbudget.ErrTooComplex} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			store := NewMemoryStore()
			last := now.Add(-time.Minute)
			if err := store.Set("flowlogs", last); err != nil {
				t.Fatal(err)
			}
			c := &budgetWindow{replayWindow: replayWindow{name: "flowlogs", overlap: 5 * time.Minute}, maxSpan: time.Hour, err: failure}
			s := NewScheduler(telemetrytest.New().Emitter(), store, WithClock(func() time.Time { return now }))
			for range 2 {
				prior, _ := store.Get("flowlogs")
				n := len(c.requests)
				_ = s.runWindow(context.Background(), c, Entry{Collector: c, InitialLookback: time.Hour, MaxWindow: time.Hour})
				if len(c.requests) != n+1 {
					t.Fatal("normal collector requested multiple windows")
				}
				got := c.requests[n]
				if !got[0].Equal(prior.Add(-5*time.Minute)) || !got[1].Equal(now) {
					t.Fatalf("normal bounds changed: %v", got)
				}
				now = now.Add(time.Minute)
			}
		})
	}
}

func TestRunWindow_BudgetFloorAndRecovery(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	if err := store.Set("flowlogs", now.Add(-20*time.Second)); err != nil {
		t.Fatal(err)
	}
	c := &budgetWindow{replayWindow: replayWindow{name: "flowlogs"}, maxSpan: 0}
	s := NewScheduler(telemetrytest.New().Emitter(), store, WithClock(func() time.Time { return now }))
	entry := Entry{Collector: c, InitialLookback: time.Minute, MaxWindow: 40 * time.Second}
	for range 3 {
		if err := s.runWindow(context.Background(), c, entry); !errors.Is(err, jsonbudget.ErrTooLarge) {
			t.Fatalf("expected budget failure, got %v", err)
		}
	}
	for _, request := range c.requests[1:] {
		if span := request[1].Sub(request[0]); span != 10*time.Second {
			t.Fatalf("floor span = %v", span)
		}
	}
	c.maxSpan = time.Hour
	for success := 1; success <= 20; success++ {
		// Keep exactly one short forward window available so each tick is one success.
		cursor, _ := store.Get("flowlogs")
		now = cursor.Add(10 * time.Second)
		if err := s.runWindow(context.Background(), c, entry); err != nil {
			t.Fatal(err)
		}
		limit := s.windowLimits["flowlogs"]
		switch {
		case success < 10:
			if limit == nil || limit.span != 10*time.Second {
				t.Fatal("limit grew before ten successes")
			}
		case success < 20:
			if limit == nil || limit.span != 20*time.Second {
				t.Fatal("limit did not double at ten successes")
			}
		default:
			if limit != nil {
				t.Fatal("limit not removed on reaching maximum window")
			}
		}
	}
}

type rejectedWindowCheckpoint struct{ CheckpointStore }

func (s rejectedWindowCheckpoint) Set(string, time.Time) error {
	return errors.New("synthetic persist rejected")
}

func TestRunWindow_LimitedPersistFailureStopsCatchup(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	base := NewMemoryStore()
	if err := base.Set("flowlogs", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	c := &budgetWindow{replayWindow: replayWindow{name: "flowlogs"}, maxSpan: time.Hour}
	s := NewScheduler(telemetrytest.New().Emitter(), rejectedWindowCheckpoint{base}, WithClock(func() time.Time { return now }))
	s.windowLimits = map[string]*windowLimit{"flowlogs": {span: time.Minute}}
	if err := s.runWindow(context.Background(), c, Entry{Collector: c, MaxWindow: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if len(c.requests) != 1 {
		t.Fatalf("failed persist repeated window %d times", len(c.requests))
	}
	if s.windowLimits["flowlogs"].successes != 0 {
		t.Fatal("failed persist counted as successful progress")
	}
}

func TestRunWindow_LimitGrowthNeedsConsecutiveSuccesses(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	if err := store.Set("flowlogs", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	c := &budgetWindow{replayWindow: replayWindow{name: "flowlogs"}, maxSpan: time.Hour, err: errors.New("transient")}
	s := NewScheduler(telemetrytest.New().Emitter(), store, WithClock(func() time.Time { return now }))
	s.windowLimits = map[string]*windowLimit{"flowlogs": {span: time.Minute, successes: 9}}
	entry := Entry{Collector: c, MaxWindow: time.Hour}
	if err := s.runWindow(context.Background(), c, entry); err == nil {
		t.Fatal("expected transient failure")
	}
	c.err = nil
	if err := s.runWindow(context.Background(), c, entry); err != nil {
		t.Fatal(err)
	}
	if limit := s.windowLimits["flowlogs"]; limit.span != time.Minute || limit.successes != 1 {
		t.Fatalf("failure did not break success streak: %+v", limit)
	}
}
