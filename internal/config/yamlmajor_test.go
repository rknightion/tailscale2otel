package config_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	yamlv3 "go.yaml.in/yaml/v3"
	yamlv4 "go.yaml.in/yaml/v4"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

// This file is the differential proof behind the go.yaml.in/yaml v3 -> v4
// move. config.Load itself decodes through koanf's YAML parser, which is
// pinned to yaml/v3 whatever this module imports, so the file-load behavior
// is pinned here as a table that was recorded on the v3 tree and must not
// move. Everything this module decodes with yaml directly (Duration's
// UnmarshalYAML, the env reference generator's node walk, the effective-config
// encoder) is compared against a v3 reference decode in the same test run.

// legacyDuration is config.Duration exactly as it was written against v3.
type legacyDuration time.Duration

func (d *legacyDuration) UnmarshalYAML(value *yamlv3.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = legacyDuration(parsed)
	return nil
}

// durationFixtures are scalar spellings an operator could plausibly write for
// a duration key, including every shape that must stay rejected.
var durationFixtures = []string{
	"30s", "5m", "168h", "500ms", "1h30m", "1.5s", "0s", "0", "-5s",
	"30", "3e9", "0x10", "1d", "notaduration",
	"''", "\"\"", "'30s'", "\"1h30m\"", "~", "null",
	"[1]", "{a: 1}", "[30s]",
}

// loadDuration runs a one-key file through the real config.Load path and
// reports whether the value decoded, and what it decoded to. A Validate-only
// failure still counts as decoded: Load returns the config alongside it.
func loadDuration(t *testing.T, spelling string) (decoded bool, got time.Duration) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "delivery:\n  log_max_record_age: " + spelling + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(path)
	if cfg == nil {
		if err == nil {
			t.Fatalf("Load(%q) returned neither a config nor an error", spelling)
		}
		return false, 0
	}
	return true, cfg.Delivery.LogMaxRecordAge.D()
}

// TestLoadDurationDecodingIsPinned pins config.Load's duration decoding. The
// table was recorded against the yaml/v3 tree before the move and is the
// owner's "must stay identical" contract for the real Load path.
func TestLoadDurationDecodingIsPinned(t *testing.T) {
	want := map[string]struct {
		decoded bool
		value   time.Duration
	}{
		"30s": {true, 30 * time.Second}, "5m": {true, 5 * time.Minute}, "168h": {true, 168 * time.Hour},
		"500ms": {true, 500 * time.Millisecond}, "1h30m": {true, 90 * time.Minute}, "1.5s": {true, 1500 * time.Millisecond},
		"0s": {true, 0}, "0": {true, 0}, "-5s": {true, -5 * time.Second},
		"30": {false, 0}, "3e9": {false, 0}, "0x10": {false, 0}, "1d": {false, 0}, "notaduration": {false, 0},
		"''": {false, 0}, "\"\"": {false, 0}, "'30s'": {true, 30 * time.Second}, "\"1h30m\"": {true, 90 * time.Minute},
		"~": {true, 0}, "null": {true, 0},
		"[1]": {false, 0}, "{a: 1}": {false, 0}, "[30s]": {false, 0},
	}
	for _, spelling := range durationFixtures {
		w, ok := want[spelling]
		if !ok {
			t.Fatalf("fixture %q has no pinned expectation", spelling)
		}
		decoded, got := loadDuration(t, spelling)
		if decoded != w.decoded || got != w.value {
			t.Errorf("Load duration %q: decoded=%v value=%v, pinned decoded=%v value=%v", spelling, decoded, got, w.decoded, w.value)
		}
	}
}

// TestDurationV3AndV4DecodeIdentically compares the v3 reference decode with
// the real config.Duration under v4 for every fixture, and checks that
// wherever v4 accepts a value config.Load accepts the same one.
func TestDurationV3AndV4DecodeIdentically(t *testing.T) {
	for _, spelling := range durationFixtures {
		var ref legacyDuration
		refErr := yamlv3.Unmarshal([]byte(spelling), &ref)
		var cur config.Duration
		curErr := yamlv4.Unmarshal([]byte(spelling), &cur)
		if (refErr == nil) != (curErr == nil) {
			t.Errorf("%q: v3 err=%v, v4 err=%v", spelling, refErr, curErr)
			continue
		}
		if refErr != nil {
			continue
		}
		if time.Duration(ref) != cur.D() {
			t.Errorf("%q: v3=%v, v4=%v", spelling, time.Duration(ref), cur.D())
		}
		// A scalar duration the YAML decode accepts must load identically.
		if strings.ContainsAny(spelling, "[{") {
			continue
		}
		if decoded, got := loadDuration(t, spelling); decoded && got != cur.D() {
			t.Errorf("%q: config.Load=%v, v4 Duration=%v", spelling, got, cur.D())
		}
	}
}

