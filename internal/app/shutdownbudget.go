package app

import (
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/stream"
	"github.com/rknightion/tailscale2otel/v4/internal/webhook"
)

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
const (
	// receiverDrainTimeout is the graceful-shutdown bound shared by both
	// receivers. Asserted equal to each package's own constant below so this
	// cannot drift from what they actually use.
	receiverDrainTimeout = 10 * time.Second

	// telemetryFlushTimeout bounds the final OTLP flush in Run.
	telemetryFlushTimeout = 10 * time.Second

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
