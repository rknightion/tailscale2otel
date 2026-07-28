package apicontract

import (
	"flag"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/app/eventsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/flowsdata"
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
)

// updateSchemas regenerates the committed docs/api/schemas/*.schema.json
// files. Run: go test ./internal/app/apicontract -run TestSchemasInSync -update
//
// It never touches the *.baseline.json files (see updateBaselines below) —
// keeping the two update paths separate is what makes the baseline gate a
// real breaking-change detector rather than something a routine -update run
// silently rubber-stamps.
var updateSchemas = flag.Bool("update", false, "rewrite the committed docs/api/schemas/*.schema.json golden files")

// updateBaselines is a SEPARATE, deliberately harder-to-reach-for flag:
// regenerating a baseline is how a breaking change gets ACKNOWLEDGED (and its
// schema_version bumped), never something routine -update tidies up as a
// side effect of an unrelated regen. Run:
//
//	go test ./internal/app/apicontract -run TestSchemasAreBackwardCompatible -update-baseline
var updateBaselines = flag.Bool("update-baseline", false, "rewrite the committed docs/api/schemas/*.baseline.json files — a DELIBERATE breaking-change acknowledgement, see docs/api/compatibility.md")

// contractCase is one published response's identity: its Go type, its
// compiled-in SchemaVersion, and the schema/baseline file pair it publishes
// against.
type contractCase struct {
	name    string
	typ     reflect.Type
	version int
	title   string
	desc    string
}

func cases() []contractCase {
	return []contractCase{
		{
			name: "status", typ: reflect.TypeFor[statusdata.Status](), version: statusdata.StatusSchemaVersion,
			title: "tailscale2otel /api/status.json",
			desc:  "The full admin status snapshot served by GET /api/status.json and rendered by the admin landing page. See docs/api/compatibility.md.",
		},
		{
			name: "config-summary", typ: reflect.TypeFor[statusdata.ConfigSummary](), version: statusdata.ConfigSummarySchemaVersion,
			title: "tailscale2otel /api/config.json",
			desc:  "The redacted effective-configuration summary served by GET /api/config.json (also Status.config). Secret VALUES never appear. See docs/api/compatibility.md.",
		},
		{
			name: "cardinality", typ: reflect.TypeFor[statusdata.CardinalityInfo](), version: statusdata.CardinalitySchemaVersion,
			title: "tailscale2otel /api/cardinality.json",
			desc:  "The live active-series cardinality snapshot served by GET /api/cardinality.json (also Status.cardinality). See docs/api/compatibility.md.",
		},
		{
			name: "flows", typ: reflect.TypeFor[flowsdata.Response](), version: flowsdata.SchemaVersion,
			title: "tailscale2otel /api/flows.json",
			desc:  "The built-in flow view's data, served by GET /api/flows.json. See docs/api/compatibility.md.",
		},
		{
			name: "events", typ: reflect.TypeFor[eventsdata.Response](), version: eventsdata.SchemaVersion,
			title: "tailscale2otel /api/events.json",
			desc:  "The built-in audit/webhook event explorer's data, served by GET /api/events.json. See docs/api/compatibility.md.",
		},
	}
}

func schemaPath(name string) string {
	return filepath.Join("..", "..", "..", "docs", "api", "schemas", name+".schema.json")
}

func baselinePath(name string) string {
	return filepath.Join("..", "..", "..", "docs", "api", "schemas", name+".baseline.json")
}

// TestSchemasInSync is the drift gate (mirrors internal/config's
// TestConfigSchemaInSync): the schema published under docs/api/schemas/ must
// equal what BuildSchema/RenderSchema produce from the LIVE Go response type,
// right now. Any shape change at all — additive or breaking — requires
// regenerating and committing the file; this test only proves the docs are
// current, not that a change was safe (that's TestSchemasAreBackwardCompatible).
func TestSchemasInSync(t *testing.T) {
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			schema := BuildSchema(c.typ)
			rendered := RenderSchema(c.title, c.desc, schema)
			path := schemaPath(c.name)
			AssertFileInSync(t, path, rendered, *updateSchemas)
		})
	}
}

// TestSchemasAreBackwardCompatible is the breaking-change gate #323 asks for:
// every path the committed baseline promises must still resolve to the same
// type in the response's CURRENT schema, and the baseline's recorded
// schema_version must match the constant compiled into the response type.
// See CompareBaseline for exactly what counts as a break, and
// docs/api/compatibility.md for the policy this enforces.
//
// Unlike TestSchemasInSync this is NEVER satisfied by the routine -update
// flag: a real break must be fixed by either reverting the change or
// deliberately re-running with -update-baseline (which also requires having
// bumped the SchemaVersion constant in code first, or the version-mismatch
// check on the NEXT run catches the omission).
func TestSchemasAreBackwardCompatible(t *testing.T) {
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			current := Flatten(BuildSchema(c.typ))
			path := baselinePath(c.name)
			if *updateBaselines {
				b := Baseline{SchemaVersion: c.version, Paths: current}
				if err := WriteGolden(path, RenderBaseline(b)); err != nil {
					t.Fatalf("write baseline %s: %v", path, err)
				}
				t.Logf("regenerated baseline %s (schema_version=%d, %d paths) — this is a DELIBERATE "+
					"breaking-change acknowledgement; confirm that was intended before committing", path, c.version, len(current))
				return
			}
			baseline, err := LoadBaseline(path)
			if err != nil {
				t.Fatalf("%v — bootstrap it with -update-baseline", err)
			}
			if broken := CompareBaseline(baseline, c.version, current); len(broken) > 0 {
				t.Errorf("breaking change(s) against the published compatibility baseline %s:\n  %s\n"+
					"acceptable only as a DELIBERATE change: bump the response's SchemaVersion constant, "+
					"then re-run with -update-baseline (see docs/api/compatibility.md)",
					path, JoinLines(broken))
			}
		})
	}
}
