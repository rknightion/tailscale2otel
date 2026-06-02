// Package auditlogs implements the "auditlogs" window collector. On each tick it
// fetches the tailnet configuration audit log for the window [from, to] and
// delegates conversion (OTEL log records plus the events counter) to the shared
// audit.Processor, which is the same instance used by the streaming receiver.
package auditlogs

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

const (
	// defaultInterval is the fallback poll cadence when New is given a
	// non-positive interval.
	defaultInterval = 60 * time.Second
	// defaultLag is the fallback trailing safety margin when New is given a
	// non-positive lag, so the scheduler never queries up to "now".
	defaultLag = 60 * time.Second
)

// api is the subset of the Tailscale API this collector needs. It is satisfied
// by *tsapi.Client.
type api interface {
	ConfigAuditLogs(ctx context.Context, start, end time.Time) (audit.ConfigurationResponse, error)
}

// Collector implements collector.WindowCollector for the configuration audit
// log. It owns no conversion logic; that lives in the shared audit.Processor.
type Collector struct {
	api      api
	proc     *audit.Processor
	interval time.Duration
	lag      time.Duration
}

// New returns an auditlogs Collector that fetches via a and converts via proc.
// A non-positive interval defaults to 60s; a non-positive lag defaults to 60s.
func New(a api, proc *audit.Processor, interval, lag time.Duration) *Collector {
	return &Collector{
		api:      a,
		proc:     proc,
		interval: interval,
		lag:      lag,
	}
}

// Name returns the stable collector identifier and checkpoint key.
func (c *Collector) Name() string { return "auditlogs" }

// DefaultInterval is the suggested poll cadence: the configured interval if
// positive, else 60s.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Lag is the trailing safety margin: the configured lag if positive, else 60s.
func (c *Collector) Lag() time.Duration {
	if c.lag > 0 {
		return c.lag
	}
	return defaultLag
}

// CollectWindow fetches the audit log for [from, to], hands every event to the
// shared processor, and returns to as the consumed high-water mark. On a fetch
// error it returns the zero time and the error, emitting nothing.
//
// Future refinement: boundary-dedup. The API treats the window as inclusive of
// both endpoints, so an event at exactly the boundary timestamp can be returned
// by two adjacent windows. Returning to (rather than the max event time) keeps
// the scheduler simple; de-duplicating events at the from/to boundary is left
// as a future refinement.
func (c *Collector) CollectWindow(ctx context.Context, from, to time.Time, e telemetry.Emitter) (time.Time, error) {
	resp, err := c.api.ConfigAuditLogs(ctx, from, to)
	if err != nil {
		return time.Time{}, err
	}
	c.proc.ProcessAll(resp, e)
	return to, nil
}
