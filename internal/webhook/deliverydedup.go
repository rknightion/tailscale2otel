package webhook

import (
	"sync"
	"time"
)

// deliveryDeduper remembers individual webhook deliveries for a bounded time.
// It is deliberately separate from cross-source audit deduplication: its key is
// the full canonical webhook event, not an inferred audit identity.
type deliveryDeduper struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	now      func() time.Time
	seen     map[string]time.Time
	order    []deliveryEntry
	head     int
}

type deliveryEntry struct {
	key     string
	expires time.Time
}

func newDeliveryDeduper(ttl time.Duration, capacity int, now func() time.Time) *deliveryDeduper {
	if ttl <= 0 {
		ttl = defaultDeliveryDedupTTL
	}
	if capacity <= 0 {
		capacity = defaultDeliveryDedupCapacity
	}
	if now == nil {
		now = time.Now
	}
	return &deliveryDeduper{
		ttl: ttl, capacity: capacity, now: now,
		seen: make(map[string]time.Time, capacity), order: make([]deliveryEntry, 0, capacity),
	}
}

// Add reports whether key has not been observed within the deduplication TTL.
// Expired entries and then the oldest live entries are evicted deterministically.
func (d *deliveryDeduper) Add(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	d.evictExpiredLocked(now)
	if _, ok := d.seen[key]; ok {
		return false
	}
	expires := now.Add(d.ttl)
	d.seen[key] = expires
	d.order = append(d.order, deliveryEntry{key: key, expires: expires})
	for len(d.seen) > d.capacity {
		entry := d.order[d.head]
		delete(d.seen, entry.key)
		d.head++
	}
	d.compactLocked()
	return true
}

func (d *deliveryDeduper) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

func (d *deliveryDeduper) evictExpiredLocked(now time.Time) {
	for d.head < len(d.order) && !d.order[d.head].expires.After(now) {
		entry := d.order[d.head]
		if expiry, ok := d.seen[entry.key]; ok && expiry.Equal(entry.expires) {
			delete(d.seen, entry.key)
		}
		d.head++
	}
	d.compactLocked()
}

func (d *deliveryDeduper) compactLocked() {
	if d.head == 0 {
		return
	}
	if d.head >= len(d.order) {
		d.order = d.order[:0]
		d.head = 0
		return
	}
	if d.head > cap(d.order)/2 {
		d.order = append(d.order[:0], d.order[d.head:]...)
		d.head = 0
	}
}
