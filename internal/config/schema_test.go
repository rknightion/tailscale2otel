package config

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// configSchemaPath is config.schema.json's location relative to this package
// (repository root, alongside config.example.yaml).
func configSchemaPath() string {
	return filepath.Join("..", "..", "config.schema.json")
}

func readExampleYAML(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read config.example.yaml: %v", err)
	}
	return data
}

// TestConfigSchemaInSync is the drift gate: config.schema.json must equal the
// schema reflected from the Config struct (with descriptions/defaults sourced
// from config.example.yaml). It rides the normal `go test` run (no separate
// tool/module), mirroring TestEnvReferenceDocInSync in envref_test.go —
// same *updateEnvDoc "-update" flag (declared once, in envref_test.go, for
// the whole package), same regenerate-vs-assert shape.
//
// Regenerate with: go test ./internal/config -run TestConfigSchemaInSync -update
func TestConfigSchemaInSync(t *testing.T) {
	example := readExampleYAML(t)
	want, err := renderConfigSchema(example)
	if err != nil {
		t.Fatalf("render config schema: %v", err)
	}

	schemaPath := configSchemaPath()
	if *updateEnvDoc {
		if err := os.WriteFile(schemaPath, want, 0o644); err != nil { //nolint:gosec // G306: generated schema is intentionally world-readable
			t.Fatalf("write config.schema.json: %v", err)
		}
		t.Logf("regenerated %s", schemaPath)
		return
	}

	current, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read config.schema.json: %v — regenerate with "+
			"`go test ./internal/config -run TestConfigSchemaInSync -update`", err)
	}
	if string(want) != string(current) {
		t.Errorf("config.schema.json is out of date with the Config struct / config.example.yaml — " +
			"regenerate with `go test ./internal/config -run TestConfigSchemaInSync -update` and commit the result")
	}
}

// flattenDefaultKeys mirrors defaultKeySet/flattenYAMLKeys in
// completeness_test.go (package config_test, so its unexported helpers are not
// reachable from here): flatten the YAML encoding of Default() into the
// canonical set of every leaf configuration key. A leaf is a scalar, a list,
// or an empty map; a non-empty nested map is recursed into. Because the
// config structs carry no `omitempty`, every field appears.
func flattenDefaultKeys(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := yaml.Marshal(Default())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("unmarshal defaults yaml: %v", err)
	}
	out := map[string]bool{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		m, ok := v.(map[string]any)
		if !ok || len(m) == 0 {
			if prefix != "" {
				out[prefix] = true
			}
			return
		}
		for k, child := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			walk(path, child)
		}
	}
	walk("", root)
	return out
}

// schemaLeafKeys walks a schema "properties" object the same way
// flattenDefaultKeys walks the marshaled defaults: an object schema that
// carries its own nested "properties" (a config struct/section) is recursed
// into; anything else — a scalar, a Duration/Secret string, an array (incl.
// one of structs — its items are deliberately NOT descended into, matching
// how Default() renders an empty slice as a single leaf), or a map-type
// object (additionalProperties, no "properties" key) — is one leaf at that
// path.
func schemaLeafKeys(t *testing.T, props map[string]any, prefix string, out map[string]bool) {
	t.Helper()
	for name, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("schema property %q at prefix %q is not an object: %#v", name, prefix, raw)
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if nested, ok := prop["properties"].(map[string]any); ok {
			schemaLeafKeys(t, nested, path, out)
			continue
		}
		out[path] = true
	}
}

// TestConfigSchemaCoversEveryKey guards that the generated schema names EVERY
// configuration key defined by config.Default() — the same walk
// completeness_test.go's TestExampleConfigCoversEveryKey uses against
// config.example.yaml, applied here against the schema's property tree
// instead of a YAML document. A field added to Config but not taught to
// schema.go (an unsupported Go type panics buildConfigSchema; a supported one
// that reflection nonetheless misses would otherwise pass silently) fails
// this test by name.
func TestConfigSchemaCoversEveryKey(t *testing.T) {
	schema, err := buildConfigSchema(readExampleYAML(t))
	if err != nil {
		t.Fatalf("build config schema: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("config schema has no top-level \"properties\" object")
	}
	got := map[string]bool{}
	schemaLeafKeys(t, props, "", got)
	want := flattenDefaultKeys(t)

	var missing, extra []string
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("config.schema.json is missing %d key(s) defined by config.Default() — teach schema.go about them:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("config.schema.json has %d key(s) not in config.Default() — likely stale:\n  %s",
			len(extra), strings.Join(extra, "\n  "))
	}
}

