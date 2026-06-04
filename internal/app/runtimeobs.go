package app

import (
	"context"
	"runtime"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// runtimeStats is a snapshot of Go runtime health, holding the raw integer
// fields so the monotonic counters can be differenced exactly between ticks.
type runtimeStats struct {
	goroutines    int
	gomaxprocs    int
	heapAlloc     uint64
	heapSys       uint64
	heapInuse     uint64
	stackInuse    uint64
	sys           uint64
	heapObjects   uint64
	nextGC        uint64
	gcCPUFraction float64
	numGC         uint32
	pauseTotalNs  uint64
	totalAlloc    uint64
}

// readRuntimeStats samples the current Go runtime state. runtime.ReadMemStats
// briefly stops the world, so it is called only once per metric interval.
func readRuntimeStats() runtimeStats {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return runtimeStats{
		goroutines:    runtime.NumGoroutine(),
		gomaxprocs:    runtime.GOMAXPROCS(0),
		heapAlloc:     ms.HeapAlloc,
		heapSys:       ms.HeapSys,
		heapInuse:     ms.HeapInuse,
		stackInuse:    ms.StackInuse,
		sys:           ms.Sys,
		heapObjects:   ms.HeapObjects,
		nextGC:        ms.NextGC,
		gcCPUFraction: ms.GCCPUFraction,
		numGC:         ms.NumGC,
		pauseTotalNs:  ms.PauseTotalNs,
		totalAlloc:    ms.TotalAlloc,
	}
}

// emitRuntime records the point-in-time gauges for cur and the monotonic-counter
// deltas (cur - *last), then advances *last to cur. On the first call *last is
// the zero value, so the counters are seeded with the full since-process-start
// cumulative — correct under the SDK's cumulative temporality. It is a standalone
// function (not a closure) so tests can drive it tick-by-tick.
func emitRuntime(e telemetry.Emitter, cur runtimeStats, last *runtimeStats) {
	gauge := func(d metricdoc.Metric, v float64) {
		e.Gauge(d.Name, d.Unit, d.Description, v, nil)
	}
	gauge(appcatalog.DocRuntimeGoroutines, float64(cur.goroutines))
	gauge(appcatalog.DocRuntimeGomaxprocs, float64(cur.gomaxprocs))
	gauge(appcatalog.DocRuntimeHeapAlloc, float64(cur.heapAlloc))
	gauge(appcatalog.DocRuntimeHeapSys, float64(cur.heapSys))
	gauge(appcatalog.DocRuntimeHeapInuse, float64(cur.heapInuse))
	gauge(appcatalog.DocRuntimeStackInuse, float64(cur.stackInuse))
	gauge(appcatalog.DocRuntimeMemSys, float64(cur.sys))
	gauge(appcatalog.DocRuntimeHeapObjects, float64(cur.heapObjects))
	gauge(appcatalog.DocRuntimeGCNextTarget, float64(cur.nextGC))
	gauge(appcatalog.DocRuntimeGCCPUFraction, cur.gcCPUFraction)

	// Counters: add the non-negative delta. A decrease (only possible via a
	// uint32 NumGC wrap or a process-impossible reset) is skipped so no spurious
	// huge spike is emitted; *last still advances to the new baseline.
	if d, ok := delta64(uint64(cur.numGC), uint64(last.numGC)); ok {
		e.Counter(appcatalog.DocRuntimeGCCount.Name, appcatalog.DocRuntimeGCCount.Unit,
			appcatalog.DocRuntimeGCCount.Description, d, nil)
	}
	if cur.pauseTotalNs >= last.pauseTotalNs {
		// Difference the integer ns, then scale to seconds once, to avoid losing
		// precision in float subtraction.
		e.Counter(appcatalog.DocRuntimeGCPauseTime.Name, appcatalog.DocRuntimeGCPauseTime.Unit,
			appcatalog.DocRuntimeGCPauseTime.Description, float64(cur.pauseTotalNs-last.pauseTotalNs)/1e9, nil)
	}
	if d, ok := delta64(cur.totalAlloc, last.totalAlloc); ok {
		e.Counter(appcatalog.DocRuntimeAllocBytes.Name, appcatalog.DocRuntimeAllocBytes.Unit,
			appcatalog.DocRuntimeAllocBytes.Description, d, nil)
	}

	*last = cur
}

// delta64 returns cur-last as a float64 and true, or (0,false) when cur<last
// (a wrap/reset) so the caller skips emitting a spurious delta.
func delta64(cur, last uint64) (float64, bool) {
	if cur < last {
		return 0, false
	}
	return float64(cur - last), true
}

// runRuntimeReporter emits the Go runtime metrics immediately and then on each
// interval until ctx is canceled, mirroring runHeartbeat. read is injectable for
// tests; production passes readRuntimeStats. A non-positive interval falls back
// to 60s (time.NewTicker(0) panics).
func runRuntimeReporter(ctx context.Context, e telemetry.Emitter, interval time.Duration, read func() runtimeStats) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	var last runtimeStats
	emit := func() { emitRuntime(e, read(), &last) }
	emit()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emit()
		}
	}
}
