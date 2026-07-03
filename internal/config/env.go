package config

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
)

// listEnvKeys are the config keys whose environment value is a comma-separated
// list (the scalar []string fields). For these, an env var like
// TS2OTEL_TAILSCALE__AUTH__OAUTH__SCOPES="all:read,devices:read" is split into a
// slice. A map field like otlp.headers is a different case: mapstructure decodes
// a single nested key fine (TS2OTEL_OTLP__HEADERS__X_ORG=tenant-1 works, one env
// var per map entry), so it needs no special handling here. A list-of-structs
// field (tailnets, collectors.node_metrics.targets) is the genuinely unsupported
// case — indexing into one (e.g. TS2OTEL_TAILNETS__0__NAME) decodes into a
// mostly-empty struct, silently corrupting the value, so Load rejects it outright
// instead (see structSliceEnvKeys).
var listEnvKeys = map[string]bool{
	"tailscale.auth.oauth.scopes":                    true,
	"collectors.devices.attribute_namespaces":        true,
	"collectors.node_metrics.metric_allow":           true,
	"collectors.node_metrics.metric_deny":            true,
	"collectors.node_metrics.drop_labels":            true,
	"collectors.node_metrics.discovery.include_tags": true,
	"collectors.node_metrics.discovery.exclude_tags": true,
}

// structSliceEnvKeys are the config keys whose YAML value is a list of
// structs (tailnets, collectors.node_metrics.targets). Unlike a scalar
// []string field (listEnvKeys, comma-split) or a map field (e.g. otlp.headers,
// which mapstructure decodes correctly one nested key at a time), a
// TS2OTEL_ environment variable that indexes into a list-of-structs key (e.g.
// TS2OTEL_COLLECTORS__NODE_METRICS__TARGETS__0__URL) does NOT do what the
// shape suggests: mapstructure decodes it into a slice holding a
// mostly-empty struct, silently dropping the value the operator meant to set.
// structSliceIndexEnvVars/structSliceEnvVarError (used by Load) turn any such
// variable into a hard, actionable Load error instead of a silent no-op or a
// confusing downstream validation error (see #79). Kept in sync with the
// actual []struct fields on Config by TestStructSliceEnvKeysMatchesStructSliceFields.
var structSliceEnvKeys = map[string]bool{
	"tailnets":                        true,
	"collectors.node_metrics.targets": true,
}

// structSliceEnvHit is one TS2OTEL_* environment variable found to index into
// a structSliceEnvKeys entry, e.g. name="TS2OTEL_TAILNETS__0__NAME" and
// key="tailnets".
type structSliceEnvHit struct {
	name string
	key  string
}

// structSliceIndexEnvVars scans the current environment for TS2OTEL_*
// variables that extend past a known list-of-structs key (a "." followed by
// more path beyond the key itself, e.g. "tailnets.0.name" extends "tailnets").
// The result is sorted by variable name for deterministic error messages.
func structSliceIndexEnvVars() []structSliceEnvHit {
	var hits []structSliceEnvHit
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, EnvPrefix) {
			continue
		}
		key := envKey(name)
		for structKey := range structSliceEnvKeys {
			if strings.HasPrefix(key, structKey+keyDelim) {
				hits = append(hits, structSliceEnvHit{name: name, key: structKey})
				break
			}
		}
	}
	slices.SortFunc(hits, func(a, b structSliceEnvHit) int { return strings.Compare(a.name, b.name) })
	return hits
}

// structSliceEnvVarError builds the actionable Load error for one or more
// structSliceIndexEnvVars hits, naming every offending variable and the
// file-only key it tried to index into.
func structSliceEnvVarError(hits []structSliceEnvHit) error {
	parts := make([]string, len(hits))
	for i, h := range hits {
		parts[i] = fmt.Sprintf("%s (indexes into %q)", h.name, h.key)
	}
	return fmt.Errorf("%s: list-of-structs configuration keys cannot be set via TS2OTEL_ "+
		"environment variables — a flat env var cannot express repeated struct elements and doing so "+
		"silently drops or corrupts the intended value; remove the variable(s) and set the value in a "+
		"YAML config file instead", strings.Join(parts, ", "))
}

// envKey maps a TS2OTEL_* environment variable name to its dotted config key:
// strip the prefix, lowercase, and turn the "__" nesting delimiter into ".". A
// single underscore inside a level is preserved (e.g. client_id stays
// client_id).
func envKey(name string) string {
	k := strings.ToLower(strings.TrimPrefix(name, EnvPrefix))
	return strings.ReplaceAll(k, envNestDelim, keyDelim)
}

// envTransform is the koanf env-provider callback: it converts the variable name
// to a config key and, for the known list fields, splits the value on commas.
func envTransform(name, value string) (string, any) {
	key := envKey(name)
	if listEnvKeys[key] {
		if strings.TrimSpace(value) == "" {
			return key, []string{}
		}
		parts := strings.Split(value, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return key, parts
	}
	return key, value
}

// durationType is the reflect.Type of the config.Duration named type.
var durationType = reflect.TypeFor[Duration]()

// durationDecodeHook lets mapstructure decode a duration string ("30s", "5m")
// from the file/env layers into a config.Duration. Default values arriving from
// the structs provider are already typed (Duration, int64 kind) and pass
// through untouched.
func durationDecodeHook() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data any) (any, error) {
		if to != durationType || from.Kind() != reflect.String {
			return data, nil
		}
		s, _ := data.(string)
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return Duration(d), nil
	}
}

// unknownEnvVars returns the TS2OTEL_* environment variables in the current
// environment whose key does not match any known config key — a likely typo, so
// the value was silently ignored. validKeys is the full set of config keys (from
// the defaults layer). Map subkeys (e.g. "otlp.headers.x_org", which legitimately
// extend past a known leaf) are not flagged. A struct-slice subkey (e.g.
// "tailnets.0.name") is never reached here at all: Load rejects those upfront via
// structSliceIndexEnvVars, before unknownEnvVars runs.
func unknownEnvVars(validKeys []string) []string {
	valid := make(map[string]bool, len(validKeys))
	for _, k := range validKeys {
		valid[k] = true
	}
	var unknown []string
	seen := map[string]bool{}
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, EnvPrefix) || seen[name] {
			continue
		}
		seen[name] = true
		key := envKey(name)
		if valid[key] || keyExtendsKnown(key, valid) {
			continue
		}
		unknown = append(unknown, name)
	}
	slices.Sort(unknown)
	return unknown
}

// keyExtendsKnown reports whether key is a child path of a known collection key
// (e.g. "otlp.headers.x_org" extends the known map key "otlp.headers"), so a
// best-effort map/struct override is not mistaken for a typo.
func keyExtendsKnown(key string, valid map[string]bool) bool {
	for i := strings.LastIndex(key, keyDelim); i > 0; i = strings.LastIndex(key[:i], keyDelim) {
		if valid[key[:i]] {
			return true
		}
	}
	return false
}
