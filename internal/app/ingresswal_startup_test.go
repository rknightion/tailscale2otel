package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
)

// blockingWAL is a WAL whose Replay parks until release is closed (or the
// context ends), so the startup drain can be held open for as long as a test
// needs without a wall-clock sleep. replayErr, when set, is returned instead of
// parking.
type blockingWAL struct {
	release   chan struct{}
	replayErr error
	mu        sync.Mutex
	released  bool
}

func (w *blockingWAL) Append(context.Context, ingresswal.Envelope) error { return nil }
func (w *blockingWAL) Commit(context.Context, string) error              { return nil }
func (w *blockingWAL) Close() error                                      { return nil }

func (w *blockingWAL) Replay(context.Context, ingresswal.Handler, ingresswal.CommitObserver) error {
	return nil
}

// PrepareWindow is where the windowed coordinator's drain parks: preparation
// is the first WAL operation of every replay pass.
func (w *blockingWAL) PrepareWindow(ctx context.Context, _ ingresswal.WindowLimits, _ []ingresswal.Generation, _ ingresswal.GenerationObserver) ([]ingresswal.PreparedEntry, error) {
	if w.replayErr != nil {
		return nil, w.replayErr
	}
	select {
	case <-w.release:
		w.mu.Lock()
		w.released = true
		w.mu.Unlock()
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (w *blockingWAL) CommitPrepared(context.Context, ingresswal.Generation) (ingresswal.PreparedOutcome, error) {
	return ingresswal.PreparedPending, nil
}
func (w *blockingWAL) ReleasePrepared(ingresswal.Generation) error { return nil }

func (w *blockingWAL) Health() ingresswal.Health {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.released {
		// Released: the parked backlog has drained.
		return ingresswal.Health{MaxEntries: 100, MaxBytes: 1 << 20}
	}
	return ingresswal.Health{PendingEntries: 7, PendingBytes: 70, MaxEntries: 100, MaxBytes: 1 << 20}
}

func startupTestApp(t *testing.T, wal ingresswal.WAL) *App {
	t.Helper()
	cfg := config.Default()
	cfg.SelfObservability.Enabled = false
	coordinator, err := newIngressWALCoordinator(wal, []ingressWALRoute{
		testIngressRoute(t, "example.com", ingressWALSourceStream, ingressWALSignalHEC),
	})
	if err != nil {
		t.Fatalf("newIngressWALCoordinator: %v", err)
	}
	return &App{
		cfg:                cfg,
		logger:             slog.New(slog.DiscardHandler),
		readyState:         newComponentHealth(),
		ingressWAL:         coordinator,
		walPromotionBudget: 20 * time.Millisecond,
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, within time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(within):
		t.Fatalf("%s was not released within %s", what, within)
	}
}

// TestStartIngressWAL_BudgetOpensReceiversMidDrain is the regression test for
// the lab incident: a leader promoted onto an inherited backlog held its
// receivers closed for the entire drain (28 minutes), so the leader-labeled
// Services had no ready endpoint and every inbound record was refused. The
// drain must now release the receivers once the budget elapses and keep
// draining behind them.
func TestStartIngressWAL_BudgetOpensReceiversMidDrain(t *testing.T) {
	wal := &blockingWAL{release: make(chan struct{})}
	a := startupTestApp(t, wal)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	walCtx, walCancel := context.WithCancel(context.Background())
	defer walCancel()

	done, open := a.startIngressWAL(ctx, walCtx)

	// The drain is still parked inside Replay, so this can only be the budget.
	waitClosed(t, open, 2*time.Second, "receiver gate")
	if a.walStartupPending.Load() {
		t.Fatal("walStartupPending still set after the receivers were released; /readyz would stay 503")
	}
	if !waitIngressWALOpen(ctx, open) {
		t.Fatal("waitIngressWALOpen reported false on a released gate")
	}

	// Let the parked drain finish, then stop the live worker it hands off to. A
	// nil here therefore means the drain COMPLETED and Run exited cleanly — the
	// permanent-failure path fills done with the error instead and never reaches
	// Run at all.
	close(wal.release)
	walCancel()
	if err := <-done; err != nil {
		t.Fatalf("worker exit = %v, want nil after the drain completed and the worker was stopped", err)
	}
	if a.walStartupFatal != nil {
		t.Fatalf("walStartupFatal = %v, want nil for a drain that completed", a.walStartupFatal)
	}
}

// TestStartIngressWAL_PermanentFailureNeverOpensReceivers pins the half of the
// old inline behavior that must survive: a WAL that cannot be read is never
// handed new ingress to store, and the error still reaches runActive's return
// so the process exits non-zero.
func TestStartIngressWAL_PermanentFailureNeverOpensReceivers(t *testing.T) {
	wal := &blockingWAL{release: make(chan struct{}), replayErr: ingresswal.ErrCorrupt}
	a := startupTestApp(t, wal)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	walCtx, walCancel := context.WithCancel(context.Background())
	defer walCancel()

	done, open := a.startIngressWAL(ctx, walCtx)

	err := <-done
	if !errors.Is(err, ingresswal.ErrCorrupt) {
		t.Fatalf("done error = %v, want it to carry %v", err, ingresswal.ErrCorrupt)
	}
	if !errors.Is(a.walStartupFatal, ingresswal.ErrCorrupt) {
		t.Fatalf("walStartupFatal = %v, want the permanent failure recorded for runActive's return", a.walStartupFatal)
	}
	// Well past the 20ms budget: the gate must NOT open on a permanent failure,
	// which is exactly what the budget timer would otherwise do.
	select {
	case <-open:
		t.Fatal("receiver gate opened after a permanent WAL failure")
	case <-time.After(200 * time.Millisecond):
	}
	if a.walStartupPending.Load() {
		t.Fatal("walStartupPending still set after a permanent failure; the failure state is the reason to report, not the drain")
	}
	if state := a.ingressWAL.Health().State; state != ingressWALStateFailed {
		t.Fatalf("WAL state = %q, want %q so /readyz reports the failure", state, ingressWALStateFailed)
	}
}

// TestStartIngressWAL_CancellationDoesNotOpenReceiversOrReportFatal covers a
// stop mid-drain: the operator's cancellation is not a fault, so it must not
// turn into a non-zero exit, and nothing should bind on the way out.
func TestStartIngressWAL_CancellationDoesNotOpenReceiversOrReportFatal(t *testing.T) {
	wal := &blockingWAL{release: make(chan struct{})}
	a := startupTestApp(t, wal)
	a.walPromotionBudget = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	walCtx, walCancel := context.WithCancel(context.Background())
	defer walCancel()

	done, open := a.startIngressWAL(ctx, walCtx)
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("done error = %v, want context.Canceled", err)
	}
	if a.walStartupFatal != nil {
		t.Fatalf("walStartupFatal = %v, want nil — a cancellation is a stop, not a fault", a.walStartupFatal)
	}
	if waitIngressWALOpen(ctx, open) {
		t.Fatal("waitIngressWALOpen reported true on a canceled lifecycle; a receiver would bind during shutdown")
	}
}

// TestWaitIngressWALOpenWithoutWAL keeps the no-WAL path honest: with the
// ingress WAL disabled there is no gate, and receivers must start immediately
// rather than block forever on a nil channel.
func TestWaitIngressWALOpenWithoutWAL(t *testing.T) {
	if !waitIngressWALOpen(t.Context(), nil) {
		t.Fatal("waitIngressWALOpen(nil) = false, want receivers to start with no WAL configured")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitIngressWALOpen(ctx, nil) {
		t.Fatal("waitIngressWALOpen(nil) on a canceled context = true, want false")
	}
}

// slowFailWAL parks in Replay until release, then returns a permanent error —
// the ordering the receiver budget cannot rule out, because a permanent failure
// is only knowable once the drain finishes.
type slowFailWAL struct {
	release chan struct{}
	appends int
	mu      sync.Mutex
}

func (w *slowFailWAL) Append(context.Context, ingresswal.Envelope) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.appends++
	return nil
}
func (w *slowFailWAL) Commit(context.Context, string) error { return nil }
func (w *slowFailWAL) Close() error                         { return nil }
func (w *slowFailWAL) Health() ingresswal.Health {
	return ingresswal.Health{MaxEntries: 100, MaxBytes: 1 << 20}
}

func (w *slowFailWAL) Replay(context.Context, ingresswal.Handler, ingresswal.CommitObserver) error {
	return nil
}

func (w *slowFailWAL) PrepareWindow(ctx context.Context, _ ingresswal.WindowLimits, _ []ingresswal.Generation, _ ingresswal.GenerationObserver) ([]ingresswal.PreparedEntry, error) {
	select {
	case <-w.release:
		return nil, ingresswal.ErrCorrupt
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (w *slowFailWAL) CommitPrepared(context.Context, ingresswal.Generation) (ingresswal.PreparedOutcome, error) {
	return ingresswal.PreparedPending, nil
}
func (w *slowFailWAL) ReleasePrepared(ingresswal.Generation) error { return nil }

// TestStartIngressWAL_PermanentFailureAfterBudgetFailsClosed covers the one
// window the receiver budget cannot close: the drain outlasts the budget (so the
// listeners bind) and only THEN turns out to be permanently broken. Binding is
// safe because the durability guarantee lives on the append path, not on the
// gate — this pins that it actually does, so the gate can stay bounded.
func TestStartIngressWAL_PermanentFailureAfterBudgetFailsClosed(t *testing.T) {
	wal := &slowFailWAL{release: make(chan struct{})}
	a := startupTestApp(t, wal)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	walCtx, walCancel := context.WithCancel(context.Background())
	defer walCancel()

	done, open := a.startIngressWAL(ctx, walCtx)
	waitClosed(t, open, 2*time.Second, "receiver gate")

	// Receivers are bound now. Break the WAL.
	close(wal.release)
	if err := <-done; !errors.Is(err, ingresswal.ErrCorrupt) {
		t.Fatalf("done error = %v, want %v", err, ingresswal.ErrCorrupt)
	}

	append := a.ingressWAL.appender("example.com", ingressWALSourceStream, ingressWALSignalHEC)
	if err := append(ctx, []byte("body"), time.Now()); !errors.Is(err, errIngressWALAppend) {
		t.Fatalf("append after a permanent failure = %v, want %v so the receiver refuses the request",
			err, errIngressWALAppend)
	}

	w := httptest.NewRecorder()
	a.readyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d, want 503 so the pod leaves the leader-labeled Services", w.Code)
	}
	if body := w.Body.String(); body != appcatalog.ComponentIngressWAL+": failed" {
		t.Fatalf("/readyz body = %q, want the WAL failure reported", body)
	}
}
