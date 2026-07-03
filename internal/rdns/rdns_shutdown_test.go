package rdns

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// TestClose_NoLookupScheduledOnceShutdownBegins covers issue #121's core
// mechanism deterministically: once Close has begun (and is blocked inside
// wg.Wait, draining an in-flight resolve), a concurrent sighting of a
// brand-new address must not reserve a slot or call wg.Add — it must return
// a plain miss without ever invoking Lookup. This is exactly the guard that
// makes the Add-after-Close race on the WaitGroup impossible: every wg.Add is
// ordered (via the cache's mutex) to happen either entirely before Close
// observes the shutdown flag, or not at all.
//
// synctest gives this deterministic, non-flaky repro: sync.WaitGroup.Wait is
// durably-blocking once Add has been called within the bubble, so
// synctest.Wait() only returns once the Close goroutine is genuinely parked
// in wg.Wait — guaranteeing (by program order on that goroutine) that
// c.closed was already set to true before we issue the racing LookupName.
func TestClose_NoLookupScheduledOnceShutdownBegins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		c := New(Options{
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				calls.Add(1)
				<-release
				return []string{"h.example.com."}, nil
			},
		})

		// Kick off one in-flight lookup and let its goroutine reach the
		// blocking point inside Lookup.
		if name, ok := c.LookupName(addr("203.0.113.1")); ok || name != "" {
			t.Fatalf("first LookupName = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait()

		// Begin shutdown concurrently. Close cannot return yet: the in-flight
		// resolve above is parked on <-release, so Close's wg.Wait blocks.
		go c.Close()
		synctest.Wait() // returns only once Close is durably blocked in wg.Wait

		// While shutdown is in progress, a sighting of a brand-new address
		// must be a plain miss and must never invoke Lookup.
		if name, ok := c.LookupName(addr("203.0.113.2")); ok || name != "" {
			t.Fatalf("LookupName during shutdown = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait()

		if got := calls.Load(); got != 1 {
			t.Errorf("Lookup invoked %d times, want 1 (no new lookup scheduled once Close began)", got)
		}

		// Let the in-flight resolve finish so Close (and the test) can settle.
		close(release)
		synctest.Wait()
	})
}

// TestLookupName_AfterCloseReturns covers the simpler, sequential case: once
// Close has fully returned, LookupName keeps working as a safe no-op miss
// (never panics, never spawns a lookup).
func TestLookupName_AfterCloseReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		c := New(Options{
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				calls.Add(1)
				return []string{"h."}, nil
			},
		})
		c.Close()

		if name, ok := c.LookupName(addr("203.0.113.3")); ok || name != "" {
			t.Fatalf("LookupName after Close = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait() // let any wrongly-spawned resolve goroutine actually run
		if got := calls.Load(); got != 0 {
			t.Errorf("Lookup invoked %d times after Close, want 0", got)
		}
	})
}

// TestCache_ConcurrentCloseAndLookup_NoWaitGroupPanic is a best-effort stress
// repro of the original hazard: many goroutines racing LookupName against a
// concurrent Close, across many trials with -race enabled, must never panic
// with "sync: WaitGroup misuse: Add called concurrently with Wait". The
// actual internal race window is nanosecond-scale (it requires a resolve's
// wg.Done to transition the counter through zero at the exact instant a
// concurrent, not-yet-guarded wg.Add lands while Close is parked in Wait), so
// this test cannot deterministically force it — TestClose_NoLookupScheduled-
// OnceShutdownBegins above is the deterministic proof that the guard closes
// the window. This stress test is the empirical backstop the issue calls out
// ("Test with synctest (or a stress loop)").
func TestCache_ConcurrentCloseAndLookup_NoWaitGroupPanic(t *testing.T) {
	const trials = 300
	const lookupers = 32

	for trial := range trials {
		c := New(Options{
			MaxEntries:  256,
			Concurrency: 16,
			Lookup: func(context.Context, netip.Addr) ([]string, error) {
				return []string{"h.example.com."}, nil
			},
		})

		var wg sync.WaitGroup
		var panicVal atomic.Value

		guard := func(fn func()) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicVal.Store(fmt.Sprint(r))
				}
			}()
			fn()
		}

		wg.Add(lookupers + 1)
		for j := range lookupers {
			go guard(func() {
				a := addr(fmt.Sprintf("203.0.113.%d", (j%250)+1))
				c.LookupName(a)
			})
		}
		go guard(func() { c.Close() })

		wg.Wait()

		if v := panicVal.Load(); v != nil {
			t.Fatalf("trial %d: panic during concurrent Close/LookupName: %v", trial, v)
		}
	}
}
