package apicontract

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// --- BuildSchema / Flatten: basic shape coverage -----------------------

type flatSample struct {
	Name    string  `json:"name"`
	Count   int     `json:"count"`
	Ratio   float64 `json:"ratio"`
	Enabled bool    `json:"enabled"`
}

func TestBuildSchema_ScalarKinds(t *testing.T) {
	got := Flatten(BuildSchema(reflect.TypeFor[flatSample]()))
	want := map[string]string{
		"name":    "string",
		"count":   "integer",
		"ratio":   "number",
		"enabled": "boolean",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("path %q = %q, want %q (full: %#v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d leaves, want %d: %#v", len(got), len(want), got)
	}
}

type nestedSample struct {
	Inner struct {
		Value string `json:"value"`
	} `json:"inner"`
	List      []string          `json:"list"`
	Items     []flatSample      `json:"items"`
	Map       map[string]string `json:"map"`
	When      time.Time         `json:"when"`
	Ptr       *flatSample       `json:"ptr,omitempty"`
	PtrScalar *string           `json:"ptr_scalar,omitempty"`
	Dyn       any               `json:"dyn,omitempty"`
}

func TestBuildSchema_CompositeKinds(t *testing.T) {
	got := Flatten(BuildSchema(reflect.TypeFor[nestedSample]()))
	cases := map[string]string{
		"inner.value":  "string",
		"list[]":       "string",
		"items[].name": "string",
		"map{}":        "string",
		"when":         "string",
		// A pointer-to-struct field's CHILDREN are indistinguishable from a
		// plain (non-pointer) struct field's — nullability is a property of
		// the object as a whole, and an object is never itself a Flatten leaf
		// (it always has "properties" and gets recursed into), so there is
		// nothing at this path for withNull's marker to attach to.
		"ptr.name": "string",
		// A pointer to a SCALAR, by contrast, IS a leaf — this is where
		// nullability is actually observable in the flattened contract.
		"ptr_scalar": "null|string",
		"dyn":        "any",
	}
	for path, want := range cases {
		if got[path] != want {
			t.Errorf("path %q = %q, want %q (full: %#v)", path, got[path], want, got)
		}
	}
}

func TestBuildSchema_UnexportedFieldsSkipped(t *testing.T) {
	type withUnexported struct {
		Public  string `json:"public"`
		private string //nolint:unused // exercising the skip path
	}
	_ = withUnexported{}.private
	got := Flatten(BuildSchema(reflect.TypeFor[withUnexported]()))
	if _, ok := got["private"]; ok {
		t.Errorf("unexported field leaked into the schema: %#v", got)
	}
	if got["public"] != "string" {
		t.Errorf("public = %q, want string", got["public"])
	}
}

func TestBuildSchema_MissingJSONTagPanics(t *testing.T) {
	type untagged struct {
		Field string
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a field with no json tag")
		}
	}()
	BuildSchema(reflect.TypeFor[untagged]())
}

// TestBuildSchema_PromotesAnonymousEmbeddedFields mirrors flowstore.PairStat
// embedding PairKey: an anonymous struct field with no json tag of its own is
// PROMOTED by encoding/json into the parent object, not nested under the
// embedded type's name. BuildSchema must match that or the published schema
// would describe a shape the wire format never actually sends.
func TestBuildSchema_PromotesAnonymousEmbeddedFields(t *testing.T) {
	type Key struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	type stat struct {
		Key
		Count int `json:"count"`
	}
	got := Flatten(BuildSchema(reflect.TypeFor[stat]()))
	want := map[string]string{"src": "string", "dst": "string", "count": "integer"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("path %q = %q, want %q (full: %#v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d leaves, want %d: %#v — an embedded field must not appear nested under its type name too", len(got), len(want), got)
	}
}

func TestBuildSchema_DashTagSkipped(t *testing.T) {
	type withDash struct {
		Public string `json:"public"`
		Hidden string `json:"-"`
	}
	got := Flatten(BuildSchema(reflect.TypeFor[withDash]()))
	if _, ok := got["Hidden"]; ok {
		t.Errorf("json:\"-\" field leaked into the schema: %#v", got)
	}
	if len(got) != 1 {
		t.Errorf("got %d leaves, want 1: %#v", len(got), got)
	}
}

// --- CompareBaseline: the breaking-change detector, negative-tested -----
//
// These are the "prove the gate actually catches it" tests #323's delivery
// requirements ask for: each one starts from a baseline that matches current
// exactly (a green run), then mutates ONE thing about current and asserts
// CompareBaseline reports it.

