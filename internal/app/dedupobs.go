package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// dedupHorizons returns the poll-boundary overlap horizon for each de-duplication
// set, omitting any set whose collector does not run the poller.
//
// ReplayOverlap and Interval are POLL-path settings (see the "poll only" note on
// config.FlowlogsCollector.ReplayOverlap). A stream- or objectstore-fed collector
// has no poll boundary, so there is no window for the overlap to describe.
// Publishing one anyway gives tailscale2otel.dedup.overlap_horizon a value that
// applies to nothing, and the shipped youngest-eviction alert then divides a
// LATCHED all-time-minimum eviction age by that inapplicable denominator: the set
// evicts within seconds the first time it fills, the low-water mark never decays
// (dedup.Set.YoungestEvictionAge keeps it deliberately), and the alert fires for
// the life of the process with no way to resolve. Omitting the gauge is what makes
// the alert correct on a streaming deployment, because runDedupReporter skips a
// non-positive horizon and the alert's ratio then has no denominator series.
//
// webhook_cross takes the audit poll interval because that is the boundary it
// dedups across — the webhook receiver against the audit poller. With no audit
// poller running there is again no boundary.
func dedupHorizons(cfg *config.Config) map[string]time.Duration {
	h := make(map[string]time.Duration, 3)
	if pollSource(cfg.Collectors.Flowlogs.Source) {
		h["flow"] = maxDuration(cfg.Collectors.Flowlogs.Interval.D(), cfg.Collectors.Flowlogs.ReplayOverlap.D())
	}
	if pollSource(cfg.Collectors.Auditlogs.Source) {
		auditInterval := cfg.Collectors.Auditlogs.Interval.D()
		h["audit"] = auditInterval
		h["webhook_cross"] = auditInterval
	}
	return h
}

// runDedupReporter reports the cross-source de-duplication sets' fill level
// (tailscale2otel.dedup.size), eviction pressure (tailscale2otel.dedup.evictions),
// and hit count (tailscale2otel.dedup.hits) — the latter two as per-interval deltas
// — immediately and then on each interval until ctx is canceled. nil sets (a
// receiver that's disabled) are skipped. Mirrors runCardinalityReporter. A
// non-positive interval falls back to 60s.
func runDedupReporter(ctx context.Context, e telemetry.Emitter, interval time.Duration, sets map[string]*dedup.Set, horizons map[string]time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	lastEvictions := make(map[string]uint64, len(sets))
	lastHits := make(map[string]uint64, len(sets))
	emit := func() { emitDedup(e, sets, horizons, lastEvictions, lastHits) }
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
func emitDedup(e telemetry.Emitter, sets map[string]*dedup.Set, horizons map[string]time.Duration, lastEvictions, lastHits map[string]uint64) {
	for name, set := range sets {
		if set == nil {
			continue
		}
		attrs := telemetry.Attrs{semconv.AttrDedupSet: name}
		e.Gauge(appcatalog.DocDedupSize.Name, appcatalog.DocDedupSize.Unit, appcatalog.DocDedupSize.Description,
			float64(set.Len()), attrs)
		if horizon := horizons[name]; horizon > 0 {
			e.Gauge(appcatalog.DocDedupOverlapHorizon.Name, appcatalog.DocDedupOverlapHorizon.Unit,
				appcatalog.DocDedupOverlapHorizon.Description, horizon.Seconds(), attrs)
		}
		if age, ok := set.YoungestEvictionAge(); ok {
			e.Gauge(
				appcatalog.DocDedupYoungestEvictionAge.Name,
				appcatalog.DocDedupYoungestEvictionAge.Unit,
				appcatalog.DocDedupYoungestEvictionAge.Description,
				age.Seconds(),
				attrs,
			)
		}

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
