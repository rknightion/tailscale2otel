package telemetry

import (
	"context"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// observingSpanExporter decorates a span exporter so trace delivery is recorded
// alongside metrics and logs. Traces had no wrapper at all, so a trace pipeline
// could fail every export with nothing anywhere reporting it (#317).
//
// Unlike the metric and log wrappers this counts nothing: there is no
// spans-exported cost proxy in the catalog, and adding one is not this issue.
type observingSpanExporter struct {
	sdktrace.SpanExporter
	delivery *deliveryTracker
}

func newObservingSpanExporter(inner sdktrace.SpanExporter, d *deliveryTracker) sdktrace.SpanExporter {
	return &observingSpanExporter{SpanExporter: inner, delivery: d}
}

func (o *observingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	start := time.Now()
	err := o.SpanExporter.ExportSpans(ctx, spans)
	o.delivery.observe(SignalTraces, err, time.Since(start).Seconds())
	return err
}
