// Package flowlogs implements the "flowlogs" polling collector: the POLL path
// for Tailscale network flow logs. On each tick the scheduler hands it a
// [from, to] window; the collector fetches that window via the Tailscale API and
// delegates record-to-OTEL conversion to the shared flowlog.Processor (the same
// processor used by the streaming receiver).
//
// Boundary-record de-duplication across adjacent windows is a future
// refinement: a record straddling a window edge could be counted in two ticks.
// The scheduler's trailing Lag keeps the overlap small, so the current
// implementation tolerates it rather than tracking per-record identity.
package flowlogs

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/flowlog"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

const (
	// defaultInterval is the poll cadence used when none is configured.
	defaultInterval = 60 * time.Second
	// defaultLag is the trailing safety margin used when none is configured.
	// Flow logs can arrive late, so the scheduler queries up to now-Lag.
	defaultLag = 120 * time.Second
)

// api is the subset of the Tailscale API this collector needs. It is satisfied
// by *tsapi.Client.
type api interface {
	NetworkFlowLogs(ctx context.Context, start, end time.Time) (flowlog.NetworkResponse, error)
}

// Collector implements collector.WindowCollector for Tailscale network flow
// logs, fetching each window and delegating conversion to a shared
// flowlog.Processor.
type Collector struct {
	api      api
	proc     *flowlog.Processor
	interval time.Duration
	lag      time.Duration
}

// New returns a flowlogs Collector that fetches windows via a, converts them
// with proc, and uses interval/lag as its poll cadence and trailing safety
// margin (non-positive values fall back to 60s and 120s respectively).
func New(a api, proc *flowlog.Processor, interval, lag time.Duration) *Collector {
	return &Collector{
		api:      a,
		proc:     proc,
		interval: interval,
		lag:      lag,
	}
}

// Name returns the stable collector identifier.
func (c *Collector) Name() string { return "flowlogs" }

// DefaultInterval returns the configured interval, or 60s when unset.
func (c *Collector) DefaultInterval() time.Duration {
	if c.interval > 0 {
		return c.interval
	}
	return defaultInterval
}

// Lag returns the configured trailing safety margin, or 120s when unset.
func (c *Collector) Lag() time.Duration {
	if c.lag > 0 {
		return c.lag
	}
	return defaultLag
}

// CollectWindow fetches flow logs for [from, to] and processes them. On a fetch
// error it returns the zero time so the scheduler does not advance the
// checkpoint and the window is retried. On success it returns to as the
// high-water mark consumed.
func (c *Collector) CollectWindow(ctx context.Context, from, to time.Time, e telemetry.Emitter) (time.Time, error) {
	resp, err := c.api.NetworkFlowLogs(ctx, from, to)
	if err != nil {
		return time.Time{}, err
	}
	c.proc.ProcessAll(resp, e)
	return to, nil
}
