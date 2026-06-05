// Package ringbuf provides a small, thread-safe, generic ring buffer.
//
// A Ring remembers the most recent N values it has been given. Once it is full,
// each new value overwrites the oldest in FIFO order. It is intended for tiny,
// bounded in-process histories (e.g. the last N scrape durations) that feed the
// admin status page's sparklines — never as durable storage.
package ringbuf

import "sync"

// Ring is a thread-safe, fixed-capacity ring buffer of T. The zero value is not
// ready for use; construct one with New. A nil *Ring is a safe no-op: Add does
// nothing and Len/Cap/Values report empty.
type Ring[T any] struct {
	mu   sync.Mutex
	buf  []T
	head int // index of the oldest element once the buffer has filled
	n    int // number of live elements (0..cap)
	cap  int
}

// New returns a Ring that retains at most the last capacity values. A capacity
// of zero or less is clamped to one, so a Ring is always usable.
func New[T any](capacity int) *Ring[T] {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring[T]{
		buf: make([]T, capacity),
		cap: capacity,
	}
}

// Add appends v, overwriting the oldest value once the Ring is full. It is O(1).
// A nil receiver is a no-op.
func (r *Ring[T]) Add(v T) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n < r.cap {
		r.buf[(r.head+r.n)%r.cap] = v
		r.n++
		return
	}
	// Full: overwrite the oldest and advance the head.
	r.buf[r.head] = v
	r.head = (r.head + 1) % r.cap
}

// Values returns a copy of the retained values, oldest first. The result is
// independent of the Ring's internal state. A nil or empty Ring returns nil.
func (r *Ring[T]) Values() []T {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == 0 {
		return nil
	}
	out := make([]T, r.n)
	for i := 0; i < r.n; i++ {
		out[i] = r.buf[(r.head+i)%r.cap]
	}
	return out
}

// Len reports how many values are currently retained. A nil receiver returns 0.
func (r *Ring[T]) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// Cap reports the configured capacity: the maximum number of values retained
// before the oldest is overwritten. A nil receiver returns 0.
func (r *Ring[T]) Cap() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cap
}
