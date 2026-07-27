package oas_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/oas"
)

// Boundary variants (#433). SynthesizeBody produced exactly one representative
// body per operation, so every consumed operation was decoded once, against a
// single happy-path shape. The boundary shapes that actually break decoders —
// null, empty containers, nulled nullable fields, extreme values, an unknown enum
// member, an additive field, a wrong container shape — were covered for ONE of
// the eighteen consumed operations, by hand, in TestFuzz_EdgeVariants.

const variantSchema = `{
  "openapi": "3.1.0",
  "paths": {"/d": {"get": {
    "operationId": "listTailnetDevices",
    "responses": {"200": {"content": {"application/json": {"schema": {
      "type": "object",
      "properties": {"devices": {"type": "array", "items": {
        "type": "object",
        "properties": {
          "id":       {"type": "string"},
          "created":  {"type": "string", "format": "date-time"},
          "count":    {"type": "integer"},
          "bigCount": {"type": "integer", "format": "int64"},
          "ratio":    {"type": "number"},
          "online":   {"type": "boolean"},
          "kind":     {"type": "string", "enum": ["a", "b"]},
          "note":     {"type": ["string", "null"]}
        }
      }}}
    }}}}}
  }}}
}`

// bareArraySchema is the other real top-level shape: listUserInvites and
// listDeviceInvites return a bare array, which inverts the empty-container and
// wrong-shape variants.
const bareArraySchema = `{
  "openapi": "3.1.0",
  "paths": {"/d": {"get": {
    "operationId": "listTailnetDevices",
    "responses": {"200": {"content": {"application/json": {"schema": {
      "type": "array",
      "items": {"type": "object", "properties": {"id": {"type": "string"}}}
    }}}}}
  }}}
}`

func variantResponse(t *testing.T) oas.Schema {
	t.Helper()
	spec, err := oas.ParseSpec([]byte(variantSchema))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	return spec.Ops["listTailnetDevices"].Response
}

func variantsByKind(t *testing.T) map[oas.BoundaryKind]oas.Variant {
	t.Helper()
	out := map[oas.BoundaryKind]oas.Variant{}
	for _, v := range oas.BoundaryVariants(variantResponse(t), 8) {
		if _, dupe := out[v.Kind]; dupe {
			t.Fatalf("kind %q emitted twice", v.Kind)
		}
		out[v.Kind] = v
	}
	return out
}

// Every kind must be produced, and every body must be valid JSON — a variant that
// is malformed at the JSON level tests the JSON parser, not our decoders.
func TestBoundaryVariants_CoversEveryKindWithValidJSON(t *testing.T) {
	got := variantsByKind(t)

	for _, kind := range oas.AllBoundaryKinds() {
		v, ok := got[kind]
		if !ok {
			t.Errorf("kind %q was not produced", kind)
			continue
		}
		var any any
		if err := json.Unmarshal(v.Body, &any); err != nil {
			t.Errorf("kind %q produced invalid JSON: %v\n%s", kind, err, v.Body)
		}
		if v.MustDecode == v.MustError {
			t.Errorf("kind %q has MustDecode=%v MustError=%v — every variant must be exactly one "+
				"of 'the decoder has to accept this' or 'the decoder has to reject this', or the "+
				"test asserts nothing", kind, v.MustDecode, v.MustError)
		}
	}
	if len(got) != len(oas.AllBoundaryKinds()) {
		t.Errorf("produced %d kinds, AllBoundaryKinds lists %d", len(got), len(oas.AllBoundaryKinds()))
	}
}

func TestBoundaryVariants_Null(t *testing.T) {
	v := variantsByKind(t)[oas.BoundaryNull]
	if strings.TrimSpace(string(v.Body)) != "null" {
		t.Errorf("null variant body = %s, want null", v.Body)
	}
	if !v.MustDecode {
		t.Error("a null body must be accepted: Go unmarshals it as a no-op and every consumed " +
			"operation tolerates it today")
	}
}

