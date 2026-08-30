package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

func dur(d time.Duration) config.Duration { return config.Duration(d) }

// TestDedupHorizons_OmittedWhenNotPolling pins the live-observed defect: the
// overlap horizon is derived from ReplayOverlap and Interval, which are
// POLL-path settings. A stream-fed collector has no poll boundary, so
// publishing one describes a window that does not exist and leaves the
// youngest-eviction alert dividing a latched all-time minimum by an
// inapplicable denominator — it fires on the first fill and can never resolve.
func TestDedupHorizons_OmittedWhenNotPolling(t *testing.T) {
	cfg := &config.Config{}
	cfg.Collectors.Flowlogs.Source = "stream"
	cfg.Collectors.Flowlogs.Interval = dur(60 * time.Second)
	cfg.Collectors.Flowlogs.ReplayOverlap = dur(5 * time.Minute)
	cfg.Collectors.Auditlogs.Source = "stream"
	cfg.Collectors.Auditlogs.Interval = dur(60 * time.Second)

	got := dedupHorizons(cfg)
	for _, set := range []string{"flow", "audit", "webhook_cross"} {
		if h, ok := got[set]; ok {
			t.Errorf("horizon for %q = %v on a stream-only config; want it absent", set, h)
		}
	}
}

// TestDedupHorizons_PresentWhenPolling keeps the horizon on the path it
// actually describes, including the empty-source default and "both".
func TestDedupHorizons_PresentWhenPolling(t *testing.T) {
	for _, src := range []string{"", "poll", "both"} {
		cfg := &config.Config{}
		cfg.Collectors.Flowlogs.Source = src
		cfg.Collectors.Flowlogs.Interval = dur(60 * time.Second)
		cfg.Collectors.Flowlogs.ReplayOverlap = dur(5 * time.Minute)
		cfg.Collectors.Auditlogs.Source = src
		cfg.Collectors.Auditlogs.Interval = dur(90 * time.Second)

		got := dedupHorizons(cfg)
		if got["flow"] != 5*time.Minute {
			t.Errorf("source %q: flow horizon = %v, want the 5m replay overlap (the larger of interval and overlap)", src, got["flow"])
		}
		if got["audit"] != 90*time.Second {
			t.Errorf("source %q: audit horizon = %v, want the 90s poll interval", src, got["audit"])
		}
		if got["webhook_cross"] != 90*time.Second {
			t.Errorf("source %q: webhook_cross horizon = %v, want the audit poll interval it dedups against", src, got["webhook_cross"])
		}
	}
}

// TestDedupHorizons_ObjectStoreIsNotAPollBoundary covers the fourth source
// value: an export-bucket feed has no poll boundary either.
func TestDedupHorizons_ObjectStoreIsNotAPollBoundary(t *testing.T) {
	cfg := &config.Config{}
	cfg.Collectors.Flowlogs.Source = "objectstore"
	cfg.Collectors.Flowlogs.Interval = dur(60 * time.Second)
	cfg.Collectors.Flowlogs.ReplayOverlap = dur(5 * time.Minute)
	if h, ok := dedupHorizons(cfg)["flow"]; ok {
		t.Errorf("flow horizon = %v for an objectstore feed; want it absent", h)
	}
}
