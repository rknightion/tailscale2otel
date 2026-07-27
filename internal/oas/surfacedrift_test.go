package oas_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/oas"
)

// Classification of the request surface (#432). Each case mutates ONE thing in
// the baseline fixture and asserts both the kind and the severity, because the
// severity is what decides whether the daily lane opens a tracking issue at all:
// HasActionable is Breaking-or-Warning, so misclassifying a breaking change as
// Info makes it silently invisible, and misclassifying an additive one as
// Warning makes the lane cry wolf on every benign upstream release.

// classify parses two documents and returns the changes for listTailnetDevices.
func classifySurface(t *testing.T, oldDoc, newDoc string) []oas.Change {
	t.Helper()
	oldSpec, err := oas.ParseSpec([]byte(oldDoc))
	if err != nil {
		t.Fatalf("ParseSpec(old): %v", err)
	}
	newSpec, err := oas.ParseSpec([]byte(newDoc))
	if err != nil {
		t.Fatalf("ParseSpec(new): %v", err)
	}
	return oas.Classify(oldSpec, newSpec, []string{"listTailnetDevices"})
}

// findChange returns the first change of the given kind whose Detail contains
// want, and reports what it did see when there is none.
func findChange(t *testing.T, changes []oas.Change, kind oas.ChangeKind, want string) oas.Change {
	t.Helper()
	for _, c := range changes {
		if c.Kind == kind && strings.Contains(c.Detail, want) {
			return c
		}
	}
	t.Fatalf("no %s change mentioning %q; got %d change(s): %+v", kind, want, len(changes), changes)
	return oas.Change{}
}

// noChangeOfKind asserts a kind is absent — the guard against a classifier that
// reports every dimension on every run and is therefore useless.
func noChangeOfKind(t *testing.T, changes []oas.Change, kind oas.ChangeKind) {
	t.Helper()
	for _, c := range changes {
		if c.Kind == kind {
			t.Errorf("unexpected %s change: %+v", kind, c)
		}
	}
}

func TestClassify_IdenticalSurfaceReportsNothing(t *testing.T) {
	changes := classifySurface(t, surfaceSpec, surfaceSpec)
	if len(changes) != 0 {
		t.Errorf("a spec compared with itself reported %d change(s): %+v", len(changes), changes)
	}
	if oas.HasActionable(changes) {
		t.Error("HasActionable is true for an unchanged spec")
	}
}

