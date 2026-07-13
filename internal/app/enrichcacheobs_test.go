package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// TestRunEnrichCacheAgeReporter_GrowsWhileStale pins #108: with the devices
// collector no longer refreshing the cache, the periodic reporter's emitted
// cache_age must grow past the staleness threshold (it can't with the old
// emit-at-refresh gauge, which was always ~0).
func TestRunEnrichCacheAgeReporter_GrowsWhileStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		cache := enrich.NewDeviceCache()
		cache.Replace(nil) // updated = now (fake clock t0)
		rt := &tailnetRuntime{emitter: rec.Emitter(), cache: cache}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go runEnrichCacheAgeReporter(ctx, []*tailnetRuntime{rt}, time.Hour)
		synctest.Wait()

		if len(rec.MetricPoints("tailscale2otel.enrich.cache_age")) == 0 {
			t.Fatal("no initial cache_age point")
		}
		// No refresh happens; advance 2h. The ticker re-emits the (now grown) age.
		time.Sleep(2 * time.Hour)
		synctest.Wait()

		pts := rec.MetricPoints("tailscale2otel.enrich.cache_age")
		if len(pts) == 0 {
			t.Fatal("no cache_age point after staleness")
		}
		if got := pts[len(pts)-1].Value; got < 3600 {
			t.Fatalf("cache_age = %vs after 2h stale, want > 3600 (must grow so the stale alert can fire)", got)
		}
	})
}
