package oas_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/oas"
)

// The OpenAPI parser reads a document tailscale2otel did not write: the daily
// drift lane fetches the LIVE spec from api.tailscale.com and hands it straight
// to ParseSpec (#434). A crash there is a broken drift lane, which fails silently
// in the sense that matters — the lane stops reporting real drift.
//
// $ref resolution is recursive and the document controls the graph, so the
// interesting inputs are cyclic refs, self-refs, refs to nothing, and depth. The
// parser's defenses are a visited set and maxRefDepth; these targets exist to
// prove no document defeats them.

// specSeeds are the shapes that matter, not arbitrary noise: 3.1 type arrays,
// typed maps, $ref cycles (direct, mutual and self), a dangling $ref, an operation
// with no id, non-GET verbs, a non-200 success status, and multiple media types.
var specSeeds = []string{
	`{"openapi":"3.1.0","paths":{"/d":{"get":{"operationId":"o","responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"a":{"type":["string","null"]}}}}}}}}}}}`,
	// Direct cycle: A -> A.
	`{"openapi":"3.1.0","paths":{"/d":{"get":{"operationId":"o","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}}},
	  "components":{"schemas":{"A":{"type":"object","properties":{"self":{"$ref":"#/components/schemas/A"}}}}}}`,
	// Mutual cycle: A -> B -> A.
	`{"openapi":"3.1.0","paths":{"/d":{"get":{"operationId":"o","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}}},
	  "components":{"schemas":{
	    "A":{"type":"object","properties":{"b":{"$ref":"#/components/schemas/B"}}},
	    "B":{"type":"object","properties":{"a":{"$ref":"#/components/schemas/A"}}}}}}`,
	// Dangling $ref, and a $ref that is not a components pointer at all.
	`{"paths":{"/d":{"get":{"operationId":"o","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Missing"}}}}}}}}}`,
	`{"paths":{"/d":{"get":{"operationId":"o","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"https://example.com/x.json#/Foo"}}}}}}}}}`,
	// Parameters: path-item level, $ref'd, a $ref to nothing, and one with no name.
	`{"paths":{"/d":{"parameters":[{"$ref":"#/components/parameters/p"},{"$ref":"#/components/parameters/gone"},{"in":"query"}],
	  "get":{"operationId":"o","responses":{"200":{"description":"ok"}}}}},
	  "components":{"parameters":{"p":{"name":"n","in":"query","schema":{"type":"string","enum":["a"],"default":"a"}}}}}`,
	// A typed map whose value is itself a $ref, and an array with no items.
	`{"paths":{"/d":{"get":{"operationId":"o","responses":{"201":{"content":{
	    "application/json":{"schema":{"type":"object","additionalProperties":{"$ref":"#/components/schemas/A"}}},
	    "application/hujson":{"schema":{"type":"string"}}}}}}}},
	  "components":{"schemas":{"A":{"type":"array"}}}}`,
	// Non-GET verbs, an operation with no operationId, a duplicate operationId.
	`{"paths":{"/a":{"post":{"operationId":"p","responses":{"200":{"description":"ok"}}},
	  "get":{"responses":{"200":{"description":"ok"}}}},
	  "/b":{"get":{"operationId":"p","responses":{"200":{"description":"ok"}}}}}}`,
	// Degenerate documents.
	`{}`, `{"paths":{}}`, `{"paths":null}`, `{"paths":"x"}`, `{"components":{"schemas":{"A":1}}}`,
	`null`, `[]`, `0`, ``,
}

