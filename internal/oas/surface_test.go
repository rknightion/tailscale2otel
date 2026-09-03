package oas_test

import (
	"os"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/oas"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi/contract"
)

// Drift detection stopped at the response body: the parser modeled the 200
// application/json schema and nothing else about the request surface (#432).
// Three whole dimensions of the contract were therefore invisible.
//
// The measurements below are of the VENDORED spec, taken before any code was
// written, so this test is sized against reality rather than against the OpenAPI
// standard:
//
//   - 34 parameters across the 18 consumed operations — 20 path, 13 query, 1
//     header. **30 of the 34 are $refs into components.parameters, and 30 are
//     declared at the PATH-ITEM level, not on the operation.** A parser reading
//     only `operation.parameters` and only inline objects sees FOUR of the
//     thirty-four, which is why both merges are asserted explicitly below.
//   - 2 parameters carry a `default` (listUsers `type`="member", `role`="all").
//     A default flipping upstream silently changes what we collect on every
//     poll, while every response field still decodes — the definition of drift
//     no response-body diff can see.
//   - 3 carry an enum; one parameter's schema is itself a $ref (logType →
//     components.schemas.LogType), so parameter schemas need the same
//     components resolution response schemas get.
//   - every consumed operation declares exactly ONE success status, "200", and
//     getPolicyFile alone offers a second response media type
//     (application/hujson) beside the application/json we decode.

const surfaceSpec = `{
  "openapi": "3.1.0",
  "paths": {
    "/tailnet/{tailnet}/devices": {
      "parameters": [
        {"$ref": "#/components/parameters/tailnet"},
        {"$ref": "#/components/parameters/fields"}
      ],
      "get": {
        "operationId": "listTailnetDevices",
        "parameters": [
          {"name": "Accept", "in": "header", "required": false, "schema": {"type": "string"}},
          {"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}
        ],
        "responses": {
          "200": {"content": {
            "application/json": {"schema": {"type": "object", "properties": {"ok": {"type": "boolean"}}}},
            "application/hujson": {"schema": {"type": "string"}}
          }},
          "404": {"description": "nope"}
        }
      }
    }
  },
  "components": {
    "parameters": {
      "tailnet": {"name": "tailnet", "in": "path", "required": true, "schema": {"type": "string"}},
      "fields": {"name": "fields", "in": "query", "required": false,
        "schema": {"type": "string", "enum": ["all", "default"], "default": "default"}}
    },
    "schemas": {
      "Kind": {"type": "string", "enum": ["a", "b"]}
    }
  }
}`

func surfaceOp(t *testing.T, doc string) oas.Operation {
	t.Helper()
	spec, err := oas.ParseSpec([]byte(doc))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op, ok := spec.Ops["listTailnetDevices"]
	if !ok {
		t.Fatal("listTailnetDevices was not parsed")
	}
	return op
}

func paramByKey(ps []oas.Param, in, name string) (oas.Param, bool) {
	for _, p := range ps {
		if p.In == in && p.Name == name {
			return p, true
		}
	}
	return oas.Param{}, false
}

// Parameters must be the MERGE of the path-item level and the operation level,
// with $refs into components.parameters resolved. Dropping either source, or
// leaving $refs unresolved, loses most of the real spec's parameters.
func TestParseSpec_ParametersMergePathItemAndOperationAndResolveRefs(t *testing.T) {
	op := surfaceOp(t, surfaceSpec)

	if len(op.Parameters) != 4 {
		t.Fatalf("Parameters = %d, want 4 (2 path-item $refs + 2 inline operation-level): %+v",
			len(op.Parameters), op.Parameters)
	}

	for _, tc := range []struct {
		in, name     string
		wantRequired bool
		wantType     string
		wantDefault  string
		wantEnum     []string
	}{
		{in: "path", name: "tailnet", wantRequired: true, wantType: "string"},
		{in: "query", name: "fields", wantType: "string", wantDefault: `"default"`, wantEnum: []string{"all", "default"}},
		{in: "header", name: "Accept", wantType: "string"},
		{in: "query", name: "kind", wantType: "string", wantEnum: []string{"a", "b"}},
	} {
		p, ok := paramByKey(op.Parameters, tc.in, tc.name)
		if !ok {
			t.Errorf("parameter %s:%s missing", tc.in, tc.name)
			continue
		}
		if p.Required != tc.wantRequired {
			t.Errorf("%s:%s Required = %v, want %v", tc.in, tc.name, p.Required, tc.wantRequired)
		}
		if p.Schema.Type != tc.wantType {
			t.Errorf("%s:%s Schema.Type = %q, want %q — a parameter schema that is itself a $ref "+
				"needs the same components resolution a response schema gets",
				tc.in, tc.name, p.Schema.Type, tc.wantType)
		}
		if p.Default != tc.wantDefault {
			t.Errorf("%s:%s Default = %q, want %q", tc.in, tc.name, p.Default, tc.wantDefault)
		}
		if len(p.Schema.Enum) != len(tc.wantEnum) {
			t.Errorf("%s:%s Enum = %v, want %v", tc.in, tc.name, p.Schema.Enum, tc.wantEnum)
		}
	}
}

