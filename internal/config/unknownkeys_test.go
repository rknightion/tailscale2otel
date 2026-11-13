package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"go.yaml.in/yaml/v3"
)

// defaultsValidKeys mirrors the validKeys computation in Load: the full set
// of dotted config keys from the defaults layer alone.
func defaultsValidKeys(t *testing.T) []string {
	t.Helper()
	k := koanf.New(keyDelim)
	if err := k.Load(structs.Provider(Default(), "yaml"), nil); err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	return k.Keys()
}

// writeTempConfig writes contents to a temp YAML file and returns its path.
func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadRejectsUnknownTopLevelKey(t *testing.T) {
	path := writeTempConfig(t, "log_leevl: debug\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for an unknown top-level key, got nil")
	}
	if !strings.Contains(err.Error(), "log_leevl") {
		t.Fatalf("expected error to name the unknown key %q, got: %v", "log_leevl", err)
	}
}

func TestLoadRejectsUnknownNestedKey(t *testing.T) {
	path := writeTempConfig(t, "collectors:\n  devices:\n    intervaal: 30s\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for an unknown nested key, got nil")
	}
	if !strings.Contains(err.Error(), "collectors.devices.intervaal") {
		t.Fatalf("expected error to name the full dotted path %q, got: %v",
			"collectors.devices.intervaal", err)
	}
}

func TestLoadRejectsIssue303ExactKeys(t *testing.T) {
	path := writeTempConfig(t, "log_leevl: debug\ncollectors:\n  devices:\n    intervaal: 30s\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for the two keys from issue #303, got nil")
	}
	for _, want := range []string{"log_leevl", "collectors.devices.intervaal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got: %v", want, err)
		}
	}
}

func TestLoadUnknownKeySuggestsNearMiss(t *testing.T) {
	// "log_level" is a real top-level key (one character off from "log_leevl").
	path := writeTempConfig(t, "log_leevl: debug\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "log_level") {
		t.Fatalf("expected error to suggest the near-miss key %q, got: %v", "log_level", err)
	}
}

func TestLoadUnknownKeyNoNonsenseSuggestion(t *testing.T) {
	// A wholly unrelated key should not receive a suggestion at all — nothing
	// in the valid key set is within a sane edit distance of "zzzznotreal".
	unknown := []string{"zzzznotreal"}
	err := unknownKeyError(unknown, defaultsValidKeys(t))
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	msg := err.Error()
	if strings.Contains(msg, "did you mean") {
		t.Fatalf("expected no suggestion for a wholly unrelated key, got: %v", msg)
	}
}

func TestLoadReportsAllUnknownKeysInOneError(t *testing.T) {
	path := writeTempConfig(t, "log_leevl: debug\ncollectors:\n  devices:\n    intervaal: 30s\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, want := range []string{"log_leevl", "collectors.devices.intervaal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected single error to mention %q, got: %v", want, err)
		}
	}
}

func TestLoadAcceptsOTLPHeadersDynamicMap(t *testing.T) {
	path := writeTempConfig(t, "otlp:\n  headers:\n    x_scope_orgid: \"12345\"\n")
	_, err := Load(path)
	if err != nil {
		t.Fatalf("expected otlp.headers.* to be accepted as a dynamic map, got: %v", err)
	}
}

func TestLoadAcceptsMultiTailnetList(t *testing.T) {
	path := writeTempConfig(t, strings.Join([]string{
		"tailnets:",
		"  - name: alpha.example.com",
		"    auth:",
		"      method: apikey",
		"      apikey: alpha-key",
		"  - name: beta.example.com",
		"    auth:",
		"      method: apikey",
		"      apikey: beta-key",
		"",
	}, "\n"))
	_, err := Load(path)
	if err != nil {
		t.Fatalf("expected a multi-tailnet config to be accepted, got: %v", err)
	}
}

func TestLoadAcceptsConfigExampleYAML(t *testing.T) {
	// config.example.yaml lives at the repo root; internal/config is one level
	// down.
	_, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatalf("expected config.example.yaml to load clean, got: %v", err)
	}
}
func TestUnknownFileKeysDirect(t *testing.T) {
	valid := []string{"log_level", "otlp.headers", "tailnets", "collectors.devices.interval"}
	fileKeys := []string{"log_level", "log_leevl", "otlp.headers.x_scope_orgid", "tailnets",
		"collectors.devices.intervaal"}

	got := unknownFileKeys(fileKeys, valid)
	want := []string{"collectors.devices.intervaal", "log_leevl"}

	if len(got) != len(want) {
		t.Fatalf("unknownFileKeys(%v, %v) = %v, want %v", fileKeys, valid, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unknownFileKeys(%v, %v) = %v, want %v", fileKeys, valid, got, want)
		}
	}
}

func TestUnknownKeyErrorNeverEchoesValues(t *testing.T) {
	// The error must name key paths only, never a config value — even if a
	// caller accidentally passed something value-shaped, the error builder's
	// contract is "keys only", so this documents/guards that shape.
	err := unknownKeyError([]string{"collectors.devices.intervaal"}, defaultsValidKeys(t))
	if err == nil {
		t.Fatal("expected a non-nil error")
	}
	if !strings.Contains(err.Error(), "collectors.devices.intervaal") {
		t.Fatalf("expected the key path in the error, got: %v", err)
	}
}

