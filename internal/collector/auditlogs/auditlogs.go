// Package auditlogs implements the "auditlogs" window collector. On each tick it
// fetches the tailnet configuration audit log for the window [from, to] and
// delegates conversion (OTEL log records plus the events counter) to the shared
// audit.Processor, which is the same instance used by the streaming receiver.
package auditlogs

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/audit"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

const (
	// defaultInterval is the fallback poll cadence when New is given a
	// non-positive interval.
	defaultInterval = 60 * time.Second
	// defaultLag is the fallback trailing safety margin when New is given a
	// non-positive lag, so the scheduler never queries up to "now".
	defaultLag = 60 * time.Second
	// dedupCapacity bounds the boundary de-dup set. Events at a window boundary
	// can appear in two adjacent ticks; remembering recent event keys lets us
	// suppress the duplicate. The set is FIFO-bounded, so old keys age out.
	dedupCapacity = 4096
)

// api is the subset of the Tailscale API this collector needs. It is satisfied
// by *tsapi.Client.
type api interface {
	ConfigAuditLogs(ctx context.Context, start, end time.Time) (audit.ConfigurationResponse, error)
}

// Collector implements collector.WindowCollector for the configuration audit
// log. It owns no conversion logic; that lives in the shared audit.Processor.
// It does own a small boundary de-dup set so an event at a window boundary,
// which the inclusive API can return in two adjacent ticks, is emitted once.
type Collector struct {
	api      api
	proc     *audit.Processor
	interval time.Duration
	lag      time.Duration
	seen     *dedup.Set
}

// New returns an auditlogs Collector that fetches via a and converts via proc.
// A non-positive interval defaults to 60s; a non-positive lag defaults to 60s.
func New(a api, proc *audit.Processor, interval, lag time.Duration) *Collector {
	return &Collector{
		api:      a,
		proc:     proc,
		interval: interval,
		lag:      lag,
		seen:     dedup.New(dedupCapacity),
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

// CollectWindow fetches the audit log for [from, to], de-duplicates events that
// straddle a window boundary, hands the survivors to the shared processor, and
// returns to as the consumed high-water mark. On a fetch error it returns the
// zero time and the error, emitting nothing.
//
// Boundary de-dup: the API treats the window as inclusive of both endpoints, so
// an event at exactly the boundary timestamp can be returned by two adjacent
// windows. Each event is keyed (see eventKey) and dropped if its key was seen
// in a recent window, so a boundary event is emitted exactly once. Returning to
// (rather than the max event time) keeps the scheduler's checkpoint simple.
func (c *Collector) CollectWindow(ctx context.Context, from, to time.Time, e telemetry.Emitter) (time.Time, error) {
	resp, err := c.api.ConfigAuditLogs(ctx, from, to)
	if err != nil {
		return time.Time{}, err
	}
	if len(resp.Logs) > 0 {
		kept := resp.Logs[:0:0]
		for _, ev := range resp.Logs {
			if c.seen.Add(eventKey(ev)) {
				kept = append(kept, ev)
			}
		}
		resp.Logs = kept
	}
	c.proc.ProcessAll(resp, e)
	return to, nil
}

// eventKey derives a stable de-dup key for a single audit event. When the event
// carries an eventGroupID it identifies a logical change, so the key is
// "<eventGroupID>|<eventTime>". When the eventGroupID is empty the key instead
// combines the event time with the action and target ID, so distinct events
// sharing a timestamp are not collapsed into one.
func eventKey(ev audit.Event) string {
	t := ev.EventTime.UTC().Format(time.RFC3339Nano)
	if ev.EventGroupID != "" {
		return ev.EventGroupID + "|" + t
	}
	return t + "|" + ev.Action + "|" + ev.Target.ID
}
