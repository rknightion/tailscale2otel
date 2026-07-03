package rdns

import (
	"context"
	"fmt"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"
)

// blockingLookup returns a Lookup func that blocks until release is closed,
// so resolve() never completes during a test and len(c.entries) stays fixed
// while c.inflight accumulates — letting a burst of admissions be inspected
// before any of them land.
func blockingLookup(release <-chan struct{}) func(context.Context, netip.Addr) ([]string, error) {
	return func(context.Context, netip.Addr) ([]string, error) {
		<-release
		return []string{"blocked.example.com."}, nil
	}
}

// TestLookupName_BurstDoesNotExceedMaxEntries covers issue #118: a burst of
// Concurrency-many brand-new addresses arriving while the cache has only one
// free slot must admit exactly one of them, not all of them. Before the fix,
// the admission check only compared len(c.entries) (committed entries) against
// max, so every address in the burst passed the check simultaneously because
// none of their resolves had landed yet — overrunning max_entries by up to
// Concurrency.
func TestLookupName_BurstDoesNotExceedMaxEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		const maxEntries = 5
		const concurrency = 8
		c := New(Options{
			MaxEntries:     maxEntries,
			Concurrency:    concurrency,
			ReportInterval: testNoTick,
			Lookup:         blockingLookup(release),
		})
		defer func() {
			close(release)
			c.Close()
		}()

		// Pre-fill 4 already-resolved entries directly, leaving exactly one
		// free slot at max_entries=5.
		farFuture := time.Now().Add(time.Hour)
		c.mu.Lock()
		for i := range 4 {
			c.entries[addr(fmt.Sprintf("203.0.113.%d", i))] = entry{name: "x", expires: farFuture}
		}
		c.mu.Unlock()

		// A burst of `concurrency` brand-new addresses, all distinct, none
		// previously seen. With only 1 free slot, exactly 1 must be admitted.
		for i := range concurrency {
			c.LookupName(addr(fmt.Sprintf("203.0.113.%d", 100+i)))
		}
		synctest.Wait() // let every admitted resolve() goroutine reach the blocking Lookup

		c.mu.Lock()
		entriesLen := len(c.entries)
		inflightLen := len(c.inflight)
		c.mu.Unlock()

		if total := entriesLen + inflightLen; total > maxEntries {
			t.Errorf("entries+inflight = %d, want <= max_entries (%d)", total, maxEntries)
		}
		if inflightLen != 1 {
			t.Errorf("inflight = %d, want 1 (only one free slot was available)", inflightLen)
		}
		if got, want := c.Stats().Overflows, int64(concurrency-1); got != want {
			t.Errorf("Overflows = %d, want %d (%d admitted, rest overflow)", got, want, 1)
		}
	})
}

// TestLookupName_BusyAddressAtCapacityNotOverflow covers issue #118's second
// bug: a repeat sighting of an address whose resolution is already in flight
// must never increment the overflow counter, even when the cache's committed
// entries happen to be at max_entries. Before the fix, the overflow check ran
// before the in-flight ("busy") check, so this case wrongly counted as an
// overflow.
func TestLookupName_BusyAddressAtCapacityNotOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		c := New(Options{
			MaxEntries:     2,
			Concurrency:    3,
			ReportInterval: testNoTick,
			Lookup:         blockingLookup(release),
		})
		defer func() {
			close(release)
			c.Close()
		}()

		pending := addr("203.0.113.50")
		if name, ok := c.LookupName(pending); ok || name != "" {
			t.Fatalf("first LookupName(pending) = (%q,%v), want (\"\",false)", name, ok)
		}
		synctest.Wait() // let resolve(pending) reach the blocking Lookup call

		// Simulate two other addresses' resolutions having already landed,
		// filling entries to max_entries, while `pending` is still in flight
		// (not yet present in c.entries).
		farFuture := time.Now().Add(time.Hour)
		c.mu.Lock()
		c.entries[addr("203.0.113.60")] = entry{name: "a", expires: farFuture}
		c.entries[addr("203.0.113.61")] = entry{name: "b", expires: farFuture}
		c.mu.Unlock()

		before := c.Stats().Overflows

		// A repeat sighting of the still-in-flight address is just a duplicate
		// miss, not a new admission decision, and must not count as overflow.
		if name, ok := c.LookupName(pending); ok || name != "" {
			t.Fatalf("repeat LookupName(pending) = (%q,%v), want (\"\",false)", name, ok)
		}

		if got := c.Stats().Overflows; got != before {
			t.Errorf("Overflows = %d, want unchanged at %d (busy address must not overflow)", got, before)
		}
	})
}