// Parameters must be in a deterministic order, or a JSON drift report reorders
// itself between runs and the deduplicated tracking issue churns.
func TestParseSpec_ParametersAreSortedDeterministically(t *testing.T) {
	first := surfaceOp(t, surfaceSpec).Parameters
	for i := range 5 {
		got := surfaceOp(t, surfaceSpec).Parameters
		if len(got) != len(first) {
			t.Fatalf("run %d: length changed", i)
		}
		for j := range got {
			if got[j].In != first[j].In || got[j].Name != first[j].Name {
				t.Fatalf("run %d position %d: %s:%s, first run had %s:%s — parameter order "+
					"must not depend on Go map iteration order",
					i, j, got[j].In, got[j].Name, first[j].In, first[j].Name)
			}
		}
	}
	// And the order must be (In, Name) ascending, not merge order.
	for i := 1; i < len(first); i++ {
		prev, cur := first[i-1], first[i]
		if prev.In > cur.In || (prev.In == cur.In && prev.Name >= cur.Name) {
			t.Errorf("Parameters not sorted by (In, Name): %s:%s before %s:%s",
				prev.In, prev.Name, cur.In, cur.Name)
		}
	}
}

// An operation-level parameter with the same (in, name) as a path-item one
// overrides it — that is what OpenAPI says, and taking the path-item copy would
// read the wrong requiredness.
func TestParseSpec_OperationParameterOverridesPathItemParameter(t *testing.T) {
	overridden := replaceOnce(surfaceSpec,
		`{"name": "Accept", "in": "header", "required": false, "schema": {"type": "string"}}`,
		`{"name": "fields", "in": "query", "required": true, "schema": {"type": "integer"}}`)
	op := surfaceOp(t, overridden)

	if len(op.Parameters) != 3 {
		t.Fatalf("Parameters = %d, want 3 (the duplicate must collapse, not appear twice): %+v",
			len(op.Parameters), op.Parameters)
	}
	p, ok := paramByKey(op.Parameters, "query", "fields")
	if !ok {
		t.Fatal("query:fields missing")
	}
	if !p.Required || p.Schema.Type != "integer" {
		t.Errorf("query:fields = {Required:%v Type:%q}, want {true integer} — the operation-level "+
			"declaration must win over the path-item one", p.Required, p.Schema.Type)
	}
}

// The success-status SET must be modeled, not assumed. parseResponseSchema
// hardcoded responses["200"]; with the set recorded, a 200 that turns into a 201
// upstream is reportable as itself rather than as a wall of removed fields.
func TestParseSpec_SuccessStatusesAndResponseMediaTypes(t *testing.T) {
	op := surfaceOp(t, surfaceSpec)

	if len(op.SuccessStatuses) != 1 || op.SuccessStatuses[0] != "200" {
		t.Errorf("SuccessStatuses = %v, want [200] — 404 is not a success and must not be included",
			op.SuccessStatuses)
	}
	want := []string{"application/hujson", "application/json"}
	if len(op.ResponseMediaTypes) != len(want) {
		t.Fatalf("ResponseMediaTypes = %v, want %v", op.ResponseMediaTypes, want)
	}
	for i := range want {
		if op.ResponseMediaTypes[i] != want[i] {
			t.Errorf("ResponseMediaTypes[%d] = %q, want %q (sorted)", i, op.ResponseMediaTypes[i], want[i])
		}
	}
	// The response schema still resolves off application/json specifically.
	if _, ok := op.Response.Properties["ok"]; !ok {
		t.Errorf("Response lost its application/json schema: %+v", op.Response)
	}
}

