package annotations

import (
	"context"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// teeEmitter forwards every call to the wrapped Emitter unchanged and, on the
// two log methods the curated rules read, ALSO offers the record to the
// Recorder.
//
// # Why the emitter and not the collectors
//
// The Emitter is the only boundary with nothing behind it. Hooking the audit
// processor and the devices collector individually would cover today's rules
// and silently miss the next one; hooking here covers every collector, the
// streaming receiver and the webhook receiver at once — they all emit through
// this same interface — and cannot be escaped. "Publish from what collectors
// already emit, add no Tailscale API call" is then a structural property, not
// a discipline: there is no API client in this package.
//
// # Every method is overridden explicitly
//
// An un-overridden method would be PROMOTED from the embedded interface and
// compile perfectly, so a method added to Emitter later would forward itself
// correctly and no test would notice — until someone added a rule reading it.
// Only LogEvent and LogEventCtx do anything extra; the rest are explicit
// pass-throughs, so a reader can see the forwarding is total and
// TestEveryEmitterMethodIsForwarded can parse it.
//
// # It never mutates and never drops
//
// The record is forwarded FIRST, unchanged, before the Recorder sees it.
// Nothing in this package can drop a record, alter an attribute, or return an
// error into a collector — an annotation writer that could would have turned a
// dashboard nicety into a data-loss path.
type teeEmitter struct {
	telemetry.Emitter
	recorder *Recorder
	tailnet  string
}

// Tee returns an Emitter that forwards everything to base and derives
// annotations from the curated set of records passing through it. A nil base or
// recorder returns base unchanged, so a call site needs no conditional.
func Tee(base telemetry.Emitter, recorder *Recorder, tailnet string) telemetry.Emitter {
	if base == nil || recorder == nil {
		return base
	}
	return &teeEmitter{Emitter: base, recorder: recorder, tailnet: tailnet}
}

func (e *teeEmitter) Counter(name, unit, desc string, add float64, attrs telemetry.Attrs) {
	e.Emitter.Counter(name, unit, desc, add, attrs)
}

func (e *teeEmitter) Gauge(name, unit, desc string, value float64, attrs telemetry.Attrs) {
	e.Emitter.Gauge(name, unit, desc, value, attrs)
}

func (e *teeEmitter) GaugeSnapshot(name, unit, desc string, points []telemetry.GaugePoint) {
	e.Emitter.GaugeSnapshot(name, unit, desc, points)
}

func (e *teeEmitter) UpDownCounter(name, unit, desc string, value float64, attrs telemetry.Attrs) {
	e.Emitter.UpDownCounter(name, unit, desc, value, attrs)
}

func (e *teeEmitter) Histogram(name, unit, desc string, value float64, bounds []float64, attrs telemetry.Attrs) {
	e.Emitter.Histogram(name, unit, desc, value, bounds, attrs)
}

func (e *teeEmitter) HistogramCtx(ctx context.Context, name, unit, desc string, value float64,
	bounds []float64, attrs telemetry.Attrs,
) {
	e.Emitter.HistogramCtx(ctx, name, unit, desc, value, bounds, attrs)
}

func (e *teeEmitter) LogEvent(ev telemetry.Event) {
	e.Emitter.LogEvent(ev)
	e.recorder.ObserveEvent(e.tailnet, ev)
}

func (e *teeEmitter) LogEventCtx(ctx context.Context, ev telemetry.Event) {
	e.Emitter.LogEventCtx(ctx, ev)
	e.recorder.ObserveEvent(e.tailnet, ev)
}
