package apistate

import (
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// EmitAvailability writes the availability of one (collector, operation): `1`
// for the current state and `0` for every other state.
//
// Zero-seeding the whole state set is deliberate and load-bearing, for two
// independent reasons:
//
//  1. Under the forced cumulative temporality a synchronous gauge never drops a
//     series it has ever seen (otel-go #3006), so emitting ONLY the current
//     state would leave the previous state pinned at `1` forever — an operation
//     that recovered would read as both supported and credential_rejected.
//
//  2. The obvious alternative, Emitter.GaugeSnapshot, is WRONG here. A snapshot
//     replaces the entire series set for a metric NAME, and this metric is
//     emitted by many collectors. Two collectors flushing snapshots for the same
//     name would each wipe the other's series on every tick.
//
// The resulting series count is bounded and static: len(States()) per
// (collector, operation) pair.
func EmitAvailability(e telemetry.Emitter, collector, operation string, s State) {
	for _, st := range States() {
		v := 0.0
		if st == s {
			v = 1
		}
		e.Gauge(docAvailability.Name, docAvailability.Unit, docAvailability.Description, v, telemetry.Attrs{
			semconv.AttrCollector:    collector,
			semconv.AttrAPIOperation: operation,
			semconv.AttrAPIState:     string(st),
		})
	}
}

// EmitLastProbe records when an operation was last probed.
func EmitLastProbe(e telemetry.Emitter, collector, operation string, at time.Time) {
	e.Gauge(docLastProbe.Name, docLastProbe.Unit, docLastProbe.Description, float64(at.Unix()), telemetry.Attrs{
		semconv.AttrCollector:    collector,
		semconv.AttrAPIOperation: operation,
	})
}

// Observe is the one-call helper for a collector that has just made an API
// call: it classifies the error, records it on the shared tracker for the admin
// status page, emits the bounded availability + last-probe signals, and returns
// the state so the caller can branch on it.
//
// Both tracker and emitter arguments tolerate the zero value (a nil *Tracker is
// a no-op), so a collector constructed without self-observability still works.
func Observe(e telemetry.Emitter, t *Tracker, collector, operation string, d Disposition, err error, now time.Time) State {
	s := Classify(err, d)
	t.Record(collector, operation, s, now)
	if e != nil {
		EmitAvailability(e, collector, operation, s)
		EmitLastProbe(e, collector, operation, now)
	}
	return s
}

// EmitCoverage writes the subrequest attempt/failure/coverage signals for every
// tallied subrequest type. Collectors call this once at the end of a tick,
// after the per-entity loop has finished.
func EmitCoverage(e telemetry.Emitter, c *Coverage) {
	for _, entry := range c.Snapshot() {
		base := telemetry.Attrs{
			semconv.AttrCollector:  entry.Collector,
			semconv.AttrSubrequest: entry.Subrequest,
		}
		e.Counter(docSubrequestAttempts.Name, docSubrequestAttempts.Unit, docSubrequestAttempts.Description,
			float64(entry.Attempted), base)
		e.Gauge(docSubrequestCoverage.Name, docSubrequestCoverage.Unit, docSubrequestCoverage.Description,
			entry.Ratio(), base)

		// Zero-seed every state so a failure class that stops occurring falls to
		// zero on the rate() rather than vanishing mid-graph.
		for _, st := range States() {
			if st == StateSupported || st == StateUnknown {
				continue
			}
			e.Counter(docSubrequestFailures.Name, docSubrequestFailures.Unit, docSubrequestFailures.Description,
				float64(entry.Failures[st]), telemetry.Attrs{
					semconv.AttrCollector:  entry.Collector,
					semconv.AttrSubrequest: entry.Subrequest,
					semconv.AttrAPIState:   string(st),
				})
		}
	}
}