// The empty container must MATCH the response's own top-level shape. Serving `[]`
// to an object-shaped response is a wrong shape, not an empty one, and the two
// carry opposite expectations.
func TestBoundaryVariants_EmptyContainerMatchesTheTopLevelShape(t *testing.T) {
	v := variantsByKind(t)[oas.BoundaryEmptyContainer]
	if string(v.Body) != "{}" {
		t.Errorf("empty-container variant for an object response = %s, want {}", v.Body)
	}
	if !v.MustDecode {
		t.Error("an empty container of the right shape must be accepted")
	}

	// Bare-array responses (listUserInvites, listDeviceInvites) invert both.
	spec, err := oas.ParseSpec([]byte(bareArraySchema))
	if err != nil {
		t.Fatalf("ParseSpec(array shape): %v", err)
	}
	for _, av := range oas.BoundaryVariants(spec.Ops["listTailnetDevices"].Response, 8) {
		switch av.Kind {
		case oas.BoundaryEmptyContainer:
			if string(av.Body) != "[]" {
				t.Errorf("empty-container variant for an array response = %s, want []", av.Body)
			}
		case oas.BoundaryWrongShape:
			if string(av.Body) != "{}" {
				t.Errorf("wrong-shape variant for an array response = %s, want {}", av.Body)
			}
		}
	}
}

// The wrong-shape variant must be REJECTED. A decoder that accepts `[]` where an
// object is expected reports zero devices instead of surfacing that the response
// shape moved — silence is the worst outcome available here.
func TestBoundaryVariants_WrongShapeMustBeRejected(t *testing.T) {
	got := variantsByKind(t)
	for _, kind := range []oas.BoundaryKind{oas.BoundaryWrongShape, oas.BoundaryScalarBody} {
		v := got[kind]
		if !v.MustError {
			t.Errorf("%s must be required to fail, not merely permitted to", kind)
		}
	}
	if string(got[oas.BoundaryWrongShape].Body) != "[]" {
		t.Errorf("wrong-shape variant for an object response = %s, want []",
			got[oas.BoundaryWrongShape].Body)
	}
}

// Nullable fields nulled out: spec-derived, not invented. The vendored spec spells
// nullability as a 3.1 type array, which #431 taught the parser to read, so this
// boundary exists only because that landed.
func TestBoundaryVariants_MinimalValuesNullsNullableFieldsAndEmptiesTheRest(t *testing.T) {
	v := variantsByKind(t)[oas.BoundaryMinimalValues]
	device := firstDevice(t, v.Body)

	if got, ok := device["note"]; !ok || got != nil {
		t.Errorf("note = %v (present=%v), want JSON null — it is the one nullable field in the "+
			"fixture and nulling it is what makes this boundary spec-derived", device["note"], ok)
	}
	if got := device["id"]; got != "" {
		t.Errorf("id = %v, want the empty string", got)
	}
	if got := device["count"]; got != float64(0) {
		t.Errorf("count = %v, want 0", got)
	}
	if got := device["online"]; got != false {
		t.Errorf("online = %v, want false", got)
	}
	// A date-time field keeps a VALID timestamp. An empty string there is a
	// distinct known quirk (external devices send "" for created, #48) covered
	// where it actually occurs; asserting it on every timestamp field would be
	// asserting wire behavior upstream does not exhibit.
	if got, _ := device["created"].(string); got == "" {
		t.Errorf("created = %q, want a valid RFC3339 timestamp", got)
	}
}

