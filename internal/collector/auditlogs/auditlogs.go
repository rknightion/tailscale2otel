// Package auditlogs implements the "auditlogs" window collector. On each tick it
// fetches the tailnet configuration audit log for the window [from, to] and
// delegates conversion (OTEL log records plus the events counter) to the shared
// audit.Processor, which is the same instance used by the streaming receiver.
package auditlogs

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/audit"
	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
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
	// onIngest, when non-nil, is called once per successful poll window with
	// ("poll","audit", <records accepted>, 0). The app supplies it (gated on
	// self-observability); the collector stays agnostic to how it's emitted.
	onIngest func(source, signal string, records, bytes int)
}

// New returns an auditlogs Collector that fetches via a and converts via proc.
// A non-positive interval defaults to 60s; a non-positive lag defaults to 60s.
// onIngest, when non-nil, is called after each successful window with the
// post-dedup record count; pass nil to disable.
func New(a api, proc *audit.Processor, interval, lag time.Duration, onIngest func(source, signal string, records, bytes int)) *Collector {
	return &Collector{
		api:      a,
		proc:     proc,
		interval: interval,
		lag:      lag,
		seen:     dedup.New(dedupCapacity),
		onIngest: onIngest,
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
	if c.onIngest != nil {
		c.onIngest(semconv.IngestSourcePoll, semconv.IngestSignalAudit, len(resp.Logs), 0)
	}
	return to, nil
}

// eventKey derives a stable de-dup key for a single audit event, used to suppress
// the inclusive-window boundary repeat. Both branches include action/target so
// two DISTINCT sub-changes that share an eventGroupID AND an identical eventTime
// (a real API shape) are not collapsed into one — the grouped branch previously
// keyed on "<eventGroupID>|<eventTime>" only and dropped the second (#97). This
// stays boundary-safe: a true boundary repeat has identical everything. It mirrors
// audit.DedupKey's cross-source key shape (action|target.id|target.property).
func eventKey(ev audit.Event) string {
	t := ev.EventTime.UTC().Format(time.RFC3339Nano)
	if ev.EventGroupID != "" {
		return ev.EventGroupID + "|" + t + "|" + ev.Action + "|" + ev.Target.ID + "|" + ev.Target.Property
	}
	return t + "|" + ev.Action + "|" + ev.Target.ID
}
