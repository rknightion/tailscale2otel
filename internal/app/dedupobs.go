package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
)

// runDedupReporter reports the cross-source de-duplication sets' fill level
// (tailscale2otel.dedup.size), eviction pressure (tailscale2otel.dedup.evictions),
// and hit count (tailscale2otel.dedup.hits) — the latter two as per-interval deltas
// — immediately and then on each interval until ctx is canceled. nil sets (a
// receiver that's disabled) are skipped. Mirrors runCardinalityReporter. A
// non-positive interval falls back to 60s.
func runDedupReporter(ctx context.Context, e telemetry.Emitter, interval time.Duration, sets map[string]*dedup.Set) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	lastEvictions := make(map[string]uint64, len(sets))
	lastHits := make(map[string]uint64, len(sets))
	emit := func() { emitDedup(e, sets, lastEvictions, lastHits) }
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

// emitDedup records one size gauge plus eviction-delta and hit-delta counters per
// non-nil set, advancing lastEvictions/lastHits to the current cumulative counts.
// It is a standalone function so the catalog guard test can drive it once.
func emitDedup(e telemetry.Emitter, sets map[string]*dedup.Set, lastEvictions, lastHits map[string]uint64) {
	for name, set := range sets {
		if set == nil {
			continue
		}
		attrs := telemetry.Attrs{semconv.AttrDedupSet: name}
		e.Gauge(appcatalog.DocDedupSize.Name, appcatalog.DocDedupSize.Unit, appcatalog.DocDedupSize.Description,
			float64(set.Len()), attrs)

		curEvictions := set.Evictions()
		if d, ok := delta64(curEvictions, lastEvictions[name]); ok {
			e.Counter(appcatalog.DocDedupEvictions.Name, appcatalog.DocDedupEvictions.Unit,
				appcatalog.DocDedupEvictions.Description, d, attrs)
		}
		lastEvictions[name] = curEvictions

		curHits := set.Hits()
		if d, ok := delta64(curHits, lastHits[name]); ok {
			e.Counter(appcatalog.DocDedupHits.Name, appcatalog.DocDedupHits.Unit,
				appcatalog.DocDedupHits.Description, d, attrs)
		}
		lastHits[name] = curHits
	}
}
