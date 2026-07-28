// Package apicontract is the versioning/publishing/compatibility-checking
// engine for tailscale2otel's read-only admin JSON API (#323):
// /api/status.json, /api/config.json, /api/cardinality.json, /api/flows.json,
// /api/events.json, and /api/flows/export.json. It is a dependency-free leaf
// (imports only the response DTO packages it reflects over), so it can be
// reused both by its own package tests (against the exported response types
// in statusdata/flowsdata/eventsdata) and by internal/app's own tests
// (against the one unexported response type, flowsExportEnvelope, that lives
// in package app).
//
// The contract has two independent, deliberately different mechanisms — see
// docs/api/compatibility.md for the full policy this code enforces:
//
//   - A published SCHEMA (this file, RenderSchema) documents the CURRENT
//     shape and is regenerated with -update whenever the Go type changes —
//     the drift gate just requires the published doc was regenerated, exactly
//     like internal/config's TestConfigSchemaInSync.
//   - A committed BASELINE (baseline.go) is the frozen set of paths/types a
//     schema_version PROMISES to keep. It is never auto-regenerated: editing
//     it is the deliberate act that accompanies a schema_version bump, so an
//     accidental breaking change (a field silently removed, renamed, or
//     retyped without editing the baseline) fails CI instead of merging.
package apicontract

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var timeType = reflect.TypeFor[time.Time]()

// BuildSchema reflects over t (which must resolve to a struct type, directly
// or through pointer/slice) and returns a JSON-Schema-like shape (plain
// map[string]any of "type"/"properties"/"items"/"additionalProperties") for
// every JSON-tagged field it reaches, recursively.
//
// It intentionally does NOT track "required" — the response DTOs use
// omitempty/omitzero freely for genuinely optional data, and getting that
// distinction right shape-only (without also encoding cross-field rules,
// which none of these responses have) would be more machinery than the
// compatibility policy needs: Flatten's path+type signature already catches
// a field disappearing, being renamed, or changing type, which is the
// concrete harm this contract exists to prevent.
func BuildSchema(t reflect.Type) map[string]any {
	return schemaForType(t)
}

func schemaForType(t reflect.Type) map[string]any {
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() { //nolint:exhaustive // default panics loudly for any kind not handled below
	case reflect.Pointer:
		return withNull(schemaForType(t.Elem()))
	case reflect.Interface:
		// The only interface-typed field reachable from any response root is
		// statusdata.ConfigFieldValue.Value: a genuinely dynamic leaf (string,
		// bool, number, or []string) by design (#320). No "type" constraint;
		// Flatten renders it as the literal type signature "any" rather than
		// silently treating it as absent.
		return map[string]any{"description": "dynamic value: string, boolean, number, or array of strings"}
	case reflect.Struct:
		return objectSchema(t)
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaForType(t.Elem())}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			panic(fmt.Sprintf("apicontract: unsupported map key kind %s for %s — only string-keyed maps are supported in a JSON response", t.Key().Kind(), t))
		}
		return map[string]any{"type": "object", "additionalProperties": schemaForType(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		// A new field type on a response DTO is a deliberate design decision,
		// not something to silently mis-render or omit from the contract.
		panic(fmt.Sprintf("apicontract: unsupported field kind %s for type %s — teach shape.go about it", t.Kind(), t))
	}
}

// withNull marks a scalar/object/array schema as also accepting null, for an
// optional pointer field (e.g. statusdata.APIRateLimit, statusdata.CheckpointStatus).
func withNull(s map[string]any) map[string]any {
	if it, ok := s["type"].(string); ok {
		s["type"] = []any{it, "null"}
	}
	return s
}

// objectSchema renders every exported, JSON-tagged field of struct type t.
// Every field reachable from a response root MUST carry an explicit `json`
// tag (matching this codebase's convention throughout statusdata/flowsdata/
// eventsdata) — a field with none panics rather than silently defaulting to
// its Go name, which would publish a shape the wire format doesn't actually
// use.
func objectSchema(t reflect.Type) map[string]any {
	props := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported: never reaches the wire
		}
		tag, hasTag := f.Tag.Lookup("json")
		// An anonymous embedded struct field with no json tag is PROMOTED by
		// encoding/json: its own fields are merged straight into the parent
		// object rather than nested under the embedded type's name (e.g.
		// flowstore.PairStat embeds PairKey, so the wire object carries "src"/
		// "dst"/"traffic_type" at the top level, not under a "pairkey" key).
		// A handful of flowstore stat types do exactly this (#323 found it the
		// hard way: it panics rather than silently nesting wrong).
		if f.Anonymous && !hasTag {
			embedded := schemaForType(f.Type)
			nested, ok := embedded["properties"].(map[string]any)
			if !ok {
				panic(fmt.Sprintf("apicontract: anonymous field %s.%s (%s) is not a struct — teach objectSchema about this embedding shape", t, f.Name, f.Type))
			}
			for name, s := range nested {
				props[name] = s
			}
			continue
		}
		if !hasTag {
			panic(fmt.Sprintf("apicontract: field %s.%s has no json tag — every response field must be explicitly tagged", t, f.Name))
		}
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			panic(fmt.Sprintf("apicontract: field %s.%s has an empty json tag name — teach it one", t, f.Name))
		}
		props[name] = schemaForType(f.Type)
	}
	return map[string]any{"type": "object", "properties": props}
}

// RenderSchema wraps schema with a JSON-Schema draft-07 envelope
// (title/description) and marshals it deterministically — json.Marshal sorts
// map[string]any keys, so this is byte-stable across regenerations, matching
// internal/config/schema.go's renderConfigSchema.
func RenderSchema(title, description string, schema map[string]any) []byte {
	doc := map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"title":       title,
		"description": description,
	}
	for k, v := range schema {
		doc[k] = v
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		// schema is built entirely from maps/strings/slices produced by this
		// file; a marshal failure here would mean a bug in BuildSchema, not a
		// runtime condition to recover from.
		panic(fmt.Sprintf("apicontract: marshal schema: %v", err))
	}
	return append(out, '\n')
}
