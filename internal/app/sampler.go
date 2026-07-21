package app

import (
	"context"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/collector"
	"github.com/rknightion/tailscale2otel/v2/internal/ringbuf"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
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

	// Emit-boundary throughput, differenced from the cumulative emitter counters
	// between consecutive samples (see telemetry.EmitStats).
	metricRate *ringbuf.Ring[float64] // metric data points/sec
	logRate    *ringbuf.Ring[float64] // log records/sec

	// Collector-fleet aggregate across every tailnet runtime.
	failing  *ringbuf.Ring[int]     // collectors whose last run failed
	runDurMs *ringbuf.Ring[float64] // mean last-run duration, milliseconds

	n int // ring capacity, for lazily created per-metric rings

	// cardMu guards the cardPerMetric MAP (create/read); the Ring values are
	// internally mutex-guarded already. cardPerMetric holds the per-source-metric
	// active-series trend feeding the cardinality growth view (empty when self-obs
	// is off). The map is written only by the sampler goroutine and read by
	// buildStatus from the HTTP handler, so the map access must be locked.
	cardMu        sync.Mutex
	cardPerMetric map[string]*ringbuf.Ring[int]

	// lastNumGC/lastEmit/lastAt carry the previous sample's GC count, emit totals
	// and time so the rates can be differenced. lastAt is the single "have a prior
	// sample" signal for every rate series, so they all start at 0 on the first
	// tick rather than reporting a cumulative total as a rate. They are touched
	// only by the single sampler goroutine.
	lastNumGC uint32
	lastEmit  telemetry.EmitStats
	lastAt    time.Time
}

// fleetStats is the collector-fleet aggregate for one sampler tick: how many
// collectors have run at all, how many failed their most recent run, and the
// mean of those most-recent run durations in milliseconds. Collectors that have
// never run contribute to none of the three.
type fleetStats struct {
	active         int
	failing        int
	meanDurationMs float64
}

// samplerTick is one pass of observations, read from the process in a single
// sweep so every ring advances against the same instant.
type samplerTick struct {
	rs        runtimeStats
	cardTotal int
	perMetric map[string]int
	emit      telemetry.EmitStats
	fleet     fleetStats
}

func newRuntimeHistory(n int) *runtimeHistory {
	return &runtimeHistory{
		goroutines:    ringbuf.New[int](n),
		heapAlloc:     ringbuf.New[uint64](n),
		gcRate:        ringbuf.New[float64](n),
		cardTotal:     ringbuf.New[int](n),
		metricRate:    ringbuf.New[float64](n),
		logRate:       ringbuf.New[float64](n),
		failing:       ringbuf.New[int](n),
		runDurMs:      ringbuf.New[float64](n),
		n:             n,
		cardPerMetric: map[string]*ringbuf.Ring[int]{},
	}
}

// perMetricSeries returns each source metric's active-series history (oldest
// first), keyed by metric name. Safe to call concurrently with the sampler.
func (h *runtimeHistory) perMetricSeries() map[string][]int {
	h.cardMu.Lock()
	defer h.cardMu.Unlock()
	out := make(map[string][]int, len(h.cardPerMetric))
	for name, r := range h.cardPerMetric {
		out[name] = r.Values()
	}
	return out
}

// sample appends one observation. The GC and emit-throughput rates are the
// change since the previous sample divided by the elapsed seconds; the first
// sample (and any counter decrease — a NumGC uint32 wrap, or a counter that
// somehow went backwards) records 0 rather than a spurious value, so a
// cumulative total is never mistaken for a rate. It is a standalone method so
// tests can drive it tick-by-tick.
func (h *runtimeHistory) sample(now time.Time, t samplerTick) {
	h.goroutines.Add(t.rs.goroutines)
	h.heapAlloc.Add(t.rs.heapAlloc)
	h.cardTotal.Add(t.cardTotal)
	h.samplePerMetric(t.perMetric)
	h.failing.Add(t.fleet.failing)
	h.runDurMs.Add(t.fleet.meanDurationMs)

	var dt float64
	if !h.lastAt.IsZero() {
		dt = now.Sub(h.lastAt).Seconds()
	}
	var gcRate float64
	if dt > 0 && t.rs.numGC >= h.lastNumGC {
		gcRate = float64(t.rs.numGC-h.lastNumGC) / dt
	}
	h.gcRate.Add(gcRate)
	h.metricRate.Add(perSecond(t.emit.MetricPoints, h.lastEmit.MetricPoints, dt))
	h.logRate.Add(perSecond(t.emit.LogRecords, h.lastEmit.LogRecords, dt))

	h.lastNumGC = t.rs.numGC
	h.lastEmit = t.emit
	h.lastAt = now
}

