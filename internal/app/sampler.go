package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/ringbuf"
)

// samplerInterval is how often the in-process runtime/cardinality history is
// sampled. It matches the status page's poll cadence so the trend resolution
// roughly equals what the page shows. runtimeHistoryLen samples (~10 min at 10s)
// are retained per series.
const (
	samplerInterval   = 10 * time.Second
	runtimeHistoryLen = 60
)

// runtimeHistory holds short-term in-memory trends for the admin status page's
// sparklines. It is independent of self-observability: the rings are populated
// for introspection only and never emitted as OTLP.
type runtimeHistory struct {
	goroutines *ringbuf.Ring[int]
	heapAlloc  *ringbuf.Ring[uint64]
	gcRate     *ringbuf.Ring[float64] // GC cycles/sec between consecutive samples
	cardTotal  *ringbuf.Ring[int]     // total active series (0 when self-obs is off)

	// lastNumGC/lastAt carry the previous sample's GC count and time so the rate
	// can be differenced. They are touched only by the single sampler goroutine.
	lastNumGC uint32
	lastAt    time.Time
}

func newRuntimeHistory(n int) *runtimeHistory {
	return &runtimeHistory{
		goroutines: ringbuf.New[int](n),
		heapAlloc:  ringbuf.New[uint64](n),
		gcRate:     ringbuf.New[float64](n),
		cardTotal:  ringbuf.New[int](n),
	}
}

// sample appends one observation. The GC rate is the change in NumGC since the
// previous sample divided by the elapsed seconds; the first sample (and any
// NumGC decrease from a uint32 wrap) records 0 rather than a spurious value. It
// is a standalone method so tests can drive it tick-by-tick.
func (h *runtimeHistory) sample(now time.Time, rs runtimeStats, cardTotal int) {
	h.goroutines.Add(rs.goroutines)
	h.heapAlloc.Add(rs.heapAlloc)
	h.cardTotal.Add(cardTotal)

	var rate float64
	if !h.lastAt.IsZero() {
		if dt := now.Sub(h.lastAt).Seconds(); dt > 0 && rs.numGC >= h.lastNumGC {
			rate = float64(rs.numGC-h.lastNumGC) / dt
		}
	}
	h.gcRate.Add(rate)
	h.lastNumGC = rs.numGC
	h.lastAt = now
}

// cardinalityTotal sums the active-series counts across the process provider and
// every tailnet runtime, for the runtime sampler's cardinality trend. It is 0
// when self-observability is off (every tracker is nil; Snapshot is nil-safe).
func (a *App) cardinalityTotal() int {
	total := 0
	for _, sc := range a.procCard.Snapshot() {
		total += sc.Count
	}
	for _, rt := range a.runtimes {
		for _, sc := range rt.card.Snapshot() {
			total += sc.Count
		}
	}
	return total
}

// runSampler records one observation immediately and then on each interval until
// ctx is canceled, mirroring runHeartbeat/runRuntimeReporter. read and cardTotal
// are injectable for tests; production passes readRuntimeStats and the app's
// cardinality total. A non-positive interval falls back to samplerInterval.
func runSampler(ctx context.Context, h *runtimeHistory, interval time.Duration, read func() runtimeStats, cardTotal func() int) {
	if interval <= 0 {
		interval = samplerInterval
	}
	sample := func() { h.sample(time.Now(), read(), cardTotal()) }
	sample()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sample()
		}
	}
}
