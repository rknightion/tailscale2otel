// Package collector defines the pluggable data-source model: the Collector
// interfaces every source implements, a Registry of enabled collectors, the
// checkpoint store for time-window pollers, and the Scheduler that drives them.
package collector

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Collector is implemented by every Tailscale data source.
type Collector interface {
	// Name is a stable identifier (e.g. "devices", "flowlogs"). Used in
	// self-observability attributes and as the checkpoint key.
	Name() string
	// DefaultInterval is the suggested poll cadence; config may override it.
	DefaultInterval() time.Duration
}

// SnapshotCollector fetches the current state on each tick (devices, users,
// keys, settings, acl, dns).
type SnapshotCollector interface {
	Collector
	Collect(ctx context.Context, e telemetry.Emitter) error
}

// WindowCollector fetches a time window [from, to] on each tick (flowlogs,
// auditlogs). It returns the high-water mark actually consumed so the
// scheduler can persist it as the next window's start.
type WindowCollector interface {
	Collector
	CollectWindow(ctx context.Context, from, to time.Time, e telemetry.Emitter) (highWaterMark time.Time, err error)
	// Lag is the trailing safety margin; the scheduler sets to = now - Lag()
	// so it never queries up to "now" (where late records may still arrive).
	Lag() time.Duration
}

// Entry is a registered collector with its resolved poll interval. The window
// fields apply only to WindowCollectors.
type Entry struct {
	Collector       Collector
	Interval        time.Duration
	InitialLookback time.Duration // cold-start lookback (window collectors)
	MaxWindow       time.Duration // per-tick window cap (window collectors)
}

// Registry holds the enabled collectors to run.
type Registry struct {
	entries []Entry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a collector with the given interval. A non-positive interval
// falls back to the collector's DefaultInterval.
func (r *Registry) Register(c Collector, interval time.Duration) {
	if interval <= 0 {
		interval = c.DefaultInterval()
	}
	r.entries = append(r.entries, Entry{Collector: c, Interval: interval})
}

// RegisterWindow adds a window collector with its interval and window bounds.
// A non-positive interval falls back to the collector's DefaultInterval.
func (r *Registry) RegisterWindow(c WindowCollector, interval, initialLookback, maxWindow time.Duration) {
	if interval <= 0 {
		interval = c.DefaultInterval()
	}
	r.entries = append(r.entries, Entry{
		Collector:       c,
		Interval:        interval,
		InitialLookback: initialLookback,
		MaxWindow:       maxWindow,
	})
}

// Entries returns the registered collectors in registration order.
func (r *Registry) Entries() []Entry { return r.entries }