// unknownKeyFixtures are whole config files. Each is classified by the real
// config.Load path and then parsed by both yaml majors.
var unknownKeyFixtures = []struct {
	name    string
	body    string
	unknown bool // config.Load must reject it as an unknown key
	badYAML bool // not a valid document for config.Load at all
}{
	{name: "known keys only", body: "log_level: debug\nadmin:\n  enabled: true\n"},
	{name: "unknown top level", body: "log_leevl: debug\n", unknown: true},
	{name: "unknown nested", body: "admin:\n  enabeld: true\n", unknown: true},
	{name: "unknown under list entry", body: "tailnets:\n  - name: a\n    bogus: 1\n", unknown: true},
	{name: "map children stay open", body: "otlp:\n  headers:\n    x_org: a\n    X-Other: b\n"},
	{name: "unknown inside flow mapping", body: "admin: {enabled: true, nope: 1}\n", unknown: true},
	{name: "unknown beside anchor merge", body: "collectors:\n  devices: &d {enabled: true}\n  dns: {<<: *d, nope: 1}\n", unknown: true},
	{name: "known keys through anchor merge", body: "collectors:\n  devices: &d {enabled: true}\n  dns: {<<: *d}\n"},
	{name: "duplicate key", body: "log_level: debug\nlog_level: info\n", badYAML: true},
	{name: "tab indentation", body: "admin:\n\tenabled: true\n", badYAML: true},
}

func loadFixture(t *testing.T, body string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := config.Load(path)
	return err
}

// leafKeys flattens a decoded document into sorted "dotted.key=%T:value" rows.
func leafKeys(prefix string, v any, out *[]string) {
	switch m := v.(type) {
	case map[string]any:
		if len(m) == 0 {
			*out = append(*out, prefix+"={}")
		}
		for k, c := range m {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			leafKeys(key, c, out)
		}
	case []any:
		for i, c := range m {
			leafKeys(fmt.Sprintf("%s[%d]", prefix, i), c, out)
		}
		if len(m) == 0 {
			*out = append(*out, prefix+"=[]")
		}
	default:
		*out = append(*out, fmt.Sprintf("%s=%T:%v", prefix, v, v))
	}
}

func TestUnknownKeyRejectionV3AndV4Agree(t *testing.T) {
	for _, tc := range unknownKeyFixtures {
		t.Run(tc.name, func(t *testing.T) {
			err := loadFixture(t, tc.body)
			isUnknown := err != nil && strings.Contains(err.Error(), "unknown")
			if isUnknown != tc.unknown {
				t.Fatalf("config.Load unknown-key rejection = %v (err=%v), pinned %v", isUnknown, err, tc.unknown)
			}
			if tc.badYAML && err == nil {
				t.Fatalf("config.Load accepted a document it must reject")
			}
			if !tc.unknown && !tc.badYAML && err != nil {
				t.Fatalf("config.Load rejected a valid document: %v", err)
			}

			var d3, d4 map[string]any
			err3 := yamlv3.Unmarshal([]byte(tc.body), &d3)
			err4 := yamlv4.Unmarshal([]byte(tc.body), &d4)
			if (err3 == nil) != (err4 == nil) {
				t.Fatalf("parse acceptance differs: v3 err=%v, v4 err=%v", err3, err4)
			}
			if tc.badYAML && err3 == nil {
				t.Fatalf("fixture is bad YAML for config.Load but v3 parses it")
			}
			if err3 != nil {
				return
			}
			var k3, k4 []string
			leafKeys("", d3, &k3)
			leafKeys("", d4, &k4)
			sort.Strings(k3)
			sort.Strings(k4)
			if !reflect.DeepEqual(k3, k4) {
				t.Fatalf("decoded key sets differ\nv3: %v\nv4: %v", k3, k4)
			}
		})
	}
}

// yamlNodeView is a neutral picture of the node fields the env reference
// generator and the schema tests read, so a v3 and a v4 tree can be compared.
type yamlNodeView struct {
	Kind               string
	Tag, Value, Anchor string
	Head, Line, Foot   string
	LineNo, Column     int
	Content            []yamlNodeView
}

func viewV3(n *yamlv3.Node) yamlNodeView {
	kinds := map[yamlv3.Kind]string{
		yamlv3.DocumentNode: "doc", yamlv3.SequenceNode: "seq", yamlv3.MappingNode: "map",
		yamlv3.ScalarNode: "scalar", yamlv3.AliasNode: "alias",
	}
	v := yamlNodeView{
		Kind: kinds[n.Kind], Tag: n.ShortTag(), Value: n.Value, Anchor: n.Anchor,
		Head: n.HeadComment, Line: n.LineComment, Foot: n.FootComment, LineNo: n.Line, Column: n.Column,
	}
	for _, c := range n.Content {
		v.Content = append(v.Content, viewV3(c))
	}
	return v
}

