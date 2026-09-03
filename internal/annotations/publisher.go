package annotations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry/pii"
)

// DropReason is the bounded reason an annotation did not reach Grafana. It is a
// metric label, so the set is closed. The FailureCode values are also valid
// reasons — a Grafana rejection is a drop with the server's verdict as its
// reason — which is why DropReason and FailureCode share a string space.
type DropReason string

const (
	// DropDuplicate is the STEADY STATE, not a fault: a snapshot source
	// re-emits a still-true condition every tick and every already-published
	// identity lands here. Counted rather than logged for exactly that reason.
	DropDuplicate DropReason = "duplicate"
	// DropQueueFull means the publisher's hand-off buffer was full. The
	// alternative — blocking — would make a Tailscale poll wait on Grafana.
	DropQueueFull DropReason = "queue_full"
	// DropRateLimited means this process's own max_per_minute ceiling was hit.
	// The annotation never reached the wire.
	DropRateLimited DropReason = "local_rate_limited"
	// DropBackoff means the writer was parked after Grafana rate-limited it.
	// Distinct from local_rate_limited: that is a ceiling we chose, this is one
	// the server imposed.
	DropBackoff DropReason = "server_backoff"
)

// DropReasons returns every non-failure drop reason in a stable order.
func DropReasons() []DropReason {
	return []DropReason{DropDuplicate, DropQueueFull, DropRateLimited, DropBackoff}
}

// Config is the resolved grafana_annotations block, as the writer needs it.
type Config struct {
	Client         ClientConfig
	Categories     map[Category]CategoryConfig
	RollupInterval time.Duration
	// DedupeRetention is how long a published annotation's key is remembered.
	DedupeRetention time.Duration
	// StateFile is where the dedupe set persists. Empty runs in memory, which
	// degrades to "may republish once per restart" rather than to silence.
	StateFile string
	// QueueSize bounds the hand-off buffer.
	QueueSize int
	// MaxPerMinute is the local write ceiling.
	MaxPerMinute int
	// ExtraTags are appended to every annotation, for deployments overlaying
	// these on an existing tag scheme.
	ExtraTags []string
}

// Options are Start's inputs.
type Options struct {
	Config Config
	// Emitter receives the self-observability metrics. It must be the BASE
	// process emitter, never a teed one — self-obs counters emitted through the
	// tee would be offered straight back to the recorder.
	Emitter telemetry.Emitter
	// PIIFilter is the operator's pii_filter, applied to annotation text. See
	// Recorder.redactor for why this is load-bearing rather than defensive.
	PIIFilter pii.Categories
	Logger    *slog.Logger
	// Version and Tailnets are carried by the lifecycle marker.
	Version  string
	Tailnets []string
	// StartedAt is the process start time, so the lifecycle marker lands where
	// the deploy happened rather than where the HTTP call completed.
	StartedAt time.Time
	Now       func() time.Time
}

// Annotator is the whole feature: the record tee, the dedupe set, the rollup
// buckets, the rate limiter and the one HTTP client.
//
// A nil *Annotator is fully functional as a no-op — Decorate passes the emitter
// through and Close does nothing — which is what keeps the unset-credential
// path free of branches at every call site.
type Annotator struct {
	client   *Client
	emitter  telemetry.Emitter
	logger   *slog.Logger
	recorder *Recorder
	dedupe   *dedupeStore
	limiter  *rateLimiter
	now      func() time.Time
	tags     []string

	// drainBudget bounds Close's shutdown drain in TOTAL — one write's budget,
	// not one per queued annotation. A Grafana that stalls must cost shutdown
	// the same as a single slow write, and annotations lost at shutdown are
	// exactly the "degraded" this feature is built to tolerate.
	drainBudget time.Duration

	queue chan Annotation
	// degraded is written by the worker only and read by the gauge report.
	degraded atomic.Bool

	// backoffUntil is the instant before which the worker writes NOTHING.
	// Bounding writes per cycle alone would only convert an unbounded storm
	// into a permanent one, which still holds a shared org-wide limit down; the
	// answer to "you are sending too many requests" has to include sending
	// fewer. backoffStreak counts CONSECUTIVE rate-limited writes and is reset
	// by the first success, so a lingering streak cannot make an isolated 429
	// months later wait minutes for no reason.
	backoffUntil  time.Time
	backoffStreak int

	workerDone chan struct{}
	closeOnce  sync.Once
	cancel     context.CancelFunc
}

