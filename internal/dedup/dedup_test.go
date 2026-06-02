package dedup_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/dedup"
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
