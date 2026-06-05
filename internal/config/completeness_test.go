package config_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/config"
	"gopkg.in/yaml.v3"
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

// TestDocsConfigurationMentionsEveryKey is a best-effort guard that every
// configuration key is described somewhere in docs/configuration.md. A key
// counts as documented if its full dotted path appears in the prose, OR its leaf
// name appears as the first cell of a markdown table row (the reference uses
// both forms: full dotted paths in the per-section tables and bare leaf names in
// the windowing / node_metrics.discovery sub-tables).
//
// It is intentionally lenient — leaf-name matching means a NEW field that reuses
// a common leaf already present in the doc (e.g. another collector's `enabled`)
// can slip through. TestExampleConfigCoversEveryKey is the rigorous guard;
// this one catches the common case of a distinctly-named new field that nobody
// wrote a reference entry for.
func TestDocsConfigurationMentionsEveryKey(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatalf("read docs/configuration.md: %v", err)
	}
	doc := string(data)
	leaves := tableLeafTokens(doc)

	var undocumented []string
	for _, key := range sortedKeys(defaultKeySet(t)) {
		leaf := key
		if i := strings.LastIndex(key, "."); i >= 0 {
			leaf = key[i+1:]
		}
		if strings.Contains(doc, key) || leaves[leaf] {
			continue
		}
		undocumented = append(undocumented, key)
	}
	if len(undocumented) > 0 {
		t.Errorf("docs/configuration.md does not mention %d config key(s) — add a reference entry:\n  %s",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}
}

// tableLeafTokens returns the set of leaf names documented in the first column
// of any markdown table row. The reference writes keys in several forms — a full
// dotted path (`collectors.devices.collect_routes`), a bare leaf
// (`instance_source`), and slash-combined shorthand
// (`streaming.tls.cert_file` / `.key_file`) — so each first cell is split on `/`
// and whitespace, backticks stripped, and every token reduced to its leaf (the
// part after the last dot). Leaf-level matching is deliberately lenient; see the
// test doc comment.
func tableLeafTokens(doc string) map[string]bool {
	leaves := map[string]bool{}
	for line := range strings.SplitSeq(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		parts := strings.Split(trimmed, "|")
		if len(parts) < 2 {
			continue
		}
		for _, tok := range strings.FieldsFunc(parts[1], func(r rune) bool {
			return r == '/' || r == ' ' || r == '`'
		}) {
			if i := strings.LastIndex(tok, "."); i >= 0 {
				tok = tok[i+1:]
			}
			if tok != "" {
				leaves[tok] = true
			}
		}
	}
	return leaves
}
