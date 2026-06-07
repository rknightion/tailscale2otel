package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/catalog"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// metricGroupMap returns a metric-source-name -> docs-group map built from the
// full catalog. Authoritative and self-maintaining: a new metric is classified by
// the Group its descriptor already declares, with no hand-maintained prefix table.
func metricGroupMap() map[string]string {
	m := map[string]string{}
	for _, d := range catalog.Metrics() {
		m[d.Name] = d.Group
	}
	return m
}

// rollupSeriesByGroup sums per-metric active-series counts by their catalog group.
// Metric names absent from groups (e.g. node-metrics passthrough) bucket as "other".
func rollupSeriesByGroup(snap []telemetry.SeriesCount, groups map[string]string) map[string]int {
	out := map[string]int{}
	for _, sc := range snap {
		g := groups[sc.Metric]
		if g == "" {
			g = "other"
		}
		out[g] += sc.Count
	}
	return out
}

// emitSeriesByGroup records one tailscale2otel.series.by_group gauge per group.
func emitSeriesByGroup(e telemetry.Emitter, byGroup map[string]int) {
	for g, n := range byGroup {
		e.Gauge(appcatalog.DocSeriesByGroup.Name, appcatalog.DocSeriesByGroup.Unit, appcatalog.DocSeriesByGroup.Description,
			float64(n), telemetry.Attrs{semconv.AttrMetricGroup: g})
	}
}

// runCardinalityReporter reports the tailscale2otel.series.active gauge once per
// export interval. It does NOT emit on the first tick (a full interval must
// elapse to measure active-per-interval cardinality) and returns immediately
// when self-observability is disabled (c == nil). A non-positive interval falls
// back to 60s, mirroring the telemetry provider and the sibling reporters
// (time.NewTicker(0) panics).
func runCardinalityReporter(ctx context.Context, e telemetry.Emitter, c *telemetry.CardinalityTracker, groups map[string]string, interval time.Duration) {
	if c == nil {
		return
	}
	if interval <= 0 {
		interval = 60 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			reportCardinalityCycle(e, c, groups)
		}
	}
}

// reportCardinalityCycle runs one report tick: emit series.active (+limit/overflow)
// via the tracker, then roll up the just-reported per-metric counts into
// tailscale2otel.series.by_group. Snapshot returns exactly the counts Report just
// emitted (Report sets t.last before returning).
func reportCardinalityCycle(e telemetry.Emitter, c *telemetry.CardinalityTracker, groups map[string]string) {
	c.Report(e)
	emitSeriesByGroup(e, rollupSeriesByGroup(c.Snapshot(), groups))
}
