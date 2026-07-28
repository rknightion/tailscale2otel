package rdns

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// TestLookupName_StaleDisabledByDefault pins today's behavior: with
// StaleTTL left at its zero value (New does not invent a default for it —
// the config layer does), an expired positive entry is an immediate miss,
// exactly as before #297.
func TestLookupName_StaleDisabledByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{TTL: time.Hour, ReportInterval: testNoTick, Lookup: okLookup("h.example.com.")})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		time.Sleep(time.Hour + time.Minute) // past TTL

		if _, ok := c.LookupName(addr("203.0.113.5")); ok {
			t.Fatal("StaleTTL<=0 must reproduce today's immediate-miss behavior")
		}
	})
}

// TestLookupName_StaleHit verifies a positive entry past its TTL but still
// inside the stale window is served with ok==true, counted as a stale hit
// (not a hit, not a miss).
func TestLookupName_StaleHit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       10 * time.Minute,
			ReportInterval: testNoTick,
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5")) // miss -> schedule
		synctest.Wait()
		if _, ok := c.LookupName(addr("203.0.113.5")); !ok {
			t.Fatal("want fresh hit before TTL")
		}

		time.Sleep(time.Hour + 5*time.Minute) // past TTL, inside the 10m stale window
		name, ok := c.LookupName(addr("203.0.113.5"))
		if !ok || name != "h.example.com" {
			t.Fatalf("stale LookupName = (%q,%v), want (h.example.com,true)", name, ok)
		}
		synctest.Wait()

		s := c.Stats()
		if s.StaleHits != 1 {
			t.Errorf("StaleHits=%d want 1", s.StaleHits)
		}
		if s.Misses != 1 {
			t.Errorf("Misses=%d want 1 (only the very first sighting)", s.Misses)
		}
		if s.Hits != 1 {
			t.Errorf("Hits=%d want 1 (the stale hit must not count as a fresh hit)", s.Hits)
		}
	})
}

// TestLookupName_StaleHitSingleRefresh verifies that many sightings of the
// same stale address while a refresh is in flight schedule exactly ONE
// background resolve, reusing the existing single-flight `inflight` map.
func TestLookupName_StaleHitSingleRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var block atomic.Bool
		release := make(chan struct{})
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			ReportInterval: testNoTick,
			Lookup: func(ctx context.Context, a netip.Addr) ([]string, error) {
				calls.Add(1)
				if block.Load() {
					<-release
				}
				return []string{"h.example.com."}, nil
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5")) // first resolve completes immediately
		synctest.Wait()

		time.Sleep(time.Hour + time.Minute) // now stale
		block.Store(true)

		c.LookupName(addr("203.0.113.5")) // stale hit #1 -> schedules the refresh
		synctest.Wait()                   // the refresh goroutine blocks durably on <-release

		c.LookupName(addr("203.0.113.5")) // stale hit #2 -> inflight, no new resolve
		c.LookupName(addr("203.0.113.5")) // stale hit #3 -> same
		synctest.Wait()

		if got := calls.Load(); got != 2 {
			t.Fatalf("lookup called %d times, want 2 (1 initial + 1 refresh, no duplicates)", got)
		}

		close(release)
		synctest.Wait()
	})
}

// TestLookupName_StaleRefreshNoOverflow verifies that refreshing an
// already-cached (now stale) address never counts as an overflow or consumes
// a new admission slot, even when the cache is at MaxEntries capacity — the
// concurrency/capacity guarantees from #118/#121 must survive this change.
func TestLookupName_StaleRefreshNoOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			MaxEntries:     1,
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			ReportInterval: testNoTick,
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.1")) // fills the single slot
		synctest.Wait()
		time.Sleep(time.Hour + time.Minute) // now stale; cache is still "full"

		if _, ok := c.LookupName(addr("203.0.113.1")); !ok {
			t.Fatal("want stale hit")
		}
		synctest.Wait()

		if got := c.Stats().Overflows; got != 0 {
			t.Errorf("Overflows=%d want 0 (refreshing the cached addr is not a new admission)", got)
		}
	})
}

