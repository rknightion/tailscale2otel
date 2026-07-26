package app

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/ingresswal"
)

type coordinatorWAL struct {
	mu                 sync.Mutex
	pending            []ingresswal.Envelope
	appendErr          error
	commitErrs         []error
	replayErr          error
	replayErrs         []error
	beforeHandlerErrAt int
	beforeHandlerErr   error
	appendCalls        []ingresswal.Envelope
	closeCalls         int
	closeErr           error
}

func (w *coordinatorWAL) Append(_ context.Context, envelope ingresswal.Envelope) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.appendCalls = append(w.appendCalls, cloneEnvelope(envelope))
	if w.appendErr != nil {
		return w.appendErr
	}
	w.pending = append(w.pending, cloneEnvelope(envelope))
	return nil
}

func (w *coordinatorWAL) Commit(_ context.Context, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.commitErrs) > 0 {
		err := w.commitErrs[0]
		w.commitErrs = w.commitErrs[1:]
		if err != nil {
			return err
		}
	}
	for i := range w.pending {
		if w.pending[i].ID == id {
			w.pending = append(w.pending[:i], w.pending[i+1:]...)
			break
		}
	}
	return nil
}

func (w *coordinatorWAL) Replay(
	ctx context.Context,
	handler ingresswal.Handler,
	observer ingresswal.CommitObserver,
) error {
	w.mu.Lock()
	snapshot := make([]ingresswal.Envelope, len(w.pending))
	for i := range w.pending {
		snapshot[i] = cloneEnvelope(w.pending[i])
	}
	replayErr := w.replayErr
	if len(w.replayErrs) > 0 {
		replayErr = w.replayErrs[0]
		w.replayErrs = w.replayErrs[1:]
	}
	w.mu.Unlock()
	if replayErr != nil {
		return replayErr
	}
	for i, envelope := range snapshot {
		w.mu.Lock()
		var beforeHandlerErr error
		if w.beforeHandlerErr != nil && i == w.beforeHandlerErrAt {
			beforeHandlerErr = w.beforeHandlerErr
			w.beforeHandlerErr = nil
		}
		w.mu.Unlock()
		if beforeHandlerErr != nil {
			return beforeHandlerErr
		}
		if err := handler(ctx, envelope); err != nil {
			return err
		}
		if err := w.Commit(ctx, envelope.ID); err != nil {
			return err
		}
		if observer != nil {
			observer(envelope.ID)
		}
	}
	return nil
}

func (w *coordinatorWAL) Health() ingresswal.Health {
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
	defer w.mu.Unlock()
	w.closeCalls++
	return w.closeErr
}

func cloneEnvelope(envelope ingresswal.Envelope) ingresswal.Envelope {
	envelope.Body = bytes.Clone(envelope.Body)
	return envelope
}

func testIngressRoute(tailnet, source, signal string) ingressWALRoute {
	route := ingressWALRoute{
		tailnet: tailnet,
		source:  source,
		signal:  signal,
		apply: func(context.Context, []byte, time.Time) (bool, error) {
			return false, nil
		},
		flush: func(context.Context) error { return nil },
	}
	if source == ingressWALSourceStream && signal == ingressWALSignalHEC {
		route.drain = func() {}
	}
	return route
}

