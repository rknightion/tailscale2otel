package telemetry_test

import (
	"strconv"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// Allocation budget for the cardinality tracker's emit-path hook (#441).
//
// CardinalityTracker.Observe runs once per emitted data point — it is the most
// frequently executed function in the process, and it exists purely to MEASURE
// cardinality, so any allocation it adds is pure overhead on every signal the
// exporter produces. That makes it the single place where an allocation
// regression matters most and would be noticed least.
//
// See internal/flowlog/benchbudget_internal_test.go for why this gates
// allocations rather than time, and why it uses testing.AllocsPerRun rather than
// the benchmarks in bench_test.go.
func TestCardinalityTrackerAllocationBudget(t *testing.T) {
	// Measured 2026-07-28 on darwin/arm64: low 0, high 1 alloc/op.
	//
	// The "low" case is the steady state — a bounded set of attribute
	// combinations seen over and over — and it must stay at ZERO. A tracker that
	// allocates on a repeat observation is allocating once per data point
	// forever, which is the regression this is really here to catch.
	t.Run("repeat observations must not allocate", func(t *testing.T) {
		tr := telemetry.NewCardinalityTracker()
		const distinct = 8
		attrs := make([]telemetry.Attrs, distinct)
		for i := range attrs {
			attrs[i] = telemetry.Attrs{"a": "x", "n": i}
		}
		// Prime every set first: the budget is about the STEADY state, not the
		// first sighting of a series.
		for _, a := range attrs {
			tr.Observe("tailscale.metric.bench", a)
		}
		i := 0
		got := testing.AllocsPerRun(100, func() {
			tr.Observe("tailscale.metric.bench", attrs[i%distinct])
			i++
		})
		t.Logf("Observe/repeat: %.0f allocs/op (budget 0)", got)
		if got > 0 {
			t.Errorf("Observe allocates %.0f/op on a REPEAT observation. This runs once per "+
				"emitted data point, so an allocation here is paid on every signal the "+
				"exporter produces, forever.", got)
		}
	})

	// The "high" case admits a new distinct series every call — the adversarial
	// shape the issue asks for. Some allocation is unavoidable (a new series must
	// be recorded), but it must stay small and, critically, must not grow once
	// the per-metric cap is reached: past the cap the tracker is meant to stop
	// counting, not keep allocating.
	t.Run("distinct series stay bounded past the cap", func(t *testing.T) {
		tr := telemetry.NewCardinalityTracker()
		// Drive well past defaultSeriesCap (10000) so the measurement happens in
		// the capped regime, where new series are supposed to be discarded.
		for i := range 12000 {
			tr.Observe("tailscale.metric.bench", telemetry.Attrs{"a": "x", "n": strconv.Itoa(i)})
		}
		i := 12000
		got := testing.AllocsPerRun(100, func() {
			tr.Observe("tailscale.metric.bench", telemetry.Attrs{"a": "x", "n": strconv.Itoa(i)})
			i++
		})
		const budget = 4
		t.Logf("Observe/capped: %.0f allocs/op (budget %d)", got, budget)
		if got > budget {
			t.Errorf("Observe allocates %.0f/op past the series cap (budget %d). Past the cap "+
				"the tracker should be discarding new series, not accumulating them — an "+
				"unbounded allocation here is a memory leak under a cardinality flood.",
				got, budget)
		}
	})
}
