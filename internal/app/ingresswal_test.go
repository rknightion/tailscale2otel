package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/webhook"
)

// coordinatorWAL is the coordinator's WAL fixture. Store-backed fixtures
// (newCoordinatorWAL) delegate preparation, generation tokens and
// generation-checked completion to a real ingresswal.Store, because opaque
// Generation tokens can only be minted by a Store; the injected errors wrap
// those real operations. A zero-value fixture is an in-memory appender and
// Health seam only: it records appends and prepares nothing.
type coordinatorWAL struct {
	mu           sync.Mutex
	store        *ingresswal.Store
	pending      []ingresswal.Envelope // in-memory fixtures only
	appendErr    error
	commitErrs   []error
	replayErr    error   // PrepareWindow fails while set
	replayErrs   []error // queued PrepareWindow outcomes; nil passes through
	prepareLimit int     // >0 caps the entries one PrepareWindow returns
	appendCalls  []ingresswal.Envelope
	closeCalls   int
	closeErr     error
	beforeAppend func()
}

func newCoordinatorWAL(t *testing.T, envelopes ...ingresswal.Envelope) *coordinatorWAL {
	t.Helper()
	store, err := ingresswal.New(ingresswal.Options{Directory: filepath.Join(t.TempDir(), "ingress-wal"), MaxBytes: 1 << 20, MaxEntries: 100})
	if err != nil {
		t.Fatalf("ingresswal.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, envelope := range envelopes {
		if err := store.Append(context.Background(), envelope); err != nil {
			t.Fatalf("seed append: %v", err)
		}
	}
	return &coordinatorWAL{store: store}
}

func (w *coordinatorWAL) Append(ctx context.Context, envelope ingresswal.Envelope) error {
	if w.beforeAppend != nil {
		w.beforeAppend()
	}
	w.mu.Lock()
	w.appendCalls = append(w.appendCalls, cloneEnvelope(envelope))
	if w.appendErr != nil {
		w.mu.Unlock()
		return w.appendErr
	}
	store := w.store
	if store == nil {
		w.pending = append(w.pending, cloneEnvelope(envelope))
	}
	w.mu.Unlock()
	if store != nil {
		return store.Append(ctx, envelope)
	}
	return nil
}

func (w *coordinatorWAL) Commit(ctx context.Context, id string) error {
	if w.store == nil {
		return nil
	}
	return w.store.Commit(ctx, id)
}

func (w *coordinatorWAL) Replay(ctx context.Context, handler ingresswal.Handler, observer ingresswal.CommitObserver) error {
	if w.store == nil {
		return nil
	}
	return w.store.Replay(ctx, handler, observer)
}

func (w *coordinatorWAL) PrepareWindow(ctx context.Context, limits ingresswal.WindowLimits, held []ingresswal.Generation, observe ingresswal.GenerationObserver) ([]ingresswal.PreparedEntry, error) {
	w.mu.Lock()
	err := w.replayErr
	if len(w.replayErrs) > 0 {
		err = w.replayErrs[0]
		w.replayErrs = w.replayErrs[1:]
	}
	if w.prepareLimit > 0 {
		limits.MaxEntries = min(limits.MaxEntries, w.prepareLimit)
	}
	store := w.store
	w.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ctx.Err()
	}
	return store.PrepareWindow(ctx, limits, held, observe)
}

func (w *coordinatorWAL) CommitPrepared(ctx context.Context, g ingresswal.Generation) (ingresswal.PreparedOutcome, error) {
	w.mu.Lock()
	var err error
	if len(w.commitErrs) > 0 {
		err = w.commitErrs[0]
		w.commitErrs = w.commitErrs[1:]
	}
	w.mu.Unlock()
	if err != nil {
		return ingresswal.PreparedPending, err
	}
	return w.store.CommitPrepared(ctx, g)
}

func (w *coordinatorWAL) ReleasePrepared(g ingresswal.Generation) error {
	return w.store.ReleasePrepared(g)
}

func (w *coordinatorWAL) Health() ingresswal.Health {
	if w.store != nil {
		return w.store.Health()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var bytes int64
	for _, envelope := range w.pending {
		bytes += int64(len(envelope.Body))
	}
	return ingresswal.Health{
		PendingBytes:   bytes,
		PendingEntries: len(w.pending),
		MaxBytes:       1 << 20,
		MaxEntries:     100,
	}
}

func (w *coordinatorWAL) Close() error {
	w.mu.Lock()
	w.closeCalls++
	closeErr := w.closeErr
	store := w.store
	w.mu.Unlock()
	if store != nil {
		if err := store.Close(); err != nil {
			return err
		}
	}
	return closeErr
}

func cloneEnvelope(envelope ingresswal.Envelope) ingresswal.Envelope {
	envelope.Body = bytes.Clone(envelope.Body)
	return envelope
}

// testIngressInterval is the fixture providers' normal metric slot. Commits
// need a real scheduled collection, so replay-driving tests run in synctest.
const testIngressInterval = 10 * time.Second

// testIngressSink is the real stdout exporter's writer: failures and delays
// are observed by actual SDK metric and log export attempts.
type testIngressSink struct {
	mu     sync.Mutex
	fail   bool
	delay  time.Duration
	block  chan struct{} // non-nil: writes stall until it closes
	failIf func([]byte) bool
	data   bytes.Buffer
	writes int
	// attempts keeps every payload handed to the sink, accepted or failed, so
	// retries can be compared byte for byte with the original attempt.
	attempts [][]byte
}

func (s *testIngressSink) Write(b []byte) (int, error) {
	s.mu.Lock()
	delay, block := s.delay, s.block
	s.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	if block != nil {
		<-block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	s.attempts = append(s.attempts, bytes.Clone(b))
	if s.fail || (s.failIf != nil && s.failIf(b)) {
		return 0, errors.New("backend included secret free text")
	}
	return s.data.Write(b)
}

func (s *testIngressSink) setFail(fail bool) {
	s.mu.Lock()
	s.fail = fail
	s.mu.Unlock()
}

func (s *testIngressSink) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.data.Bytes())
}

func (s *testIngressSink) Writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func newTestIngressDelivery(t *testing.T, interval time.Duration) (*telemetry.Provider, *testIngressSink) {
	t.Helper()
	sink := &testIngressSink{}
	p, err := telemetry.NewProvider(context.Background(), telemetry.Options{
		Protocol: "stdout", StdoutWriter: sink, ServiceName: "synthetic-ingress", MetricInterval: interval,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})
	return p, sink
}

