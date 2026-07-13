package dedup_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
)

func TestAdd_NewKeyReturnsTrue(t *testing.T) {
	s := dedup.New(16)
	if !s.Add("a") {
		t.Fatalf("Add(%q) on fresh set = false, want true", "a")
	}
	if got := s.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
}

func TestAdd_SameKeyReturnsFalse(t *testing.T) {
	s := dedup.New(16)
	if !s.Add("a") {
		t.Fatalf("first Add(%q) = false, want true", "a")
	}
	if s.Add("a") {
		t.Fatalf("second Add(%q) = true, want false", "a")
	}
	if got := s.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 after duplicate Add", got)
	}
}

func TestSet_Cap(t *testing.T) {
	if got := dedup.New(16).Cap(); got != 16 {
		t.Fatalf("New(16).Cap() = %d, want 16", got)
	}
	// A non-positive capacity selects the default, so Cap must report it too.
	for _, capacity := range []int{0, -1, -100} {
		if got := dedup.New(capacity).Cap(); got != 4096 {
			t.Fatalf("New(%d).Cap() = %d, want 4096 (default capacity)", capacity, got)
		}
	}
}

func TestNew_NonPositiveCapacityUsesDefault(t *testing.T) {
	for _, capacity := range []int{0, -1, -100} {
		s := dedup.New(capacity)
		// Adding more than a handful of keys must not evict immediately, proving
		// the default capacity is substantially larger than the requested value.
		for i := 0; i < 100; i++ {
			s.Add(fmt.Sprintf("k%d", i))
		}
		if got := s.Len(); got != 100 {
			t.Fatalf("New(%d): Len() = %d, want 100 (default capacity)", capacity, got)
		}
	}
}

func TestAdd_EvictsOldestWhenOverCapacity(t *testing.T) {
	s := dedup.New(3)
	for _, k := range []string{"a", "b", "c"} {
		if !s.Add(k) {
			t.Fatalf("Add(%q) = false, want true", k)
		}
	}
	if got := s.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3 at capacity", got)
	}

	// "d" pushes size over capacity; oldest key "a" must be evicted.
	if !s.Add("d") {
		t.Fatalf("Add(%q) = false, want true", "d")
	}
	if got := s.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3 (bounded at capacity)", got)
	}

	// "b", "c", "d" are still present.
	for _, k := range []string{"b", "c", "d"} {
		if s.Add(k) {
			t.Fatalf("Add(%q) = true, want false (still present)", k)
		}
	}

	// "a" was evicted, so re-adding it counts as new again.
	if !s.Add("a") {
		t.Fatalf("re-Add(%q) after eviction = false, want true", "a")
	}
}

func TestEvictions_CountsEachEvictedKey(t *testing.T) {
	s := dedup.New(3)
	if got := s.Evictions(); got != 0 {
		t.Fatalf("Evictions() = %d before any eviction, want 0", got)
	}
	// Fill to capacity (no eviction yet), then push three more uniques: each
	// evicts exactly one oldest key.
	for _, k := range []string{"a", "b", "c"} {
		s.Add(k)
	}
	if got := s.Evictions(); got != 0 {
		t.Fatalf("Evictions() = %d at capacity, want 0", got)
	}
	for _, k := range []string{"d", "e", "f"} {
		s.Add(k)
	}
	if got := s.Evictions(); got != 3 {
		t.Fatalf("Evictions() = %d after 3 over-capacity adds, want 3", got)
	}
	// A duplicate add evicts nothing.
	s.Add("f")
	if got := s.Evictions(); got != 3 {
		t.Fatalf("Evictions() = %d after a duplicate add, want 3 (unchanged)", got)
	}
}

func TestAdd_DuplicateDoesNotEvict(t *testing.T) {
	s := dedup.New(2)
	s.Add("a")
	s.Add("b")
	// Re-adding an existing key must not push another entry into the ring and so
	// must not evict the oldest key.
	if s.Add("a") {
		t.Fatalf("Add(%q) = true, want false", "a")
	}
	if s.Add("b") {
		t.Fatalf("Add(%q) = true on still-present key, want false", "b")
	}
	if got := s.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
}

func TestAdd_LongUniqueStreamStaysBounded(t *testing.T) {
	const capacity = 8
	s := dedup.New(capacity)
	// A long stream of unique keys must keep the set bounded at capacity and
	// keep eviction correct (oldest gone, newest present) throughout.
	for i := 0; i < 10000; i++ {
		s.Add(fmt.Sprintf("k%d", i))
		if got := s.Len(); got > capacity {
			t.Fatalf("after %d inserts Len() = %d, want <= %d", i, got, capacity)
		}
	}
	// The most recent capacity keys are present; an older key is gone.
	if !s.Add("k0") {
		t.Fatalf("re-Add(k0) after long stream = false, want true (evicted)")
	}
	if s.Add("k9999") {
		t.Fatalf("Add(k9999) = true, want false (still present)")
	}
}

func TestAdd_ConcurrentNoRaceBounded(t *testing.T) {
	const capacity = 256
	s := dedup.New(capacity)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				s.Add(fmt.Sprintf("g%d-k%d", g, i))
			}
		}(g)
	}
	wg.Wait()

	if got := s.Len(); got > capacity {
		t.Fatalf("Len() = %d, want <= %d", got, capacity)
	}
}

func TestHits_CountsEachDuplicateAdd(t *testing.T) {
	s := dedup.New(16)
	if got := s.Hits(); got != 0 {
		t.Fatalf("Hits() = %d before any add, want 0", got)
	}
	// First Add is a miss; Hits must remain 0.
	s.Add("a")
	if got := s.Hits(); got != 0 {
		t.Fatalf("Hits() = %d after one miss, want 0", got)
	}
	// Second Add of the same key is a hit.
	s.Add("a")
	if got := s.Hits(); got != 1 {
		t.Fatalf("Hits() = %d after one duplicate add, want 1", got)
	}
	// A third duplicate increments again.
	s.Add("a")
	if got := s.Hits(); got != 2 {
		t.Fatalf("Hits() = %d after two duplicate adds, want 2", got)
	}
	// A fresh key is another miss; Hits must stay at 2.
	s.Add("b")
	if got := s.Hits(); got != 2 {
		t.Fatalf("Hits() = %d after a fresh-key add, want 2 (unchanged)", got)
	}
}

func TestHits_PureMissSequenceLeavesZero(t *testing.T) {
	s := dedup.New(1024)
	for i := 0; i < 100; i++ {
		s.Add(fmt.Sprintf("k%d", i))
	}
	if got := s.Hits(); got != 0 {
		t.Fatalf("Hits() = %d after 100 unique adds, want 0", got)
	}
}

func TestHits_ConcurrentNoRace(t *testing.T) {
	const capacity = 256
	s := dedup.New(capacity)

	// Pre-populate a set of keys that will be hit concurrently.
	const sharedKeys = 16
	for i := 0; i < sharedKeys; i++ {
		s.Add(fmt.Sprintf("shared-%d", i))
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < sharedKeys; i++ {
				// Each goroutine re-adds every shared key (all hits).
				s.Add(fmt.Sprintf("shared-%d", i))
			}
		}(g)
	}
	wg.Wait()

	// 8 goroutines × 16 shared keys = 128 hits; Hits() must be exactly 128.
	const wantHits = 8 * sharedKeys
	if got := s.Hits(); got != wantHits {
		t.Fatalf("Hits() = %d after concurrent duplicate adds, want %d", got, wantHits)
	}
}
