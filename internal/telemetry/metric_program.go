package telemetry

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"
)

var errMetricProgram = errors.New("ingress metric program invalid")

type metricOperation struct {
	kind                    uint8
	name, unit, description string
	value                   float64
	attrs                   Attrs
	bounds                  []float64
	points                  []GaugePoint
	ctx                     context.Context
}
type metricProgram struct {
	mu               sync.Mutex
	operations       []metricOperation
	bytes            int64
	cursor           int
	sealed           bool
	owner            *IngressReceipt
	maxOps, maxBytes int64
}

func copyMetricAttrs(attrs Attrs) (Attrs, int64, error) {
	if attrs == nil {
		return nil, 0, nil
	}
	copy := make(Attrs, len(attrs))
	var bytes int64
	for key, v := range attrs {
		bytes += int64(len(key)) + 32
		switch value := v.(type) {
		case string:
			copy[key] = value
			bytes += int64(len(value))
		case bool, int, int64, float64:
			copy[key] = value
			bytes += 8
		case []string:
			copy[key] = slices.Clone(value)
			bytes += int64(len(value)) * 16
			for _, s := range value {
				bytes += int64(len(s))
			}
		default:
			return nil, 0, errMetricProgram
		}
	}
	return copy, bytes, nil
}

func (p *metricProgram) add(op metricOperation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	charge := int64(len(op.name)+len(op.unit)+len(op.description)) + 128
	attrs, bytes, err := copyMetricAttrs(op.attrs)
	charge += bytes
	op.attrs = attrs
	op.bounds = slices.Clone(op.bounds)
	charge += int64(len(op.bounds)) * 8
	op.points = slices.Clone(op.points)
	for i := range op.points {
		var n int64
		op.points[i].Attrs, n, err = copyMetricAttrs(op.points[i].Attrs)
		charge += n + 32
		if err != nil {
			break
		}
	}
	if p.sealed || err != nil || op.kind > 4 || charge < 0 || charge > math.MaxInt64-p.bytes || (p.owner != nil && (int64(len(p.operations)) >= p.maxOps || charge > p.maxBytes-p.bytes)) {
		if p.owner != nil {
			p.owner.poison(errMetricProgram)
		}
		return
	}
	p.operations = append(p.operations, op)
	p.bytes += charge
}

func (p *metricProgram) EmitOnce(e Emitter) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sealed = true
	defer func() {
		if recover() != nil {
			err = errMetricProgram
			if p.owner != nil {
				p.owner.poison(err)
			}
		}
	}()
	for p.cursor < len(p.operations) {
		op := p.operations[p.cursor]
		p.cursor++ // never replay an ambiguous operation
		switch op.kind {
		case 0:
			e.Counter(op.name, op.unit, op.description, op.value, op.attrs)
		case 1:
			e.Gauge(op.name, op.unit, op.description, op.value, op.attrs)
		case 2:
			e.UpDownCounter(op.name, op.unit, op.description, op.value, op.attrs)
		case 3:
			e.HistogramCtx(op.ctx, op.name, op.unit, op.description, op.value, op.bounds, op.attrs)
		case 4:
			e.GaugeSnapshot(op.name, op.unit, op.description, op.points)
		default:
			if p.owner != nil {
				p.owner.poison(errMetricProgram)
			}
			return errMetricProgram
		}
	}
	return nil
}

type recordingEmitter struct {
	program  *metricProgram
	delegate Emitter
	ctx      context.Context
}

var _ Emitter = (*recordingEmitter)(nil)

func (e *recordingEmitter) Counter(n, u, d string, v float64, a Attrs) {
	e.program.add(metricOperation{kind: 0, name: n, unit: u, description: d, value: v, attrs: a})
}
func (e *recordingEmitter) Gauge(n, u, d string, v float64, a Attrs) {
	e.program.add(metricOperation{kind: 1, name: n, unit: u, description: d, value: v, attrs: a})
}
func (e *recordingEmitter) UpDownCounter(n, u, d string, v float64, a Attrs) {
	e.program.add(metricOperation{kind: 2, name: n, unit: u, description: d, value: v, attrs: a})
}
func (e *recordingEmitter) Histogram(n, u, d string, v float64, b []float64, a Attrs) {
	e.HistogramCtx(context.Background(), n, u, d, v, b, a)
}
func (e *recordingEmitter) HistogramCtx(ctx context.Context, n, u, d string, v float64, b []float64, a Attrs) {
	e.program.add(metricOperation{kind: 3, name: n, unit: u, description: d, value: v, bounds: b, attrs: a, ctx: ctx})
}
func (e *recordingEmitter) GaugeSnapshot(n, u, d string, points []GaugePoint) {
	e.program.add(metricOperation{kind: 4, name: n, unit: u, description: d, points: points})
}
func (e *recordingEmitter) LogEvent(ev Event) { e.LogEventCtx(e.ctx, ev) }
func (e *recordingEmitter) LogEventCtx(ctx context.Context, ev Event) {
	if r := ingressReceiptFromContext(e.ctx); r != nil {
		ctx = contextWithIngressReceipt(ctx, r)
	}
	if delegate, ok := e.delegate.(*otelEmitter); ok {
		delegate.logEventWithSink(ctx, ev, e)
		return
	}
	e.delegate.LogEventCtx(ctx, ev)
}

// NewCollectionProgram is for bounded CPU-only producer stages. It records
// metrics with the same exhaustive emitter tape used by durable originals.
func NewCollectionProgram() (Emitter, CollectionStage) {
	p := &metricProgram{}
	return &recordingEmitter{program: p, delegate: noopProgramLogs{}, ctx: context.Background()}, p
}
func (*metricProgram) CommitCollected() {}

type noopProgramLogs struct{ Emitter }

func (noopProgramLogs) LogEvent(Event)                     {}
func (noopProgramLogs) LogEventCtx(context.Context, Event) {}
