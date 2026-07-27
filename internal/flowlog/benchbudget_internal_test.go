package flowlog

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// Allocation budgets for the flow hot path (#441).
//
// WHY ALLOCATIONS AND NOT TIME. `go test -bench` reports ns/op, and ns/op on a
// shared CI runner is noisy enough that a threshold tight enough to catch a real
// regression also fires on a noisy neighbor. A gate that cries wolf gets
// deleted, so time is measured and reported but never blocks. Allocations per
// operation are a property of the code rather than the machine, so they are what
// this gate asserts.
//
// WHY testing.AllocsPerRun AND NOT THE BENCHMARKS. The benchmarks in
// bench_internal_test.go are for humans reading numbers. They cannot be reused
// as a gate directly, because allocs/op is only stable once the benchmark has
// converged: BenchmarkProcess_RawMode reports 227 allocs/op at -benchtime 200x
// and 112 at the ~90000 iterations a default run reaches. A gate wired to a
// short benchmark would be measuring warm-up, and would "regress" purely because
// the runner was fast enough to pick a different iteration count that day.
// AllocsPerRun does a warm-up pass, pins GOMAXPROCS and averages over a fixed
// run count, so it answers the same question reproducibly.
//
// WHY THE HEADROOM. Budgets sit meaningfully above today's measurement, not on
// top of it. The purpose is to catch a step change — an allocation added per
// connection, a []byte escaping to the heap — not to force a commit every time
// a dependency shifts a number by one. Each budget records what it was measured
// at, so the headroom that has been consumed is visible.
//
// WHEN THIS FAILS, do not just raise the number. Find what started allocating.
// If the increase is deliberate and justified, raise it IN A COMMIT THAT SAYS
// WHY — that commit is the before/after evidence #441 asks for.

const budgetRuns = 100

// assertAllocBudget runs one unit of work and fails if it allocates more than
// the budget. It always logs the measurement so a passing run still tells you
// how much headroom is left.
func assertAllocBudget(t *testing.T, name string, budget float64, work func()) {
	t.Helper()
	got := testing.AllocsPerRun(budgetRuns, work)
	t.Logf("%s: %.0f allocs/op (budget %.0f, %.0f%% used)",
		name, got, budget, 100*got/budget)
	if got > budget {
		t.Errorf("%s allocates %.0f/op, over its budget of %.0f. Something started "+
			"allocating on the flow hot path; find it rather than raising the number. If "+
			"the increase is deliberate, raise the budget in a commit that explains it.",
			name, got, budget)
	}
}

func benchBudgetProcessor(t *testing.T, mode string, opts func(*Options)) (*Processor, FlowLog) {
	t.Helper()
	fl := benchDecodeLiveRecord(t)
	o := Options{
		FlowMetricsMode: mode,
		Store:           benchDiscardStore{},
		LogMode:         "off",
	}
	if opts != nil {
		opts(&o)
	}
	return NewProcessor(enrich.NewDeviceCache(), o), fl
}

func TestFlowHotPathAllocationBudget(t *testing.T) {
	// Measured 2026-07-28 on darwin/arm64 at converged benchtime:
	//   raw 112, rollup 82, both 112, identity-on 122 allocs/op.
	for _, tc := range []struct {
		name   string
		mode   string
		budget float64
	}{
		{"Process/raw", flowModeAll, 150},
		{"Process/rollup", flowModeRollup, 110},
		{"Process/both", flowModeBoth, 150},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, fl := benchBudgetProcessor(t, tc.mode, nil)
			e := telemetrytest.New().Emitter()
			assertAllocBudget(t, tc.name, tc.budget, func() { p.Process(fl, e) })
		})
	}
}
