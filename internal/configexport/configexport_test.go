package configexport_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/configexport"
	"gopkg.in/yaml.v3"
)

// TestBuildFullConfigMap_CompletenessMatchesDefaultKeys is the reflection/
// completeness guard issue #320 asks for, in the same spirit as
// internal/config/completeness_test.go: it derives the authoritative "every
// config key" set by flattening the YAML encoding of config.Default() (every
// field, because the config structs carry no `omitempty`), and asserts
// configexport.Build produces EXACTLY that key set. Because configexport.Build
// is a generic reflect walk over every exported field (no hand-maintained
// list), the only way this test can fail from a genuinely new config field is
// if the walker's type-switch doesn't know how to render that field's Go
// kind — which is exactly the failure #320 is about.
func TestBuildFullConfigMap_CompletenessMatchesDefaultKeys(t *testing.T) {
	want := defaultKeySet(t)
	got := configexport.Build(config.Default())

	gotKeys := make(map[string]bool, len(got))
	for k := range got {
		gotKeys[k] = true
	}

	var missing, extra []string
	for k := range want {
		if !gotKeys[k] {
			missing = append(missing, k)
		}
	}
	for k := range gotKeys {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("configexport.Build is missing keys present in config.Default(): %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("configexport.Build has keys not in config.Default(): %v", extra)
	}
}

// defaultKeySet flattens the YAML encoding of config.Default() into the set of
// every leaf dotted key, mirroring internal/config/completeness_test.go's
// defaultKeySet/flattenYAMLKeys (duplicated here rather than imported: that
// helper is unexported in the config_test package, and this package must not
// depend on internal/config's test-only code).
func defaultKeySet(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := yaml.Marshal(config.Default())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("unmarshal yaml: %v", err)
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

// TestBuildFullConfigMap_SecretFieldsNeverLeakValue is the sentinel leak test
// issue #320 asks for: it plants a distinctive, unique-per-field sentinel into
// every config.Secret-typed field (scalar and map[string]Secret) and every
// generic map[string]string field (which configexport shows rather than redacts — see its
// comment for why), across every populated slice (tailnets, streaming routes,
// webhook routes, node-metrics targets — the multi-tailnet/dynamic-map
// coverage #320 calls for), then asserts NONE of those sentinels appear
// anywhere in the JSON-encoded configexport.Build output. It also asserts the
// corresponding "*_file" PATH sentinels DO appear — the file path itself is
// safe to show (only the file's content is not, and this map never reads file
// contents), so a passing leak test must not depend on hiding those too.
func TestBuildFullConfigMap_SecretFieldsNeverLeakValue(t *testing.T) {
	cfg := config.Default()
	// Populate one entry in every slice that carries its own secret-bearing
	// fields, so those get planted and walked too (#320's multi-tailnet/
	// dynamic-map requirement) instead of being skipped as empty.
	cfg.Tailnets = append(cfg.Tailnets, config.TailnetConfig{Name: "planted"})
	cfg.Streaming.Routes = append(cfg.Streaming.Routes, config.StreamingRoute{Tailnet: "planted"})
	cfg.Webhook.Routes = append(cfg.Webhook.Routes, config.WebhookRoute{Tailnet: "planted"})
	cfg.Collectors.NodeMetrics.Targets = append(cfg.Collectors.NodeMetrics.Targets, config.NodeMetricsTarget{URL: "http://planted"})

	sentinels, shown := plantSentinels(cfg)
	if len(sentinels) == 0 {
		t.Fatal("plantSentinels planted nothing — the walk found no secret-bearing field, which would make this test vacuous")
	}

	body, err := json.Marshal(configexport.Build(cfg))
	if err != nil {
		t.Fatalf("marshal full config map: %v", err)
	}

	for sentinel, path := range sentinels {
		if strings.Contains(string(body), sentinel) {
			t.Errorf("sentinel for %s leaked into the full-config export: %q found in output", path, sentinel)
		}
	}
	for sentinel, path := range shown {
		if !strings.Contains(string(body), sentinel) {
			t.Errorf("sentinel for %s did not appear, but this value MUST be shown: %q missing from output. "+
				"A *_file PATH is safe to display (only its contents are not), and a plain string map value "+
				"(pyroscope tags, node-metrics labels) is already published to the backend as a tag/attribute. "+
				"Over-redaction is a real cost here, not a safe default.", path, sentinel)
		}
	}
}

// plantSentinels reflects over cfg and, for every config.Secret-typed field
// (scalar or map[string]Secret) and every generic map[string]string field,
// overwrites it with a distinctive sentinel value, returning a map of
// sentinel -> field path for assertions. It also plants a distinctive path
// string into every "*_file" sibling of a Secret field it finds, returned
// separately in shown, since those are expected to be SHOWN (paths are
// safe) rather than hidden.
func plantSentinels(cfg *config.Config) (secrets map[string]string, shown map[string]string) {
	// secrets: sentinels that must NEVER appear in the output.
	// shown:   sentinels that MUST appear — a *_file PATH (the path is safe,
	//          its contents are not) and a plain string map value (already
	//          published to the backend as a tag/attribute). Asserting both
	//          directions is what stops a future "redact everything" change
	//          from passing this test by hiding data operators need.
	secrets = map[string]string{}
	shown = map[string]string{}
	secretType := reflect.TypeOf(config.Secret(""))
	n := 0
	var walk func(v reflect.Value, path string)
	walk = func(v reflect.Value, path string) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			walk(v.Elem(), path)
		case reflect.Struct:
			t := v.Type()
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.PkgPath != "" {
					continue
				}
				fv := v.Field(i)
				childPath := path + "." + f.Name
				if fv.Type() == secretType {
					n++
					sentinel := fmt.Sprintf("SENTINEL-SECRET-%d", n)
					fv.Set(reflect.ValueOf(config.Secret(sentinel)))
					secrets[sentinel] = childPath
					// Plant a distinguishable PATH (not a secret) into the
					// sibling "*File" field, if this field has one, so the
					// leak test can also assert paths are NOT hidden.
					if ff, ok := t.FieldByName(f.Name + "File"); ok && ff.Type.Kind() == reflect.String {
						n++
						filePath := fmt.Sprintf("/sentinel/path/%d", n)
						v.FieldByName(f.Name + "File").SetString(filePath)
						shown[filePath] = childPath + "File"
					}
					continue
				}
				walk(fv, childPath)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		case reflect.Map:
			elemType := v.Type().Elem()
			switch {
			case elemType == secretType:
				n++
				sentinel := fmt.Sprintf("SENTINEL-MAPSECRET-%d", n)
				nv := reflect.MakeMap(v.Type())
				nv.SetMapIndex(reflect.ValueOf("planted-key"), reflect.ValueOf(config.Secret(sentinel)))
				v.Set(nv)
				secrets[sentinel] = path + ".planted-key"
			case elemType.Kind() == reflect.String:
				// A plain string map is profiling.pyroscope.tags or a
				// node-metrics target's labels. Those values are published
				// verbatim to the backend as profile tags / metric attributes,
				// so they are shown, not redacted — and this sentinel asserts
				// they ARE, so a future blanket redaction here is caught rather
				// than quietly costing an operator the one local place they
				// could check an effective label.
				n++
				sentinel := fmt.Sprintf("SENTINEL-MAPSTRING-%d", n)
				nv := reflect.MakeMap(v.Type())
				nv.SetMapIndex(reflect.ValueOf("planted-key"), reflect.ValueOf(sentinel))
				v.Set(nv)
				shown[sentinel] = path + ".planted-key"
			}
		}
	}
	walk(reflect.ValueOf(cfg).Elem(), "")
	return secrets, shown
}
