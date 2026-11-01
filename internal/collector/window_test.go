package collector

import (
	"testing"
	"time"
)

func TestNextWindow(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	lag := 2 * time.Minute
	initial := 5 * time.Minute
	maxW := time.Hour

	tests := []struct {
		name     string
		last     time.Time
		hasLast  bool
		wantFrom time.Time
		wantTo   time.Time
		wantOK   bool
	}{
		{
			name:     "cold start looks back initialLookback from now-lag",
			hasLast:  false,
			wantFrom: now.Add(-lag).Add(-initial),
			wantTo:   now.Add(-lag),
			wantOK:   true,
		},
		{
			name:     "warm poll runs from last to now-lag",
			last:     now.Add(-10 * time.Minute),
			hasLast:  true,
			wantFrom: now.Add(-10 * time.Minute),
			wantTo:   now.Add(-lag),
			wantOK:   true,
		},
		{
			name:     "long outage caps the window to maxWindow",
			last:     now.Add(-5 * time.Hour),
			hasLast:  true,
			wantFrom: now.Add(-5 * time.Hour),
			wantTo:   now.Add(-5 * time.Hour).Add(maxW),
			wantOK:   true,
		},
		{
			name:    "nothing to poll when caught up to now-lag",
			last:    now.Add(-lag),
			hasLast: true,
			wantOK:  false,
		},
		{
			name:    "nothing to poll when last is in the future",
			last:    now.Add(time.Hour),
			hasLast: true,
			wantOK:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from, to, ok := nextWindow(tc.last, tc.hasLast, now, lag, initial, maxW, 0)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if !from.Equal(tc.wantFrom) {
				t.Fatalf("from = %v, want %v", from, tc.wantFrom)
			}
			if !to.Equal(tc.wantTo) {
				t.Fatalf("to = %v, want %v", to, tc.wantTo)
			}
		})
	}
}

func TestNextWindowLogHorizon(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, warm := range []bool{false, true} {
		from, to, ok := nextWindow(now.Add(-10*24*time.Hour), warm, now, time.Minute, 10*24*time.Hour, time.Hour, 6*24*time.Hour)
		if !ok || !from.Equal(now.Add(-6*24*time.Hour)) || !to.Equal(from.Add(time.Hour)) {
			t.Fatalf("warm=%v got %v %v %v", warm, from, to, ok)
		}
	}
}
