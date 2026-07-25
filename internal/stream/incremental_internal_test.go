package stream

import (
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestFrozenConnectionBudget pins the GHSA-7rg3-xj9w-2gm8 constant next to the
// other frozen limits. hardening/budget tests outside the package restate the
// value; this is what keeps the two in step.
func TestFrozenConnectionBudget(t *testing.T) {
	if maxConnectionsPerRequest != 500_000 {
		t.Errorf("maxConnectionsPerRequest = %d, want 500000", maxConnectionsPerRequest)
	}
}

// wideArrayElements is how many children the materialization tests put in one
// array. It is unrelated to maxRecordsPerRequest on purpose: the tests drive
// unwrap with a small, explicit budget instead of the production one, so they
// stay fast while still being an order of magnitude wider than any budget.
const wideArrayElements = 4_000_000

// repeatedObjects returns "{},{},..." with n elements.
func repeatedObjects(n int) string {
	return strings.TrimSuffix(strings.Repeat("{},", n), ",")
}

// allocatedBy reports the bytes allocated while running f.
func allocatedBy(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestUnwrap_BareArrayDoesNotMaterializeFullWidth is the GHSA-429c-x655-jwpx
// control, and it has to be MEASURED rather than inferred from the response: the
// request is refused with 413 either way, so the only observable difference
// between "materialize the whole array, then reject" and "stop at budget+1" is
// how much was allocated getting there.
//
// The value is a 4M-element array walked with a budget of 8.
//
//   - materialize-first (the vulnerable shape): decodeOnce into a
//     []json.RawMessage turns every one of the 4M children into its own
//     allocation inside a slice grown to the full width — hundreds of MiB —
//     before the record budget is consulted even once;
//   - visit-incrementally (the fix): nine children are ever touched, so
//     allocation tracks the BUDGET, not the width the sender chose.
//
// The threshold is deliberately loose. It only has to separate "a few KiB" from
// "hundreds of MiB".
func TestUnwrap_BareArrayDoesNotMaterializeFullWidth(t *testing.T) {
	const allocBudget = 32 << 20 // MiB

	value := json.RawMessage(`[` + repeatedObjects(wideArrayElements) + `]`)
	st := unwrapState{remaining: 8}
	var out []extractedRecord

	got := allocatedBy(func() { out = unwrap(value, time.Time{}, 0, &st) })

	if !st.overflow {
		t.Fatal("st.overflow = false; a 4M-element array must bust a budget of 8")
	}
	if got > allocBudget {
		t.Fatalf("unwrap allocated %d bytes walking a 4M-element array with a budget of 8, want <= %d; "+
			"the array is still being materialized before the budget is enforced", got, allocBudget)
	}
	if len(out) != 0 {
		t.Fatalf("unwrap returned %d records on overflow, want 0 (no partial batch)", len(out))
	}
}

// TestUnwrap_LogsWrapperDoesNotMaterializeFullWidth is the same measurement for
// the {"logs":[...]} branch (GHSA-6j2r-56pv-qrm7), which decoded every wrapper
// child into a []json.RawMessage AND preallocated its output slice to that same
// full width before the traversal-level guard could reject.
//
// The threshold here has to clear one legitimate cost the bare-array case does
// not pay: the envelope decode copies the wrapper's raw bytes once (~12 MiB for
// this input). That is bounded by the body cap and is the point — one copy of the
// body, not one allocation per child.
func TestUnwrap_LogsWrapperDoesNotMaterializeFullWidth(t *testing.T) {
	const allocBudget = 64 << 20 // MiB

	value := json.RawMessage(`{"logs":[` + repeatedObjects(wideArrayElements) + `]}`)
	st := unwrapState{remaining: 8}
	var out []extractedRecord

	got := allocatedBy(func() { out = unwrap(value, time.Time{}, 0, &st) })

	if !st.overflow {
		t.Fatal("st.overflow = false; a 4M-child wrapper must bust a budget of 8")
	}
	if got > allocBudget {
		t.Fatalf("unwrap allocated %d bytes walking a 4M-child logs wrapper with a budget of 8, want <= %d; "+
			"the wrapper's children are still being materialized to full width before the guard rejects",
			got, allocBudget)
	}
	if len(out) != 0 {
		t.Fatalf("unwrap returned %d records on overflow, want 0 (no partial batch)", len(out))
	}
}

// TestUnwrapArray_StopsAtBudgetPlusOne pins the incremental contract exactly: the
// walk reads at most `budget` children and then refuses, so the number of
// children ever decoded is a function of the BUDGET and never of the array's
// width.
func TestUnwrapArray_StopsAtBudgetPlusOne(t *testing.T) {
	arr := []byte(`[` + repeatedObjects(10_000) + `]`)

	st := unwrapState{remaining: 8}
	out, elements, ok := unwrapArray(arr, time.Time{}, 0, &st)

	if !ok {
		t.Fatal("unwrapArray ok = false for a well-formed array")
	}
	if !st.overflow {
		t.Fatal("st.overflow = false; a 10000-element array must bust a budget of 8")
	}
	if elements > 9 {
		t.Fatalf("unwrapArray decoded %d children with a budget of 8, want <= 9", elements)
	}
	if len(out) != 0 {
		t.Fatalf("unwrapArray returned %d records on overflow, want 0", len(out))
	}
}

// TestUnwrapArray_UnderBudgetWalksEveryChild is the other half: with budget to
// spare, every child is still visited and unwrapped, so the early-stop logic
// cannot silently truncate a legitimate batch.
func TestUnwrapArray_UnderBudgetWalksEveryChild(t *testing.T) {
	arr := []byte(`[{"a":1},{"b":2},{"c":3}]`)

	st := unwrapState{remaining: maxRecordsPerRequest}
	out, elements, ok := unwrapArray(arr, time.Time{}, 0, &st)

	if !ok || st.overflow {
		t.Fatalf("ok = %v, overflow = %v; want a clean walk", ok, st.overflow)
	}
	if elements != 3 || len(out) != 3 {
		t.Fatalf("elements = %d, records = %d, want 3 and 3", elements, len(out))
	}
	if st.remaining != maxRecordsPerRequest-3 {
		t.Fatalf("remaining = %d, want %d (three records taken)", st.remaining, maxRecordsPerRequest-3)
	}
}

// TestUnwrapArray_TruncatedArrayIsNotUsable guards the shape json.Decoder.More()
// gets wrong on its own: it returns false at a truncated input just as it does at
// a closing bracket, so `[{}` would look like a clean one-element array unless the
// closing token is consumed explicitly. The old whole-value decode failed it, and
// so must this.
func TestUnwrapArray_TruncatedArrayIsNotUsable(t *testing.T) {
	st := unwrapState{remaining: maxRecordsPerRequest}
	if _, _, ok := unwrapArray([]byte(`[{},{}`), time.Time{}, 0, &st); ok {
		t.Fatal("unwrapArray ok = true for a truncated array")
	}
}

// TestExtractRecords_OverBudgetReturnsNoPartialRecords pins the atomic
// invariant at the extraction seam: a body that busts the record budget hands
// back nothing at all, so no caller can accidentally emit a prefix of it.
func TestExtractRecords_OverBudgetReturnsNoPartialRecords(t *testing.T) {
	body := []byte(`{"logs":[` + strings.TrimSuffix(strings.Repeat("{},", maxRecordsPerRequest+1), ",") + `]}`)
	recs, outcome, err := extractRecords(body)
	if !errors.Is(err, errTooManyRecords) {
		t.Fatalf("error = %v, want errTooManyRecords", err)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %d, want 0 (an over-budget batch must return nothing)", len(recs))
	}
	if outcome.corrupt {
		t.Error("outcome.corrupt = true; an over-budget batch is a size refusal, not corruption")
	}
}

// TestCountJSONArrayElements covers the counting pass used by the connection
// budget: it must tolerate the wire-format shapes the receiver really sees, stop
// early once the limit is reached (so it never walks an attacker-sized array),
// and report a non-array as an error so the caller keeps treating it as a decode
// failure rather than a size problem.
func TestCountJSONArrayElements(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		limit   int
		want    int
		wantErr bool
	}{
		{"absent", ``, 10, 0, false},
		{"null", `null`, 10, 0, false},
		{"empty", `[]`, 10, 0, false},
		{"three", `[{},{},{}]`, 10, 3, false},
		{"nested-objects-count-once", `[{"a":[1,2,3]},{"b":{"c":[4]}}]`, 10, 2, false},
		// Early stop: the answer only has to be "at least the limit".
		{"stops-at-limit", `[{},{},{},{},{},{},{},{}]`, 3, 3, false},
		{"object-is-not-an-array", `{"a":1}`, 10, 0, true},
		{"scalar-is-not-an-array", `5`, 10, 0, true},
		{"truncated", `[{},{}`, 10, 2, true},
		{"garbage-element", `[{},@]`, 10, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := countJSONArrayElements(json.RawMessage(tc.raw), tc.limit)
			if (err != nil) != tc.wantErr {
				t.Fatalf("countJSONArrayElements(%q) err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("countJSONArrayElements(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// TestCountConnections sums a flow record's four traffic arrays and stops as
// soon as the running total reaches the caller's limit.
func TestCountConnections(t *testing.T) {
	rec := json.RawMessage(`{"nodeId":"n1",
		"virtualTraffic":[{},{}],
		"subnetTraffic":[{}],
		"exitTraffic":null,
		"physicalTraffic":[{},{},{}]}`)

	kind, sh := classify(rec)
	if kind != kindFlow {
		t.Fatalf("classify kind = %v, want kindFlow", kind)
	}
	n, err := countConnections(sh, 100)
	if err != nil {
		t.Fatalf("countConnections: %v", err)
	}
	if n != 6 {
		t.Fatalf("countConnections = %d, want 6", n)
	}
	// With a limit of 3 the count stops as soon as the budget is met; the caller
	// only needs "at least 3", never the true total.
	n, err = countConnections(sh, 3)
	if err != nil {
		t.Fatalf("countConnections(limit=3): %v", err)
	}
	if n < 3 {
		t.Fatalf("countConnections(limit=3) = %d, want >= 3", n)
	}
}