// TestConfigSchemaFieldShapes spot-checks the field-type -> JSON-Schema
// mapping schema.go implements: config.Secret never carries a default/example
// (it holds a credential), config.Duration is a pattern-constrained string
// (not a bare integer of nanoseconds), closed enums from validate.go's
// oneOf(...) checks are attached, structs reject unknown properties the same
// way unknownkeys.go does at runtime, and a genuinely open-ended map (like
// otlp.headers) does NOT.
func TestConfigSchemaFieldShapes(t *testing.T) {
	schema, err := buildConfigSchema(readExampleYAML(t))
	if err != nil {
		t.Fatalf("build config schema: %v", err)
	}
	props := schema["properties"].(map[string]any) //nolint:errcheck // shape asserted by TestConfigSchemaCoversEveryKey

	// Secret: tailscale.auth.apikey — string, no default, no examples.
	tailscale := props["tailscale"].(map[string]any)["properties"].(map[string]any)
	auth := tailscale["auth"].(map[string]any)["properties"].(map[string]any)
	apikey := auth["apikey"].(map[string]any)
	if apikey["type"] != "string" {
		t.Errorf("tailscale.auth.apikey type = %v, want string", apikey["type"])
	}
	if _, has := apikey["default"]; has {
		t.Errorf("tailscale.auth.apikey must carry no default (it is a credential), got %v", apikey["default"])
	}
	if _, has := apikey["examples"]; has {
		t.Errorf("tailscale.auth.apikey must carry no examples (it is a credential), got %v", apikey["examples"])
	}

	// Duration: otlp.metric_interval — pattern-constrained string with a real
	// default/example taken from config.example.yaml, not a raw integer.
	otlp := props["otlp"].(map[string]any)["properties"].(map[string]any)
	metricInterval := otlp["metric_interval"].(map[string]any)
	if metricInterval["type"] != "string" {
		t.Errorf("otlp.metric_interval type = %v, want string", metricInterval["type"])
	}
	if metricInterval["pattern"] != durationPattern {
		t.Errorf("otlp.metric_interval pattern = %v, want %v", metricInterval["pattern"], durationPattern)
	}
	if metricInterval["default"] == "" || metricInterval["default"] == nil {
		t.Errorf("otlp.metric_interval default missing")
	}
	if exs, ok := metricInterval["examples"].([]any); !ok || len(exs) == 0 {
		t.Errorf("otlp.metric_interval examples missing, got %#v", metricInterval["examples"])
	}

	// Enum: otlp.protocol must be exactly the set validate.go's oneOf enforces.
	protocol := otlp["protocol"].(map[string]any)
	wantEnum := []any{"grpc", "http", "stdout"}
	if enum, ok := protocol["enum"].([]any); !ok || !equalAny(enum, wantEnum) {
		t.Errorf("otlp.protocol enum = %#v, want %#v", protocol["enum"], wantEnum)
	}

	// Struct rejects unknown properties, mirroring unknownkeys.go.
	otlpField := props["otlp"].(map[string]any)
	if otlpField["additionalProperties"] != false {
		t.Errorf("otlp additionalProperties = %v, want false", otlpField["additionalProperties"])
	}

	// Map: otlp.headers is genuinely open-ended (operator-chosen header
	// names), so additionalProperties must be a value schema, not false.
	headers := otlp["headers"].(map[string]any)
	if headers["type"] != "object" {
		t.Errorf("otlp.headers type = %v, want object", headers["type"])
	}
	additional, ok := headers["additionalProperties"].(map[string]any)
	if !ok {
		t.Fatalf("otlp.headers additionalProperties = %#v, want a value schema (map is open-ended)", headers["additionalProperties"])
	}
	if additional["type"] != "string" {
		t.Errorf("otlp.headers additionalProperties.type = %v, want string", additional["type"])
	}

	// Map of integer slices: each operator-chosen tag maps to a non-empty
	// list of valid TCP ports.
	collectors := props["collectors"].(map[string]any)["properties"].(map[string]any)
	nodeMetrics := collectors["node_metrics"].(map[string]any)["properties"].(map[string]any)
	discovery := nodeMetrics["discovery"].(map[string]any)["properties"].(map[string]any)
	portOverrides := discovery["port_overrides"].(map[string]any)
	portList, ok := portOverrides["additionalProperties"].(map[string]any)
	if !ok || portList["type"] != "array" || portList["minItems"] != 1 {
		t.Fatalf("port_overrides additionalProperties = %#v, want non-empty array schema", portOverrides["additionalProperties"])
	}
	portItem, ok := portList["items"].(map[string]any)
	if !ok || portItem["type"] != "integer" || portItem["minimum"] != float64(1) || portItem["maximum"] != float64(65535) {
		t.Errorf("port_overrides item schema = %#v, want integer 1..65535", portList["items"])
	}
}