type testIngressApply func(ctx context.Context, body []byte, accepted time.Time, e telemetry.Emitter) error

// testIngressRouteOn builds prepared work whose callback runs apply and then
// records one counter, so every original owns a required metric first cover.
func testIngressRouteOn(tailnet, source, signal string, delivery *telemetry.Provider, apply testIngressApply) ingressWALRoute {
	return ingressWALRoute{
		tailnet:  tailnet,
		source:   source,
		signal:   signal,
		delivery: delivery,
		prepare: func(ctx context.Context, body []byte, accepted time.Time) (telemetry.IngressWork, error) {
			if err := ctx.Err(); err != nil {
				return telemetry.IngressWork{}, err
			}
			body = bytes.Clone(body)
			return telemetry.IngressWork{
				Bounds: telemetry.WorkBounds{InputBytes: int64(len(body)), MetricOps: 64, MetricBytes: 1 << 16},
				Apply: func(ctx context.Context, e telemetry.Emitter) error {
					if apply != nil {
						if err := apply(ctx, body, accepted, e); err != nil {
							return err
						}
					}
					e.Counter("synthetic.ingress.applied", "1", "synthetic applied originals", 1, nil)
					return nil
				},
			}, nil
		},
	}
}

func testIngressRoute(t *testing.T, tailnet, source, signal string) ingressWALRoute {
	t.Helper()
	delivery, _ := newTestIngressDelivery(t, testIngressInterval)
	return testIngressRouteOn(tailnet, source, signal, delivery, nil)
}

func TestIngressWALCoordinator_ConfiguredDashRouteIsExact(t *testing.T) {
	wal := &coordinatorWAL{}
	route := testIngressRoute(t, "-", ingressWALSourceStream, ingressWALSignalHEC)
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.appender("-", ingressWALSourceStream, ingressWALSignalHEC)(
		context.Background(), []byte(`{"event":"ok"}`), time.Unix(1_700_000_000, 123).UTC(),
	); err != nil {
		t.Fatalf("configured route append: %v", err)
	}

	if got := len(wal.appendCalls); got != 1 {
		t.Fatalf("WAL append calls = %d, want 1", got)
	}
}

func TestIngressWALCoordinator_MissingOrMismatchedRouteHasNoEffects(t *testing.T) {
	wal := &coordinatorWAL{}
	var effects int
	delivery, _ := newTestIngressDelivery(t, testIngressInterval)
	route := testIngressRouteOn("-", ingressWALSourceStream, ingressWALSignalHEC, delivery,
		func(context.Context, []byte, time.Time, telemetry.Emitter) error {
			effects++
			return nil
		})
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	for _, key := range [][3]string{
		{"display-name", ingressWALSourceStream, ingressWALSignalHEC},
		{"-", ingressWALSourceWebhook, ingressWALSignalWebhook},
		{"-", ingressWALSourceStream, "flow"},
	} {
		err := coordinator.appender(key[0], key[1], key[2])(
			context.Background(), []byte(`secret body`), time.Unix(1, 0),
		)
		if !errors.Is(err, errIngressWALRoute) {
			t.Errorf("mismatched route error = %v, want bounded route error", err)
		}
		if err != nil && (bytes.Contains([]byte(err.Error()), []byte(key[0])) ||
			bytes.Contains([]byte(err.Error()), []byte("secret body"))) {
			t.Errorf("route error exposes route or body: %q", err)
		}
	}

	if got := len(wal.appendCalls); got != 0 {
		t.Errorf("WAL append calls = %d, want 0", got)
	}
	if effects != 0 {
		t.Errorf("route effects = %d, want 0", effects)
	}
}

