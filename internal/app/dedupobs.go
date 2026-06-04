package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// runDedupReporter reports the cross-source de-duplication sets' fill level
// (tailscale2otel.dedup.size) and eviction pressure (tailscale2otel.dedup.evictions,
// as a per-interval delta) immediately and then on each interval until ctx is
// canceled. nil sets (a receiver that's disabled) are skipped. Mirrors
// runCardinalityReporter. A non-positive interval falls back to 60s.
func runDedupReporter(ctx context.Context, e telemetry.Emitter, interval time.Duration, sets map[string]*dedup.Set) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	lastEvictions := make(map[string]uint64, len(sets))
	emit := func() { emitDedup(e, sets, lastEvictions) }
	emit()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emit()
		}
	}
}

// emitDedup records one size gauge and one eviction-delta counter per non-nil
// set, advancing lastEvictions to the current cumulative count. It is a
// standalone function so the catalog guard test can drive it once.
func emitDedup(e telemetry.Emitter, sets map[string]*dedup.Set, lastEvictions map[string]uint64) {
	for name, set := range sets {
		if set == nil {
			continue
		}
		attrs := telemetry.Attrs{semconv.AttrDedupSet: name}
		e.Gauge(appcatalog.DocDedupSize.Name, appcatalog.DocDedupSize.Unit, appcatalog.DocDedupSize.Description,
			float64(set.Len()), attrs)

		cur := set.Evictions()
		if d, ok := delta64(cur, lastEvictions[name]); ok {
			e.Counter(appcatalog.DocDedupEvictions.Name, appcatalog.DocDedupEvictions.Unit,
				appcatalog.DocDedupEvictions.Description, d, attrs)
		}
		lastEvictions[name] = cur
	}
}
