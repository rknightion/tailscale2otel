package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// This file generates config.schema.json (the standalone JSON Schema for
// config.yaml — #308) by reflecting over the Config struct. It is the
// application-config analog of deploy/helm/tailscale2otel/values.schema.json:
// an operator's editor (yaml-language-server) gets completion and validation
// on config.yaml itself, not just on the Helm values file.
//
// Descriptions are not hand-written here: they are pulled from the SAME
// parsed config.example.yaml comments docs/env-vars.md is generated from
// (envReferenceRows in envref.go), called directly rather than duplicated —
// config.example.yaml stays the single place a field's prose lives.
//
// The generated file is regenerated with:
//
//	go test ./internal/config -run TestConfigSchemaInSync -update
//
// and TestConfigSchemaInSync (schema_test.go) is the drift gate, mirroring
// TestEnvReferenceDocInSync.

// configSchemaDescription is the schema's top-level "description". It exists
// to state plainly what this schema does NOT check: JSON Schema can express a
// field's shape (type, enum, numeric range) but not a relationship between
// two fields, so every cross-field rule in Validate() (mutually exclusive
// keys, conditional requirements, "required when X is set") is invisible
// here and enforced only at runtime.
const configSchemaDescription = "JSON Schema for tailscale2otel's config.yaml. " +
	"This schema covers SHAPE ONLY: field names, types, closed enums, and the " +
	"numeric ranges internal/config/validate.go enforces unconditionally. It " +
	"does NOT and CANNOT express cross-field rules — mutually exclusive keys " +
	"(e.g. a value field and its *_file sibling), fields required only when " +
	"another field takes a particular value, or relationships between two " +
	"sections (e.g. tailscale.tailnet vs. tailnets:). Those are enforced only " +
	"at runtime by Config.Validate(). Passing this schema is necessary, not " +
	"sufficient: `tailscale2otel -validate` (or a normal startup) against the " +
	"real config file is the authoritative pre-flight check."

// secretType and durationTypeForSchema let schemaForField special-case the two
// named types (Secret, Duration) before falling back to reflect.Kind, since
// both have an underlying Kind (string, int64) that would otherwise be
// indistinguishable from a plain string/integer field.
var (
	secretType             = reflect.TypeFor[Secret]()
	durationTypeForSchema  = reflect.TypeFor[Duration]()
	defaultDurationExample = "30s"
)

// durationPattern matches the subset of time.ParseDuration's grammar this
// config actually uses: one or more signed decimal (optionally fractional)
// numbers, each immediately followed by a unit.
const durationPattern = `^-?([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`

// schemaRule is an enum and/or numeric-range constraint keyed by a dotted
// config-key SUFFIX in rulesBySuffix (see applyRule).
type schemaRule struct {
	enum     []string
	min, max *float64
}

func f64(v float64) *float64 { return &v }

// rulesBySuffix carries the closed enums and unconditional numeric ranges
// Validate() enforces, keyed by the dotted-path SUFFIX of the field they
// apply to (matched by applyRule, which tries the full path then
// progressively shorter suffixes — so one entry covers a struct reused in
// several places, e.g. TailscaleAuth.Method appears at both
// "tailscale.auth.method" and, inside the tailnets[] item schema,
// "tailnets.auth.method"; both end in "auth.method").
//
// THESE ARE HAND-COPIED FROM validate.go'S oneOf(...)/RANGE CHECKS AND MUST
// BE KEPT IN SYNC BY HAND. Validate operates on a decoded *Config value, not
// on key strings, so there is no single function this file can call to derive
// them instead — this is a hand-maintained bridge, not a generated one, the
// same tradeoff completeness_test.go's leaf-name doc matching makes for the
// same reason.
//
// Only UNCONDITIONAL numeric ranges are included (checked regardless of any
// other field's value): a bound Validate only enforces when another field is
// set a certain way (e.g. ingress_wal.max_bytes, only checked when
// ingress_wal.enabled) is a cross-field rule, not a shape rule, and is
// deliberately left out per configSchemaDescription's cross-field-rules
// disclaimer — encoding it as a plain minimum would reject a value that is
// perfectly valid whenever that gating field is false/unset.
var rulesBySuffix = map[string]schemaRule{
	"provider":                 {enum: []string{"tailscale", "headscale"}},
	"log_format":               {enum: []string{"text", "json"}},
	"log_level":                {enum: []string{"debug", "info", "warn", "error"}},
	"otlp.protocol":            {enum: []string{"grpc", "http", "stdout"}},
	"checkpoint.store":         {enum: []string{"memory", "file"}},
	"source":                   {enum: []string{"poll", "stream", "both", "objectstore"}}, // collectors.flowlogs.source / collectors.auditlogs.source
	"log_mode":                 {enum: []string{"per_connection", "per_record", "off"}},
	"metrics_mode":             {enum: []string{"all", "rollup", "both"}},
	"posture_log_mode":         {enum: []string{"changes", "always", "off"}},
	"decompress":               {enum: []string{"auto", "gzip", "zstd", "none"}},
	"auth.method":              {enum: []string{"oauth", "apikey", "workload_identity"}},
	"sampler":                  {enum: []string{"always_on", "always_off", "traceidratio", "parentbased_always_on", "parentbased_traceidratio"}},
	"scheme":                   {enum: []string{"http", "https"}}, // collectors.node_metrics.discovery.scheme
	"address_order":            {enum: []string{"ipv4", "ipv6"}},
	"instance_source":          {enum: []string{"address", "name", "hostname"}},
	"corruption":               {enum: []string{"fail"}},
	"layout":                   {enum: []string{"", "partitioned", "flat"}}, // *.objectstore.layout (empty = partitioned)
	"metric_export_batch_size": {min: f64(1)},
	"rollup_top_n":             {min: f64(0)},
	"label_value_sample_cap":   {min: f64(0)},
	"warning_threshold":        {min: f64(0)},
	"critical_threshold":       {min: f64(0)},
	"sampler_arg":              {min: f64(0), max: f64(1)},
	"port":                     {min: f64(1), max: f64(65535)}, // collectors.node_metrics.discovery.port
}