// perSecond differences two cumulative totals over dt seconds. A non-positive dt
// (no prior sample, or two samples at the same instant) or a total that went
// backwards yields 0.
func perSecond(cur, prev uint64, dt float64) float64 {
	if dt <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

// fleetAggregate reduces one or more collector status-tracker snapshots (one per
// tailnet runtime) to the fleet-wide aggregate. Collectors that have never run
// are excluded entirely: they are neither healthy nor failing, and they have no
// duration to average.
func fleetAggregate(snaps ...map[string]collector.CollectorRun) fleetStats {
	var fs fleetStats
	var totalMs float64
	for _, snap := range snaps {
		for _, r := range snap {
			if r.Runs == 0 {
				continue
			}
			fs.active++
			if !r.LastSuccess {
				fs.failing++
			}
			totalMs += float64(r.LastDuration.Microseconds()) / 1000
		}
	}
	if fs.active > 0 {
		fs.meanDurationMs = totalMs / float64(fs.active)
	}
	return fs
}

// samplePerMetric appends one active-series observation per source metric,
// lazily creating a ring for a metric first seen this tick. A metric absent this
// tick keeps its prior samples (its series simply doesn't advance) — acceptable
// for a short-window growth view. Called only by the sampler goroutine.
func (h *runtimeHistory) samplePerMetric(perMetric map[string]int) {
	if len(perMetric) == 0 {
		return
	}
	h.cardMu.Lock()
	defer h.cardMu.Unlock()
	for name, count := range perMetric {
		r := h.cardPerMetric[name]
		if r == nil {
			r = ringbuf.New[int](h.n)
			h.cardPerMetric[name] = r
		}
		r.Add(count)
	}
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

// cardinalityPerMetric aggregates the per-source-metric active-series counts
// across the process provider and every tailnet runtime (summing per metric),
// for the sampler's per-metric growth history. Empty when self-obs is off.
func (a *App) cardinalityPerMetric() map[string]int {
	out := map[string]int{}
	add := func(snaps []telemetry.SeriesCount) {
		for _, sc := range snaps {
			out[sc.Metric] += sc.Count
		}
	}
	add(a.procCard.Snapshot())
	for _, rt := range a.runtimes {
		add(rt.card.Snapshot())
	}
	return out
}

// emitStats sums the emit-boundary counters across every distinct Emitter in the
// process (the process provider's plus each tailnet runtime's). Emitters are
// deduplicated by identity because a Headscale runtime shares the process
// emitter — counting both would double the reported throughput. An Emitter that
// does not count (a test double) contributes nothing.
func (a *App) emitStats() telemetry.EmitStats {
	var out telemetry.EmitStats
	seen := make(map[telemetry.EmitCounter]struct{}, len(a.runtimes)+1)
	add := func(e telemetry.Emitter) {
		c, ok := e.(telemetry.EmitCounter)
		if !ok {
			return
		}
		if _, dup := seen[c]; dup {
			return
		}
		seen[c] = struct{}{}
		s := c.EmitStats()
		out.MetricPoints += s.MetricPoints
		out.LogRecords += s.LogRecords
	}
	add(a.procEmitter)
	for _, rt := range a.runtimes {
		add(rt.emitter)
	}
	return out
}

// collectorFleet aggregates every runtime's collector status tracker into the
// fleet-wide view sampled on each tick and shown on the status page.
func (a *App) collectorFleet() fleetStats {
	snaps := make([]map[string]collector.CollectorRun, 0, len(a.runtimes))
	for _, rt := range a.runtimes {
		snaps = append(snaps, rt.status.Snapshot())
	}
	return fleetAggregate(snaps...)
}

// samplerSources are the injectable readers the sampler polls each tick.
// Production wires them to the process (runtime stats) and the app's aggregate
// accessors; tests supply whichever subset they exercise — a nil reader simply
// contributes a zero value.
type samplerSources struct {
	read      func() runtimeStats
	cardTotal func() int
	perMetric func() map[string]int
	emit      func() telemetry.EmitStats
	fleet     func() fleetStats
}

// tick reads every source once, so all rings advance against one consistent
// observation.
func (s samplerSources) tick() samplerTick {
	var t samplerTick
	if s.read != nil {
		t.rs = s.read()
	}
	if s.cardTotal != nil {
		t.cardTotal = s.cardTotal()
	}
	if s.perMetric != nil {
		t.perMetric = s.perMetric()
	}
	if s.emit != nil {
		t.emit = s.emit()
	}
	if s.fleet != nil {
		t.fleet = s.fleet()
	}
	return t
}

// runSampler records one observation immediately and then on each interval until
// ctx is canceled, mirroring runHeartbeat/runRuntimeReporter. The sources are
// injectable for tests; production passes readRuntimeStats and the app's
// aggregate accessors. A non-positive interval falls back to samplerInterval.
func runSampler(ctx context.Context, h *runtimeHistory, interval time.Duration, src samplerSources) {
	if interval <= 0 {
		interval = samplerInterval
	}
	sample := func() { h.sample(time.Now(), src.tick()) }
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