func TestBoundaryVariants_ExtremeValues(t *testing.T) {
	v := variantsByKind(t)[oas.BoundaryExtremeValues]
	device := firstDevice(t, v.Body)

	if s, _ := device["id"].(string); len(s) < 256 {
		t.Errorf("id length = %d, want a long string", len(s))
	}
	if got, _ := device["count"].(float64); got != math.MaxInt32 {
		t.Errorf("count = %v, want MaxInt32 — a plain `integer` gets the int32 ceiling because "+
			"nothing in the spec promises the Go field is 64-bit", got)
	}
	if got, _ := device["bigCount"].(float64); got != math.MaxInt64 {
		t.Errorf("bigCount = %v, want MaxInt64 — `format: int64` does promise it", got)
	}
	// Arrays carry more than one element, so a decoder that only ever reads the
	// first is exercised past it.
	if n := len(devicesArray(t, v.Body)); n < 2 {
		t.Errorf("devices has %d element(s), want at least 2", n)
	}
}

// An unknown enum member must decode. Every enum here is mapped through a
// normalizer with an "other" bucket, so a new upstream value must not be fatal —
// and a value that IS in the enum would test nothing.
func TestBoundaryVariants_UnknownEnum(t *testing.T) {
	v := variantsByKind(t)[oas.BoundaryUnknownEnum]
	device := firstDevice(t, v.Body)

	got, _ := device["kind"].(string)
	if got == "a" || got == "b" || got == "" {
		t.Errorf("kind = %q, want a value outside the declared enum [a b]", got)
	}
	// And a RECOGNIZABLE one. "x" is also outside the enum, so a generic filler
	// would satisfy the check above while making a failure report ambiguous about
	// which boundary produced the body.
	if !strings.Contains(got, "ts2otel") {
		t.Errorf("kind = %q, want a self-describing sentinel so a failing report says what the "+
			"variant was doing", got)
	}
	if !v.MustDecode {
		t.Error("an unknown enum member must be accepted")
	}
}

// An additive field must decode, at every level. Upstream adds response fields
// routinely; a decoder that rejected them would break on a benign release.
func TestBoundaryVariants_AdditiveFieldAtEveryLevel(t *testing.T) {
	v := variantsByKind(t)[oas.BoundaryAdditiveField]

	var top map[string]any
	if err := json.Unmarshal(v.Body, &top); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, v.Body)
	}
	if !hasUnknownKey(top) {
		t.Errorf("no additive key at the top level: %s", v.Body)
	}
	if !hasUnknownKey(firstDevice(t, v.Body)) {
		t.Errorf("no additive key inside the nested object — a top-level-only injection would "+
			"miss exactly the case that breaks a strict nested decoder: %s", v.Body)
	}
}

func hasUnknownKey(obj map[string]any) bool {
	for k := range obj {
		if strings.HasPrefix(k, "ts2otel") {
			return true
		}
	}
	return false
}

func devicesArray(t *testing.T, body []byte) []any {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	arr, ok := top["devices"].([]any)
	if !ok {
		t.Fatalf("devices is not an array: %s", body)
	}
	return arr
}

func firstDevice(t *testing.T, body []byte) map[string]any {
	t.Helper()
	arr := devicesArray(t, body)
	if len(arr) == 0 {
		t.Fatalf("devices is empty: %s", body)
	}
	obj, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("devices[0] is not an object: %s", body)
	}
	return obj
}

// The variant set must be byte-identical across runs: it feeds a table-driven test
// whose failures name a specific body, and a body that changes between runs makes
// a reported failure unreproducible.
func TestBoundaryVariants_AreDeterministic(t *testing.T) {
	first := oas.BoundaryVariants(variantResponse(t), 8)
	for range 15 {
		got := oas.BoundaryVariants(variantResponse(t), 8)
		if len(got) != len(first) {
			t.Fatalf("variant count changed: %d then %d", len(first), len(got))
		}
		for i := range got {
			if got[i].Kind != first[i].Kind || string(got[i].Body) != string(first[i].Body) {
				t.Fatalf("variant %d changed:\n  %s %s\n  %s %s",
					i, first[i].Kind, first[i].Body, got[i].Kind, got[i].Body)
			}
		}
	}
}
