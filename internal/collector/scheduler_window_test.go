package collector

import (
	"testing"
	"time"
)

func TestScheduler_InitialDelayHonorsConfiguredWindow(t *testing.T) {
	const window = 250 * time.Millisecond
	s := NewScheduler(nil, nil, WithStaggerWindow(window))
	for range 100 {
		d := s.initialDelay()
		if d < 0 || d >= window {
			t.Fatalf("initial delay = %v, want [0, %v)", d, window)
		}
	}
}

func TestScheduler_NonPositiveStaggerWindowDisablesDelay(t *testing.T) {
	for _, window := range []time.Duration{0, -time.Second} {
		s := NewScheduler(nil, nil, WithStaggerWindow(window))
		if got := s.initialDelay(); got != 0 {
			t.Fatalf("initialDelay with window %v = %v, want 0", window, got)
		}
	}
}