func TestIngressWALCoordinator_AppenderPersistsExactEnvelopeThenSignalsOnce(t *testing.T) {
	wal := &coordinatorWAL{}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	body := []byte{0, 1, 2, 0xff}
	accepted := time.Unix(1_700_000_000, 987_654_321).UTC()

	if err := coordinator.appender("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)(
		context.Background(), body, accepted,
	); err != nil {
		t.Fatalf("append: %v", err)
	}
	body[0] = 9

	if got := len(wal.appendCalls); got != 1 {
		t.Fatalf("WAL append calls = %d, want 1", got)
	}
	got := wal.appendCalls[0]
	wantBody := []byte{0, 1, 2, 0xff}
	wantID, err := ingresswal.NewID("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, wantBody)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if got.ID != wantID || got.Tailnet != "example.com" ||
		got.Source != ingressWALSourceWebhook || got.Signal != ingressWALSignalWebhook ||
		!got.Accepted.Equal(accepted) || !bytes.Equal(got.Body, wantBody) {
		t.Errorf("persisted envelope = %+v, want exact route/time/body with ID %q", got, wantID)
	}
	select {
	case <-coordinator.wake:
	default:
		t.Fatal("successful append did not signal replay wake")
	}
	select {
	case <-coordinator.wake:
		t.Fatal("one append produced more than one wake signal")
	default:
	}
}

func TestIngressWALCoordinator_AppendFailureDoesNotWake(t *testing.T) {
	wal := &coordinatorWAL{appendErr: ingresswal.ErrFull}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	err = coordinator.appender("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)(
		context.Background(), []byte(`[]`), time.Unix(1, 0),
	)
	if !errors.Is(err, ingresswal.ErrFull) {
		t.Fatalf("append error = %v, want bounded full error", err)
	}
	select {
	case <-coordinator.wake:
		t.Fatal("failed append signaled replay wake")
	default:
	}
	if got := coordinator.Health().State; got != ingressWALStateFull {
		t.Errorf("state = %q, want %q", got, ingressWALStateFull)
	}
}

func coordinatorEnvelope(t *testing.T, tailnet, source, signal string, body []byte) ingresswal.Envelope {
	t.Helper()
	id, err := ingresswal.NewID(tailnet, source, signal, body)
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	return ingresswal.Envelope{
		ID:       id,
		Tailnet:  tailnet,
		Source:   source,
		Signal:   signal,
		Accepted: time.Unix(1_700_000_000, 123).UTC(),
		Body:     bytes.Clone(body),
	}
}

// advanceIngressSlot lets exactly one fixture provider slot elapse and every
// bubble goroutine (clock, export worker) settle before the caller observes.
func advanceIngressSlot() {
	time.Sleep(testIngressInterval)
	synctest.Wait()
}

func appliedOriginals(t *testing.T, sink *testIngressSink) float64 {
	t.Helper()
	return cadenceRuntimeLatestTotal(cadenceRuntimeSignals(t, sink.Bytes()), "synthetic.ingress.applied")
}

// The old per-body apply -> drain -> ForceFlush -> commit order is replaced by
// apply -> scheduled first cover -> ACK -> generation-checked commit. The
// application itself performs no collection.
func TestIngressWALCoordinator_ReplayAppliesThenCommitsOnlyAfterScheduledCover(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(
			t, "-", ingressWALSourceStream, ingressWALSignalHEC, []byte(`{"event":"flow"}`),
		)
		wal := newCoordinatorWAL(t, envelope)
		delivery, sink := newTestIngressDelivery(t, testIngressInterval)
		var order []string
		route := testIngressRouteOn("-", ingressWALSourceStream, ingressWALSignalHEC, delivery,
			func(_ context.Context, body []byte, accepted time.Time, _ telemetry.Emitter) error {
				if !bytes.Equal(body, envelope.Body) || !accepted.Equal(envelope.Accepted) {
					t.Errorf("apply body/time = %q/%v, want exact persisted values", body, accepted)
				}
				order = append(order, "apply")
				return nil
			})
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}

		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want the applied original pending its scheduled cover", err)
		}
		if got := wal.Health().PendingEntries; got != 1 {
			t.Fatalf("pending entries before the scheduled cover = %d, want 1", got)
		}
		if stats := delivery.CollectionStats(); stats.ScheduledAttempts != 0 || stats.SnapshotsCollected != 0 || stats.TerminalAttempts != 0 {
			t.Fatalf("application collected out of schedule: %+v", stats)
		}
		advanceIngressSlot()
		if err := coordinator.Replay(context.Background()); err != nil {
			t.Fatalf("Replay: %v", err)
		}

		if got, want := order, []string{"apply"}; !equalStrings(got, want) {
			t.Errorf("effect order = %v, want %v", got, want)
		}
		if got := wal.Health().PendingEntries; got != 0 {
			t.Errorf("pending entries = %d, want 0", got)
		}
		if !coordinator.Ready() {
			t.Errorf("coordinator state = %q, want ready", coordinator.Health().State)
		}
		if stats := delivery.CollectionStats(); stats.ScheduledAttempts != 1 || stats.RequiredSnapshotsAcknowledged != 1 {
			t.Errorf("collection stats = %+v, want one normal slot acknowledging the first cover", stats)
		}
		if got := appliedOriginals(t, sink); got != 1 {
			t.Errorf("exported applied total = %v, want 1", got)
		}
	})
}

func TestIngressWALCoordinator_ReappliesAfterCrashBetweenApplyAndCommit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const tailnet = "example.com"
		body := []byte(`[{"timestamp":"2026-08-30T09:00:00Z","version":1,"type":"nodeCreated","tailnet":"example.com","message":"node created"}]`)
		accepted := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
		dir := filepath.Join(t.TempDir(), "ingress-wal")
		opts := ingresswal.Options{Directory: dir, MaxBytes: 1 << 20, MaxEntries: 10}
		store, err := ingresswal.New(opts)
		if err != nil {
			t.Fatalf("ingresswal.New: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		id, err := ingresswal.NewID(tailnet, ingressWALSourceWebhook, ingressWALSignalWebhook, body)
		if err != nil {
			t.Fatalf("ingresswal.NewID: %v", err)
		}
		if err := store.Append(context.Background(), ingresswal.Envelope{
			ID: id, Tailnet: tailnet, Source: ingressWALSourceWebhook, Signal: ingressWALSignalWebhook,
			Accepted: accepted, Body: body,
		}); err != nil {
			t.Fatalf("store.Append: %v", err)
		}

		applies := 0
		webhookRoute := func(delivery *telemetry.Provider) ingressWALRoute {
			receiver := webhook.New(webhook.Options{}, delivery.Emitter(), nil)
			return ingressWALRoute{
				tailnet: tailnet, source: ingressWALSourceWebhook, signal: ingressWALSignalWebhook, delivery: delivery,
				prepare: func(ctx context.Context, body []byte, accepted time.Time) (telemetry.IngressWork, error) {
					work, err := receiver.PrepareDurable(ctx, body, accepted)
					apply := work.Apply
					work.Apply = func(ctx context.Context, e telemetry.Emitter) error { applies++; return apply(ctx, e) }
					return work, err
				},
			}
		}
		// The first process applies the original, but its delivery never
		// acknowledges the covering snapshot, so the generation stays pending.
		firstDelivery, firstSink := newTestIngressDelivery(t, testIngressInterval)
		firstSink.setFail(true)
		firstCoordinator, err := newIngressWALCoordinator(store, []ingressWALRoute{webhookRoute(firstDelivery)})
		if err != nil {
			t.Fatalf("first newIngressWALCoordinator: %v", err)
		}
		if err := firstCoordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want bounded pending delivery", err)
		}
		advanceIngressSlot()
		if err := firstCoordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass after failed delivery = %v, want bounded pending delivery", err)
		}
		if got := store.Health().PendingEntries; got != 1 {
			t.Fatalf("pending entries after apply-before-commit failure = %d, want 1", got)
		}

		// A process crash discards the coordinator's in-memory progress ledger.
		// Close and reopen the real WAL so a fresh provider epoch reads the
		// durable pending envelope from the same directory.
		if err := store.Close(); err != nil {
			t.Fatalf("close first WAL: %v", err)
		}
		firstCoordinator = nil
		reopened, err := ingresswal.New(opts)
		if err != nil {
			t.Fatalf("reopen ingress WAL: %v", err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		secondDelivery, secondSink := newTestIngressDelivery(t, testIngressInterval)
		secondCoordinator, err := newIngressWALCoordinator(reopened, []ingressWALRoute{webhookRoute(secondDelivery)})
		if err != nil {
			t.Fatalf("second newIngressWALCoordinator: %v", err)
		}
		if err := secondCoordinator.Replay(context.Background()); err != nil {
			t.Fatalf("second Replay: %v", err)
		}
		if got := reopened.Health().PendingEntries; got != 0 {
			t.Fatalf("pending entries after healthy replay = %d, want 0", got)
		}
		if applies != 2 {
			t.Fatalf("applications = %d, want 2 (at-least-once reapply after the crash)", applies)
		}
		if got := len(firstSink.Bytes()); got != 0 {
			t.Fatalf("failed first delivery accepted %d bytes", got)
		}
		signals := cadenceRuntimeSignals(t, secondSink.Bytes())
		logs := 0
		for _, signal := range signals {
			if signal.EventName != "" {
				logs++
				if signal.EventName != "tailscale.webhook.nodeCreated" || !bytes.Contains(signal.Body, []byte("node created")) {
					t.Errorf("webhook log = (%q, %s), want nodeCreated/node created", signal.EventName, signal.Body)
				}
			}
		}
		if logs != 1 {
			t.Fatalf("second-process webhook log records = %d, want 1", logs)
		}
		if got := cadenceRuntimeLatestTotal(signals, webhook.MetricEvents); got != 1 {
			t.Fatalf("second-process webhook event total = %v, want 1 in its fresh cumulative epoch", got)
		}
	})
}