// A success response that is not literally "200" must still be selected, or the
// response schema silently becomes empty and every field reads as removed.
func TestParseSpec_NonTwoHundredSuccessResponseIsSelected(t *testing.T) {
	moved := replaceOnce(surfaceSpec, `"200": {"content": {`, `"201": {"content": {`)
	op := surfaceOp(t, moved)

	if len(op.SuccessStatuses) != 1 || op.SuccessStatuses[0] != "201" {
		t.Errorf("SuccessStatuses = %v, want [201]", op.SuccessStatuses)
	}
	if _, ok := op.Response.Properties["ok"]; !ok {
		t.Errorf("Response is empty for a 201-only operation: %+v — hardcoding responses[\"200\"] "+
			"turns a success-status change into a phantom removal of every response field", op.Response)
	}
}

// Path is carried so a drift report can name WHERE the change is, which is the
// human-readable evidence a maintainer needs to check it upstream (#432).
func TestParseSpec_OperationCarriesItsPath(t *testing.T) {
	op := surfaceOp(t, surfaceSpec)
	if op.Path != "/tailnet/{tailnet}/devices" {
		t.Errorf("Path = %q, want /tailnet/{tailnet}/devices", op.Path)
	}
}

// Request bodies: no consumed operation has one today (every consumed operation
// is a GET), so this is asserted on a synthetic POST-shaped GET to keep the
// classification honest rather than untested. The reachable case is guarded by
// TestVendoredSpec_NoConsumedOperationHasARequestBody below.
func TestParseSpec_RequestMediaTypesAreModeled(t *testing.T) {
	withBody := replaceOnce(surfaceSpec,
		`"operationId": "listTailnetDevices",`,
		`"operationId": "listTailnetDevices",
         "requestBody": {"content": {
            "application/json": {"schema": {"type": "object", "required": ["src"]}},
            "application/hujson": {"schema": {"type": "string"}}
         }},`)
	op := surfaceOp(t, withBody)

	want := []string{"application/hujson", "application/json"}
	if len(op.RequestMediaTypes) != len(want) {
		t.Fatalf("RequestMediaTypes = %v, want %v", op.RequestMediaTypes, want)
	}
	for i := range want {
		if op.RequestMediaTypes[i] != want[i] {
			t.Errorf("RequestMediaTypes[%d] = %q, want %q (sorted)", i, op.RequestMediaTypes[i], want[i])
		}
	}
	if len(op.RequestRequired) != 1 || op.RequestRequired[0] != "src" {
		t.Errorf("RequestRequired = %v, want [src]", op.RequestRequired)
	}
}

// ---------------------------------------------------------------------------
// Pins against the real vendored spec. A parser that returned empty slices
// would satisfy every fixture-based assertion above that only checks a shape;
// these fail unless real parameters actually come out of the real document.
// ---------------------------------------------------------------------------

func vendoredSpec(t *testing.T) *oas.Spec {
	t.Helper()
	b, err := os.ReadFile("../../spec/tailscale-api.json")
	if err != nil {
		t.Fatalf("read vendored spec: %v", err)
	}
	spec, err := oas.ParseSpec(b)
	if err != nil {
		t.Fatalf("ParseSpec(vendored): %v", err)
	}
	return spec
}

