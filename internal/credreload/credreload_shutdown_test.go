package credreload

import (
	"runtime"
	"testing"
	"time"
)

// TestStop_NoGoroutineLeak proves Stop() actually waits for the poller
// goroutine to exit rather than just signaling it, by taking a
// runtime.NumGoroutine() baseline, starting and stopping many Reloaders, and
// confirming the count returns to baseline. A leaking poller (e.g. Stop()
// that closed stopCh but did not <-doneCh) would show a monotonically
// growing count across iterations.
func TestStop_NoGoroutineLeak(t *testing.T) {
	// Let any goroutines from earlier tests in this binary settle.
	runtime.GC()
	baseline := goroutineCountStable(t)

	const iterations = 50
	for i := 0; i < iterations; i++ {
		r, err := New(Options{Interval: time.Microsecond})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		r.Start()
		// Let the poller tick at least once so we are proving a running
		// goroutine actually exits, not just an unstarted one.
		time.Sleep(200 * time.Microsecond)
		r.Stop()
	}

	runtime.GC()
	after := goroutineCountStable(t)

	// Allow generous slack for unrelated background goroutines (GC workers,
	// test framework internals) that are not under this package's control;
	// the invariant we care about is "does not grow with iteration count".
	if after > baseline+5 {
		t.Errorf("goroutine count after %d start/stop cycles = %d, baseline = %d (leak suspected)",
			iterations, after, baseline)
	}
}

// goroutineCountStable polls runtime.NumGoroutine() until it stops changing,
// to avoid a flaky read mid-teardown of some unrelated goroutine.
func goroutineCountStable(t *testing.T) int {
	t.Helper()
	var last int
	stable := 0
	for i := 0; i < 200; i++ {
		n := runtime.NumGoroutine()
		if n == last {
			stable++
			if stable >= 3 {
				return n
			}
		} else {
			stable = 0
		}
		last = n
		time.Sleep(time.Millisecond)
	}
	return last
}