// TestLookupName_StaleRefreshFailureKeepsServing is the core #297 regression
// test: a failed refresh of a stale-but-servable positive entry must NOT
// downgrade it to a negative entry — that would flap the flow-metric label to
// "external" over a single transient resolver blip, exactly the bug being
// fixed.
func TestLookupName_StaleRefreshFailureKeepsServing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fail atomic.Bool
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			ReportInterval: testNoTick,
			Lookup: func(ctx context.Context, a netip.Addr) ([]string, error) {
				if fail.Load() {
					return nil, errors.New("resolver blip")
				}
				return []string{"h.example.com."}, nil
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		time.Sleep(time.Hour + time.Minute) // now stale
		fail.Store(true)

		name, ok := c.LookupName(addr("203.0.113.5")) // stale hit, schedules a failing refresh
		if !ok || name != "h.example.com" {
			t.Fatalf("stale LookupName = (%q,%v), want (h.example.com,true)", name, ok)
		}
		synctest.Wait()

		name2, ok2 := c.LookupName(addr("203.0.113.5"))
		if !ok2 || name2 != "h.example.com" {
			t.Fatalf("after failed refresh = (%q,%v), want (h.example.com,true) — must keep serving stale", name2, ok2)
		}

		s := c.Stats()
		if s.RefreshFail != 1 {
			t.Errorf("RefreshFail=%d want 1", s.RefreshFail)
		}
		if s.Negatives != 0 {
			t.Errorf("Negatives=%d want 0 (failed refresh must not create a negative entry)", s.Negatives)
		}
	})
}

// TestLookupName_StaleRefreshSuccessRestoresFreshTTL verifies a successful
// refresh replaces the entry with a fresh TTL (not just extends the stale
// window).
func TestLookupName_StaleRefreshSuccessRestoresFreshTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			ReportInterval: testNoTick,
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		time.Sleep(time.Hour + time.Minute) // stale window
		c.LookupName(addr("203.0.113.5"))   // triggers the refresh
		synctest.Wait()

		// The refresh reset expires to now+TTL; 50m later it must still be a
		// FRESH hit, not stale again.
		time.Sleep(50 * time.Minute)
		if _, ok := c.LookupName(addr("203.0.113.5")); !ok {
			t.Fatal("want fresh hit after refresh restored the TTL")
		}

		s := c.Stats()
		if s.RefreshSuccess != 1 {
			t.Errorf("RefreshSuccess=%d want 1", s.RefreshSuccess)
		}
	})
}

// TestLookupName_PastStaleWindowIsMiss verifies the third band: once
// now >= expires.Add(StaleTTL), the entry is a plain miss again.
func TestLookupName_PastStaleWindowIsMiss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       10 * time.Minute,
			ReportInterval: testNoTick,
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		time.Sleep(time.Hour + 11*time.Minute) // past expires(1h) + StaleTTL(10m)

		if _, ok := c.LookupName(addr("203.0.113.5")); ok {
			t.Fatal("want miss once the stale window has elapsed")
		}
	})
}

// TestLookupName_NegativeNeverServedStale verifies negative entries are
// completely unaffected by StaleTTL — they miss immediately past NegativeTTL
// regardless of how large StaleTTL is configured.
func TestLookupName_NegativeNeverServedStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			NegativeTTL:    time.Minute,
			StaleTTL:       time.Hour,
			ReportInterval: testNoTick,
			Lookup: func(ctx context.Context, a netip.Addr) ([]string, error) {
				return nil, errors.New("no PTR")
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.9"))
		synctest.Wait()
		time.Sleep(time.Minute + time.Second) // past NegativeTTL

		if _, ok := c.LookupName(addr("203.0.113.9")); ok {
			t.Fatal("a negative entry must never be served stale")
		}
	})
}

