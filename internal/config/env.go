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
// slice. Complex collections (the otlp.headers map, node_metrics.targets list of
// structs) are not settable via flat env vars — use a config file for those.
var listEnvKeys = map[string]bool{
	"tailscale.auth.oauth.scopes":                    true,
	"collectors.devices.attribute_namespaces":        true,
	"collectors.node_metrics.metric_allow":           true,
	"collectors.node_metrics.metric_deny":            true,
	"collectors.node_metrics.drop_labels":            true,
	"collectors.node_metrics.discovery.include_tags": true,
	"collectors.node_metrics.discovery.exclude_tags": true,
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
// the defaults layer). Complex collection subkeys (map/struct-slice element
// keys, which legitimately extend past a known leaf) are not flagged.
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
