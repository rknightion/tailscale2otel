package telemetry

// EmitStats is the cumulative count of metric data points and log records handed
// to an Emitter since process start, measured at the EMIT boundary — i.e. what
// the collectors produced, before batching, aggregation, or export. It is the
// input to the admin status page's throughput trend and is deliberately distinct
// from ExportStats, which counts what the OTLP exporters actually shipped (a
// cumulative-temporality metric pipeline re-exports idle series, so the two
// numbers answer different questions).
//
// Unlike ExportStats the counters are always live: they are two atomic adds on
// the emit path with no allocation and no locking, so they do not depend on
// self-observability being enabled.
type EmitStats struct {
	MetricPoints uint64
	LogRecords   uint64
}

// EmitCounter is implemented by Emitters that tally what they emit. The concrete
// *otelEmitter satisfies it; test doubles need not. Callers type-assert rather
// than widening the Emitter interface, so collectors keep the smallest possible
// surface.
type EmitCounter interface {
	EmitStats() EmitStats
}

// EmitStats returns this emitter's cumulative emit-boundary counts. Safe to call
// concurrently with emitting.
func (e *otelEmitter) EmitStats() EmitStats {
	return EmitStats{
		MetricPoints: e.emittedPoints.Load(),
		LogRecords:   e.emittedLogs.Load(),
	}
}

// EmitStats returns the emit-boundary counts for this provider's Emitter, or a
// zero value if that Emitter does not count (never the case for a Provider built
// by NewProvider). Safe to call concurrently.
func (p *Provider) EmitStats() EmitStats {
	if c, ok := p.emitter.(EmitCounter); ok {
		return c.EmitStats()
	}
	return EmitStats{}
}