func TestClassify_ParameterDrift(t *testing.T) {
	for _, tc := range []struct {
		name         string
		old, new     string
		kind         oas.ChangeKind
		detail       string
		wantSeverity oas.Severity
	}{
		{
			// A required parameter appearing upstream means every request we
			// already send is now missing a mandatory field.
			name: "required parameter added",
			old:  `{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
			new: `{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}},
			      {"name": "since", "in": "query", "required": true, "schema": {"type": "string"}}`,
			kind:         oas.RequiredParamAdded,
			detail:       "query:since",
			wantSeverity: oas.Breaking,
		},
		{
			name: "optional parameter added is additive",
			old:  `{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
			new: `{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}},
			      {"name": "since", "in": "query", "required": false, "schema": {"type": "string"}}`,
			kind:         oas.OptionalParamAdded,
			detail:       "query:since",
			wantSeverity: oas.Info,
		},
		{
			// We may still be sending it. Upstream ignoring an unknown parameter
			// silently changes what we collect — behavioral, not fatal.
			name:         "parameter removed",
			old:          `{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
			new:          `{"name": "other", "in": "query", "required": false, "schema": {"type": "string"}}`,
			kind:         oas.ParamRemoved,
			detail:       "query:kind",
			wantSeverity: oas.Warning,
		},
		{
			name:         "parameter became required",
			old:          `{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
			new:          `{"name": "kind", "in": "query", "required": true, "schema": {"$ref": "#/components/schemas/Kind"}}`,
			kind:         oas.ParamBecameRequired,
			detail:       "query:kind",
			wantSeverity: oas.Breaking,
		},
		{
			name:         "parameter became optional is additive",
			old:          `{"name": "kind", "in": "query", "required": true, "schema": {"$ref": "#/components/schemas/Kind"}}`,
			new:          `{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
			kind:         oas.ParamBecameOptional,
			detail:       "query:kind",
			wantSeverity: oas.Info,
		},
		{
			name:         "parameter type changed",
			old:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string"}}`,
			new:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "integer"}}`,
			kind:         oas.ParamTypeChanged,
			detail:       "string → integer",
			wantSeverity: oas.Breaking,
		},
		{
			// The dimension no response-body diff can see: every field still
			// decodes, but what upstream returns when we omit the parameter has
			// moved. listUsers `type` defaults to "member" and `role` to "all".
			name:         "parameter default changed is behavioral",
			old:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string", "default": "member"}}`,
			new:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string", "default": "all"}}`,
			kind:         oas.ParamDefaultChanged,
			detail:       `"member" → "all"`,
			wantSeverity: oas.Warning,
		},
		{
			name:         "parameter default added is behavioral",
			old:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string"}}`,
			new:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string", "default": "all"}}`,
			kind:         oas.ParamDefaultChanged,
			detail:       `(none) → "all"`,
			wantSeverity: oas.Warning,
		},
		{
			// We may be sending a value upstream now rejects.
			name:         "parameter enum value removed",
			old:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string", "enum": ["a", "b"]}}`,
			new:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string", "enum": ["a"]}}`,
			kind:         oas.ParamEnumValueRemoved,
			detail:       `"b"`,
			wantSeverity: oas.Warning,
		},
		{
			name:         "parameter enum value added is additive",
			old:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string", "enum": ["a"]}}`,
			new:          `{"name": "kind", "in": "query", "required": false, "schema": {"type": "string", "enum": ["a", "b"]}}`,
			kind:         oas.ParamEnumValueAdded,
			detail:       `"b"`,
			wantSeverity: oas.Info,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldDoc := replaceOnce(surfaceSpec,
				`{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
				tc.old)
			newDoc := replaceOnce(surfaceSpec,
				`{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
				tc.new)

			changes := classifySurface(t, oldDoc, newDoc)
			c := findChange(t, changes, tc.kind, tc.detail)
			if c.Severity != tc.wantSeverity {
				t.Errorf("severity = %q, want %q — HasActionable only fires on breaking or "+
					"warning, so this decides whether the daily lane reports it at all",
					c.Severity, tc.wantSeverity)
			}
			if c.Where != "GET /tailnet/{tailnet}/devices" {
				t.Errorf("Where = %q, want %q — the report needs to say where the change is",
					c.Where, "GET /tailnet/{tailnet}/devices")
			}
		})
	}
}

// A required parameter added at the PATH-ITEM level is just as breaking as one
// added on the operation, and 30 of the 34 real parameters live there.
func TestClassify_RequiredPathItemParameterAddedIsBreaking(t *testing.T) {
	newDoc := replaceOnce(surfaceSpec,
		`{"$ref": "#/components/parameters/fields"}`,
		`{"$ref": "#/components/parameters/fields"},
		 {"name": "since", "in": "query", "required": true, "schema": {"type": "string"}}`)

	changes := classifySurface(t, surfaceSpec, newDoc)
	c := findChange(t, changes, oas.RequiredParamAdded, "query:since")
	if c.Severity != oas.Breaking {
		t.Errorf("severity = %q, want breaking", c.Severity)
	}
}

// The same parameter name in two locations is two parameters. Keying the diff on
// name alone would treat a path parameter moving to the query string as no change
// at all — and that IS a breaking change to how we build the request.
func TestClassify_SameNameDifferentLocationIsNotTheSameParameter(t *testing.T) {
	newDoc := replaceOnce(surfaceSpec,
		`{"name": "kind", "in": "query", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`,
		`{"name": "kind", "in": "header", "required": false, "schema": {"$ref": "#/components/schemas/Kind"}}`)

	changes := classifySurface(t, surfaceSpec, newDoc)
	findChange(t, changes, oas.ParamRemoved, "query:kind")
	findChange(t, changes, oas.OptionalParamAdded, "header:kind")
}