// Start builds the annotation writer, PROVES the token can write, and launches
// the publisher goroutine.
//
// It returns (nil, nil) when the feature is not configured — no client, no
// goroutine, no log line. That is the default deployment and it must be silent.
//
// When it IS configured, a write failure here is a HARD error the caller must
// treat as fatal. Discovering a token that cannot write at the first real event
// means the annotations an operator is relying on for incident context simply
// are not there when they look, and nothing in the process would have said so.
func Start(ctx context.Context, opts Options) (*Annotator, error) {
	if opts.Config.Client.URL == "" {
		return nil, nil
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	client, err := NewClient(opts.Config.Client)
	if err != nil {
		return nil, err
	}
	client.now = now

	a := &Annotator{
		client:      client,
		emitter:     opts.Emitter,
		logger:      logger,
		dedupe:      newDedupeStore(openStateStore(opts.Config.StateFile, logger), opts.Config.DedupeRetention),
		limiter:     newRateLimiter(opts.Config.MaxPerMinute, now),
		now:         now,
		tags:        opts.Config.ExtraTags,
		drainBudget: opts.Config.Client.Timeout,
		queue:       make(chan Annotation, max(1, opts.Config.QueueSize)),
		workerDone:  make(chan struct{}),
	}
	a.recorder = NewRecorder(RecorderOptions{
		Config: RecorderConfig{
			Categories:     opts.Config.Categories,
			RollupInterval: opts.Config.RollupInterval,
		},
		Sink:     a,
		Dedupe:   a.dedupe,
		Redactor: pii.New(opts.PIIFilter),
		Now:      now,
	})

	if err := a.preflight(ctx, opts); err != nil {
		return nil, err
	}

	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.cancel = cancel
	go a.run(workerCtx)
	return a, nil
}

// openStateStore opens the dedupe set's backing file, degrading to memory-only
// on any failure. A state file that cannot be opened must not stop the process:
// the cost is republishing once per restart, and refusing to start over it
// would take collection down for a dashboard nicety. (The token being wrong is
// a different matter — see preflight — because that one silently produces
// nothing at all.)
func openStateStore(path string, logger *slog.Logger) collector.CheckpointStore {
	if path == "" {
		return nil
	}
	store, err := collector.NewFileStore(path)
	if err != nil {
		logger.Warn("annotation dedupe state could not be opened; a restart may republish "+
			"recent annotations once", "path", path, "error", err)
		return nil
	}
	return store
}

// preflight writes the lifecycle annotation SYNCHRONOUSLY, before any collector
// runs, and reports a failure as an error.
//
// The probe and the marker are deliberately the same write. A synthetic
// "can I write?" probe would either leave a junk annotation in the operator's
// Grafana or need a delete — which would require annotations:delete, widening
// exactly the token scope this feature's narrowness rests on.
func (a *Annotator) preflight(ctx context.Context, opts Options) error {
	startedAt := opts.StartedAt
	if startedAt.IsZero() {
		startedAt = a.now()
	}
	text := fmt.Sprintf("tailscale2otel %s started", opts.Version)
	if len(opts.Tailnets) > 0 {
		text += fmt.Sprintf(" (tailnets: %d)", len(opts.Tailnets))
	}
	marker := Annotation{
		Category:  CategoryLifecycle,
		RuleID:    RuleStartup,
		Time:      startedAt,
		Text:      text,
		DedupeKey: DedupeKey("", RuleStartup, startedAt.UTC().Format(time.RFC3339Nano)),
	}
	if err := a.client.Publish(ctx, marker, a.tags); err != nil {
		return fmt.Errorf("grafana annotation writer refusing to start: %w\n"+
			"The service-account token must hold the Grafana action `annotations:create` on "+
			"scope `annotations:type:organization`, and tailscale2otel needs no other Grafana "+
			"permission. A custom role granting exactly that pair is the documented minimum; "+
			"the fixed role `fixed:annotations:writer` also works but additionally grants "+
			"annotations:write and annotations:delete, which tailscale2otel never uses. "+
			"Unset grafana_annotations.url to run without annotations", err)
	}
	a.recordPublished(marker)
	return nil
}

// Decorate wraps a tailnet's emitter so the records it emits are offered to the
// curated rule set. A nil *Annotator returns base unchanged, so the composition
// root needs no conditional.
func (a *Annotator) Decorate(tailnet string, base telemetry.Emitter) telemetry.Emitter {
	if a == nil {
		return base
	}
	return Tee(base, a.recorder, tailnet)
}

// Publish enqueues an annotation. It NEVER blocks: a full queue drops and
// counts, because the caller is a collector goroutine mid-poll.
func (a *Annotator) Publish(annotation Annotation) {
	select {
	case a.queue <- annotation:
	default:
		a.recordDropped(DropQueueFull)
	}
}

// Duplicate counts one occurrence suppressed by the dedupe set.
func (a *Annotator) Duplicate(string) { a.recordDropped(DropDuplicate) }

// run is the single publisher goroutine. Everything that touches Grafana
// happens here, so the rate limiter, the backoff state and the HTTP client need
// no locking and no collector goroutine is ever inside an HTTP call.
func (a *Annotator) run(ctx context.Context) {
	defer close(a.workerDone)
	// The tick drives three things: closing rollup buckets, persisting the
	// dedupe set, and refreshing the degraded gauge. One second is well below
	// any rollup interval and costs nothing when there is nothing to do.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case annotation := <-a.queue:
			a.write(ctx, annotation)
		case <-ticker.C:
			a.recorder.Flush(a.now())
			a.reportDegraded()
			if err := a.dedupe.Persist(a.now()); err != nil {
				a.logger.Warn("annotation dedupe state not persisted", "error", err)
			}
		}
	}
}

