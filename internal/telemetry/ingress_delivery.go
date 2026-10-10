package telemetry

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

var (
	ErrIngressBackpressure = errors.New("ingress delivery backpressure")
	ErrIngressTerminal     = errors.New("ingress delivery terminal")
	errIngressPoison       = errors.New("ingress work poisoned")
)

type LogBounds struct{ Records, Bytes int64 }
type WorkBounds struct {
	InputBytes             int64
	Logs                   LogBounds
	MetricOps, MetricBytes int64
}
type IngressWork struct {
	Bounds WorkBounds
	Apply  func(context.Context, Emitter) error
}
type ReceiptPhase uint8

const (
	Reserved ReceiptPhase = iota
	Building
	Ready
	Covered
	Delivered
	Poisoned
)

type IngressReceipt struct {
	mu                            sync.Mutex
	owner                         *Provider
	sequence                      uint64
	phase                         ReceiptPhase
	bounds                        WorkBounds
	program                       *metricProgram
	group                         *ingressGroup
	records                       []sdklog.Record
	recordBytes                   int64
	logOffset                     int
	logPermanentRejections        int
	metricsExcluded, logsExcluded bool
	metricAck, logAck             bool
	firstCoverID                  collectionID
	err                           error
	changed                       chan struct{}
}

func (r *IngressReceipt) notifyLocked()       { close(r.changed); r.changed = make(chan struct{}) }
func (r *IngressReceipt) Phase() ReceiptPhase { r.mu.Lock(); defer r.mu.Unlock(); return r.phase }
func (r *IngressReceipt) Eligible() bool      { r.mu.Lock(); defer r.mu.Unlock(); return r.eligibleLocked() }
func (r *IngressReceipt) eligibleLocked() bool {
	return r.phase != Reserved && r.phase != Building && r.phase != Poisoned && (r.metricsExcluded || r.metricAck) && (r.logsExcluded || r.logAck)
}
func (r *IngressReceipt) poison(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = Poisoned
	r.err = errIngressPoison
	r.notifyLocked()
}
func (r *IngressReceipt) Wait(ctx context.Context) error {
	for {
		r.mu.Lock()
		if r.phase == Poisoned {
			r.mu.Unlock()
			return errIngressPoison
		}
		if r.eligibleLocked() {
			r.mu.Unlock()
			return nil
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

type ingressReceiptContextKey struct{}

func contextWithIngressReceipt(ctx context.Context, r *IngressReceipt) context.Context {
	return context.WithValue(ctx, ingressReceiptContextKey{}, r)
}
func ingressReceiptFromContext(ctx context.Context) *IngressReceipt {
	r, _ := ctx.Value(ingressReceiptContextKey{}).(*IngressReceipt)
	return r
}

func validWorkBounds(b WorkBounds) bool {
	return b.InputBytes >= 0 && b.Logs.Records >= 0 && b.Logs.Bytes >= 0 && b.MetricOps >= 0 && b.MetricBytes >= 0
}

// IngressFailure is a closed admission check, not a delivery ACK. In particular,
// ordinary retention backpressure is NOT permanent and does not refuse WAL
// appends before their unchanged disk capacity is exhausted.
func (p *Provider) IngressFailure() error {
	if p == nil || p.metricReader == nil {
		return ErrIngressTerminal
	}
	r := p.metricReader
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed || r.terminal {
		return ErrIngressTerminal
	}
	return nil
}

func (p *Provider) ReserveIngress(b WorkBounds) (*IngressReceipt, error) {
	if !validWorkBounds(b) {
		return nil, errIngressPoison
	}
	reader := p.metricReader
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.terminal || reader.failed {
		return nil, ErrIngressTerminal
	}
	if len(reader.receipts) >= 64 {
		return nil, ErrIngressBackpressure
	}
	var group *ingressGroup
	if !p.metricsExcluded {
		group = reader.openGroup
		if group == nil || group.closed {
			if !reader.makeCreditLocked() {
				return nil, ErrIngressBackpressure
			}
			group = &ingressGroup{}
			reader.groups = append(reader.groups, group)
			reader.openGroup = group
		}
	}
	reader.workSequence++
	r := &IngressReceipt{owner: p, sequence: reader.workSequence, phase: Reserved, bounds: b, group: group, metricsExcluded: p.metricsExcluded, logsExcluded: p.logsExcluded, changed: make(chan struct{})}
	r.program = &metricProgram{owner: r, maxOps: b.MetricOps, maxBytes: b.MetricBytes}
	if group != nil {
		group.members = append(group.members, r)
	}
	reader.receipts[r] = true
	reader.signalLocked()
	return r, nil
}
func (p *Provider) ReleaseUnstarted(r *IngressReceipt) error {
	reader := p.metricReader
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if r == nil || r.owner != p || !reader.receipts[r] {
		return ErrIngressTerminal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != Reserved {
		return errIngressPoison
	}
	delete(reader.receipts, r)
	if r.group != nil {
		members := r.group.members
		for i, member := range members {
			if member == r {
				r.group.members = append(members[:i], members[i+1:]...)
				break
			}
		}
		if len(r.group.members) == 0 {
			reader.removeGroupLocked(r.group)
		}
	}
	reader.signalLocked()
	return nil
}
func (p *Provider) ApplyIngress(ctx context.Context, r *IngressReceipt, work IngressWork) (err error) {
	reader := p.metricReader
	reader.mu.Lock()
	if reader.terminal || r == nil || r.owner != p || !reader.receipts[r] {
		reader.mu.Unlock()
		return ErrIngressTerminal
	}
	r.mu.Lock()
	if r.phase != Reserved || work.Apply == nil || work.Bounds != r.bounds {
		r.mu.Unlock()
		reader.mu.Unlock()
		return errIngressPoison
	}
	if ctx.Err() != nil {
		r.mu.Unlock()
		reader.mu.Unlock()
		return ctx.Err()
	}
	if r.group != nil && r.group.closed {
		r.mu.Unlock()
		reader.mu.Unlock()
		return ErrIngressBackpressure
	}
	r.phase = Building
	r.notifyLocked()
	r.mu.Unlock()
	reader.mu.Unlock()
	defer func() {
		if recover() != nil {
			err = errIngressPoison
		}
		if err != nil {
			r.poison(err)
		} else {
			r.program.mu.Lock()
			r.program.sealed = true
			r.program.mu.Unlock()
			r.mu.Lock()
			if r.phase != Poisoned {
				r.phase = Ready
				r.logAck = r.logsExcluded || len(r.records) == 0
				if r.eligibleLocked() {
					r.phase = Delivered
				}
				r.notifyLocked()
			} else {
				err = errIngressPoison
			}
			r.mu.Unlock()
		}
		reader.mu.Lock()
		if err != nil {
			reader.failed = true
		}
		reader.signalLocked()
		reader.mu.Unlock()
	}()
	ctx = contextWithIngressReceipt(ctx, r)
	var emitter Emitter = &recordingEmitter{program: r.program, delegate: p.emitter, ctx: ctx}
	if r.metricsExcluded {
		// Keep live pull observations without a retained excluded-signal tape.
		emitter = &excludedMetricEmitter{Emitter: p.emitter, ctx: ctx}
	}
	return work.Apply(ctx, emitter)
}

type excludedMetricEmitter struct {
	Emitter
	ctx context.Context
}

func (e *excludedMetricEmitter) LogEvent(ev Event) { e.LogEventCtx(e.ctx, ev) }
func (e *excludedMetricEmitter) LogEventCtx(ctx context.Context, ev Event) {
	e.Emitter.LogEventCtx(contextWithIngressReceipt(ctx, ingressReceiptFromContext(e.ctx)), ev)
}

// ReleaseIngress releases the volatile original only after generation-checked
// disk retirement. Never use content IDs or unrelated provider success here.
func (p *Provider) ReleaseIngress(r *IngressReceipt) error {
	if r == nil || r.owner != p || !r.Eligible() {
		return errIngressPoison
	}
	reader := p.metricReader
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if !reader.receipts[r] {
		return errIngressPoison
	}
	delete(reader.receipts, r)
	reader.signalLocked()
	return nil
}
func (p *Provider) SealIngress() {
	r := p.metricReader
	r.mu.Lock()
	if r.openGroup != nil {
		r.openGroup.closed = true
		r.openGroup = nil
	}
	r.signalLocked()
	r.mu.Unlock()
}

// IngressLogBounds derives content/storage reservation from the full source
// schema and effective facade limits, not a receiver admission limit or raw size.
func (p *Provider) IngressLogBounds(records int64, keys []string, eventNameBytes int64) (LogBounds, error) {
	return ingressLogBounds(p.options, records, keys, eventNameBytes)
}
func DefaultIngressLogBounds(records int64, keys []string, eventNameBytes int64) (LogBounds, error) {
	return ingressLogBounds(Options{}, records, keys, eventNameBytes)
}
func ingressLogBounds(opts Options, records int64, keys []string, eventNameBytes int64) (LogBounds, error) {
	if records < 0 || eventNameBytes < 0 {
		return LogBounds{}, errIngressPoison
	}
	limits := logLimits{bodyBytes: opts.MaxLogBodyBytes, attrValueBytes: opts.MaxLogAttrValueBytes}
	maxKey := 0
	for _, key := range keys {
		maxKey = max(maxKey, len(key))
	}
	per := int64(limits.body()) + int64(len(keys))*(int64(limits.attrValue())+int64(maxKey)+32) + eventNameBytes + 1024
	for _, kv := range constLabelAttrs(opts) {
		per += int64(len(kv.Key)) + int64(len(kv.Value.String())) + 32
	}
	// Slice/map/SDK record storage is charged separately from content.
	per += int64(len(keys))*64 + 512
	if per < 0 || (records > 0 && per > math.MaxInt64/records) {
		return LogBounds{}, errIngressPoison
	}
	return LogBounds{Records: records, Bytes: records * per}, nil
}
func recordCharge(record *sdklog.Record) int64 {
	n := int64(len(record.EventName())+len(record.Body().String())) + 1024
	record.WalkAttributes(func(kv attribute.KeyValue) bool { n += int64(len(kv.Key)+len(kv.Value.String())) + 96; return true })
	return n
}

type ingressLogProcessor struct {
	next  sdklog.Processor
	owner *Provider
}

func (p *ingressLogProcessor) Enabled(ctx context.Context, param sdklog.EnabledParameters) bool {
	if ingressReceiptFromContext(ctx) != nil {
		return true
	}
	return p.next.Enabled(ctx, param)
}
func (p *ingressLogProcessor) OnEmit(ctx context.Context, record *sdklog.Record) error {
	r := ingressReceiptFromContext(ctx)
	if r == nil {
		return p.next.OnEmit(ctx, record)
	}
	if r.owner != p.owner {
		r.poison(errIngressPoison)
		return errIngressPoison
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != Building {
		r.phase = Poisoned
		r.err = errIngressPoison
		r.notifyLocked()
		return errIngressPoison
	}
	if r.logsExcluded {
		return p.next.OnEmit(ctx, record)
	}
	charge := recordCharge(record)
	if int64(len(r.records)) >= r.bounds.Logs.Records || charge > r.bounds.Logs.Bytes-r.recordBytes {
		r.phase = Poisoned
		r.err = errIngressPoison
		r.notifyLocked()
		return errIngressPoison
	}
	r.records = append(r.records, record.Clone())
	r.recordBytes += charge
	return nil
}
func (p *ingressLogProcessor) ForceFlush(ctx context.Context) error { return p.next.ForceFlush(ctx) }
func (p *ingressLogProcessor) Shutdown(ctx context.Context) error   { return p.next.Shutdown(ctx) }

// The provider owns the underlying log exporter exactly once. SDK processor
// Shutdown quiesces its ordinary queue; it must not tear down durable workers.
type serialLogExporter struct {
	sdklog.Exporter
	gate      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newSerialLogExporter(e sdklog.Exporter) *serialLogExporter {
	return &serialLogExporter{Exporter: e, gate: make(chan struct{}, 1)}
}
func (e *serialLogExporter) enter(ctx context.Context) error {
	select {
	case e.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *serialLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if err := e.enter(ctx); err != nil {
		return err
	}
	defer func() { <-e.gate }()
	return e.Exporter.Export(ctx, records)
}
func (e *serialLogExporter) ForceFlush(ctx context.Context) error {
	if err := e.enter(ctx); err != nil {
		return err
	}
	defer func() { <-e.gate }()
	return e.Exporter.ForceFlush(ctx)
}
func (*serialLogExporter) Shutdown(context.Context) error { return nil }
func (e *serialLogExporter) close(ctx context.Context) error {
	if err := e.enter(ctx); err != nil {
		return err
	}
	defer func() { <-e.gate }()
	e.closeOnce.Do(func() { e.closeErr = e.Exporter.Shutdown(ctx) })
	return e.closeErr
}

func (p *Provider) flushIngressLogs(ctx context.Context) error {
	if p.metricReader == nil {
		return nil
	}
	ctx, cancel := boundedReaderContext(ctx, p.metricReader.timeout)
	defer cancel()
	reader := p.metricReader
	reader.mu.Lock()
	var prefix []*IngressReceipt
	for r := range reader.receipts {
		phase := r.Phase()
		if phase == Ready || phase == Covered || phase == Delivered {
			prefix = append(prefix, r)
		}
	}
	reader.mu.Unlock()
	for {
		reader.mu.Lock()
		pending := false
		for _, r := range prefix {
			r.mu.Lock()
			if !r.logAck && !r.logsExcluded {
				pending = true
			}
			r.mu.Unlock()
		}
		if !pending {
			reader.mu.Unlock()
			return nil
		}
		changed := reader.changed
		reader.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (p *Provider) runIngressLogs(ctx context.Context) {
	reader := p.metricReader
	failures := 0
	for {
		reader.mu.Lock()
		var selected *IngressReceipt
		var earliest uint64
		for r := range reader.receipts {
			r.mu.Lock()
			ready := r.phase != Reserved && r.phase != Building && r.phase != Poisoned && !r.logAck && !r.logsExcluded
			r.mu.Unlock()
			if ready && (selected == nil || r.sequence < earliest) {
				selected = r
				earliest = r.sequence
			}
		}
		changed := reader.changed
		reader.mu.Unlock()
		if selected == nil {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
			continue
		}
		selected.mu.Lock()
		offset := selected.logOffset
		end := min(len(selected.records), offset+p.logBatchSize)
		chunk := selected.records[offset:end]
		selected.mu.Unlock()
		call, cancel := context.WithTimeout(ctx, p.logTimeout)
		reader.mu.Lock()
		reader.logExportStarted = time.Now()
		reader.mu.Unlock()
		exportErr := p.serialLogs.Export(call, chunk)
		cancel()
		reader.mu.Lock()
		reader.logExportStarted = time.Time{}
		reader.mu.Unlock()
		if exportErr != nil {
			// As for scheduled metrics: keep tailscale2otel.export.failures
			// counting durable-log export failures (bounded class only).
			handleExportError(exportErr)
		}
		result := classifyExportResult(exportErr, SignalLogs)
		selected.mu.Lock()
		if !result.ack && permanentExportRejection(exportErr) {
			selected.logPermanentRejections++
		} else {
			selected.logPermanentRejections = 0
		}
		dropped := !result.ack && selected.logPermanentRejections >= reader.maxPermanentRejections
		selected.mu.Unlock()
		if result.ack || dropped {
			failures = 0
			reader.mu.Lock()
			if dropped {
				reader.stats.PermanentLogBatchesDropped++
			}
			reader.logFailures = 0
			reader.mu.Unlock()
			selected.mu.Lock()
			for i := offset; i < end; i++ {
				selected.recordBytes -= recordCharge(&selected.records[i])
				selected.records[i] = sdklog.Record{}
			}
			selected.logPermanentRejections = 0
			selected.logOffset = end
			if end == len(selected.records) {
				selected.records = nil
				selected.logAck = true
				if selected.eligibleLocked() {
					selected.phase = Delivered
				}
				selected.notifyLocked()
			}
			selected.mu.Unlock()
			reader.mu.Lock()
			reader.signalLocked()
			reader.mu.Unlock()
			if dropped && reader.onPermanentDrop != nil {
				reader.onPermanentDrop(p.emitter, SignalLogs)
			}
			continue
		}
		failures++
		reader.mu.Lock()
		reader.logFailures = failures
		reader.signalLocked()
		reader.mu.Unlock()
		timer := time.NewTimer(deliveryRetryDelay(failures))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