// Replaces WebhookReplayDoesNotDrain: no route owns a drain or flush any more,
// so the invariant is now that application performs no collection at all.
func TestIngressWALCoordinator_WebhookReplayDoesNotCollectOutOfSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
		)
		wal := newCoordinatorWAL(t, envelope)
		delivery, _ := newTestIngressDelivery(t, testIngressInterval)
		var order []string
		route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery,
			func(context.Context, []byte, time.Time, telemetry.Emitter) error {
				order = append(order, "apply")
				return nil
			})
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}
		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("pass = %v, want pending delivery", err)
		}
		if got, want := order, []string{"apply"}; !equalStrings(got, want) {
			t.Errorf("effect order = %v, want %v", got, want)
		}
		if stats := delivery.CollectionStats(); stats != (telemetry.CollectionStats{ReservedCredits: 1}) {
			t.Errorf("webhook application touched collection: %+v", stats)
		}
		advanceIngressSlot()
		if err := coordinator.Replay(context.Background()); err != nil {
			t.Fatalf("Replay: %v", err)
		}
	})
}

func TestIngressWALCoordinator_ExportRetryDoesNotReapply(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(
			t, "-", ingressWALSourceStream, ingressWALSignalHEC, []byte(`{"event":"flow"}`),
		)
		wal := newCoordinatorWAL(t, envelope)
		delivery, sink := newTestIngressDelivery(t, testIngressInterval)
		sink.setFail(true)
		applyCalls := 0
		route := testIngressRouteOn("-", ingressWALSourceStream, ingressWALSignalHEC, delivery,
			func(context.Context, []byte, time.Time, telemetry.Emitter) error {
				applyCalls++
				return nil
			})
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}

		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want pending delivery", err)
		}
		advanceIngressSlot()
		err = coordinator.replay(context.Background())
		if !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("pass after failed export = %v, want bounded pending delivery", err)
		} else if bytes.Contains([]byte(err.Error()), []byte("secret free text")) {
			t.Fatalf("delivery error exposes backend free text: %q", err)
		}
		if got := coordinator.Health().State; got != ingressWALStateRetrying {
			t.Fatalf("state = %q, want retrying", got)
		}
		sink.setFail(false)
		if err := coordinator.Replay(context.Background()); err != nil {
			t.Fatalf("Replay after recovery: %v", err)
		}

		if applyCalls != 1 {
			t.Errorf("apply calls = %d, want 1", applyCalls)
		}
		if stats := delivery.CollectionStats(); stats.ScheduledAttempts != 1 || stats.RequiredSnapshotsAcknowledged != 1 {
			t.Errorf("collection stats = %+v, want the original snapshot retried without a new collection", stats)
		}
		if got := appliedOriginals(t, sink); got != 1 {
			t.Errorf("exported applied total = %v, want 1", got)
		}
	})
}

// A cumulative collection split into many parts that together outlast the
// former 10s per-body flush budget still acknowledges and commits.
func TestIngressWALCoordinator_CommitsAfterHealthyBatchedDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`))
		wal := newCoordinatorWAL(t, envelope)
		sink := &testIngressSink{delay: time.Second}
		delivery, err := telemetry.NewProvider(context.Background(), telemetry.Options{
			Protocol: "stdout", StdoutWriter: sink, ServiceName: "synthetic-ingress",
			MetricInterval: testIngressInterval, MetricExportBatchSize: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// The deliberately slow writer ignores cancellation; let it finish
			// promptly so shutdown can join the export worker.
			sink.mu.Lock()
			sink.delay = 0
			sink.mu.Unlock()
			if err := delivery.Shutdown(ctx); err != nil {
				t.Errorf("Shutdown: %v", err)
			}
		})
		route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery,
			func(_ context.Context, _ []byte, _ time.Time, e telemetry.Emitter) error {
				for i := range 15 {
					e.Counter("synthetic.ingress.batched", "1", "synthetic", 1, telemetry.Attrs{"part": fmt.Sprint(i)})
				}
				return nil
			})
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatal(err)
		}
		if err := coordinator.Replay(context.Background()); err != nil {
			t.Fatalf("healthy batched delivery did not commit: %v (writes %d)", err, sink.Writes())
		}
		if pending := wal.Health().PendingEntries; pending != 0 {
			t.Fatalf("pending entries = %d after successful delivery, want 0", pending)
		}
		if writes := sink.Writes(); writes < 16 || coordinator.Health().State != ingressWALStateReady {
			t.Fatalf("writes = %d, state = %s; want >= 16 one-point parts and ready", writes, coordinator.Health().State)
		}
		if got := cadenceRuntimeLatestTotal(cadenceRuntimeSignals(t, sink.Bytes()), "synthetic.ingress.batched"); got != 15 {
			t.Fatalf("batched total = %v, want 15", got)
		}
	})
}

// Replaces BoundsEachFlushAttempt: the coordinator no longer makes a per-body
// flush call, so the invariant is that a replay pass never waits on a stalled
// exporter (per-attempt export bounds live in the telemetry reader).
func TestIngressWALCoordinator_ReplayPassDoesNotWaitOnStalledDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`))
		wal := newCoordinatorWAL(t, envelope)
		delivery, sink := newTestIngressDelivery(t, testIngressInterval)
		route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery, nil)
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}
		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want pending delivery", err)
		}
		stall := make(chan struct{})
		sink.mu.Lock()
		sink.block = stall
		sink.mu.Unlock()
		defer close(stall)              // release the non-cooperative writer before cleanup
		time.Sleep(testIngressInterval) // the slot's export now stalls in the writer
		start := time.Now()
		err = coordinator.replay(context.Background())
		if !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("pass = %v, want bounded retryable pending delivery", err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("replay pass waited %v on a stalled exporter", elapsed)
		}
		if got := wal.Health().PendingEntries; got != 1 {
			t.Fatalf("pending entries = %d, want retryable entry retained", got)
		}
		// An export still in flight has not failed: like work awaiting its
		// normal slot it is not a readiness failure. A failed attempt is what
		// surfaces as retrying (TestIngressWALReadiness_FailingDeliverySurfacesRetrying).
		if got := coordinator.Health().State; got != ingressWALStateReady {
			t.Fatalf("state = %q, want ready while the export is in flight", got)
		}
	})
}

