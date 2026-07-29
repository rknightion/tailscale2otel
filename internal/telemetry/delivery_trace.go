package telemetry

import (
	"context"
	"sync/atomic"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// observingSpanExporter decorates a span exporter so trace delivery is recorded
// alongside metrics and logs. Traces had no wrapper at all, so a trace pipeline
// could fail every export with nothing anywhere reporting it (#317).
//
// It also tallies the spans handed to the exporter (#359: delivery_trace.go
// previously counted nothing here, and this issue's scope explicitly asks for a
// span-count tally parallel to the existing datapoint/log-record counters).
// Unlike countingMetricExporter/countingLogExporter, tallying here is
// unconditional rather than gated by a "counting" flag: an atomic add per export
// batch is negligible even when self-observability is off, and gating it would
// need a constructor parameter that provider.go's existing two-argument call site
// does not pass (see this issue's WIRING REQUEST for how to plumb the count out
// to Provider.ExportStats()).
//
// The return type is the concrete *observingSpanExporter (not the
// sdktrace.SpanExporter interface the old signature returned) so callers that
// need count() — including this package's own tests — don't have to type-assert;
// assigning it to an sdktrace.SpanExporter-typed variable, as provider.go already
// does, is unaffected.
type observingSpanExporter struct {
	sdktrace.SpanExporter
	delivery *deliveryTracker
	spans    atomic.Int64
}

func newObservingSpanExporter(inner sdktrace.SpanExporter, d *deliveryTracker) *observingSpanExporter {
	return &observingSpanExporter{SpanExporter: inner, delivery: d}
}

func (o *observingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	o.spans.Add(int64(len(spans)))
	start := time.Now()
	err := o.SpanExporter.ExportSpans(ctx, spans)
	o.delivery.observe(SignalTraces, err, time.Since(start).Seconds())
	// Tag the returned error with its signal (#359), same as the metric/log
	// wrappers — see the doc comment on withExportSignal in delivery.go.
	return withExportSignal(SignalTraces, err)
}

// count returns the cumulative number of spans handed to the exporter since
// start. Safe for concurrent use.
func (o *observingSpanExporter) count() int64 { return o.spans.Load() }
