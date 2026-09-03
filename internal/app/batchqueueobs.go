package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// batchQueueReport pairs one provider's log/span queue tracker with the emitter
// that must report it. The pairing matters: reporting a tailnet provider's queue
// depth through the PROCESS emitter would strip tailscale.tailnet and collapse
// every tailnet's saturation into one indistinguishable series, which is exactly
// the attribution ProviderSet exists to preserve.
type batchQueueReport struct {
	emitter telemetry.Emitter
	tracker *telemetry.BatchQueueTracker
}

// runBatchQueueReporter emits the log/span processor queue depth, capacity and
// drop count once per export interval (#358), mirroring runCardinalityReporter.
//
// It has to be a periodic gauge read rather than an event: the SDK's batch
// processors expose neither their queue length nor their drop count (both are
// private fields on unexported types — see #382), so the numbers come from this
// repo's own queueing wrappers and are only meaningful when sampled. Dropped is
// read destructively as a per-interval delta, so exactly one reporter may own a
// tracker.
func runBatchQueueReporter(ctx context.Context, e telemetry.Emitter, t *telemetry.BatchQueueTracker, interval time.Duration) {
	if e == nil || t == nil {
		return
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.Report(e)
		}
	}
}