// write performs one annotation write, applying the server-imposed backoff and
// then the local ceiling.
func (a *Annotator) write(ctx context.Context, annotation Annotation) {
	if now := a.now(); now.Before(a.backoffUntil) {
		a.recordDropped(DropBackoff)
		return
	}
	if !a.limiter.allow() {
		a.recordDropped(DropRateLimited)
		return
	}

	err := a.client.Publish(ctx, annotation, a.tags)
	if err == nil {
		a.degraded.Store(false)
		// A write landing proves the limit has cleared, so the ladder starts
		// from the bottom again on the next one.
		a.backoffStreak = 0
		a.backoffUntil = time.Time{}
		a.recordPublished(annotation)
		return
	}

	a.degraded.Store(true)
	code := FailureTransport
	var pubErr *PublishError
	if errors.As(err, &pubErr) {
		code = pubErr.Code
	}
	a.recordDropped(DropReason(code))
	if code == FailureRateLimited {
		a.armBackoff(pubErr.RetryAfter)
	}
	// Logged at Warn, never fatal: a Grafana outage is not a collection
	// failure. The bounded code is on the metric; the detail is here, because
	// Grafana's own body is the most useful line for a permission failure.
	a.logger.Warn("grafana annotation not published",
		"category", annotation.Category, "rule", annotation.RuleID, "error", err)
}

// armBackoff parks the writer after a rate-limited write.
func (a *Annotator) armBackoff(retryAfter time.Duration) {
	a.backoffStreak++
	delay, source := backoffDelay(retryAfter, a.backoffStreak)
	a.backoffUntil = a.now().Add(delay)
	// One WARN per rate-limited write, at WARN rather than INFO because a
	// shared org-wide limit being hit is other people's problem too.
	a.logger.Warn("annotation writes rate limited by grafana; backing off",
		"delay", delay.String(), "source", source, "streak", a.backoffStreak,
		"until", a.backoffUntil.UTC().Format(time.RFC3339))
}

func (a *Annotator) recordPublished(annotation Annotation) {
	if a.emitter == nil {
		return
	}
	a.emitter.Counter(DocPublished.Name, DocPublished.Unit, DocPublished.Description, 1,
		telemetry.Attrs{"category": string(annotation.Category)})
}

func (a *Annotator) recordDropped(reason DropReason) {
	if a.emitter == nil {
		return
	}
	a.emitter.Counter(DocDropped.Name, DocDropped.Unit, DocDropped.Description, 1,
		telemetry.Attrs{"reason": string(reason)})
}

// reportDegraded republishes the degraded gauge. GaugeSnapshot rather than
// Gauge because an observable gauge reports exactly what the callback observes
// each cycle, so the value cannot linger at a stale 1 under Grafana Cloud's
// forced cumulative temporality.
func (a *Annotator) reportDegraded() {
	if a.emitter == nil {
		return
	}
	value := 0.0
	if a.degraded.Load() {
		value = 1
	}
	a.emitter.GaugeSnapshot(DocDegraded.Name, DocDegraded.Unit, DocDegraded.Description,
		[]telemetry.GaugePoint{{Value: value}})
}

// Close stops the publisher, flushes the open rollup buckets, drains what is
// already queued, and persists the dedupe set.
//
// CLOSE OWNS THE DRAIN'S DEADLINE, not the caller. Deferring to the caller's
// context would bound nothing: a stalled Grafana would cost one request timeout
// for EVERY queued annotation — up to queue_size × timeout, easily an hour of a
// shutdown that is supposed to be prompt. The whole drain gets one write's
// budget, and a caller context that expires sooner still wins.
func (a *Annotator) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	var err error
	a.closeOnce.Do(func() {
		a.cancel()
		<-a.workerDone
		a.recorder.FlushAll()
		drainCtx, cancelDrain := context.WithTimeout(ctx, a.drainBudget)
		defer cancelDrain()
		for {
			select {
			case annotation := <-a.queue:
				a.write(drainCtx, annotation)
				continue
			default:
			}
			break
		}
		err = a.dedupe.Persist(a.now())
	})
	return err
}