// applyRule looks up path (and, failing that, each of its dotted suffixes,
// shortest match wins on the first hit scanning from the full path down) in
// rulesBySuffix and merges any enum/minimum/maximum onto s.
func applyRule(s map[string]any, path string) {
	parts := strings.Split(path, keyDelim)
	for i := range parts {
		suffix := strings.Join(parts[i:], keyDelim)
		r, ok := rulesBySuffix[suffix]
		if !ok {
			continue
		}
		if len(r.enum) > 0 {
			enum := make([]any, len(r.enum))
			for i, v := range r.enum {
				enum[i] = v
			}
			s["enum"] = enum
		}
		if r.min != nil {
			s["minimum"] = *r.min
		}
		if r.max != nil {
			s["maximum"] = *r.max
		}
		return
	}
}

// schemaBuilder holds the description lookup (built once from
// config.example.yaml via envReferenceRows) used while walking the Config
// struct.
type schemaBuilder struct {
	byKey map[string]envRow
}

// buildConfigSchema reflects over Config and returns the full JSON Schema
// document as a plain map, ready for json.MarshalIndent.
func buildConfigSchema(exampleYAML []byte) (map[string]any, error) {
	rows, err := envReferenceRows(exampleYAML)
	if err != nil {
		return nil, fmt.Errorf("derive config schema descriptions: %w", err)
	}
	byKey := make(map[string]envRow, len(rows))
	for _, r := range rows {
		byKey[r.Key] = r
	}

	b := &schemaBuilder{byKey: byKey}
	return map[string]any{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"title":                "tailscale2otel configuration",
		"description":          configSchemaDescription,
		"type":                 "object",
		"additionalProperties": false,
		"properties":           b.structProperties(reflect.TypeOf(Config{}), ""),
	}, nil
}

// renderConfigSchema is buildConfigSchema plus JSON encoding — the pair
// TestConfigSchemaInSync compares against the committed config.schema.json.
func renderConfigSchema(exampleYAML []byte) ([]byte, error) {
	schema, err := buildConfigSchema(exampleYAML)
	if err != nil {
		return nil, err
	}
	// json.Marshal sorts map[string]any keys, so this is byte-stable across
	// regenerations without any extra ordering bookkeeping here.
	out, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config schema: %w", err)
	}
	return append(out, '\n'), nil
}

// structProperties returns the JSON Schema "properties" object for every
// exported, yaml-tagged field of t, keyed by its yaml tag name. prefix is the
// dotted path to t itself ("" at the Config root).
func (b *schemaBuilder) structProperties(t reflect.Type, prefix string) map[string]any {
	props := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported (unknownEnv, configFileWarning, secretFileConflicts)
			continue
		}
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + keyDelim + name
		}
		props[name] = b.schemaForField(f.Type, path)
	}
	return props
}

// schemaForField returns the JSON Schema for one field of Go type t at dotted
// path, then applies any enum/range rule for that path.
func (b *schemaBuilder) schemaForField(t reflect.Type, path string) map[string]any {
	switch t {
	case durationTypeForSchema:
		s := b.durationSchema(path)
		applyRule(s, path)
		return s
	case secretType:
		s := b.secretSchema(path)
		applyRule(s, path)
		return s
	}

	switch t.Kind() {
	case reflect.Pointer:
		// The only pointer field in Config is *NodeMetricsTargetTLS: an optional
		// nested block. Recurse on the pointed-to type and additionally allow
		// null (omitted/unset), rather than forcing an empty object.
		inner := b.schemaForField(t.Elem(), path)
		if it, ok := inner["type"].(string); ok {
			inner["type"] = []any{it, "null"}
		}
		return inner
	case reflect.Struct:
		s := b.objectSchema(t, path)
		applyRule(s, path)
		return s
	case reflect.Slice:
		s := b.sliceSchema(t, path)
		applyRule(s, path)
		return s
	case reflect.Map:
		s := b.mapSchema(t, path)
		applyRule(s, path)
		return s
	case reflect.String:
		s := b.scalarSchema("string", path)
		applyRule(s, path)
		return s
	case reflect.Bool:
		s := b.scalarSchema("boolean", path)
		applyRule(s, path)
		return s
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s := b.scalarSchema("integer", path)
		applyRule(s, path)
		return s
	case reflect.Float32, reflect.Float64:
		s := b.scalarSchema("number", path)
		applyRule(s, path)
		return s
	default:
		// Every Config field type is one of the above; a new field type is a
		// deliberate design decision, not something to silently mis-render.
		panic(fmt.Sprintf("config schema: unsupported field type %s at %q — teach schema.go about it", t, path))
	}
}

