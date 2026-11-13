package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"go.yaml.in/yaml/v3"
)

// defaultKeySet is the canonical set of every leaf configuration key, derived by
// flattening the YAML encoding of Default(). Because the config structs carry no
// `omitempty`, every field — including zero-valued ones — appears, so this is the
// authoritative "every key" list that the example file and the prose reference
// are checked against. A leaf is a scalar, a list, or an empty map (e.g.
// otlp.headers); non-empty nested maps are recursed into.
func defaultKeySet(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := yaml.Marshal(config.Default())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	return flattenYAMLKeys(t, raw)
}

func flattenYAMLKeys(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
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

func sortedKeys(m map[string]bool) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// TestExampleConfigCoversEveryKey guards that config.example.yaml explicitly
// shows EVERY configuration key, not just that it loads to the right values.
// TestExampleConfigMatchesDefaults compares the loaded result, which silently
// passes when a key is omitted (it falls back to the default), so it cannot
// catch a field that was added to the struct but never written into the example.
// This test compares the set of keys literally present in the file against the
// canonical key set from Default(): a missing key means the example is stale, an
// extra key means a typo/rename in the example.
func TestExampleConfigCoversEveryKey(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	want := defaultKeySet(t)
	got := flattenYAMLKeys(t, data)

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
		t.Errorf("config.example.yaml is missing %d key(s) defined by config.Default() — add them so the example documents every field:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("config.example.yaml has %d key(s) not in config.Default() — likely a typo or stale rename:\n  %s",
			len(extra), strings.Join(extra, "\n  "))
	}
}

// TestHelmValuesCoverEveryKey guards the Helm chart's `config:` block against the
// same drift TestExampleConfigCoversEveryKey guards config.example.yaml against.
// The chart renders `.Values.config | toYaml` verbatim as config.yaml (see
// _helpers.tpl "tailscale2otel.config"), and values.yaml declares itself "the FULL
// application config ... kept in sync with config.example.yaml" — so a field added
// to the struct but never written into values.yaml silently leaves chart users
// without a documented knob (values.schema.json and the chart README are both
// generated FROM values.yaml, so the omission propagates into every artifact).
//
// Nothing else catches this: the schema sets no additionalProperties, so an
// omitted key is not rejected at install time, it is merely undiscoverable.
func TestHelmValuesCoverEveryKey(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "helm", "tailscale2otel", "values.yaml"))
	if err != nil {
		t.Fatalf("read chart values: %v", err)
	}
	var values struct {
		Config map[string]any `yaml:"config"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatalf("unmarshal chart values: %v", err)
	}
	cfg, err := yaml.Marshal(values.Config)
	if err != nil {
		t.Fatalf("re-marshal chart config block: %v", err)
	}
	want := defaultKeySet(t)
	got := flattenYAMLKeys(t, cfg)

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
		t.Errorf("deploy/helm/tailscale2otel/values.yaml config: is missing %d key(s) defined by config.Default() — add them so the chart exposes every field:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("deploy/helm/tailscale2otel/values.yaml config: has %d key(s) not in config.Default() — the chart would render an unknown key into config.yaml:\n  %s",
			len(extra), strings.Join(extra, "\n  "))
	}
}

// TestDocsConfigurationMentionsEveryKey requires the key's own path, a
// heading-relative or slash-combined table entry, or an explicit mirrored subtree.
func TestDocsConfigurationMentionsEveryKey(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	keys := defaultKeySet(t)
	documented := documentedConfigKeys(string(data), keys)
	var undocumented []string
	for _, key := range sortedKeys(keys) {
		if !documented[key] {
			undocumented = append(undocumented, key)
		}
	}
	if len(undocumented) > 0 {
		t.Errorf("docs/configuration.md does not mention %d config key(s) — add a reference entry:\n  %s", len(undocumented), strings.Join(undocumented, "\n  "))
	}
}

func documentedConfigKeys(doc string, keys map[string]bool) map[string]bool {
	documented := map[string]bool{}
	keyChar := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
	}
	for key := range keys {
		for offset := 0; offset < len(doc); {
			i := strings.Index(doc[offset:], key)
			if i < 0 {
				break
			}
			i += offset
			end := i + len(key)
			if (i == 0 || !keyChar(doc[i-1]) && doc[i-1] != '.') && (end == len(doc) || !keyChar(doc[end])) {
				documented[key] = true
				break
			}
			offset = end
		}
	}
	quoted := regexp.MustCompile("`([^`]+)`")
	prefix := ""
	for line := range strings.SplitSeq(doc, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			prefix = ""
			matches := quoted.FindAllStringSubmatch(line, -1)
			if len(matches) == 1 && strings.Contains(matches[0][1], ".") {
				candidate := matches[0][1] + "."
				for key := range keys {
					if strings.HasPrefix(key, candidate) {
						prefix = candidate
						break
					}
				}
			}
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cell := strings.Split(line, "|")[1]
		tokens := strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || r == ' ' || r == '`' })
		for i, token := range tokens {
			if i > 0 && strings.HasPrefix(token, ".") && strings.Contains(tokens[0], ".") {
				first := tokens[0]
				key := first[:strings.LastIndex(first, ".")] + token
				if keys[first] && keys[key] {
					documented[key] = true
				}
			} else if !strings.Contains(token, ".") && prefix != "" && keys[prefix+token] {
				documented[prefix+token] = true
			}
		}
	}
	mirrors := map[string]string{
		"collectors.auditlogs.objectstore.": "collectors.flowlogs.objectstore.",
		"collectors.k8s_audit.objectstore.": "collectors.flowlogs.objectstore.",
	}
	for prefix, source := range mirrors {
		if !strings.Contains(doc, "`"+prefix+"*`") {
			continue
		}
		for key := range keys {
			if strings.HasPrefix(key, prefix) && documented[source+strings.TrimPrefix(key, prefix)] {
				documented[key] = true
			}
		}
	}
	return documented
}