func TestIngressWALCoordinator_CommitRetryDoesNotReapplyOrRecollect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
		)
		wal := newCoordinatorWAL(t, envelope)
		wal.commitErrs = []error{errors.New("commit path and free text"), nil}
		delivery, _ := newTestIngressDelivery(t, testIngressInterval)
		applyCalls := 0
		route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery,
			func(context.Context, []byte, time.Time, telemetry.Emitter) error {
				applyCalls++
				return nil
			})
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}
		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want pending delivery", err)
		}
		advanceIngressSlot()
		if err := coordinator.Replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
			t.Fatalf("Replay with failing commit = %v, want bounded replay error", err)
		} else if bytes.Contains([]byte(err.Error()), []byte("free text")) {
			t.Fatalf("commit error exposes backend free text: %q", err)
		}
		if err := coordinator.Replay(context.Background()); err != nil {
			t.Fatalf("second Replay: %v", err)
		}
		if applyCalls != 1 {
			t.Errorf("apply calls = %d, want 1", applyCalls)
		}
		if stats := delivery.CollectionStats(); stats.ScheduledAttempts != 1 || stats.RequiredSnapshotsAcknowledged != 1 {
			t.Errorf("collection stats = %+v, want commit retry without another collection", stats)
		}
	})
}

func TestIngressWALCoordinator_UnknownPersistedRouteFailsClosed(t *testing.T) {
	envelope := coordinatorEnvelope(t, "-", "unknown", "unknown", []byte(`sensitive`))
	wal := newCoordinatorWAL(t, envelope)
	effects := 0
	delivery, _ := newTestIngressDelivery(t, testIngressInterval)
	route := testIngressRouteOn("-", ingressWALSourceStream, ingressWALSignalHEC, delivery,
		func(context.Context, []byte, time.Time, telemetry.Emitter) error {
			effects++
			return nil
		})
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	err = coordinator.Replay(context.Background())
	if !errors.Is(err, errIngressWALRoute) {
		t.Fatalf("Replay error = %v, want bounded route error", err)
	}
	for _, forbidden := range []string{"unknown", envelope.ID, "sensitive"} {
		if bytes.Contains([]byte(err.Error()), []byte(forbidden)) {
			t.Errorf("route error exposes forbidden diagnostic %q: %q", forbidden, err)
		}
	}
	if effects != 0 {
		t.Errorf("route effects = %d, want 0", effects)
	}
	if got := coordinator.Health().State; got != ingressWALStateFailed {
		t.Errorf("state = %q, want failed", got)
	}
}

func TestIngressWALCoordinator_ProgressClearsAfterSuccessfulReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
		)
		wal := newCoordinatorWAL(t, envelope)
		wal.commitErrs = []error{errors.New("once"), nil}
		route := testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}

		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want pending delivery", err)
		}
		advanceIngressSlot()
		if err := coordinator.Replay(context.Background()); err == nil {
			t.Fatal("Replay with failing commit unexpectedly succeeded")
		}
		if got := coordinatorProgressLen(coordinator); got != 1 {
			t.Fatalf("progress entries after commit failure = %d, want 1", got)
		}
		if err := coordinator.Replay(context.Background()); err != nil {
			t.Fatalf("second Replay: %v", err)
		}
		if got := coordinatorProgressLen(coordinator); got != 0 {
			t.Errorf("progress entries after successful replay = %d, want 0", got)
		}
		for range 10 {
			if err := coordinator.Replay(context.Background()); err != nil {
				t.Fatalf("empty Replay: %v", err)
			}
		}
		if got := coordinatorProgressLen(coordinator); got != 0 {
			t.Errorf("progress entries leaked across empty replays = %d, want 0", got)
		}
	})
}

func TestIngressWALCoordinator_ReAdmittedCommittedIDIsAppliedAgainAfterInterveningReplayError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bodyA := []byte(`{"record":"A"}`)
		bodyB := []byte(`{"record":"B"}`)
		envelopeA := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, bodyA,
		)
		envelopeB := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, bodyB,
		)
		wal := newCoordinatorWAL(t, envelopeA, envelopeB)
		wal.prepareLimit = 1
		applies := map[string]int{}
		delivery, _ := newTestIngressDelivery(t, testIngressInterval)
		route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery,
			func(_ context.Context, body []byte, _ time.Time, _ telemetry.Emitter) error {
				applies[string(body)]++
				return nil
			})
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}

		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want A pending delivery", err)
		}
		advanceIngressSlot()
		wal.mu.Lock()
		wal.replayErrs = []error{errors.New("fail before B is prepared")}
		wal.mu.Unlock()
		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
			t.Fatalf("second pass = %v, want bounded replay error", err)
		}
		if got := applies[string(bodyA)]; got != 1 {
			t.Fatalf("first A apply calls = %d, want 1", got)
		}
		if got := applies[string(bodyB)]; got != 0 {
			t.Fatalf("B apply calls before injected error = %d, want 0", got)
		}
		if got := wal.Health().PendingEntries; got != 1 {
			t.Fatalf("pending after A commit = %d, want only B", got)
		}

		reaccepted := time.Unix(1_800_000_000, 456).UTC()
		if err := coordinator.appender(
			"example.com", ingressWALSourceWebhook, ingressWALSignalWebhook,
		)(context.Background(), bodyA, reaccepted); err != nil {
			t.Fatalf("re-admit A: %v", err)
		}
		if got := wal.appendCalls[len(wal.appendCalls)-1].ID; got != envelopeA.ID {
			t.Fatalf("re-admitted A ID = %q, want deterministic original ID %q", got, envelopeA.ID)
		}

		wal.mu.Lock()
		wal.prepareLimit = 0
		wal.mu.Unlock()
		if err := coordinator.Replay(context.Background()); err != nil {
			t.Fatalf("second Replay: %v", err)
		}
		if got := applies[string(bodyA)]; got != 2 {
			t.Errorf("A apply calls after deterministic re-admission = %d, want 2", got)
		}
		if got := applies[string(bodyB)]; got != 1 {
			t.Errorf("B apply calls = %d, want 1", got)
		}
		if got := wal.Health().PendingEntries; got != 0 {
			t.Errorf("pending entries = %d, want 0", got)
		}
	})
}