// FuzzParseSpec asserts the parser terminates and stays inside its own bounds
// whatever the document says.
func FuzzParseSpec(f *testing.F) {
	for _, s := range specSeeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, doc []byte) {
		spec, err := oas.ParseSpec(doc)
		if err != nil {
			return // not a JSON document; the drift lane reports the parse failure
		}

		for id, op := range spec.Ops {
			if op.ID != id {
				t.Fatalf("Ops key %q holds an operation with ID %q", id, op.ID)
			}
			if op.Method != "get" {
				t.Fatalf("%s: Ops must be GET-only, got %q — the fuzz and decode harnesses are "+
					"built on that invariant", id, op.Method)
			}

			// Schema depth must stay inside maxRefDepth. A document cannot be allowed
			// to choose the recursion depth of anything that walks these schemas —
			// flattenPaths, SynthesizeBody and BoundaryVariants all do.
			if d := schemaDepth(op.Response, 0); d > oas.MaxRefDepth {
				t.Fatalf("%s: response schema is %d deep, past the %d cap", id, d, oas.MaxRefDepth)
			}

			// Parameters must be sorted and unique on (In, Name), because the drift
			// report's determinism depends on it.
			for i := 1; i < len(op.Parameters); i++ {
				prev, cur := op.Parameters[i-1], op.Parameters[i]
				if prev.In > cur.In || (prev.In == cur.In && prev.Name >= cur.Name) {
					t.Fatalf("%s: parameters not strictly sorted: %s before %s", id, prev.Key(), cur.Key())
				}
			}
			for _, p := range op.Parameters {
				if p.Name == "" {
					t.Fatalf("%s: a parameter with no name was kept; it cannot be tracked", id)
				}
				if p.Default != "" && !json.Valid([]byte(p.Default)) {
					t.Fatalf("%s: parameter %s default %q is not valid JSON", id, p.Key(), p.Default)
				}
			}

			// Success statuses must be 2xx and sorted; anything else means an error
			// response was mistaken for the body we decode.
			for i, code := range op.SuccessStatuses {
				if len(code) != 3 || code[0] != '2' {
					t.Fatalf("%s: %q is in SuccessStatuses", id, code)
				}
				if i > 0 && op.SuccessStatuses[i-1] >= code {
					t.Fatalf("%s: SuccessStatuses not sorted: %v", id, op.SuccessStatuses)
				}
			}

			// Everything downstream must survive the parsed schema and emit valid
			// JSON — SynthesizeBody and BoundaryVariants feed real decoders.
			if !json.Valid(oas.SynthesizeBody(op.Response, 8)) {
				t.Fatalf("%s: SynthesizeBody produced invalid JSON", id)
			}
			for _, v := range oas.BoundaryVariants(op.Response, 8) {
				if !json.Valid(v.Body) {
					t.Fatalf("%s: boundary %s produced invalid JSON: %s", id, v.Kind, v.Body)
				}
			}
		}
	})
}

// FuzzClassifySelfDiff asserts the property that makes the drift lane trustworthy:
// a spec compared with ITSELF reports nothing. A classifier that finds phantom
// drift is worse than one that finds none — the daily lane opens a tracking issue
// on every run, and a lane that always cries wolf gets ignored, so a real breaking
// change then arrives in a stream of noise.
func FuzzClassifySelfDiff(f *testing.F) {
	for _, s := range specSeeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, doc []byte) {
		spec, err := oas.ParseSpec(doc)
		if err != nil {
			return
		}
		ids := make([]string, 0, len(spec.Ops))
		for id := range spec.Ops {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			return
		}

		if changes := oas.Classify(spec, spec, ids); len(changes) != 0 {
			t.Fatalf("a spec compared with itself reported %d change(s): %+v", len(changes), changes)
		}

		// Two SEPARATE parses of the same bytes must also agree, so nothing in the
		// parse depends on map iteration order.
		other, err := oas.ParseSpec(doc)
		if err != nil {
			t.Fatalf("the same document parsed once and failed once: %v", err)
		}
		if changes := oas.Classify(spec, other, ids); len(changes) != 0 {
			t.Fatalf("two parses of the same document differ: %+v", changes)
		}
	})
}