func TestDocumentedConfigKeys(t *testing.T) {
	keys := map[string]bool{
		"first.enabled": true, "second.enabled": true, "streaming.tls.cert_file": true,
		"streaming.tls.key_file": true, "collector.discovery.interval": true,
		"collectors.flowlogs.objectstore.bucket":  true,
		"collectors.auditlogs.objectstore.bucket": true, "collectors.k8s_audit.objectstore.bucket": true,
	}
	cases := []struct {
		name, doc string
		want      []string
	}{
		{"full path", "`first.enabled`", []string{"first.enabled"}},
		{"shared leaf", "| enabled | true |", nil},
		{"bounded path", "prefixfirst.enabled first.enabled_suffix", nil},
		{"descendant path", "`first.enabled.child`", []string{"first.enabled"}},
		{"slash", "| `streaming.tls.cert_file` / `.key_file` |", []string{"streaming.tls.cert_file", "streaming.tls.key_file"}},
		{"heading", "#### `collector.discovery`\n| interval |", []string{"collector.discovery.interval"}},
		{"ambiguous heading", "#### `collector.discovery` and `other.path`\n| interval |", nil},
		{"reset heading", "#### `collector.discovery`\n#### Other\n| interval |", nil},
		{"mirrors", "`collectors.flowlogs.objectstore.bucket` `collectors.auditlogs.objectstore.*` `collectors.k8s_audit.objectstore.*`", []string{"collectors.flowlogs.objectstore.bucket", "collectors.auditlogs.objectstore.bucket", "collectors.k8s_audit.objectstore.bucket"}},
		{"undeclared mirror", "`collectors.flowlogs.objectstore.bucket`", []string{"collectors.flowlogs.objectstore.bucket"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := documentedConfigKeys(tc.doc, keys)
			want := map[string]bool{}
			for _, key := range tc.want {
				want[key] = true
			}
			for key := range keys {
				if got[key] != want[key] {
					t.Errorf("documented[%s] = %v, want %v", key, got[key], want[key])
				}
			}
		})
	}
}
