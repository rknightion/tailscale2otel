package app

import (
	"context"
	"errors"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/ingresswal"
)

func (a *App) buildIngressWAL(routes []ingressWALRoute) error {
	if !a.cfg.IngressWAL.Enabled {
		coordinator, err := newIngressWALCoordinator(nil, nil)
		if err != nil {
			return err
		}
		a.ingressWAL = coordinator
		return nil
	}

	store, err := ingresswal.New(ingresswal.Options{
		Directory:  a.cfg.IngressWAL.Directory,
		MaxBytes:   a.cfg.IngressWAL.MaxBytes,
		MaxEntries: a.cfg.IngressWAL.MaxEntries,
	})
	if err != nil {
		return err
	}
	coordinator, err := newIngressWALCoordinator(store, routes)
	if err != nil {
		_ = store.Close()
		return err
	}
	a.ingressWAL = coordinator
	return nil
}

// ingressWALPromotionBudget bounds how long RECEIVER startup waits for the
// ingress WAL's startup drain. Nothing else waits: collectors, the heartbeat,
// the self-obs reporters and the admin/Prometheus listeners all come up
// immediately (see runActive), so a slow drain is observable while it happens
// rather than after it.
//
// A normal drain is sub-second — the live worker keeps the backlog near empty —
// so this budget is invisible in the common case. It exists for the case that
// is not common: a pod that was leader, was restarted with entries still
// pending, sat as a standby (a standby never replays), and then inherited the
// whole backlog on promotion. Draining that took 28 minutes on the lab, during
// which the leader-labeled pod was NotReady and every inbound record was
// refused, because the receivers had not opened yet.
//
// Waiting is still the right default: replaying accepted ingress ahead of new
// ingress is why the drain runs first at all. Waiting FOREVER is not, because
// the WAL is append-safe during replay — that is the steady state, where the
// live worker replays while receivers append — so opening late costs ordering
// on one promotion and buys back an unbounded ingress outage.
const ingressWALPromotionBudget = 30 * time.Second

// startIngressWAL runs the ingress WAL's startup drain and then its live worker,
// off the active lifecycle's critical path. It returns the worker's exit channel
// (which runActive's teardown drains) and an open channel that is closed once
// the stream and webhook receivers may start.
//
// open is closed when the startup drain completes OR when
// ingressWALPromotionBudget elapses, whichever comes first. It is deliberately
// NEVER closed when the drain has already failed permanently (a corrupt,
// incompatible or unowned WAL, or an unknown persisted route): a WAL that cannot
// be read must not be handed new ingress to store. A permanent failure that only
// surfaces AFTER the budget released the receivers is caught by the append path
// instead — see the budget branch below.
//
// ctx is the active lifecycle's context and bounds the drain; walCtx outlives it
// so the live worker can be stopped separately, after the receivers are joined.
func (a *App) startIngressWAL(ctx, walCtx context.Context) (chan error, <-chan struct{}) {
	done := make(chan error, 1)
	replayed := make(chan struct{})
	failed := make(chan struct{})
	open := make(chan struct{})

	budget := a.walPromotionBudget
	if budget <= 0 {
		budget = ingressWALPromotionBudget
	}
	started := time.Now()
	health := a.ingressWAL.Health().WAL
	a.walStartupPending.Store(true)
	// Logged unconditionally, including the zero-backlog case. The incident this
	// exists for produced not one line between acquiring the Lease and the end of
	// the drain, so "how long has it been in here" was unanswerable from logs.
	a.logger.Info("ingress WAL startup drain beginning",
		"component", appcatalog.ComponentIngressWAL,
		"pending_entries", health.PendingEntries,
		"pending_bytes", health.PendingBytes,
		"receiver_budget", budget)

	go func() {
		if err := a.ingressWAL.ReplayStartup(ctx); err != nil {
			if ctx.Err() == nil {
				a.logger.Error("ingress WAL startup drain unavailable; receivers will not open",
					"component", appcatalog.ComponentIngressWAL, "error", err)
				a.componentError(appcatalog.ComponentIngressWAL)
			}
			// A context error is the operator stopping us mid-drain, not a fault.
			// Anything else is fatal to this run and is surfaced by runActive's
			// return value, as it was when this replay ran inline.
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				a.walStartupFatal = err
			}
			close(failed)
			// The send below happens-after the write above and happens-before
			// runActive's receive, so walStartupFatal needs no lock of its own.
			done <- err
			return
		}
		a.logger.Info("ingress WAL startup drain complete",
			"component", appcatalog.ComponentIngressWAL,
			"duration", time.Since(started))
		close(replayed)
		done <- a.ingressWAL.Run(walCtx)
	}()

	go func() {
		timer := time.NewTimer(budget)
		defer timer.Stop()
		select {
		case <-replayed:
		case <-failed:
			a.walStartupPending.Store(false)
			return
		case <-ctx.Done():
			a.walStartupPending.Store(false)
			return
		case <-timer.C:
			// The drain is still running, so a permanent failure cannot be ruled
			// out yet — it can only be ruled out by the drain finishing. Binding
			// the listeners anyway is safe because the durability guarantee is
			// enforced at APPEND time, not by this gate: once the WAL is failed,
			// every appender returns errIngressWALAppend and each receiver
			// refuses the request with reason wal_unavailable, while /readyz
			// reports "ingress_wal: failed" and Kubernetes pulls the pod out of
			// the leader-labeled Services. A bound listener that refuses
			// everything accepts no data it cannot store.
			// TestStartIngressWAL_PermanentFailureAfterBudgetFailsClosed pins that.
			health := a.ingressWAL.Health()
			if health.State == ingressWALStateFailed {
				// Already known to be broken: no reason to bind at all.
				a.walStartupPending.Store(false)
				return
			}
			a.logger.Warn("ingress WAL startup drain exceeded its receiver budget; opening receivers while the backlog keeps draining",
				"component", appcatalog.ComponentIngressWAL,
				"receiver_budget", budget,
				"pending_entries", health.WAL.PendingEntries,
				"pending_bytes", health.WAL.PendingBytes)
		}
		a.walStartupPending.Store(false)
		close(open)
	}()

	return done, open
}

// waitIngressWALOpen blocks until receivers may open, reporting false when the
// lifecycle was canceled or the WAL failed permanently first — in which case the
// caller must not start its listener. A nil open channel means no WAL is
// configured, so there is nothing to wait for.
func waitIngressWALOpen(ctx context.Context, open <-chan struct{}) bool {
	if open == nil {
		return ctx.Err() == nil
	}
	select {
	case <-open:
		return true
	case <-ctx.Done():
		return false
	}
}
