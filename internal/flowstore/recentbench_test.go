package flowstore_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/flowstore"
)

// Record runs on the emit path, under the store lock, once per connection — and
// a single live capture carried 44,825 of them in one poll. The package's
// contract is "a short lock, a handful of map writes, and return", so the recent
// ring's insert cost is load-bearing rather than incidental.
//
// These are not gates (a timing assertion would be flaky, and allocs/op is
// unstable across benchtime in this repo). They exist so the cost is visible: an
// implementation that shifts the whole ring per observation measured 12.6us/op
// here, versus ~250ns for the amortized-eviction form.

// BenchmarkRecordRecentFullRing is the ordinary case: a full ring and strictly
// increasing event times, so every insert evicts the oldest row and appends.
func BenchmarkRecordRecentFullRing(b *testing.B) {
	s := flowstore.NewMemory(0, flowstore.WithMaxFutureSkew(365*24*time.Hour))
	t0 := time.Now().Add(-time.Hour)
	for i := range flowstore.MaxRecent + 10 {
		s.Record(flowstore.Observation{
			Time: t0.Add(time.Duration(i) * time.Millisecond), SrcNode: "a", DstNode: "b",
		})
	}
	i := flowstore.MaxRecent + 10
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		i++
		s.Record(flowstore.Observation{
			Time: t0.Add(time.Duration(i) * time.Millisecond), SrcNode: "a", DstNode: "b",
		})
	}
}

// BenchmarkRecordRecentOutOfOrder is the adversarial case: every insert lands in
// the middle of the ring, which is the one shape that still costs a memmove.
// Sorted insertion cannot avoid it; the point is that it is the exception rather
// than what every observation pays.
func BenchmarkRecordRecentOutOfOrder(b *testing.B) {
	s := flowstore.NewMemory(0, flowstore.WithMaxFutureSkew(365*24*time.Hour))
	t0 := time.Now().Add(-time.Hour)
	for i := range flowstore.MaxRecent + 10 {
		s.Record(flowstore.Observation{
			Time: t0.Add(time.Duration(i) * time.Millisecond), SrcNode: "a", DstNode: "b",
		})
	}
	// Land each new row roughly halfway through the retained window, forever.
	mid := flowstore.MaxRecent / 2
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		i++
		s.Record(flowstore.Observation{
			Time:    t0.Add(time.Duration(flowstore.MaxRecent+10+mid) * time.Millisecond).Add(time.Duration(i) * time.Nanosecond),
			SrcNode: "a", DstNode: "b",
		})
	}
}