func TestClassify_SuccessStatusDrift(t *testing.T) {
	t.Run("200 removed is breaking", func(t *testing.T) {
		newDoc := replaceOnce(surfaceSpec, `"200": {"content": {`, `"201": {"content": {`)
		changes := classifySurface(t, surfaceSpec, newDoc)

		c := findChange(t, changes, oas.SuccessStatusRemoved, "200")
		if c.Severity != oas.Breaking {
			t.Errorf("severity = %q, want breaking — every consumed operation returns 200 today "+
				"and the client treats anything else as an error", c.Severity)
		}
		added := findChange(t, changes, oas.SuccessStatusAdded, "201")
		if added.Severity != oas.Info {
			t.Errorf("added-status severity = %q, want info", added.Severity)
		}
		// The point of modeling the status set: the response schema still resolves,
		// so this must NOT masquerade as every response field being removed.
		noChangeOfKind(t, changes, oas.RemovedResponseField)
	})

	t.Run("extra 2xx added is additive", func(t *testing.T) {
		newDoc := replaceOnce(surfaceSpec, `"404": {"description": "nope"}`,
			`"404": {"description": "nope"}, "204": {"description": "empty"}`)
		changes := classifySurface(t, surfaceSpec, newDoc)

		c := findChange(t, changes, oas.SuccessStatusAdded, "204")
		if c.Severity != oas.Info {
			t.Errorf("severity = %q, want info", c.Severity)
		}
		noChangeOfKind(t, changes, oas.SuccessStatusRemoved)
	})

	t.Run("a new error status is not a success status", func(t *testing.T) {
		newDoc := replaceOnce(surfaceSpec, `"404": {"description": "nope"}`,
			`"404": {"description": "nope"}, "502": {"description": "gateway"}`)
		changes := classifySurface(t, surfaceSpec, newDoc)
		if len(changes) != 0 {
			t.Errorf("a new 5xx produced %d change(s): %+v — error statuses are not part of the "+
				"success set and reporting them would make the lane noisy on every upstream release",
				len(changes), changes)
		}
	})
}

func TestClassify_ResponseMediaTypeDrift(t *testing.T) {
	t.Run("losing application/json is breaking", func(t *testing.T) {
		newDoc := replaceOnce(surfaceSpec,
			`"application/json": {"schema": {"type": "object", "properties": {"ok": {"type": "boolean"}}}},
            "application/hujson": {"schema": {"type": "string"}}`,
			`"application/hujson": {"schema": {"type": "string"}}`)
		changes := classifySurface(t, surfaceSpec, newDoc)

		c := findChange(t, changes, oas.ResponseMediaTypeRemoved, "application/json")
		if c.Severity != oas.Breaking {
			t.Errorf("severity = %q, want breaking — every decoder here parses JSON, so losing "+
				"that media type is fatal however many others remain", c.Severity)
		}
	})

	t.Run("losing a media type we do not decode is informational", func(t *testing.T) {
		newDoc := replaceOnce(surfaceSpec,
			`,
            "application/hujson": {"schema": {"type": "string"}}`, ``)
		changes := classifySurface(t, surfaceSpec, newDoc)

		c := findChange(t, changes, oas.ResponseMediaTypeRemoved, "application/hujson")
		if c.Severity != oas.Info {
			t.Errorf("severity = %q, want info — getPolicyFile's application/hujson is offered "+
				"but never requested here", c.Severity)
		}
	})

	t.Run("gaining a media type is additive", func(t *testing.T) {
		newDoc := replaceOnce(surfaceSpec,
			`"application/hujson": {"schema": {"type": "string"}}`,
			`"application/hujson": {"schema": {"type": "string"}},
             "text/csv": {"schema": {"type": "string"}}`)
		changes := classifySurface(t, surfaceSpec, newDoc)

		c := findChange(t, changes, oas.ResponseMediaTypeAdded, "text/csv")
		if c.Severity != oas.Info {
			t.Errorf("severity = %q, want info", c.Severity)
		}
	})
}

// The latent request-body dimension. #432 asked for it on "the safe consumed
// POST" (validateAndTestPolicyFile), which is dispositioned `implement`, not
// `consumed` — so this is exercised on a synthetic operation and guarded for real
// by TestVendoredSpec_NoConsumedOperationHasARequestBody.
func TestClassify_RequestBodyDrift(t *testing.T) {
	withBody := replaceOnce(surfaceSpec,
		`"operationId": "listTailnetDevices",`,
		`"operationId": "listTailnetDevices",
         "requestBody": {"content": {"application/json": {"schema": {"type": "object", "required": ["src"]}}}},`)

	t.Run("losing the JSON request media type is breaking", func(t *testing.T) {
		newDoc := replaceOnce(withBody, `"application/json": {"schema": {"type": "object", "required": ["src"]}}`,
			`"application/hujson": {"schema": {"type": "string"}}`)
		changes := classifySurface(t, withBody, newDoc)

		c := findChange(t, changes, oas.RequestMediaTypeRemoved, "application/json")
		if c.Severity != oas.Breaking {
			t.Errorf("severity = %q, want breaking", c.Severity)
		}
	})

	t.Run("a new required request field is still breaking", func(t *testing.T) {
		newDoc := replaceOnce(withBody, `"required": ["src"]`, `"required": ["src", "dst"]`)
		changes := classifySurface(t, withBody, newDoc)
		findChange(t, changes, oas.NewRequiredRequestField, "dst")
	})
}

