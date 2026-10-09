package telemetry

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var errMetricDelivery = errors.New("metric delivery not acknowledged")

// FlushReason names the ONLY permitted out-of-schedule metric collections:
// process shutdown and loss of leadership. There is deliberately no reason for
// ingress, WAL replay, log flushing, startup, promotion or export recovery; those
// can never collect outside the configured interval. Each provider makes at most
// one such terminal collection, and only when retention can hold it.
type FlushReason uint8

const (
	FlushShutdown FlushReason = iota + 1
	FlushLeadershipLost
)

type collectionID struct {
	epoch, slot uint64
	reason      FlushReason
}
type CollectionStage interface {
	EmitOnce(Emitter) error
	CommitCollected()
}
type BeforeCollect func() CollectionStage
type CollectionObservation struct {
	SlotAt              time.Time
	Reason              FlushReason
	Required, Discarded bool
	Data                *metricdata.ResourceMetrics
	Err                 error
}
type CollectionStats struct {
	ScheduledAttempts, CollectFailures, SnapshotsCollected, BestEffortDiscardedFull, BestEffortEvicted, RequiredSnapshotsAcknowledged uint64
	RetainedCredits, ReservedCredits                                                                                                  int
	LastSlotAt                                                                                                                        time.Time
	TerminalSkippedFull, TerminalAttempts                                                                                             uint64
}

func (s *CollectionStats) add(other CollectionStats) {
	s.ScheduledAttempts += other.ScheduledAttempts
	s.CollectFailures += other.CollectFailures
	s.SnapshotsCollected += other.SnapshotsCollected
	s.BestEffortDiscardedFull += other.BestEffortDiscardedFull
	s.BestEffortEvicted += other.BestEffortEvicted
	s.RequiredSnapshotsAcknowledged += other.RequiredSnapshotsAcknowledged
	s.RetainedCredits += other.RetainedCredits
	s.ReservedCredits += other.ReservedCredits
	s.TerminalSkippedFull += other.TerminalSkippedFull
	s.TerminalAttempts += other.TerminalAttempts
	if other.LastSlotAt.After(s.LastSlotAt) {
		s.LastSlotAt = other.LastSlotAt
	}
}

type ingressGroup struct {
	members []*IngressReceipt
	closed  bool
}
type producerStage struct {
	fence          uint64
	members        []*IngressReceipt
	programs       []*metricProgram
	ordinary       CollectionStage
	group          *ingressGroup
	materialized   bool
	ordinaryCredit bool
}
type metricCollection struct {
	id                 collectionID
	members            []*IngressReceipt
	parts              []metricPart
	required, inFlight bool
	// failedAttempts/lastErr let a ForceFlush barrier report a failed delivery
	// attempt instead of silently waiting out its deadline. Retries continue.
	failedAttempts uint64
	lastErr        error
}

var providerEpoch atomic.Uint64

type scheduledMetricReader struct {
	*sdkmetric.ManualReader
	exporter                    sdkmetric.Exporter
	interval, timeout           time.Duration
	batchSize                   int
	epoch                       uint64
	mu                          sync.Mutex
	collectMu                   sync.Mutex
	emitter                     Emitter
	before                      BeforeCollect
	observer                    func(CollectionObservation)
	collections                 []*metricCollection
	groups                      []*ingressGroup
	openGroup                   *ingressGroup
	stage                       *producerStage
	receipts                    map[*IngressReceipt]bool
	workSequence, slot          uint64
	stats                       CollectionStats
	changed                     chan struct{}
	terminal, terminalAttempted bool
	failed                      bool
	// collectFailing and logFailures report CURRENT delivery trouble (the last
	// normal Collect failed; consecutive durable-log export failures) so the
	// ingress coordinator can tell real retrying from healthy waiting.
	collectFailing bool
	logFailures    int
	// Attempt start times are protected by mu and cleared as soon as Export
	// returns, including when an exporter ignores its context deadline.
	metricExportStarted, logExportStarted time.Time
	clockCancel, workerCancel             context.CancelFunc
	clockDone, workerDone                 chan struct{}
	startOnce, stopOnce                   sync.Once
	shutdownOnce                          sync.Once
	shutdownErr                           error
}

