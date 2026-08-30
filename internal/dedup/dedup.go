// Package dedup provides a small, thread-safe, bounded de-duplication set.
//
// A Set remembers the keys it has seen and reports whether a given key is new.
// It is bounded: once the number of remembered keys exceeds the configured
// capacity, the oldest-inserted keys are evicted in FIFO order. A key that was
// evicted and is added again counts as new.
package dedup

import (
	"crypto/sha256"
	"sync"
	"time"
)

// defaultCapacity is used when New is given a non-positive capacity.
const defaultCapacity = 4096

// Set is a thread-safe bounded de-duplication set. The zero value is not ready
// for use; construct one with New.
type Set struct {
	mu                  sync.Mutex
	capacity            int
	seen                map[digest]digest
	order               []entry // insertion order, used to evict the oldest key first
	head                int     // index into order of the oldest live key
	evictions           uint64  // cumulative count of keys evicted for capacity
	hits                uint64  // cumulative count of duplicate-key adds (key already present)
	now                 func() time.Time
	youngestEvictionAge time.Duration
	hasYoungestEviction bool
}

type digest [sha256.Size]byte

// entry keeps the insertion time alongside the key so an eviction can report
// how long the key was retained. The timestamp is intentionally not part of
// the public key/value state: it is only observability metadata.
type entry struct {
	key        digest
	insertedAt time.Time
}

// Option configures a Set.
type Option func(*Set)

// WithClock overrides the time source used to timestamp retained keys. It is
// primarily useful for deterministic tests; a nil clock is ignored.
func WithClock(now func() time.Time) Option {
	return func(s *Set) {
		if now != nil {
			s.now = now
		}
	}
}

// New returns a Set that remembers at most capacity keys. A capacity of zero or
// less selects a sensible default.
func New(capacity int, opts ...Option) *Set {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	s := &Set{
		capacity: capacity,
		seen:     make(map[digest]digest, capacity),
		order:    make([]entry, 0, capacity),
		now:      time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Add records key and reports whether it was newly added. It returns true if
// key had not been seen before (and is now remembered), or false if key was
// already present. When adding a new key pushes the set beyond its capacity,
// the oldest-inserted key is evicted.
func (s *Set) Add(key string) bool {
	return s.CompareAndAdd(key, "") == ResultNew
}

// Result is the outcome of comparing a value against the value first recorded
// for a key.
type Result uint8

const (
	// ResultNew reports that key was absent and value is now its remembered value.
	ResultNew Result = iota
	// ResultExact reports that key was present with the same remembered value.
	ResultExact
	// ResultConflict reports that key was present with a different remembered value.
	// The original value remains authoritative.
	ResultConflict
)

// CompareAndAdd records key and value when key has not been seen and otherwise
// reports whether value matches the first value observed for that key. It keeps
// the same bounded FIFO retention and duplicate-hit accounting as Add. A
// conflicting value never replaces the first observed value.
func (s *Set) CompareAndAdd(key, value string) Result {
	keyDigest := sha256.Sum256([]byte(key))
	valueDigest := sha256.Sum256([]byte(value))
	s.mu.Lock()
	defer s.mu.Unlock()

	if prior, ok := s.seen[keyDigest]; ok {
		s.hits++
		if prior == valueDigest {
			return ResultExact
		}
		return ResultConflict
	}

	s.seen[keyDigest] = valueDigest
	s.order = append(s.order, entry{key: keyDigest, insertedAt: s.now()})
	s.evictLocked(s.order[len(s.order)-1].insertedAt)
	return ResultNew
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

// YoungestEvictionAge reports the smallest residency age observed for any key
// evicted because the set reached capacity. The age is measured when the key
// is evicted, not when this method is called, and is never negative even if the
// wall clock moves backwards. It returns false until the first capacity
// eviction. Keeping the low-water mark makes a short burst that proves the set
// is undersized remain visible to the operator after the burst subsides.
func (s *Set) YoungestEvictionAge() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.youngestEvictionAge, s.hasYoungestEviction
}

// evictLocked drops oldest keys until the set is within capacity. The caller
// must hold s.mu.
func (s *Set) evictLocked(now time.Time) {
	for len(s.seen) > s.capacity {
		oldest := s.order[s.head]
		delete(s.seen, oldest.key)
		s.order[s.head] = entry{}
		s.head++
		s.evictions++
		age := now.Sub(oldest.insertedAt)
		if age < 0 {
			age = 0
		}
		if !s.hasYoungestEviction || age < s.youngestEvictionAge {
			s.youngestEvictionAge = age
			s.hasYoungestEviction = true
		}
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