// A key this project REMOVED is not a typo, and telling an operator "did you
// mean cardinality.flow.destination_port?" sends them to fix the wrong thing.
//
// It also matters more than an ordinary unknown key: CHANGELOG.md and the chart
// notes for 0.13.0 both promised that a leftover destination_service: would be
// SILENTLY IGNORED. Rejecting unknown keys breaks that promise, so an upgrade
// that was documented as safe now fails at startup. The error has to say so.
func TestRemovedKeyIsNamedAsRemovedNotAsATypo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("cardinality:\n  flow:\n    destination_service: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("a removed key loaded successfully; it must still be rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cardinality.flow.destination_service") {
		t.Errorf("error does not name the key: %s", msg)
	}
	if !strings.Contains(msg, "removed") {
		t.Errorf("error does not say the key was REMOVED, so an operator reads it as a typo and "+
			"goes looking for the correct spelling of a key that no longer exists: %s", msg)
	}
	if strings.Contains(msg, "did you mean") {
		t.Errorf("a removed key was offered a spelling suggestion, which points at a DIFFERENT "+
			"setting than the one that went away: %s", msg)
	}
}

func TestLoadRejectsUnknownKeysInListEntries(t *testing.T) {
	cases := []struct{ name, yaml, key, suggestion string }{
		{"tailnet", "tailnets:\n  - name: fixture\n    auth:\n      oauth:\n        client_secert: synthetic\n", "tailnets[0].auth.oauth.client_secert", "tailnets[0].auth.oauth.client_secret"},
		{"section", "tailnets:\n  - name: fixture\n    auht: {}\n", "tailnets[0].auht", "tailnets[0].auth"},
		{"target", "collectors:\n  node_metrics:\n    targets:\n      - bearer_tokeen: synthetic\n", "collectors.node_metrics.targets[0].bearer_tokeen", "collectors.node_metrics.targets[0].bearer_token"},
		{"stream route", "streaming:\n  routes:\n    - tokeen: synthetic\n", "streaming.routes[0].tokeen", "streaming.routes[0].token"},
		{"webhook route", "webhook:\n  routes:\n    - secrte: synthetic\n", "webhook.routes[0].secrte", "webhook.routes[0].secret"},
		{"posture", "collectors:\n  devices:\n    posture_compliance_checks:\n      - naem: fixture\n", "collectors.devices.posture_compliance_checks[0].naem", "collectors.devices.posture_compliance_checks[0].name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTempConfig(t, tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), "did you mean \""+tc.suggestion+"\"") {
				t.Fatalf("want indexed unknown key and local suggestion, got %v", err)
			}
		})
	}
	t.Run("dynamic maps", func(t *testing.T) {
		cfg, err := Load(writeTempConfig(t, "collectors:\n  node_metrics:\n    targets:\n      - url: https://metrics.example.invalid\n        headers:\n          X-Fixture: synthetic\n        labels:\n          fixture: arbitrary\n"))
		if err != nil {
			t.Fatal(err)
		}
		target := cfg.Collectors.NodeMetrics.Targets[0]
		if target.Labels["fixture"] != "arbitrary" || target.Headers["X-Fixture"].Reveal() != "synthetic" {
			t.Fatal("dynamic maps not retained")
		}
	})
}

func TestUnknownListEntryKeysCoversConfigStructLists(t *testing.T) {
	var walk func(reflect.Type, []string)
	walk = func(typ reflect.Type, path []string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
			if field.PkgPath != "" || name == "" || name == "-" {
				continue
			}
			next := append(append([]string(nil), path...), name)
			ft := field.Type
			if ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct {
				t.Run(strings.Join(next, "."), func(t *testing.T) {
					var data any = []any{map[string]any{"unexpected_fixture_key": true}}
					for i := len(next) - 1; i >= 0; i-- {
						data = map[string]any{next[i]: data}
					}
					raw, err := yaml.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
					_, err = Load(writeTempConfig(t, string(raw)))
					want := strings.Join(next, ".") + "[0].unexpected_fixture_key"
					if err == nil || !strings.Contains(err.Error(), want) {
						t.Fatalf("want rejection of %s, got %v", want, err)
					}
				})
			} else {
				walk(ft, next)
			}
		}
	}
	walk(reflect.TypeFor[Config](), nil)
}

func TestUnknownStructListKeysNestedLists(t *testing.T) {
	type child struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	}
	type entry struct {
		Children []child `yaml:"children"`
	}
	type root struct {
		Entries []entry `yaml:"entries"`
	}
	data := map[string]any{"entries": []any{map[string]any{"children": []any{
		map[string]any{"name": "fixture", "labels": map[string]any{"arbitrary": "value"}},
		map[string]any{"typo": "value"},
	}}}}
	got, _ := unknownStructListKeys(reflect.TypeFor[root](), data)
	if fmt.Sprint(got) != "[entries[0].children[1].typo]" {
		t.Fatalf("unknown keys = %v", got)
	}
}

func TestLoadRejectsMalformedListEntryKeyNames(t *testing.T) {
	for _, name := range []string{"bad]", "bad].x", "[", "bad[1]", "bad[1].field", "bad.field", "bad["} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeTempConfig(t, "tailnets:\n  - name: fixture\n    \""+name+"\": 1\n"))
			if err == nil || !strings.Contains(err.Error(), "tailnets[0]."+name) {
				t.Fatalf("want unknown key error, got %v", err)
			}
		})
	}
}