func newScheduledMetricReader(exp sdkmetric.Exporter, opts Options) (*scheduledMetricReader, error) {
	interval := opts.MetricInterval
	if interval <= 0 {
		if opts.Protocol == "stdout" {
			interval = opts.Stdout.MetricInterval
			if interval <= 0 {
				interval = stdoutDefaultMetricInterval
			}
		} else {
			interval = defaultMetricInterval
		}
	}
	timeout := 30 * time.Second
	if ms, err := strconv.ParseInt(os.Getenv("OTEL_METRIC_EXPORT_TIMEOUT"), 10, 64); err == nil && ms > 0 && ms <= int64((1<<63-1)/time.Millisecond) {
		timeout = time.Duration(ms) * time.Millisecond
	}
	options := []sdkmetric.ManualReaderOption{sdkmetric.WithTemporalitySelector(exp.Temporality), sdkmetric.WithAggregationSelector(exp.Aggregation)}
	for _, producer := range opts.MetricProducers {
		options = append(options, sdkmetric.WithProducer(producer))
	}
	return &scheduledMetricReader{ManualReader: sdkmetric.NewManualReader(options...), exporter: exp, interval: interval, timeout: timeout, batchSize: opts.MetricExportBatchSize, epoch: providerEpoch.Add(1), receipts: make(map[*IngressReceipt]bool), changed: make(chan struct{}), clockDone: make(chan struct{}), workerDone: make(chan struct{})}, nil
}
func (r *scheduledMetricReader) signalLocked() { close(r.changed); r.changed = make(chan struct{}) }
func (r *scheduledMetricReader) Start() {
	r.startOnce.Do(func() {
		clock, cancel := context.WithCancel(context.Background())
		r.clockCancel = cancel
		worker, cancel := context.WithCancel(context.Background())
		r.workerCancel = cancel
		// Epoch is established after SDK registration, with no initial collection.
		ticker := time.NewTicker(r.interval)
		go func() {
			defer close(r.clockDone)
			defer ticker.Stop()
			for {
				select {
				case <-clock.Done():
					return
				case <-ticker.C:
					r.mu.Lock()
					r.slot++
					id := collectionID{epoch: r.epoch, slot: r.slot}
					r.mu.Unlock()
					_ = r.collectSlot(clock, id)
				}
			}
		}()
		go func() { defer close(r.workerDone); r.exportCollections(worker) }()
	})
}
func (r *scheduledMetricReader) creditsLocked() int {
	n := len(r.collections) + len(r.groups)
	if r.stage != nil && r.stage.ordinaryCredit {
		n++
	}
	return n
}
func (r *scheduledMetricReader) makeCreditLocked() bool {
	if r.creditsLocked() < 2 {
		return true
	}
	for i, c := range r.collections {
		if !c.required && !c.inFlight {
			r.collections = append(r.collections[:i], r.collections[i+1:]...)
			r.stats.BestEffortEvicted++
			r.signalLocked()
			return true
		}
	}
	return false
}
func (r *scheduledMetricReader) removeGroupLocked(group *ingressGroup) {
	for i, g := range r.groups {
		if g == group {
			r.groups = append(r.groups[:i], r.groups[i+1:]...)
			break
		}
	}
	if r.openGroup == group {
		r.openGroup = nil
	}
}
func groupReady(g *ingressGroup) bool {
	if !g.closed || len(g.members) == 0 {
		return false
	}
	for _, m := range g.members {
		if m.Phase() != Ready {
			return false
		}
	}
	return true
}
func (r *scheduledMetricReader) collectSlot(ctx context.Context, id collectionID) error {
	r.collectMu.Lock()
	defer r.collectMu.Unlock()
	r.mu.Lock()
	if id.reason == 0 {
		r.stats.ScheduledAttempts++
		r.stats.LastSlotAt = time.Now()
	} else {
		r.stats.TerminalAttempts++
	}
	if r.stage == nil {
		stage := &producerStage{}
		if r.openGroup != nil {
			r.openGroup.closed = true
			r.openGroup = nil
		}
		for _, g := range r.groups {
			if groupReady(g) {
				stage.group = g
				stage.members = append([]*IngressReceipt(nil), g.members...)
				for _, m := range stage.members {
					stage.programs = append(stage.programs, m.program)
					stage.fence = m.sequence
				}
				break
			}
		}
		if r.before != nil {
			stage.ordinary = r.before()
		}
		if stage.ordinary != nil || stage.group != nil {
			r.stage = stage
		}
	}
	stage := r.stage
	emitter := r.emitter
	observe := r.observer
	r.mu.Unlock()
	if stage != nil && !stage.materialized {
		var materializeErr error
		if stage.ordinary != nil {
			materializeErr = stage.ordinary.EmitOnce(emitter)
		}
		if materializeErr == nil {
			for _, program := range stage.programs {
				if materializeErr = program.EmitOnce(emitter); materializeErr != nil {
					break
				}
			}
		}
		if materializeErr != nil {
			for _, member := range stage.members {
				member.poison(materializeErr)
			}
			r.mu.Lock()
			r.failed = true
			// The poisoned group keeps its credit and originals until close.
			// Still Collect this original slot; never cover a partial program.
			r.stage = nil
			r.signalLocked()
			r.mu.Unlock()
			stage = nil
		} else {
			stage.materialized = true
		}
	}
	var data metricdata.ResourceMetrics
	call, cancel := context.WithTimeout(ctx, r.timeout)
	err := r.Collect(call, &data)
	cancel()
	if err != nil {
		return r.collectFailure(stage, err, observe, id)
	}
	collection := &metricCollection{id: id, required: stage != nil && stage.group != nil}
	r.mu.Lock()
	r.stats.SnapshotsCollected++
	r.collectFailing = false
	// Decide retention BEFORE cloning: a saturated best-effort slot owns only
	// the one transient SDK result, not an additional cloned transient snapshot.
	retain := collection.required || (stage != nil && stage.ordinaryCredit) || r.makeCreditLocked()
	if retain {
		parts, freezeErr := freezeMetricParts(&data, r.batchSize)
		if freezeErr != nil {
			r.failed = true
			r.mu.Unlock()
			for _, member := range stageMembers(stage) {
				member.poison(freezeErr)
			}
			return r.collectFailure(stage, freezeErr, observe, id)
		}
		collection.parts = parts
		if collection.required {
			collection.members = stage.members
			for _, m := range stage.members {
				m.mu.Lock()
				if m.phase == Poisoned {
					// A poisoned member never gains coverage or an ACK.
					m.mu.Unlock()
					continue
				}
				m.firstCoverID = id
				m.phase = Covered
				m.program = nil
				m.notifyLocked()
				m.mu.Unlock()
			}
			r.removeGroupLocked(stage.group)
		}
		if stage != nil {
			stage.ordinaryCredit = false
		}
		r.collections = append(r.collections, collection)
	} else {
		r.stats.BestEffortDiscardedFull++
	}
	r.stage = nil
	r.signalLocked()
	r.mu.Unlock()
	if stage != nil && stage.ordinary != nil {
		stage.ordinary.CommitCollected()
	}
	if observe != nil {
		observe(CollectionObservation{SlotAt: time.Now(), Reason: id.reason, Required: collection.required, Discarded: !retain, Data: &data})
	}
	return nil
}
func stageMembers(stage *producerStage) []*IngressReceipt {
	if stage == nil {
		return nil
	}
	return stage.members
}
func (r *scheduledMetricReader) collectFailure(stage *producerStage, err error, observe func(CollectionObservation), id collectionID) error {
	r.mu.Lock()
	r.stats.CollectFailures++
	r.collectFailing = true
	if stage != nil && stage.group == nil && !stage.ordinaryCredit {
		if r.makeCreditLocked() {
			stage.ordinaryCredit = true
		} else {
			r.stage = nil
			r.stats.BestEffortDiscardedFull++
		}
	}
	r.signalLocked()
	r.mu.Unlock()
	if observe != nil {
		observe(CollectionObservation{SlotAt: time.Now(), Reason: id.reason, Err: err})
	}
	return err
}
func deliveryRetryDelay(failures int) time.Duration {
	delay := 100 * time.Millisecond
	for i := 1; i < failures && delay < 5*time.Second; i++ {
		delay = min(delay*2, 5*time.Second)
	}
	return delay
}
func (r *scheduledMetricReader) exportCollections(ctx context.Context) {
	failures := 0
	for {
		r.mu.Lock()
		if len(r.collections) == 0 {
			changed := r.changed
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
			continue
		}
		collection := r.collections[0]
		collection.inFlight = true
		r.mu.Unlock()
		ack := true
		var exportErr error
		for i := range collection.parts {
			part := &collection.parts[i]
			if part.acknowledged {
				continue
			}
			call, cancel := context.WithTimeout(ctx, r.timeout)
			r.mu.Lock()
			r.metricExportStarted = time.Now()
			r.mu.Unlock()
			raw := r.exporter.Export(call, &part.data)
			cancel()
			r.mu.Lock()
			r.metricExportStarted = time.Time{}
			r.mu.Unlock()
			if raw != nil {
				// The pinned PeriodicReader reported every non-nil export error
				// here; tailscale2otel.export.failures and its alert depend on it.
				// The installed handler records only a bounded class and signal.
				handleExportError(raw)
			}
			result := classifyExportResult(raw, SignalMetrics)
			if !result.ack {
				ack = false
				exportErr = result.err
				if exportErr == nil {
					exportErr = errMetricDelivery
				}
				break
			}
			part.acknowledged = true
		}
		r.mu.Lock()
		collection.inFlight = false
		if ack {
			for _, m := range collection.members {
				m.mu.Lock()
				if m.phase != Poisoned {
					m.metricAck = true
					if m.eligibleLocked() {
						m.phase = Delivered
					}
					m.notifyLocked()
				}
				m.mu.Unlock()
			}
			r.collections = r.collections[1:]
			if collection.required {
				r.stats.RequiredSnapshotsAcknowledged++
			}
			failures = 0
			r.signalLocked()
			r.mu.Unlock()
			continue
		}
		failures++
		collection.failedAttempts++
		collection.lastErr = exportErr
		r.signalLocked()
		r.mu.Unlock()
		timer := time.NewTimer(deliveryRetryDelay(failures))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func boundedReaderContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

// ForceFlush is a finite-prefix barrier: it never Collects or resets the clock.
// It returns once every already retained collection is delivered, or reports the
// failure of a delivery attempt that completed after the barrier began (the
// exporter worker keeps retrying that unchanged snapshot), or the deadline.
func (r *scheduledMetricReader) ForceFlush(ctx context.Context) error {
	ctx, cancel := boundedReaderContext(ctx, r.timeout)
	defer cancel()
	type mark struct {
		collection *metricCollection
		failed     uint64
	}
	r.mu.Lock()
	prefix := make([]mark, 0, len(r.collections))
	for _, c := range r.collections {
		prefix = append(prefix, mark{collection: c, failed: c.failedAttempts})
	}
	r.mu.Unlock()
	for {
		r.mu.Lock()
		pending := false
		var failure error
		for _, wanted := range prefix {
			for _, c := range r.collections {
				if c == wanted.collection {
					pending = true
					if c.failedAttempts > wanted.failed && failure == nil {
						failure = c.lastErr
					}
				}
			}
		}
		changed := r.changed
		r.mu.Unlock()
		if !pending {
			return r.exporter.ForceFlush(ctx)
		}
		if failure != nil {
			return failure
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
func (r *scheduledMetricReader) stopClock(ctx context.Context) error {
	r.stopOnce.Do(func() {
		if r.clockCancel != nil {
			r.clockCancel()
		}
	})
	select {
	case <-r.clockDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *scheduledMetricReader) flushLifecycle(ctx context.Context, reason FlushReason) error {
	if reason != FlushShutdown && reason != FlushLeadershipLost {
		return ErrIngressTerminal
	}
	ctx, cancel := boundedReaderContext(ctx, r.timeout)
	defer cancel()
	r.mu.Lock()
	r.terminal = true
	r.signalLocked()
	r.mu.Unlock()
	if err := r.stopClock(ctx); err != nil {
		return err
	}
	// Join entered applications before touching their programs/capture storage.
	for {
		r.mu.Lock()
		building := false
		for m := range r.receipts {
			if m.Phase() == Building {
				building = true
			}
		}
		changed := r.changed
		r.mu.Unlock()
		if !building {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
	r.mu.Lock()
	if r.terminalAttempted {
		r.mu.Unlock()
		return r.ForceFlush(ctx)
	}
	r.terminalAttempted = true
	if r.openGroup != nil {
		r.openGroup.closed = true
		r.openGroup = nil
	}
	ready := false
	for _, group := range r.groups {
		ready = ready || groupReady(group)
	}
	allowed := (r.stage != nil && (r.stage.group != nil || r.stage.ordinaryCredit)) || ready || r.makeCreditLocked()
	if !allowed {
		r.stats.TerminalSkippedFull++
		r.mu.Unlock()
		return r.ForceFlush(ctx)
	}
	r.mu.Unlock()
	err := r.collectSlot(ctx, collectionID{epoch: r.epoch, slot: 1, reason: reason})
	return errors.Join(err, r.ForceFlush(ctx))
}
func (r *scheduledMetricReader) Shutdown(ctx context.Context) error {
	r.shutdownOnce.Do(func() {
		if err := r.flushLifecycle(ctx, FlushShutdown); err != nil {
			r.shutdownErr = err
		}
		if r.workerCancel != nil {
			r.workerCancel()
		}
		select {
		case <-r.workerDone:
		case <-ctx.Done():
			r.shutdownErr = errors.Join(r.shutdownErr, ctx.Err())
			return
		}
		r.shutdownErr = errors.Join(r.shutdownErr, r.ManualReader.Shutdown(ctx), r.exporter.Shutdown(ctx))
	})
	return r.shutdownErr
}
func (p *Provider) SetBeforeCollect(before BeforeCollect) error {
	r := p.metricReader
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal || r.before != nil {
		return ErrIngressTerminal
	}
	r.before = before
	return nil
}
func (p *Provider) SetCollectionObserver(observe func(CollectionObservation)) error {
	r := p.metricReader
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal || r.observer != nil {
		return ErrIngressTerminal
	}
	r.observer = observe
	return nil
}
func (p *Provider) CollectionStats() CollectionStats {
	r := p.metricReader
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stats
	s.RetainedCredits = len(r.collections)
	s.ReservedCredits = len(r.groups)
	if r.stage != nil && r.stage.ordinaryCredit {
		s.ReservedCredits++
	}
	return s
}
func (p *Provider) FlushLifecycle(ctx context.Context, reason FlushReason) error {
	return p.metricReader.flushLifecycle(ctx, reason)
}
func (s *ProviderSet) FlushLifecycle(ctx context.Context, reason FlushReason) error {
	fns := []func(context.Context) error{}
	if s.process != nil {
		fns = append(fns, func(ctx context.Context) error { return s.process.FlushLifecycle(ctx, reason) })
	}
	for _, p := range s.tailnet {
		fns = append(fns, func(ctx context.Context) error { return p.FlushLifecycle(ctx, reason) })
	}
	return shutdownAll(ctx, fns...)
}

// IngressDeliveryRetrying reports whether this provider currently has delivery
// trouble that work waiting on it depends on: a retained collection whose
// export has failed, a failed normal Collect not yet followed by a success, or
// consecutive durable-log export failures, or an in-flight metrics/durable-log
// export older than the resolved reader timeout. Healthy work waiting for its
// next normal slot is NOT retrying. This is the ingress WAL delivery health
// signal, not a readiness gate for providers with ingress WAL disabled.
func (p *Provider) IngressDeliveryRetrying() bool {
	if p == nil || p.metricReader == nil {
		return false
	}
	r := p.metricReader
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.collectFailing || r.logFailures > 0 {
		return true
	}
	now := time.Now()
	for _, started := range []time.Time{r.metricExportStarted, r.logExportStarted} {
		if !started.IsZero() && now.Sub(started) > r.timeout {
			return true
		}
	}
	for _, c := range r.collections {
		if c.failedAttempts > 0 {
			return true
		}
	}
	return false
}

// exportErrorMu serializes this package's background calls into the global
// OpenTelemetry error handler. The handler is process-global and may be any
// user function; several providers' export workers would otherwise invoke it
// concurrently.
var exportErrorMu sync.Mutex

// handleExportError hands a failed export to otel.Handle, which is how
// tailscale2otel.export.failures counts (InstallExportErrorHandler records only
// a bounded error class and the signal, never backend text).
func handleExportError(err error) {
	exportErrorMu.Lock()
	defer exportErrorMu.Unlock()
	otel.Handle(err)
}