func agreeingBaselineAndCurrent() (Baseline, map[string]string) {
	current := map[string]string{
		"a.b": "string",
		"a.c": "integer",
		"d":   "boolean",
	}
	baseline := Baseline{SchemaVersion: 1, Paths: map[string]string{
		"a.b": "string",
		"a.c": "integer",
		"d":   "boolean",
	}}
	return baseline, current
}

func TestCompareBaseline_NoChangesIsClean(t *testing.T) {
	baseline, current := agreeingBaselineAndCurrent()
	if broken := CompareBaseline(baseline, 1, current); len(broken) != 0 {
		t.Errorf("identical baseline/current reported broken: %v", broken)
	}
}

func TestCompareBaseline_AdditiveFieldIsClean(t *testing.T) {
	baseline, current := agreeingBaselineAndCurrent()
	current["a.newfield"] = "string" // a genuinely new field: additive, not breaking
	if broken := CompareBaseline(baseline, 1, current); len(broken) != 0 {
		t.Errorf("an additive new field was reported as breaking: %v", broken)
	}
}

// TestCompareBaseline_RemovedFieldIsCaught proves a field disappearing from
// the response is detected.
func TestCompareBaseline_RemovedFieldIsCaught(t *testing.T) {
	baseline, current := agreeingBaselineAndCurrent()
	delete(current, "a.c") // simulate a field removed from the Go struct
	broken := CompareBaseline(baseline, 1, current)
	if len(broken) != 1 {
		t.Fatalf("removed field: got %d breaks, want exactly 1: %v", len(broken), broken)
	}
	if want := `a.c: REMOVED (baseline promised type "integer")`; broken[0] != want {
		t.Errorf("break message = %q, want %q", broken[0], want)
	}
}

// TestCompareBaseline_RenamedFieldIsCaught proves a rename — the old path
// vanishing, a new one taking its place — is still caught as the old path's
// removal (the new path being present is additive, not a mitigating factor:
// a consumer reading the OLD name breaks regardless of what replaced it).
func TestCompareBaseline_RenamedFieldIsCaught(t *testing.T) {
	baseline, current := agreeingBaselineAndCurrent()
	delete(current, "a.c")
	current["a.count"] = "integer" // renamed a.c -> a.count
	broken := CompareBaseline(baseline, 1, current)
	if len(broken) != 1 {
		t.Fatalf("renamed field: got %d breaks, want exactly 1: %v", len(broken), broken)
	}
	if want := `a.c: REMOVED (baseline promised type "integer")`; broken[0] != want {
		t.Errorf("break message = %q, want %q", broken[0], want)
	}
}

// TestCompareBaseline_ChangedTypeIsCaught proves a field kept its name but
// changed shape (e.g. a duration string became a raw integer) is caught.
func TestCompareBaseline_ChangedTypeIsCaught(t *testing.T) {
	baseline, current := agreeingBaselineAndCurrent()
	current["a.c"] = "string" // was "integer"
	broken := CompareBaseline(baseline, 1, current)
	if len(broken) != 1 {
		t.Fatalf("changed type: got %d breaks, want exactly 1: %v", len(broken), broken)
	}
	if want := `a.c: type changed "integer" -> "string"`; broken[0] != want {
		t.Errorf("break message = %q, want %q", broken[0], want)
	}
}

// TestCompareBaseline_VersionMismatchIsCaught proves the baseline and the
// compiled-in SchemaVersion constant disagreeing is itself flagged, even
// with no path-level change — this is what prevents the two files from
// silently drifting apart (a version bumped without the baseline being
// updated to match, or vice versa).
func TestCompareBaseline_VersionMismatchIsCaught(t *testing.T) {
	baseline, current := agreeingBaselineAndCurrent()
	broken := CompareBaseline(baseline, 2, current) // code says v2, baseline says v1
	if len(broken) != 1 {
		t.Fatalf("version mismatch: got %d breaks, want exactly 1: %v", len(broken), broken)
	}
	want := `schema_version mismatch: baseline records 1, code compiles to 2 — a version bump must update the baseline in the SAME change`
	if broken[0] != want {
		t.Errorf("break message = %q, want %q", broken[0], want)
	}
}

func TestCompareBaseline_MultipleBreaksAllReported(t *testing.T) {
	baseline, current := agreeingBaselineAndCurrent()
	delete(current, "a.b")
	current["d"] = "string" // was boolean
	broken := CompareBaseline(baseline, 1, current)
	if len(broken) != 2 {
		t.Fatalf("got %d breaks, want 2: %v", len(broken), broken)
	}
}

// --- Baseline round-trip -------------------------------------------------

func TestRenderBaseline_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/x.baseline.json"
	want := Baseline{SchemaVersion: 3, Paths: map[string]string{"a": "string"}}
	if err := os.WriteFile(path, RenderBaseline(want), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.SchemaVersion != want.SchemaVersion || got.Paths["a"] != "string" {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}