func equalAny(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ruleApplications is every path in the generated schema that carries an enum
// or a numeric bound, pinned exactly.
//
// rulesBySuffix is matched by dotted-path SUFFIX, so one entry can cover a
// struct reused in several places — but the same mechanism means a bare leaf
// name like "source", "port" or "layout" silently claims ANY future field with
// that name, anywhere in the tree, and hands it an enum copied from an
// unrelated setting. A wrong enum on a config key is worse than no enum: an
// editor would mark a perfectly valid value as an error, and the operator
// would believe it.
//
// This list makes that loud. If a field you added appears here, the schema is
// constraining it — confirm the rule is genuinely the one validate.go enforces
// for YOUR field, and if it is not, give it a longer, more specific key in
// rulesBySuffix rather than editing this list to match.
var ruleApplications = []string{
	"cardinality.critical_threshold",
	"cardinality.flow.metrics_mode",
	"cardinality.flow.rollup_top_n",
	"cardinality.label_value_sample_cap",
	"cardinality.warning_threshold",
	"checkpoint.evidence_store",
	"checkpoint.store",
	"collectors.auditlogs.dedup_capacity",
	"collectors.auditlogs.objectstore.layout",
	"collectors.auditlogs.objectstore.max_seen_keys",
	"collectors.auditlogs.source",
	"collectors.devices.expiry_log_mode",
	"collectors.devices.posture_log_mode",
	"collectors.flowlogs.dedup_capacity",
	"collectors.flowlogs.log_mode",
	"collectors.flowlogs.objectstore.layout",
	"collectors.flowlogs.objectstore.max_seen_keys",
	"collectors.flowlogs.source",
	"collectors.k8s_audit.objectstore.layout",
	"collectors.k8s_audit.objectstore.max_seen_keys",
	"collectors.keys.expiry_log_mode",
	"collectors.node_metrics.discovery.address_order",
	"collectors.node_metrics.discovery.instance_source",
	"collectors.node_metrics.discovery.port",
	"collectors.node_metrics.discovery.port_overrides.*",
	"collectors.node_metrics.discovery.scheme",
	"delivery.mode",
	"flows.capacity_profile",
	"ingress_wal.corruption",
	"log_format",
	"log_level",
	"otlp.compression",
	"otlp.credential_reload.interval",
	"otlp.metric_export_batch_size",
	"otlp.protocol",
	"profiling.pyroscope.credential_reload.interval",
	"profiling.pyroscope.tailnet_label",
	"prometheus.max_requests_in_flight",
	"provider",
	"streaming.decompress",
	"tailnets.auth.method",
	"tailnets.cardinality.critical_threshold",
	"tailnets.cardinality.warning_threshold",
	"tailnets.objectstore.audit.layout",
	"tailnets.objectstore.audit.max_seen_keys",
	"tailnets.objectstore.flow.layout",
	"tailnets.objectstore.flow.max_seen_keys",
	"tailnets.objectstore.k8s_audit.layout",
	"tailnets.objectstore.k8s_audit.max_seen_keys",
	"tailscale.auth.method",
	"tracing.remote_parent",
	"tracing.sampler",
	"tracing.sampler_arg",
	"tracing.samplers.background.arg",
	"tracing.samplers.background.sampler",
	"tracing.samplers.receiver.arg",
	"tracing.samplers.receiver.sampler",
	"tracing.samplers.scrape.arg",
	"tracing.samplers.scrape.sampler",
}

func TestConfigSchemaRuleApplicationsArePinned(t *testing.T) {
	schema, err := buildConfigSchema(readExampleYAML(t))
	if err != nil {
		t.Fatalf("build config schema: %v", err)
	}
	var got []string
	collectConstrained(schema, "", &got)
	sort.Strings(got)

	want := slices.Clone(ruleApplications)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("the set of schema keys carrying an enum or numeric bound changed.\n"+
			"got:\n  %s\nwant:\n  %s\n"+
			"A key that gained one unexpectedly is rulesBySuffix matching it by leaf name — "+
			"check the rule is the one validate.go enforces for THAT key before updating this list.",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// collectConstrained walks a built schema and records the dotted path of every
// node carrying an enum, a minimum or a maximum.
func collectConstrained(node map[string]any, path string, out *[]string) {
	_, hasEnum := node["enum"]
	_, hasMin := node["minimum"]
	_, hasMax := node["maximum"]
	if path != "" && (hasEnum || hasMin || hasMax) {
		*out = append(*out, path)
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for k, v := range props {
			child, ok := v.(map[string]any)
			if !ok {
				continue
			}
			next := k
			if path != "" {
				next = path + keyDelim + k
			}
			collectConstrained(child, next, out)
		}
	}
	// A list's item schema keeps the list's own path: tailnets[].auth.method is
	// spelled "tailnets.auth.method" throughout rulesBySuffix and this file.
	if items, ok := node["items"].(map[string]any); ok {
		collectConstrained(items, path, out)
	}
	if ap, ok := node["additionalProperties"].(map[string]any); ok {
		collectConstrained(ap, path+keyDelim+"*", out)
	}
}
