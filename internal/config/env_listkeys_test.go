package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// stringSliceFieldPaths walks the Config struct and returns the dotted yaml-key
// path of every []string field. These are exactly the scalar-list fields whose
// environment value must be comma-split, so the result must equal the
// listEnvKeys registry. Slices of structs (node_metrics.targets) and maps
// (otlp.headers) are file-only and deliberately excluded.
func stringSliceFieldPaths(t reflect.Type, prefix string, out map[string]bool) {
	for f := range t.Fields() {
		if f.PkgPath != "" { // unexported (e.g. unknownEnv) — not a config key
			continue
		}
		tag := f.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		switch f.Type.Kind() {
		case reflect.Struct:
			stringSliceFieldPaths(f.Type, path, out)
		case reflect.Slice:
			if f.Type.Elem().Kind() == reflect.String {
				out[path] = true
			}
		}
	}
}

// TestListEnvKeysMatchesStringSliceFields guards that listEnvKeys (the registry
// that tells envTransform which env values to comma-split) stays in sync with
// the actual []string fields on Config. Add a new scalar-list field without
// registering it and its env var would be parsed as a single-element list;
// register a key that no longer exists and the entry is dead. Either way this
// fails with the exact offending key.
func TestListEnvKeysMatchesStringSliceFields(t *testing.T) {
	fields := map[string]bool{}
	stringSliceFieldPaths(reflect.TypeFor[Config](), "", fields)

	var unregistered, stale []string
	for k := range fields {
		if !listEnvKeys[k] {
			unregistered = append(unregistered, k)
		}
	}
	for k := range listEnvKeys {
		if !fields[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(unregistered)
	sort.Strings(stale)

	if len(unregistered) > 0 {
		t.Errorf("[]string config field(s) missing from listEnvKeys (env values won't be comma-split):\n  %s",
			strings.Join(unregistered, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("listEnvKeys entries that match no []string field (dead/renamed):\n  %s",
			strings.Join(stale, "\n  "))
	}
}
