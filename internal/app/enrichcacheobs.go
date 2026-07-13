package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector/devices"
)

// runEnrichCacheAgeReporter emits tailscale2otel.enrich.cache_age for each runtime
// at the export interval, computed at emit time as now - cache.updated.
//
// The old emit site was synchronous inside the devices collector's Collect, right
// after cache.Replace — when the age is always ~0 — so a last-value gauge could
// never grow, and the ts2o-enrich-cache-stale alert / dashboards could never detect
// a devices collector that had stopped refreshing (API 500s etc.). Emitting from a
// periodic reporter makes the age actually grow while stale (#108). A non-positive
// interval falls back to 60s (time.NewTicker(0) panics). It emits immediately, then
// every interval, until ctx is canceled.
func runEnrichCacheAgeReporter(ctx context.Context, runtimes []*tailnetRuntime, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	emit := func() {
		for _, rt := range runtimes {
			if rt.cache == nil {
				continue
			}
			rt.emitter.Gauge(devices.DocCacheAge.Name, devices.DocCacheAge.Unit, devices.DocCacheAge.Description,
				rt.cache.Age().Seconds(), nil)
		}
	}
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