// A consumed operation that neither spec models gets ZERO drift coverage, and
// before #432 that was silent: Classify's `if !inOld { continue }` skipped it.
// Spec.Ops is GET-only, so the day validateAndTestPolicyFile (a POST) is flipped
// from `implement` to `consumed`, this is what says so.
func TestClassify_ConsumedOperationAbsentFromBothSpecs(t *testing.T) {
	spec, err := oas.ParseSpec([]byte(surfaceSpec))
	if err != nil {
		t.Fatal(err)
	}
	changes := oas.Classify(spec, spec, []string{"validateAndTestPolicyFile"})

	c := findChange(t, changes, oas.OperationUnmodeled, "validateAndTestPolicyFile")
	if c.Severity != oas.Warning {
		t.Errorf("severity = %q, want warning — silently skipping it is how a consumed "+
			"operation ends up with no drift coverage at all", c.Severity)
	}
	if !oas.HasActionable(changes) {
		t.Error("HasActionable is false, so the daily lane would not report this")
	}
}

// The baseline modeling nothing while the candidate does is the same loss of
// coverage seen from the other side, and must not be silent either.
func TestClassify_OperationAbsentFromBaselineOnly(t *testing.T) {
	empty, err := oas.ParseSpec([]byte(`{"openapi":"3.1.0","paths":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	populated, err := oas.ParseSpec([]byte(surfaceSpec))
	if err != nil {
		t.Fatal(err)
	}
	changes := oas.Classify(empty, populated, []string{"listTailnetDevices"})
	findChange(t, changes, oas.OperationUnmodeled, "listTailnetDevices")
}

// Classify appends response-field changes while iterating a map, so several
// same-severity changes on one operation arrive in an order that varies between
// runs. The sort's tie-breakers are what make the report stable, and the
// scheduled lane compares rendered bodies to decide whether to update its
// tracking issue — an unstable order reads as fresh drift every single day.
func TestClassify_OutputOrderIsStableAcrossRuns(t *testing.T) {
	fields := func(typ string) string {
		return `{"openapi":"3.1.0","paths":{"/d":{"get":{"operationId":"op",
			"responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{
				"alpha":{"type":"` + typ + `"},
				"bravo":{"type":"` + typ + `"},
				"charlie":{"type":"` + typ + `"},
				"delta":{"type":"` + typ + `"},
				"echo":{"type":"` + typ + `"}
			}}}}}}}}}}`
	}
	oldSpec, err := oas.ParseSpec([]byte(fields("string")))
	if err != nil {
		t.Fatal(err)
	}
	newSpec, err := oas.ParseSpec([]byte(fields("integer")))
	if err != nil {
		t.Fatal(err)
	}

	var first []string
	for run := range 25 {
		var got []string
		for _, c := range oas.Classify(oldSpec, newSpec, []string{"op"}) {
			got = append(got, string(c.Kind)+"|"+c.Detail)
		}
		if len(got) != 5 {
			t.Fatalf("run %d: %d changes, want 5 type changes: %v", run, len(got), got)
		}
		if first == nil {
			first = got
			continue
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("run %d position %d: %q, first run had %q — same-severity changes on one "+
					"operation must be totally ordered, or the report reshuffles between runs",
					run, i, got[i], first[i])
			}
		}
	}
	// And the order must be ascending Detail, not merely repeatable.
	for i := 1; i < len(first); i++ {
		if first[i-1] >= first[i] {
			t.Errorf("not sorted ascending: %q before %q", first[i-1], first[i])
		}
	}
}

// Every kind must carry a severity the sort understands. A kind added without one
// gets the zero Severity: HasActionable is then false, so the daily lane never
// reports it, and severityRank puts it at 99, below the informational noise it
// should outrank. That failure is invisible in every other test — the kind simply
// never shows up — which is why the severity policy is a table and this walks it.
func TestClassify_EveryKindHasARankedSeverity(t *testing.T) {
	kinds := oas.AllChangeKinds()
	if len(kinds) < 20 {
		t.Fatalf("AllChangeKinds() = %d kinds, expected at least 20 after #432: %v", len(kinds), kinds)
	}
	for _, k := range kinds {
		switch sev := oas.SeverityOf(k); sev {
		case oas.Breaking, oas.Warning, oas.Info:
		default:
			t.Errorf("kind %q has severity %q, which severityRank does not rank", k, sev)
		}
	}
}