// durationSchema renders config.Duration as a pattern-constrained string (the
// YAML encoding is always a Go duration literal like "30s", never a number of
// nanoseconds), carrying the field's actual default (from config.example.yaml)
// as both "default" and "examples".
func (b *schemaBuilder) durationSchema(path string) map[string]any {
	s := map[string]any{
		"type":    "string",
		"pattern": durationPattern,
	}
	example := defaultDurationExample
	if row, ok := b.byKey[path]; ok {
		if row.Desc != "" {
			s["description"] = row.Desc
		}
		if row.Default != "" && row.Default != `""` {
			example = row.Default
		}
	}
	s["default"] = example
	s["examples"] = []any{example}
	return s
}

// secretSchema renders config.Secret as a bare string with NO "default" and
// NO "examples": Secret holds a credential (API key, OAuth secret, token,
// password — see secret.go), and the schema must never suggest or echo one,
// even the redacted placeholder Secret's own MarshalYAML would produce.
func (b *schemaBuilder) secretSchema(path string) map[string]any {
	s := map[string]any{"type": "string"}
	if row, ok := b.byKey[path]; ok && row.Desc != "" {
		s["description"] = row.Desc
	}
	return s
}

// objectSchema renders a nested config struct: additionalProperties:false
// mirrors the runtime's hard rejection of an unknown YAML key
// (unknownkeys.go), so a schema-following editor and the process agree on
// what is spellable.
func (b *schemaBuilder) objectSchema(t reflect.Type, path string) map[string]any {
	obj := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           b.structProperties(t, path),
	}
	if row, ok := b.byKey[path]; ok && row.Desc != "" {
		obj["description"] = row.Desc
	}
	return obj
}

// sliceSchema renders a []string, []Secret, or []struct field.
func (b *schemaBuilder) sliceSchema(t reflect.Type, path string) map[string]any {
	elem := t.Elem()
	var items map[string]any
	switch {
	case elem.Kind() == reflect.String:
		items = map[string]any{"type": "string"}
	case elem == secretType:
		items = map[string]any{"type": "string"}
	case elem.Kind() == reflect.Struct:
		items = map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           b.structProperties(elem, path),
		}
	default:
		panic(fmt.Sprintf("config schema: unsupported slice element kind %s at %q — teach schema.go about it", elem.Kind(), path))
	}
	arr := map[string]any{"type": "array", "items": items}
	if row, ok := b.byKey[path]; ok && row.Desc != "" {
		arr["description"] = row.Desc
	}
	return arr
}

// mapSchema renders a map[string]string or map[string]Secret field
// (otlp.headers, profiling.pyroscope.tags, node-metrics target labels/
// headers). Unlike a config struct these are genuinely open-ended — the keys
// are header/label/tag NAMES the operator chooses — so additionalProperties
// carries the value schema rather than false.
func (b *schemaBuilder) mapSchema(t reflect.Type, path string) map[string]any {
	valType := t.Elem()
	var additional map[string]any
	switch {
	case valType.Kind() == reflect.String:
		additional = map[string]any{"type": "string"}
	case valType == secretType:
		additional = map[string]any{"type": "string"}
	default:
		panic(fmt.Sprintf("config schema: unsupported map value kind %s at %q — teach schema.go about it", valType.Kind(), path))
	}
	m := map[string]any{"type": "object", "additionalProperties": additional}
	if row, ok := b.byKey[path]; ok && row.Desc != "" {
		m["description"] = row.Desc
	}
	return m
}

// scalarSchema renders a plain string/bool/int/float leaf, carrying its
// description and (when parseable) its default value from
// config.example.yaml.
func (b *schemaBuilder) scalarSchema(jsonType, path string) map[string]any {
	s := map[string]any{"type": jsonType}
	row, ok := b.byKey[path]
	if !ok {
		return s
	}
	if row.Desc != "" {
		s["description"] = row.Desc
	}
	if v, ok := scalarJSONDefault(jsonType, row.Default); ok {
		s["default"] = v
	}
	return s
}

// scalarJSONDefault converts an envRow.Default string (as literally written
// in config.example.yaml, e.g. "true", "10000", or `""` for an empty string —
// see scalarDefault in envref.go) into the JSON-typed value the schema's
// "default" keyword expects.
func scalarJSONDefault(jsonType, raw string) (any, bool) {
	switch jsonType {
	case "boolean":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, false
		}
		return v, true
	case "integer":
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, false
		}
		return v, true
	case "number":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, false
		}
		return v, true
	case "string":
		if raw == `""` {
			return "", true
		}
		return raw, true
	default:
		return nil, false
	}
}
