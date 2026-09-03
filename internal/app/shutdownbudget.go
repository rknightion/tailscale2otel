package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/stream"
	"github.com/rknightion/tailscale2otel/v5/internal/webhook"
)

// closeFlowStores closes every runtime's flow store concurrently under one
// shared deadline, following the shutdownAll fan-out shape used by telemetry.
// Store.Close has no context, though, so a backend blocked in its internal
// wg.Wait cannot be canceled: this method logs that runtime and returns when
// ctx expires while its Close goroutine may outlive the wait. The buffered
// result channel lets a late return finish without stranding another goroutine.
func (a *App) closeFlowStores(ctx context.Context) error {
	type result struct {
		index int
		err   error
	}

	results := make(chan result, len(a.runtimes))
	pending := make(map[int]struct{}, len(a.runtimes))
	for i, rt := range a.runtimes {
		if rt == nil || rt.flowStore == nil {
			continue
		}
		pending[i] = struct{}{}
		go func(index int, runtime *tailnetRuntime) {
			results <- result{index: index, err: runtime.flowStore.Close()}
		}(i, rt)
	}
	if len(pending) == 0 {
		return nil
	}

	logger := a.logger
	if logger == nil {
		logger = slog.Default()
	}
	var errs []error
	record := func(r result) {
		if _, ok := pending[r.index]; !ok {
			return
		}
		delete(pending, r.index)
		if r.err != nil {
			errs = append(errs, fmt.Errorf("tailnet %q flow store: %w", a.runtimeName(a.runtimes[r.index]), r.err))
		}
	}

	for len(pending) > 0 {
		select {
		case r := <-results:
			record(r)
		case <-ctx.Done():
			// Prefer completion results already published at the deadline, so
			// only runtimes whose Close has not reported are logged as pending.
			for {
				select {
				case r := <-results:
					record(r)
				default:
					for index := range pending {
						name := a.runtimeName(a.runtimes[index])
						logger.Error("flow store close timed out", "tailnet", name, "error", ctx.Err())
						errs = append(errs, fmt.Errorf("tailnet %q flow store close: %w", name, ctx.Err()))
					}
					return errors.Join(errs...)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// Shutdown is staged, and every stage is separately bounded. These names exist
// so the total is derived in one place rather than restated as a literal in the
// deployment assets — see shutdownbudget_test.go, which fails if Compose's
// stop_grace_period or the chart's terminationGracePeriodSeconds stops covering
// the sum (#332).
//
// The stages, in the order Run performs them once the operator's context is
// canceled:
//
//  1. schedulers stop — unbounded in principle, but every collector run takes
//     the same canceled context, so this returns promptly and is not budgeted.
//  2. receiverDrainTimeout — the stream and webhook receivers each do a
//     graceful http.Server.Shutdown so already-ACKed requests finish emitting.
//     They run in parallel goroutines joined by one Wait, so the two together
//     cost ONE of these, not two.
//  3. ingressWALFlushTimeout — one final bounded drain of the accepted backlog.
//  4. telemetryFlushTimeout — the OTLP exporters' final flush and shutdown.
//  5. flowStoreCloseTimeout — each runtime's flow-store close runs concurrently
//     under one deadline. Store.Close has no context, so a blocked close
//     goroutine may outlive the wait for this stage.
const (
	// receiverDrainTimeout is the graceful-shutdown bound shared by both
	// receivers. Asserted equal to each package's own constant below so this
	// cannot drift from what they actually use.
	receiverDrainTimeout = 10 * time.Second

	// telemetryFlushTimeout bounds the final OTLP flush in Run.
	telemetryFlushTimeout = 10 * time.Second

	// flowStoreCloseTimeout bounds the deferred sqlitestore.Store.Close calls:
	// Close waits for the retention worker with wg.Wait(), then closes the DB.
	// The calls run concurrently so one runtime's unbounded internal wait cannot
	// prevent other runtimes from closing within the shared stage budget.
	flowStoreCloseTimeout = 10 * time.Second

	// shutdownBudgetHeadroom is what a deployment budget must carry ON TOP of
	// the worst-case drain. The stages above are bounds rather than durations,
	// and process teardown plus container-runtime overhead land after them; a
	// budget merely equal to the drain is a coin flip on every rollout.
	shutdownBudgetHeadroom = 15 * time.Second
)

// Compile-time proof that receiverDrainTimeout is the receivers' real bound.
// A change in either package that this file did not follow fails the build
// here rather than quietly shrinking the margin in production.
const (
	_ = uint(receiverDrainTimeout - stream.ShutdownTimeout)
	_ = uint(stream.ShutdownTimeout - receiverDrainTimeout)
	_ = uint(receiverDrainTimeout - webhook.ShutdownTimeout)
	_ = uint(webhook.ShutdownTimeout - receiverDrainTimeout)
)
