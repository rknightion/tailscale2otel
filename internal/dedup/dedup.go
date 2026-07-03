// Package dedup provides a small, thread-safe, bounded de-duplication set.
//
// A Set remembers the keys it has seen and reports whether a given key is new.
// It is bounded: once the number of remembered keys exceeds the configured
// capacity, the oldest-inserted keys are evicted in FIFO order. A key that was
// evicted and is added again counts as new.
package dedup

import "sync"

// defaultCapacity is used when New is given a non-positive capacity.
const defaultCapacity = 4096

// Set is a thread-safe bounded de-duplication set. The zero value is not ready
// for use; construct one with New.
type Set struct {
	mu        sync.Mutex
	capacity  int
	seen      map[string]struct{}
	order     []string // insertion order, used to evict the oldest key first
	head      int      // index into order of the oldest live key
	evictions uint64   // cumulative count of keys evicted for capacity
	hits      uint64   // cumulative count of duplicate-key adds (key already present)
}

// New returns a Set that remembers at most capacity keys. A capacity of zero or
// less selects a sensible default.
func New(capacity int) *Set {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Set{
		capacity: capacity,
		seen:     make(map[string]struct{}, capacity),
		order:    make([]string, 0, capacity),
	}
}

// Add records key and reports whether it was newly added. It returns true if
// key had not been seen before (and is now remembered), or false if key was
// already present. When adding a new key pushes the set beyond its capacity,
// the oldest-inserted key is evicted.
func (s *Set) Add(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.seen[key]; ok {
		s.hits++
		return false
	}

	s.seen[key] = struct{}{}
	s.order = append(s.order, key)
	s.evictLocked()
	return true
}

// Len reports the number of keys currently remembered.
func (s *Set) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// Cap reports the configured capacity: the maximum number of keys the set
// remembers before it begins evicting the oldest in FIFO order.
func (s *Set) Cap() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capacity
}

// Evictions reports the cumulative number of keys evicted because the set was at
// capacity. Steady-state evictions are NORMAL: dedup keys are effectively unique
// (flow keys embed each batch's window timestamps), so once the fixed-size set
// first fills it evicts exactly one key per insert forever, even when everything
// is healthy — a monotonically rising counter here is expected, not a fault. The
// real failure mode is keys evicted younger than the poll-overlap horizon, i.e.
// evictions approaching the set's capacity within a single poll interval; that
// (not sustained nonzero evictions) is what signals genuine boundary
// double-counting.
func (s *Set) Evictions() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictions
}

// Hits reports the cumulative number of Add calls that found the key already
// present (i.e. calls that returned false). A high or fast-growing value means
// the workload sends many duplicate keys, which is the normal case when the set
// is working correctly.
func (s *Set) Hits() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

// evictLocked drops oldest keys until the set is within capacity. The caller
// must hold s.mu.
func (s *Set) evictLocked() {
	for len(s.seen) > s.capacity {
		oldest := s.order[s.head]
		delete(s.seen, oldest)
		s.order[s.head] = "" // release the string for GC
		s.head++
		s.evictions++
	}
	// Compact the order slice once the consumed prefix grows large, so it does
	// not grow without bound under a long stream of unique keys.
	if s.head > 0 && s.head >= len(s.order) {
		s.order = s.order[:0]
		s.head = 0
	} else if s.head > cap(s.order)/2 {
		s.order = append(s.order[:0], s.order[s.head:]...)
		s.head = 0
	}
}