// TestSweep_RetainsThroughStaleWindowThenDrops verifies sweep() keeps a
// positive entry alive through its stale window and only then evicts it,
// counting it under the new stale_expired reason rather than expired.
func TestSweep_RetainsThroughStaleWindowThenDrops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       10 * time.Minute,
			ReportInterval: testNoTick,
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()

		time.Sleep(time.Hour + 5*time.Minute) // past TTL, inside the stale window
		c.sweep()
		if c.Stats().Size != 1 {
			t.Fatal("sweep must not drop an entry still inside its stale window")
		}

		time.Sleep(6 * time.Minute) // now past expires+StaleTTL
		c.sweep()

		s := c.Stats()
		if s.Size != 0 {
			t.Errorf("Size=%d want 0 once the stale window elapses", s.Size)
		}
		if s.EvictedStaleExpired != 1 {
			t.Errorf("EvictedStaleExpired=%d want 1", s.EvictedStaleExpired)
		}
		if s.EvictedExpired != 0 {
			t.Errorf("EvictedExpired=%d want 0 (this eviction is stale_expired, not expired)", s.EvictedExpired)
		}
	})
}

// TestReport_EmitsStaleAndRefreshMetrics verifies the new "stale" lookup
// result and the new refreshes metric are emitted with the right attributes.
func TestReport_EmitsStaleAndRefreshMetrics(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			ReportInterval: testNoTick,
			Emitter:        rec.Emitter(),
			Lookup:         okLookup("h.example.com."),
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		time.Sleep(time.Hour + time.Minute)
		c.LookupName(addr("203.0.113.5")) // stale hit + schedules a refresh
		synctest.Wait()
		c.report()

		assertCounter(t, rec, MetricLookups, "result", "stale", 1)
		assertCounter(t, rec, MetricRefreshes, "result", "success", 1)
	})
}

// TestLookupName_PastStaleWindowFailureCachesNegative pins the boundary the
// stale window has on its far side. Once an entry is past expires+StaleTTL it
// is dead: the next sighting is a plain miss, and a lookup that then fails
// must cache a NEGATIVE entry exactly as a first-sighting failure does.
//
// The trap is that sweep() only runs on the report interval, so the dead
// positive entry is still sitting in the map when that resolve lands. Treating
// "a positive entry is present" as "this is a refresh" would take the
// keep-serving-stale early return — leaving the dead entry in place, never
// caching the negative, and re-scheduling a resolver query on every single
// sighting until the next sweep. Servability, not mere presence, is what makes
// a resolve a refresh (#297).
func TestLookupName_PastStaleWindowFailureCachesNegative(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fail atomic.Bool
		c := New(Options{
			TTL:            time.Hour,
			StaleTTL:       time.Hour,
			ReportInterval: testNoTick, // no sweep: the dead entry lingers in the map
			Lookup: func(_ context.Context, _ netip.Addr) ([]string, error) {
				if fail.Load() {
					return nil, errors.New("resolver blip")
				}
				return []string{"h.example.com."}, nil
			},
		})
		defer c.Close()

		c.LookupName(addr("203.0.113.5"))
		synctest.Wait()
		time.Sleep(2*time.Hour + time.Minute) // past TTL *and* the stale window
		fail.Store(true)

		if _, ok := c.LookupName(addr("203.0.113.5")); ok { // miss -> schedules a failing lookup
			t.Fatal("past expires+StaleTTL must be a miss")
		}
		synctest.Wait()

		if _, ok := c.LookupName(addr("203.0.113.5")); ok {
			t.Fatal("want a miss/negative after the failed lookup")
		}
		s := c.Stats()
		if s.Negatives != 1 {
			t.Errorf("Negatives=%d want 1: a failure past the stale window must cache a negative entry", s.Negatives)
		}
		if s.RefreshFail != 0 {
			t.Errorf("RefreshFail=%d want 0: a dead entry's re-resolution is a first sighting, not a refresh", s.RefreshFail)
		}
	})
}