func TestIngressWALCoordinator_CommitFailureRetainsProgressUntilRetryCommitObserved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelopeA := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`{"record":"A"}`),
		)
		envelopeB := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`{"record":"B"}`),
		)
		wal := newCoordinatorWAL(t, envelopeA, envelopeB)
		wal.prepareLimit = 1
		wal.commitErrs = []error{errors.New("commit failed"), nil}
		delivery, _ := newTestIngressDelivery(t, testIngressInterval)
		applyCalls := 0
		route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery,
			func(context.Context, []byte, time.Time, telemetry.Emitter) error {
				applyCalls++
				return nil
			})
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}

		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("first pass = %v, want A pending delivery", err)
		}
		advanceIngressSlot()
		wal.mu.Lock()
		wal.replayErrs = []error{errors.New("hold B"), nil}
		wal.mu.Unlock()
		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
			t.Fatalf("second pass = %v, want bounded replay error", err)
		}
		if got := coordinatorProgressLen(coordinator); got != 1 {
			t.Fatalf("progress after commit failure = %d, want retained acknowledged phase", got)
		}
		stats := delivery.CollectionStats()

		wal.mu.Lock()
		wal.replayErrs = []error{errors.New("fail after retry commit before B is prepared")}
		wal.mu.Unlock()
		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
			t.Fatalf("third pass = %v, want bounded later replay error", err)
		}
		if got := coordinatorProgressLen(coordinator); got != 0 {
			t.Errorf("progress after observed retry commit = %d, want 0", got)
		}
		if applyCalls != 1 {
			t.Errorf("A apply calls across commit retry = %d, want 1", applyCalls)
		}
		if got := delivery.CollectionStats(); got.ScheduledAttempts != stats.ScheduledAttempts {
			t.Errorf("commit retry collected again: %+v -> %+v", stats, got)
		}
	})
}

func coordinatorProgressLen(coordinator *ingressWALCoordinator) int {
	coordinator.progressMu.Lock()
	defer coordinator.progressMu.Unlock()
	return len(coordinator.progress)
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestIngressWALCoordinator_ConstructionStateAndRouteValidation(t *testing.T) {
	disabled, err := newIngressWALCoordinator(nil, nil)
	if err != nil {
		t.Fatalf("disabled coordinator: %v", err)
	}
	if got := disabled.Health().State; got != ingressWALStateDisabled {
		t.Errorf("disabled state = %q, want disabled", got)
	}
	if disabled.Ready() {
		t.Error("disabled coordinator reported ready")
	}

	route := testIngressRoute(t, "-", ingressWALSourceStream, ingressWALSignalHEC)
	enabled, err := newIngressWALCoordinator(&coordinatorWAL{}, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("enabled coordinator: %v", err)
	}
	if got := enabled.Health().State; got != ingressWALStateReplaying {
		t.Errorf("startup state = %q, want replaying", got)
	}
	if enabled.Ready() {
		t.Error("replaying coordinator reported ready")
	}

	for name, routes := range map[string][]ingressWALRoute{
		"missing":   nil,
		"duplicate": {route, route},
		"open pair": {testIngressRoute(t, "-", ingressWALSourceStream, "flow")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newIngressWALCoordinator(&coordinatorWAL{}, routes)
			if !errors.Is(err, errIngressWALRoute) {
				t.Fatalf("construction error = %v, want bounded route error", err)
			}
		})
	}
}

func TestIngressWALCoordinator_ReadyOnlyInReadyState(t *testing.T) {
	for _, state := range []ingressWALState{
		ingressWALStateDisabled,
		ingressWALStateReplaying,
		ingressWALStateRetrying,
		ingressWALStateFull,
		ingressWALStateFailed,
		ingressWALStateDraining,
		ingressWALStateStopped,
	} {
		t.Run(string(state), func(t *testing.T) {
			coordinator := &ingressWALCoordinator{state: state}
			if coordinator.Ready() {
				t.Errorf("state %q reported ready", state)
			}
		})
	}
	coordinator := &ingressWALCoordinator{state: ingressWALStateReady}
	if !coordinator.Ready() {
		t.Error("ready state reported not ready")
	}
}