func TestIngressWALCoordinator_ConfiguredDashRouteIsExact(t *testing.T) {
	wal := &coordinatorWAL{}
	route := testIngressRoute("-", ingressWALSourceStream, ingressWALSignalHEC)
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
	route := testIngressRoute("-", ingressWALSourceStream, ingressWALSignalHEC)
	route.apply = func(context.Context, []byte, time.Time) (bool, error) {
		effects++
		return false, nil
	}
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
		testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
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
		testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
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

func TestIngressWALCoordinator_ReplayAppliesDrainsThenFlushes(t *testing.T) {
	envelope := coordinatorEnvelope(
		t, "-", ingressWALSourceStream, ingressWALSignalHEC, []byte(`{"event":"flow"}`),
	)
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	var order []string
	route := testIngressRoute("-", ingressWALSourceStream, ingressWALSignalHEC)
	route.apply = func(_ context.Context, body []byte, accepted time.Time) (bool, error) {
		if !bytes.Equal(body, envelope.Body) || !accepted.Equal(envelope.Accepted) {
			t.Errorf("apply body/time = %q/%v, want exact persisted values", body, accepted)
		}
		order = append(order, "apply")
		return true, nil
	}
	route.drain = func() { order = append(order, "drain") }
	route.flush = func(context.Context) error {
		order = append(order, "flush")
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.Replay(context.Background()); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if got, want := order, []string{"apply", "drain", "flush"}; !equalStrings(got, want) {
		t.Errorf("effect order = %v, want %v", got, want)
	}
	if got := wal.Health().PendingEntries; got != 0 {
		t.Errorf("pending entries = %d, want 0", got)
	}
	if !coordinator.Ready() {
		t.Errorf("coordinator state = %q, want ready", coordinator.Health().State)
	}
}

func TestIngressWALCoordinator_WebhookReplayDoesNotDrain(t *testing.T) {
	envelope := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
	)
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	var order []string
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	route.apply = func(context.Context, []byte, time.Time) (bool, error) {
		order = append(order, "apply")
		// A webhook route never drains even if a faulty adapter reports flow
		// effects; the closed source/signal route owns that decision.
		return true, nil
	}
	route.drain = func() { order = append(order, "drain") }
	route.flush = func(context.Context) error {
		order = append(order, "flush")
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.Replay(context.Background()); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if got, want := order, []string{"apply", "flush"}; !equalStrings(got, want) {
		t.Errorf("effect order = %v, want %v", got, want)
	}
}

func TestIngressWALCoordinator_FlushRetryDoesNotReapply(t *testing.T) {
	envelope := coordinatorEnvelope(
		t, "-", ingressWALSourceStream, ingressWALSignalHEC, []byte(`{"event":"flow"}`),
	)
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	applyCalls, drainCalls, flushCalls := 0, 0, 0
	route := testIngressRoute("-", ingressWALSourceStream, ingressWALSignalHEC)
	route.apply = func(context.Context, []byte, time.Time) (bool, error) {
		applyCalls++
		return true, nil
	}
	route.drain = func() { drainCalls++ }
	route.flush = func(context.Context) error {
		flushCalls++
		if flushCalls == 1 {
			return errors.New("backend included secret free text")
		}
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.Replay(context.Background()); !errors.Is(err, errIngressWALFlush) {
		t.Fatalf("first Replay error = %v, want bounded flush error", err)
	} else if bytes.Contains([]byte(err.Error()), []byte("secret free text")) {
		t.Fatalf("flush error exposes backend free text: %q", err)
	}
	if err := coordinator.Replay(context.Background()); err != nil {
		t.Fatalf("second Replay: %v", err)
	}

	if applyCalls != 1 {
		t.Errorf("apply calls = %d, want 1", applyCalls)
	}
	if drainCalls != 2 {
		t.Errorf("drain calls = %d, want 2 (safe repeat before each flush attempt)", drainCalls)
	}
	if flushCalls != 2 {
		t.Errorf("flush calls = %d, want 2", flushCalls)
	}
}

func TestIngressWALCoordinator_BoundsEachFlushAttempt(t *testing.T) {
	envelope := coordinatorEnvelope(
		t,
		"example.com",
		ingressWALSourceWebhook,
		ingressWALSignalWebhook,
		[]byte(`[]`),
	)
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	route := testIngressRoute(
		"example.com",
		ingressWALSourceWebhook,
		ingressWALSignalWebhook,
	)
	route.flush = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	coordinator.flushTimeout = 10 * time.Millisecond

	start := time.Now()
	err = coordinator.Replay(context.Background())
	if !errors.Is(err, errIngressWALFlush) {
		t.Fatalf("Replay error = %v, want bounded retryable flush failure", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("bounded flush attempt took %v", elapsed)
	}
	if got := wal.Health().PendingEntries; got != 1 {
		t.Fatalf("pending entries = %d, want retryable entry retained", got)
	}
	if got := coordinator.Health().State; got != ingressWALStateRetrying {
		t.Fatalf("state = %q, want retrying", got)
	}
}

func TestIngressWALCoordinator_CommitRetryDoesNotReapplyOrReflush(t *testing.T) {
	envelope := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
	)
	wal := &coordinatorWAL{
		pending:    []ingresswal.Envelope{envelope},
		commitErrs: []error{errors.New("commit path and free text"), nil},
	}
	applyCalls, flushCalls := 0, 0
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	route.apply = func(context.Context, []byte, time.Time) (bool, error) {
		applyCalls++
		return false, nil
	}
	route.flush = func(context.Context) error {
		flushCalls++
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.Replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
		t.Fatalf("first Replay error = %v, want bounded replay error", err)
	} else if bytes.Contains([]byte(err.Error()), []byte("free text")) {
		t.Fatalf("commit error exposes backend free text: %q", err)
	}
	if err := coordinator.Replay(context.Background()); err != nil {
		t.Fatalf("second Replay: %v", err)
	}

	if applyCalls != 1 {
		t.Errorf("apply calls = %d, want 1", applyCalls)
	}
	if flushCalls != 1 {
		t.Errorf("flush calls = %d, want 1", flushCalls)
	}
}

func TestIngressWALCoordinator_UnknownPersistedRouteFailsClosed(t *testing.T) {
	envelope := coordinatorEnvelope(t, "-", "unknown", "unknown", []byte(`sensitive`))
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	effects := 0
	route := testIngressRoute("-", ingressWALSourceStream, ingressWALSignalHEC)
	route.apply = func(context.Context, []byte, time.Time) (bool, error) {
		effects++
		return true, nil
	}
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
	envelope := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
	)
	wal := &coordinatorWAL{
		pending:    []ingresswal.Envelope{envelope},
		commitErrs: []error{errors.New("once"), nil},
	}
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.Replay(context.Background()); err == nil {
		t.Fatal("first Replay unexpectedly succeeded")
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
}

func TestIngressWALCoordinator_ReAdmittedCommittedIDIsAppliedAgainAfterInterveningReplayError(t *testing.T) {
	bodyA := []byte(`{"record":"A"}`)
	bodyB := []byte(`{"record":"B"}`)
	envelopeA := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, bodyA,
	)
	envelopeB := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, bodyB,
	)
	wal := &coordinatorWAL{
		pending:            []ingresswal.Envelope{envelopeA, envelopeB},
		beforeHandlerErrAt: 1,
		beforeHandlerErr:   errors.New("fail before B handler"),
	}
	applies := map[string]int{}
	flushes := 0
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	route.apply = func(_ context.Context, body []byte, _ time.Time) (bool, error) {
		applies[string(body)]++
		return false, nil
	}
	route.flush = func(context.Context) error {
		flushes++
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.Replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
		t.Fatalf("first Replay error = %v, want bounded replay error", err)
	}
	if got := applies[string(bodyA)]; got != 1 {
		t.Fatalf("first A apply calls = %d, want 1", got)
	}
	if got := applies[string(bodyB)]; got != 0 {
		t.Fatalf("B apply calls before injected error = %d, want 0", got)
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

	if err := coordinator.Replay(context.Background()); err != nil {
		t.Fatalf("second Replay: %v", err)
	}
	if got := applies[string(bodyA)]; got != 2 {
		t.Errorf("A apply calls after deterministic re-admission = %d, want 2", got)
	}
	if got := applies[string(bodyB)]; got != 1 {
		t.Errorf("B apply calls = %d, want 1", got)
	}
	if flushes != 3 {
		t.Errorf("flush calls = %d, want 3 (first A, B, second A)", flushes)
	}
}

func TestIngressWALCoordinator_CommitFailureRetainsProgressUntilRetryCommitObserved(t *testing.T) {
	envelopeA := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`{"record":"A"}`),
	)
	envelopeB := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`{"record":"B"}`),
	)
	wal := &coordinatorWAL{
		pending:    []ingresswal.Envelope{envelopeA, envelopeB},
		commitErrs: []error{errors.New("commit failed"), nil},
	}
	applyCalls, flushCalls := 0, 0
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	route.apply = func(context.Context, []byte, time.Time) (bool, error) {
		applyCalls++
		return false, nil
	}
	route.flush = func(context.Context) error {
		flushCalls++
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}

	if err := coordinator.Replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
		t.Fatalf("first Replay error = %v, want bounded replay error", err)
	}
	if got := coordinatorProgressLen(coordinator); got != 1 {
		t.Fatalf("progress after commit failure = %d, want retained flushed phase", got)
	}

	wal.mu.Lock()
	wal.beforeHandlerErrAt = 1
	wal.beforeHandlerErr = errors.New("fail after retry commit before B handler")
	wal.mu.Unlock()
	if err := coordinator.Replay(context.Background()); !errors.Is(err, errIngressWALReplay) {
		t.Fatalf("second Replay error = %v, want bounded later replay error", err)
	}
	if got := coordinatorProgressLen(coordinator); got != 0 {
		t.Errorf("progress after observed retry commit = %d, want 0", got)
	}
	if applyCalls != 1 {
		t.Errorf("A apply calls across commit retry = %d, want 1", applyCalls)
	}
	if flushCalls != 1 {
		t.Errorf("A flush calls across commit retry = %d, want 1", flushCalls)
	}
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

	route := testIngressRoute("-", ingressWALSourceStream, ingressWALSignalHEC)
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
		"open pair": {testIngressRoute("-", ingressWALSourceStream, "flow")},
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
		testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
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
		testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
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
	transient := errors.New("transient backend detail")
	first := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[{"first":true}]`),
	)
	second := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[{"second":true}]`),
	)
	wal := &coordinatorWAL{
		pending:    []ingresswal.Envelope{first, second},
		replayErrs: []error{transient, transient, nil},
	}
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	flushCalls := 0
	route.flush = func(context.Context) error {
		flushCalls++
		if flushCalls == 2 {
			return transient
		}
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	var delays []time.Duration
	coordinator.wait = func(_ context.Context, _ <-chan struct{}, delay time.Duration) ingressWALWaitResult {
		delays = append(delays, delay)
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
	if got := wal.Health().PendingEntries; got != 1 {
		t.Errorf("pending entries after partial progress = %d, want 1", got)
	}
	if got := coordinatorProgressLen(coordinator); got != 1 {
		t.Errorf("progress entries after partial replay = %d, want bounded to 1 pending entry", got)
	}
}

func TestIngressWALCoordinator_RunCancellationIsClean(t *testing.T) {
	coordinator, err := newIngressWALCoordinator(&coordinatorWAL{}, []ingressWALRoute{
		testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
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
	envelope := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
	)
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	flushStarted := make(chan struct{})
	releaseFlush := make(chan struct{})
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	route.flush = func(context.Context) error {
		close(flushStarted)
		<-releaseFlush
		return nil
	}
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{route})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- coordinator.Drain(context.Background()) }()
	<-flushStarted
	if got := coordinator.Health().State; got != ingressWALStateDraining {
		t.Errorf("state during Drain = %q, want draining", got)
	}
	if coordinator.Ready() {
		t.Error("draining coordinator reported ready")
	}
	close(releaseFlush)
	if err := <-done; err != nil {
		t.Fatalf("Drain: %v", err)
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
}

func TestIngressWALCoordinator_CanceledReplayHasNoEffects(t *testing.T) {
	envelope := coordinatorEnvelope(
		t, "example.com", ingressWALSourceWebhook, ingressWALSignalWebhook, []byte(`[]`),
	)
	wal := &coordinatorWAL{pending: []ingresswal.Envelope{envelope}}
	effects := 0
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
	route.apply = func(context.Context, []byte, time.Time) (bool, error) {
		effects++
		return false, nil
	}
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
	route := testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook)
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
		testIngressRoute("example.com", ingressWALSourceWebhook, ingressWALSignalWebhook),
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