// FuzzClassifyAgainstVendored diffs a fuzzed candidate against the real committed
// baseline, on the real consumed operation set — the exact call the daily lane
// makes. Every emitted change must be well-formed and ranked, because an unranked
// change is invisible to HasActionable and therefore never reported.
func FuzzClassifyAgainstVendored(f *testing.F) {
	baselineBytes, err := os.ReadFile("../../spec/tailscale-api.json")
	if err != nil {
		f.Fatalf("read vendored spec: %v", err)
	}
	baseline, err := oas.ParseSpec(baselineBytes)
	if err != nil {
		f.Fatalf("ParseSpec(vendored): %v", err)
	}

	for _, s := range specSeeds {
		f.Add([]byte(s))
	}
	f.Add(baselineBytes) // the identity case: the live spec matching the baseline

	ids := make([]string, 0, len(baseline.Ops))
	for id := range baseline.Ops {
		ids = append(ids, id)
	}

	f.Fuzz(func(t *testing.T, doc []byte) {
		candidate, err := oas.ParseSpec(doc)
		if err != nil {
			return
		}

		changes := oas.Classify(baseline, candidate, ids)
		var lastRank = -1
		for _, c := range changes {
			if c.OpID == "" || c.Kind == "" || c.Detail == "" {
				t.Fatalf("malformed change: %+v", c)
			}
			switch c.Severity {
			case oas.Breaking, oas.Warning, oas.Info:
			default:
				t.Fatalf("change %+v carries severity %q, which HasActionable cannot see", c, c.Severity)
			}
			if oas.SeverityOf(c.Kind) == "" {
				t.Fatalf("kind %q has no entry in the severity policy", c.Kind)
			}
			// Sorted by severity rank, most urgent first.
			rank := map[oas.Severity]int{oas.Breaking: 0, oas.Warning: 1, oas.Info: 2}[c.Severity]
			if rank < lastRank {
				t.Fatalf("changes are not sorted by severity: %+v came after rank %d", c, lastRank)
			}
			lastRank = rank
			// A change must never leak a whole schema into the report body.
			if len(c.Detail) > 4096 {
				t.Fatalf("change detail is %d bytes; a drift report must stay readable", len(c.Detail))
			}
		}
	})
}

// schemaDepth measures how deep a resolved schema goes, following properties,
// items and typed-map values.
func schemaDepth(s oas.Schema, depth int) int {
	if depth > 64 {
		return depth // already past anything the cap permits; stop walking
	}
	deepest := depth
	track := func(child oas.Schema) {
		if d := schemaDepth(child, depth+1); d > deepest {
			deepest = d
		}
	}
	for _, child := range s.Properties {
		track(child)
	}
	if s.Items != nil {
		track(*s.Items)
	}
	if s.AdditionalProperties != nil {
		track(*s.AdditionalProperties)
	}
	return deepest
}

// A dangling or non-local $ref must resolve to the empty schema rather than being
// left as a literal that a downstream consumer would try to fetch.
func TestParseSpec_DanglingRefsResolveToNothing(t *testing.T) {
	for _, doc := range []string{specSeeds[3], specSeeds[4]} {
		spec, err := oas.ParseSpec([]byte(doc))
		if err != nil {
			t.Fatalf("ParseSpec: %v", err)
		}
		op, ok := spec.Ops["o"]
		if !ok {
			t.Fatal("operation was dropped entirely")
		}
		if op.Response.Type != "" || len(op.Response.Properties) != 0 {
			t.Errorf("a dangling $ref resolved to %+v, want the empty schema", op.Response)
		}
	}
}

// A cyclic $ref must terminate. This is the seed the parser's visited set and depth
// cap exist for, asserted directly so a regression names the cause.
func TestParseSpec_CyclicRefsTerminate(t *testing.T) {
	for _, name := range []string{"direct self-reference", "mutual A->B->A"} {
		doc := specSeeds[1]
		if strings.HasPrefix(name, "mutual") {
			doc = specSeeds[2]
		}
		spec, err := oas.ParseSpec([]byte(doc))
		if err != nil {
			t.Fatalf("%s: ParseSpec: %v", name, err)
		}
		op := spec.Ops["o"]
		if d := schemaDepth(op.Response, 0); d > oas.MaxRefDepth {
			t.Errorf("%s: resolved %d deep, past the %d cap", name, d, oas.MaxRefDepth)
		}
	}
}