func TestIngressWALCoordinator_WakeCoalesces(t *testing.T) {
	wal := &coordinatorWAL{}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	appendBody := coordinator.appender(
		"example.com", ingressWALSourceWebhook, ingressWALSignalWebhook,
	)
	for i := range 10 {
		if err := appendBody(
			context.Background(), []byte{byte(i)}, time.Unix(int64(i+1), 0),
		); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if got := len(coordinator.wake); got != ingressWALWakeCapacity {
		t.Errorf("queued wakes = %d, want capacity %d", got, ingressWALWakeCapacity)
	}
}

func TestIngressWALRetryDelayIsBoundedExponential(t *testing.T) {
	for _, tc := range []struct {
		failures int
		want     time.Duration
	}{
		{failures: 0, want: ingressWALInitialRetry},
		{failures: 1, want: ingressWALInitialRetry},
		{failures: 2, want: 200 * time.Millisecond},
		{failures: 3, want: 400 * time.Millisecond},
		{failures: 6, want: 3200 * time.Millisecond},
		{failures: 7, want: ingressWALMaximumRetry},
		{failures: 100, want: ingressWALMaximumRetry},
	} {
		if got := ingressWALRetryDelay(tc.failures); got != tc.want {
			t.Errorf("retry delay(%d) = %v, want %v", tc.failures, got, tc.want)
		}
	}
}

func TestIngressWALCoordinator_RunRetryBackoffResetsOnWake(t *testing.T) {
	transient := errors.New("transient backend detail")
	wal := &coordinatorWAL{replayErrs: []error{transient, transient, transient}}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	var delays []time.Duration
	coordinator.wait = func(_ context.Context, _ <-chan struct{}, delay time.Duration) ingressWALWaitResult {
		delays = append(delays, delay)
		if len(delays) == 2 {
			return ingressWALWaitWake
		}
		if len(delays) == 3 {
			return ingressWALWaitCanceled
		}
		return ingressWALWaitTimer
	}

	if err := coordinator.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got, want := delays, []time.Duration{
		ingressWALInitialRetry,
		2 * ingressWALInitialRetry,
		ingressWALInitialRetry,
	}; !equalDurations(got, want) {
		t.Errorf("retry delays = %v, want %v", got, want)
	}
}

func TestIngressWALCoordinator_RunRetryBackoffResetsOnProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transient := errors.New("transient backend detail")
		first := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[{"first":true}]`),
		)
		second := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[{"second":true}]`),
		)
		wal := newCoordinatorWAL(t, first, second)
		// Pass 1 applies only the first original; the next two passes fail to
		// prepare, so the only progress is the first original's commit.
		wal.prepareLimit = 1
		wal.replayErrs = []error{nil, transient, transient}
		route := testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}
		var delays []time.Duration
		coordinator.wait = func(_ context.Context, _ <-chan struct{}, delay time.Duration) ingressWALWaitResult {
			delays = append(delays, delay)
			switch len(delays) {
			case 2:
				// Let the first original's scheduled cover be acknowledged.
				advanceIngressSlot()
			case 3:
				return ingressWALWaitCanceled
			}
			return ingressWALWaitTimer
		}

		if err := coordinator.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}

		// 1: Replay's own pending-delivery wait; 2: first failure; 3: reset to
		// the initial delay because the commit made progress (else 200ms).
		if got, want := delays, []time.Duration{
			ingressWALInitialRetry,
			ingressWALInitialRetry,
			ingressWALInitialRetry,
		}; !equalDurations(got, want) {
			t.Errorf("retry delays = %v, want %v", got, want)
		}
		if got := wal.Health().PendingEntries; got != 1 {
			t.Errorf("pending entries after partial progress = %d, want 1", got)
		}
		if got := coordinatorProgressLen(coordinator); got != 0 {
			t.Errorf("progress entries after partial replay = %d, want the retired original released", got)
		}
	})
}

func TestIngressWALCoordinator_RunCancellationIsClean(t *testing.T) {
	coordinator, err := newIngressWALCoordinator(&coordinatorWAL{}, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for !coordinator.Ready() {
		select {
		case <-deadline:
			t.Fatal("Run did not finish startup replay")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run cancellation error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestIngressWALCoordinator_DrainStateAndIdempotentClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		envelope := coordinatorEnvelope(
			t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
		)
		wal := newCoordinatorWAL(t, envelope)
		delivery, sink := newTestIngressDelivery(t, testIngressInterval)
		route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery, nil)
		coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
		if err != nil {
			t.Fatalf("newIngressWALCoordinator: %v", err)
		}
		// Applied and covered at its normal slot, but delivery is failing: the
		// terminal drain may only retry this existing receipt.
		sink.setFail(true)
		if err := coordinator.replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
			t.Fatalf("pass = %v, want pending delivery", err)
		}
		advanceIngressSlot()
		done := make(chan error, 1)
		go func() { done <- coordinator.Drain(context.Background()) }()
		synctest.Wait()
		if got := coordinator.Health().State; got != ingressWALStateDraining {
			t.Errorf("state during Drain = %q, want draining", got)
		}
		if coordinator.Ready() {
			t.Error("draining coordinator reported ready")
		}
		select {
		case err := <-done:
			t.Fatalf("Drain returned %v before the covered receipt was delivered", err)
		default:
		}
		sink.setFail(false)
		if err := <-done; err != nil {
			t.Fatalf("Drain: %v", err)
		}
		if got := wal.Health().PendingEntries; got != 0 {
			t.Errorf("pending entries after Drain = %d, want 0", got)
		}
		if got := coordinator.Health().State; got != ingressWALStateDraining {
			t.Errorf("state after Drain = %q, want draining until Close", got)
		}

		if err := coordinator.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
		if err := coordinator.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
		if wal.closeCalls != 1 {
			t.Errorf("WAL Close calls = %d, want 1", wal.closeCalls)
		}
		if got := coordinator.Health().State; got != ingressWALStateStopped {
			t.Errorf("state after Close = %q, want stopped", got)
		}
		if coordinator.Ready() {
			t.Error("stopped coordinator reported ready")
		}
	})
}

func TestIngressWALCoordinator_CanceledReplayHasNoEffects(t *testing.T) {
	envelope := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
	)
	wal := newCoordinatorWAL(t, envelope)
	effects := 0
	delivery, _ := newTestIngressDelivery(t, testIngressInterval)
	route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery,
		func(context.Context, []byte, time.Time, telemetry.Emitter) error {
			effects++
			return nil
		})
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := coordinator.Replay(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay error = %v, want context.Canceled", err)
	}
	if effects != 0 {
		t.Errorf("apply effects = %d, want 0", effects)
	}
}

func TestIngressWALCoordinator_SuccessfulAppendDoesNotMaskRetryingWorker(t *testing.T) {
	wal := &coordinatorWAL{replayErr: errors.New("transient free text")}
	route := testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	if err := coordinator.Replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
		t.Fatalf("Replay error = %v, want bounded replay error", err)
	}
	if got := coordinator.Health().State; got != ingressWALStateRetrying {
		t.Fatalf("state after replay failure = %q, want retrying", got)
	}
	wal.mu.Lock()
	wal.replayErr = nil
	wal.mu.Unlock()

	if err := coordinator.appender("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)(
		context.Background(), []byte(`[]`), time.Unix(1, 0),
	); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := coordinator.Health().State; got != ingressWALStateRetrying {
		t.Errorf("state after successful append = %q, want retrying until replay runs", got)
	}
	if coordinator.Ready() {
		t.Error("successful append masked retrying replay worker")
	}
}