func viewV4(n *yamlv4.Node) yamlNodeView {
	kinds := map[yamlv4.Kind]string{
		yamlv4.DocumentNode: "doc", yamlv4.SequenceNode: "seq", yamlv4.MappingNode: "map",
		yamlv4.ScalarNode: "scalar", yamlv4.AliasNode: "alias",
	}
	v := yamlNodeView{
		Kind: kinds[n.Kind], Tag: n.ShortTag(), Value: n.Value, Anchor: n.Anchor,
		Head: n.HeadComment, Line: n.LineComment, Foot: n.FootComment, LineNo: n.Line, Column: n.Column,
	}
	for _, c := range n.Content {
		v.Content = append(v.Content, viewV4(c))
	}
	return v
}

// TestEnvReferenceNodeTreeV3AndV4Agree proves the node walk behind
// docs/env-vars.md (kinds, values, inline comments, order) reads an identical
// tree under both majors for the real example config and for comment and
// scalar shapes the walk depends on.
func TestEnvReferenceNodeTreeV3AndV4Agree(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read config.example.yaml: %v", err)
	}
	docs := map[string][]byte{
		"config.example.yaml": example,
		"inline comments":     []byte("# banner\na: 1 # one\nb:\n  c: \"x\" # two\n  d: [1, 2] # three\n  e: {} # four\n"),
		"empty and null":      []byte("a: \"\"\nb:\nc: ~\nd: []\n"),
		"anchors":             []byte("a: &x {k: v}\nb: *x\nc: {<<: *x}\n"),
	}
	for name, src := range docs {
		var n3 yamlv3.Node
		var n4 yamlv4.Node
		if err := yamlv3.Unmarshal(src, &n3); err != nil {
			t.Fatalf("%s: v3 parse: %v", name, err)
		}
		if err := yamlv4.Unmarshal(src, &n4); err != nil {
			t.Fatalf("%s: v4 parse: %v", name, err)
		}
		if !reflect.DeepEqual(viewV3(&n3), viewV4(&n4)) {
			t.Errorf("%s: node trees differ between yaml v3 and v4", name)
		}
	}
}

// TestEnvOverlayDurationDecodingIsPinned pins the TS2OTEL_* path, which never
// touches a YAML library: a duration set from the environment decodes the same
// before and after the move.
func TestEnvOverlayDurationDecodingIsPinned(t *testing.T) {
	cases := []struct {
		in      string
		decoded bool
		want    time.Duration
	}{
		{"30m", true, 30 * time.Minute}, {"0s", true, 0}, {"1h30m", true, 90 * time.Minute},
		{"30", false, 0}, {"1d", false, 0}, {"notaduration", false, 0},
	}
	for _, tc := range cases {
		t.Setenv("TS2OTEL_DELIVERY__LOG_MAX_RECORD_AGE", tc.in)
		cfg, err := config.Load("")
		if (cfg != nil) != tc.decoded {
			t.Errorf("env %q: decoded=%v (err=%v), pinned %v", tc.in, cfg != nil, err, tc.decoded)
			continue
		}
		if cfg != nil && cfg.Delivery.LogMaxRecordAge.D() != tc.want {
			t.Errorf("env %q: got %v, pinned %v", tc.in, cfg.Delivery.LogMaxRecordAge.D(), tc.want)
		}
	}
}

// TestMarshalV3AndV4Agree compares the bytes the effective-config encoder and
// the redacting Secret marshaler produce under both majors.
func TestMarshalV3AndV4Agree(t *testing.T) {
	def := config.Default()
	def.Admin.Auth.Token = config.Secret("s3cret-value")
	b3, err3 := yamlv3.Marshal(def)
	b4, err4 := yamlv4.Marshal(def)
	if err3 != nil || err4 != nil {
		t.Fatalf("marshal: v3=%v v4=%v", err3, err4)
	}
	if !bytes.Equal(b3, b4) {
		t.Errorf("Marshal(Default()) differs between yaml v3 and v4")
	}
	if bytes.Contains(b4, []byte("s3cret-value")) {
		t.Errorf("v4 Marshal leaked a Secret")
	}

	rows := []map[string]any{
		{"key": "a.b", "value": "x: y", "secret": false, "set": true, "source": "file"},
		{"key": "c", "value": "multi\nline", "secret": true, "set": false, "source": ""},
	}
	var e3, e4 bytes.Buffer
	enc3 := yamlv3.NewEncoder(&e3)
	enc3.SetIndent(2)
	enc4 := yamlv4.NewEncoder(&e4)
	enc4.SetIndent(2)
	if err := enc3.Encode(rows); err != nil {
		t.Fatal(err)
	}
	if err := enc4.Encode(rows); err != nil {
		t.Fatal(err)
	}
	_ = enc3.Close()
	_ = enc4.Close()
	if e3.String() != e4.String() {
		t.Errorf("NewEncoder output differs\nv3:\n%s\nv4:\n%s", e3.String(), e4.String())
	}
}