// The counts here were measured from spec/tailscale-api.json. They are pinned
// deliberately: if a spec refresh changes them the diff is the point, and a
// parser regression that silently drops a source of parameters cannot hide.
func TestVendoredSpec_ConsumedOperationParameterCensus(t *testing.T) {
	spec := vendoredSpec(t)

	byLocation := map[string]int{}
	total, withDefault, withEnum, required := 0, 0, 0, 0
	refSchemaSeen := false
	for _, id := range contract.ConsumedOpIDs() {
		op, ok := spec.Ops[id]
		if !ok {
			t.Errorf("consumed operation %q absent from the vendored spec's modeled operations", id)
			continue
		}
		for _, p := range op.Parameters {
			total++
			byLocation[p.In]++
			if p.Default != "" {
				withDefault++
			}
			if len(p.Schema.Enum) > 0 {
				withEnum++
			}
			if p.Required {
				required++
			}
			if p.Name == "logType" && p.Schema.Type == "string" && len(p.Schema.Enum) > 0 {
				refSchemaSeen = true // its schema is a $ref into components.schemas
			}
		}
	}

	if total != 37 {
		t.Errorf("consumed operations expose %d parameters, want 37 — reading only "+
			"operation.parameters yields 4 and leaving $refs unresolved yields 4",
			total)
	}
	for loc, want := range map[string]int{"path": 21, "query": 15, "header": 1} {
		if byLocation[loc] != want {
			t.Errorf("%s parameters = %d, want %d", loc, byLocation[loc], want)
		}
	}
	if withDefault != 3 {
		t.Errorf("parameters carrying a default = %d, want 3 (listUsers type/role, listOrganizationTailnets limit)", withDefault)
	}
	// FOUR, not the three a naive scan reports: listUsers `type` and `role` and
	// the shared `fields` carry an inline enum, and `logType`'s enum arrives only
	// once its $ref into components.schemas is resolved. My own pre-implementation
	// probe did not resolve that $ref and said three; this census is what
	// corrected it.
	if withEnum != 4 {
		t.Errorf("parameters carrying an enum = %d, want 4 (fields, type, role, and logType "+
			"via its $ref'd schema)", withEnum)
	}
	if required != 26 {
		t.Errorf("required parameters = %d, want 26", required)
	}
	if !refSchemaSeen {
		t.Error("the logType parameter's $ref schema did not resolve to an enum of strings")
	}
}

// Every consumed operation returns exactly one success status, and every one of
// them offers the application/json we decode. getPolicyFile alone offers a
// second media type.
func TestVendoredSpec_ConsumedSuccessStatusesAndMediaTypes(t *testing.T) {
	spec := vendoredSpec(t)

	extraMediaTypes := map[string][]string{}
	for _, id := range contract.ConsumedOpIDs() {
		op, ok := spec.Ops[id]
		if !ok {
			continue
		}
		if len(op.SuccessStatuses) != 1 || op.SuccessStatuses[0] != "200" {
			t.Errorf("%s SuccessStatuses = %v, want exactly [200]", id, op.SuccessStatuses)
		}
		hasJSON := false
		for _, mt := range op.ResponseMediaTypes {
			if mt == "application/json" {
				hasJSON = true
				continue
			}
			extraMediaTypes[id] = append(extraMediaTypes[id], mt)
		}
		if !hasJSON {
			t.Errorf("%s does not offer application/json, which every decoder here assumes", id)
		}
	}

	if len(extraMediaTypes) != 1 || len(extraMediaTypes["getPolicyFile"]) != 1 ||
		extraMediaTypes["getPolicyFile"][0] != "application/hujson" {
		t.Errorf("non-JSON success media types = %v, want only getPolicyFile:[application/hujson]",
			extraMediaTypes)
	}
}

// Records the premise #432 was filed on. The issue asked for request-body drift
// on "the safe consumed POST" — validateAndTestPolicyFile — but its disposition
// is `implement`, not `consumed`, so no consumed operation has a request body
// and none can, while Spec.Ops stays GET-only. If this ever fails, the
// request-body classification stops being latent and
// TestClassify_ConsumedOperationAbsentFromBothSpecs is the guard that says so.
func TestVendoredSpec_NoConsumedOperationHasARequestBody(t *testing.T) {
	spec := vendoredSpec(t)
	for _, id := range contract.ConsumedOpIDs() {
		op, ok := spec.Ops[id]
		if !ok {
			continue
		}
		if len(op.RequestMediaTypes) > 0 || len(op.RequestRequired) > 0 {
			t.Errorf("%s carries a request body (%v / %v) — request-body drift is no longer "+
				"latent and needs a live assertion, not this guard",
				id, op.RequestMediaTypes, op.RequestRequired)
		}
	}
}
