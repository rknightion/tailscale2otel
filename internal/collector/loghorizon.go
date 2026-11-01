package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// DefaultLogMaxRecordAge leaves a day of margin below common seven-day log limits.
const DefaultLogMaxRecordAge = 6 * 24 * time.Hour

// ClampLogStart bounds catch-up independently of lag and chunk size. Zero disables it.
func ClampLogStart(from, now time.Time, age time.Duration) time.Time {
	if age > 0 && from.Before(now.Add(-age)) {
		return now.Add(-age)
	}
	return from
}

// ReportLogHorizonSkip reports an intentionally consumed span once, after its
// clamped cursor has been consumed. The metric is a duration, not a record estimate.
func ReportLogHorizonSkip(e telemetry.Emitter, logger *slog.Logger, name string, from, to time.Time, selfObs bool) {
	if selfObs {
		e.Counter(docLogHorizonSkipped.Name, docLogHorizonSkipped.Unit, docLogHorizonSkipped.Description, to.Sub(from).Seconds(), telemetry.Attrs{semconv.AttrCollector: name})
	}
	logger.Warn("log catch-up horizon clamped", "collector", name, "skipped_seconds", to.Sub(from).Seconds(), "from", from, "to", to)
}

type logHorizonEmitter struct {
	telemetry.Emitter
	now func() time.Time
	age time.Duration
}

// LogHorizonEmitter guards source timestamps as well as query ranges: an object
// may contain historical records, and upstream APIs may return outside the range.
// Metrics and zero-timestamp logs keep their existing behavior.
func LogHorizonEmitter(e telemetry.Emitter, now func() time.Time, age time.Duration) telemetry.Emitter {
	if age <= 0 {
		return e
	}
	return logHorizonEmitter{Emitter: e, now: now, age: age}
}

func (e logHorizonEmitter) LogEvent(event telemetry.Event) {
	if event.Timestamp.IsZero() || !event.Timestamp.Before(e.now().Add(-e.age)) {
		e.Emitter.LogEvent(event)
	}
}
func (e logHorizonEmitter) LogEventCtx(ctx context.Context, event telemetry.Event) {
	if event.Timestamp.IsZero() || !event.Timestamp.Before(e.now().Add(-e.age)) {
		e.Emitter.LogEventCtx(ctx, event)
	}
}