func TestIngressWALCoordinator_CloseErrorIsBoundedAndStillStops(t *testing.T) {
	wal := &coordinatorWAL{closeErr: errors.New("secret filesystem path")}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	err = coordinator.Close()
	if !errors.Is(err, errIngressWALClose) {
		t.Fatalf("Close error = %v, want bounded close error", err)
	}
	if bytes.Contains([]byte(err.Error()), []byte("secret filesystem path")) {
		t.Fatalf("Close error exposes backend free text: %q", err)
	}
	if got := coordinator.Health().State; got != ingressWALStateStopped {
		t.Errorf("state after failed Close = %q, want stopped", got)
	}
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestIngressWALCoordinator_AppendFailsClosedOnPermanentFailure pins the
// durability boundary at the append path. A WAL in the failed state can never be
// replayed, so an entry appended to it would be acknowledged to the sender and
// then never applied — even when the underlying store happily accepts the write,
// as this fake does. Both the startup drain's bounded receiver budget and the
// live worker (which can reach this state long after the listeners bound) rely
// on it.
func TestIngressWALCoordinator_AppendFailsClosedOnPermanentFailure(t *testing.T) {
	wal := &coordinatorWAL{}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceStream, ingressWALSignalHEC),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	appendBody := coordinator.appender("example.com", ingressWALSourceStream, ingressWALSignalHEC)

	if err := appendBody(t.Context(), []byte("first"), time.Now()); err != nil {
		t.Fatalf("append while healthy = %v, want nil", err)
	}

	coordinator.setState(ingressWALStateFailed)
	if err := appendBody(t.Context(), []byte("second"), time.Now()); !errors.Is(err, errIngressWALAppend) {
		t.Fatalf("append while failed = %v, want %v", err, errIngressWALAppend)
	}
	wal.mu.Lock()
	calls := len(wal.appendCalls)
	wal.mu.Unlock()
	if calls != 1 {
		t.Fatalf("underlying WAL saw %d appends, want 1 — the second must never reach a store that would accept it", calls)
	}
}

// TestIngressWALCoordinator_AppendRacingPermanentFailureIsNotAcknowledged covers
// the interleaving the pre-write check alone cannot: replay marks the WAL
// permanently failed WHILE an append is in flight. The entry is already on disk
// and will never replay, so the request must still be refused rather than
// acknowledged.
func TestIngressWALCoordinator_AppendRacingPermanentFailureIsNotAcknowledged(t *testing.T) {
	wal := &coordinatorWAL{}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceStream, ingressWALSignalHEC),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	// Fail the WAL from inside Append, i.e. exactly between the pre-write check
	// and the return — the window a lock around Append would be closing.
	wal.beforeAppend = func() { coordinator.setState(ingressWALStateFailed) }

	appendBody := coordinator.appender("example.com", ingressWALSourceStream, ingressWALSignalHEC)
	if err := appendBody(t.Context(), []byte("racing"), time.Now()); !errors.Is(err, errIngressWALAppend) {
		t.Fatalf("append racing a permanent failure = %v, want %v — the sender must not be acknowledged", err, errIngressWALAppend)
	}
}

// runReadinessFixture starts the live worker over one applied webhook original
// and returns the pieces the readiness tests observe.
func runReadinessFixture(t *testing.T) (*App, *coordinatorWAL, *testIngressSink, func()) {
	t.Helper()
	envelope := coordinatorEnvelope(t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`))
	wal := newCoordinatorWAL(t, envelope)
	delivery, sink := newTestIngressDelivery(t, testIngressInterval)
	route := testIngressRouteOn("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, delivery, nil)
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{ingressWAL: coordinator, readyState: newComponentHealth()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()
	return a, wal, sink, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	}
}

// An applied original that is only waiting for its normal scheduled
// collection is healthy work: readiness must not fail and the WAL must not
// report itself as retrying for up to one metric interval after each arrival.
func TestIngressWALReadiness_HealthyAwaitingCoverStaysReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, wal, _, stop := runReadinessFixture(t)
		defer stop()
		time.Sleep(2 * time.Second) // applied, before the first slot
		synctest.Wait()
		if got := wal.Health().PendingEntries; got != 1 {
			t.Fatalf("setup: pending=%d, want one applied original awaiting its cover", got)
		}
		if reason := a.ingressWALFailure(); reason != "" {
			t.Fatalf("readiness failure %q while healthy work awaits its scheduled cover", reason)
		}
		if state := a.ingressWAL.Health().State; state == ingressWALStateRetrying || state == ingressWALStateFailed {
			t.Fatalf("WAL state %q while healthy work awaits its scheduled cover", state)
		}
		time.Sleep(testIngressInterval)
		synctest.Wait()
		if got := wal.Health().PendingEntries; got != 0 {
			t.Fatalf("pending=%d after the healthy cover, want 0", got)
		}
		if reason := a.ingressWALFailure(); reason != "" {
			t.Fatalf("readiness failure %q after the healthy cover", reason)
		}
	})
}

// Genuine delivery failure still surfaces: retrying, and not ready.
func TestIngressWALReadiness_FailingDeliverySurfacesRetrying(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, wal, sink, stop := runReadinessFixture(t)
		defer stop()
		sink.setFail(true)
		time.Sleep(testIngressInterval + time.Second) // the cover's export fails
		synctest.Wait()
		if state := a.ingressWAL.Health().State; state != ingressWALStateRetrying {
			t.Fatalf("WAL state %q with a failing exporter, want retrying", state)
		}
		if reason := a.ingressWALFailure(); reason != appcatalog.ComponentIngressWAL+": retrying" {
			t.Fatalf("readiness reason %q with a failing exporter, want ingress_wal: retrying", reason)
		}
		sink.setFail(false)
		time.Sleep(6 * time.Second) // retry backoff, inside the slot
		synctest.Wait()
		if got := wal.Health().PendingEntries; got != 0 {
			t.Fatalf("pending=%d after recovery, want 0", got)
		}
		if reason := a.ingressWALFailure(); reason != "" {
			t.Fatalf("readiness failure %q after recovery", reason)
		}
	})
}
