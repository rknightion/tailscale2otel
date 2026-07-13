package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// pointForMode returns the single MetricPoint for process.cpu.time with the given
// cpu.mode attribute value, failing if not found.
func pointForMode(t *testing.T, rec *telemetrytest.Recorder, mode string) telemetrytest.MetricPoint {
	t.Helper()
	pts := rec.MetricPoints("process.cpu.time")
	for _, p := range pts {
		if p.Attrs[semconv.AttrCPUMode] == mode {
			return p
		}
	}
	t.Fatalf("process.cpu.time: no point with cpu.mode=%q (all points: %+v)", mode, pts)
	return telemetrytest.MetricPoint{}
}

// TestEmitProcess_UptimeGauge verifies that emitProcess always records a
// process.uptime gauge with a positive value.
func TestEmitProcess_UptimeGauge(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	start := time.Now().Add(-5 * time.Second) // 5 seconds ago
	var lastUser, lastSys float64

	stub := func() (float64, float64, bool) { return 1.0, 0.5, true }
	emitProcess(e, start, stub, &lastUser, &lastSys)

	pts := rec.MetricPoints("process.uptime")
	if len(pts) != 1 {
		t.Fatalf("process.uptime: got %d points, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "gauge" {
		t.Errorf("process.uptime kind=%q, want gauge", p.Kind)
	}
	if p.Value <= 0 {
		t.Errorf("process.uptime value=%v, want >0", p.Value)
	}
	if p.Value < 4 || p.Value > 10 {
		t.Errorf("process.uptime value=%v, want ~5s (4–10 acceptable range)", p.Value)
	}
}

// TestEmitProcess_CPUTimeDeltasAcrossTicks is the primary delta-semantics test.
// It drives emitProcess across two ticks with a stub readCPU that returns
// increasing cumulative values and asserts that:
//   - tick 1 seeds the counter with the full cumulative value
//   - tick 2 adds only the per-interval delta
//   - both user and system series carry the cpu.mode attribute
//   - the instrument is a monotonic sum (counter)
func TestEmitProcess_CPUTimeDeltasAcrossTicks(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	start := time.Now().Add(-10 * time.Second)
	var lastUser, lastSys float64

	// Tick 1: cumulative user=1.0s, sys=0.5s (seed tick — last* are zero).
	stub1 := func() (float64, float64, bool) { return 1.0, 0.5, true }
	emitProcess(e, start, stub1, &lastUser, &lastSys)

	// After tick 1: counter should hold the seed values (1.0 user, 0.5 sys).
	u1 := pointForMode(t, rec, semconv.CPUModeUser)
	s1 := pointForMode(t, rec, semconv.CPUModeSystem)

	if u1.Value != 1.0 {
		t.Errorf("tick1 user cpu.time = %v, want 1.0", u1.Value)
	}
	if s1.Value != 0.5 {
		t.Errorf("tick1 sys cpu.time = %v, want 0.5", s1.Value)
	}
	if u1.Kind != "sum" || !u1.Monotonic {
		t.Errorf("user cpu.time kind=%q monotonic=%v, want sum/true", u1.Kind, u1.Monotonic)
	}
	if s1.Kind != "sum" || !s1.Monotonic {
		t.Errorf("sys cpu.time kind=%q monotonic=%v, want sum/true", s1.Kind, s1.Monotonic)
	}

	// Verify cpu.mode attribute is present.
	if got := u1.Attrs[semconv.AttrCPUMode]; got != semconv.CPUModeUser {
		t.Errorf("user point attr cpu.mode=%q, want %q", got, semconv.CPUModeUser)
	}
	if got := s1.Attrs[semconv.AttrCPUMode]; got != semconv.CPUModeSystem {
		t.Errorf("sys point attr cpu.mode=%q, want %q", got, semconv.CPUModeSystem)
	}

	// Tick 2: cumulative user=2.5s (+1.5 delta), sys=0.8s (+0.3 delta).
	stub2 := func() (float64, float64, bool) { return 2.5, 0.8, true }
	emitProcess(e, start, stub2, &lastUser, &lastSys)

	// The SDK uses cumulative temporality: the reported value is the running sum,
	// so after the seed (1.0) + delta (1.5) the user counter should be 2.5, and
	// after seed (0.5) + delta (0.3) the sys counter should be 0.8.
	u2 := pointForMode(t, rec, semconv.CPUModeUser)
	s2 := pointForMode(t, rec, semconv.CPUModeSystem)

	const epsilon = 1e-9
	if diff := u2.Value - 2.5; diff > epsilon || diff < -epsilon {
		t.Errorf("tick2 user cpu.time = %v, want 2.5 (seed 1.0 + delta 1.5)", u2.Value)
	}
	if diff := s2.Value - 0.8; diff > epsilon || diff < -epsilon {
		t.Errorf("tick2 sys cpu.time = %v, want 0.8 (seed 0.5 + delta 0.3)", s2.Value)
	}
}

// TestEmitProcess_CPUTimeSkippedWhenNotOK verifies that when readCPU returns
// ok=false no process.cpu.time points are recorded.
func TestEmitProcess_CPUTimeSkippedWhenNotOK(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	start := time.Now().Add(-1 * time.Second)
	var lastUser, lastSys float64

	stub := func() (float64, float64, bool) { return 0, 0, false }
	emitProcess(e, start, stub, &lastUser, &lastSys)

	pts := rec.MetricPoints("process.cpu.time")
	if len(pts) != 0 {
		t.Errorf("process.cpu.time: got %d points, want 0 when readCPU ok=false", len(pts))
	}
	// Uptime should still be emitted.
	if pts2 := rec.MetricPoints("process.uptime"); len(pts2) != 1 {
		t.Errorf("process.uptime: got %d points, want 1 even when readCPU fails", len(pts2))
	}
}

// TestEmitProcess_CPUTimeNegativeDeltaSkipped guards against a spurious huge
// counter spike if getrusage were ever to return a value lower than the previous
// one (shouldn't happen but defensive).
func TestEmitProcess_CPUTimeNegativeDeltaSkipped(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	start := time.Now().Add(-1 * time.Second)
	var lastUser, lastSys float64

	// Tick 1: seed with 5.0 user, 2.0 sys.
	stub1 := func() (float64, float64, bool) { return 5.0, 2.0, true }
	emitProcess(e, start, stub1, &lastUser, &lastSys)

	v1u := pointForMode(t, rec, semconv.CPUModeUser).Value
	if v1u != 5.0 {
		t.Fatalf("seed user = %v, want 5.0", v1u)
	}

	// Tick 2: values go DOWN (impossible in real getrusage but guard nonetheless).
	// Neither delta should fire; counter stays at its prior cumulative sum.
	stub2 := func() (float64, float64, bool) { return 1.0, 0.5, true }
	emitProcess(e, start, stub2, &lastUser, &lastSys)

	v2u := pointForMode(t, rec, semconv.CPUModeUser).Value
	if v2u != 5.0 {
		t.Errorf("after reset tick user = %v, want 5.0 (delta skipped)", v2u)
	}
}
