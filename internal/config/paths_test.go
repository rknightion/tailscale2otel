package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// --- resolveConfigPaths: the core #310 resolution rule ---------------------

func TestResolveConfigPaths_RelativeFileSourcedPathResolvesAgainstConfigDir(t *testing.T) {
	c := &Config{}
	c.Checkpoint.FilePath = "state/checkpoints.json"

	got := resolveConfigPaths(c, "/etc/ts2otel/config.yaml", map[string]bool{})

	want := filepath.Join("/etc/ts2otel", "state/checkpoints.json")
	if c.Checkpoint.FilePath != want {
		t.Fatalf("Checkpoint.FilePath = %q, want %q", c.Checkpoint.FilePath, want)
	}
	pr, ok := got["checkpoint.file_path"]
	if !ok {
		t.Fatalf("resolveConfigPaths did not report checkpoint.file_path")
	}
	if pr.Configured != "state/checkpoints.json" || pr.Resolved != want {
		t.Fatalf("pathResolution = %+v, want Configured=%q Resolved=%q", pr, "state/checkpoints.json", want)
	}
}

func TestResolveConfigPaths_AbsolutePathUnchanged(t *testing.T) {
	c := &Config{}
	c.Checkpoint.FilePath = "/var/lib/tailscale2otel/checkpoints.json"

	resolveConfigPaths(c, "/etc/ts2otel/config.yaml", map[string]bool{})

	if c.Checkpoint.FilePath != "/var/lib/tailscale2otel/checkpoints.json" {
		t.Fatalf("an absolute path must be used as-is, got %q", c.Checkpoint.FilePath)
	}
}

func TestResolveConfigPaths_EnvSourcedPathKeepsCWDSemantics(t *testing.T) {
	c := &Config{}
	c.Checkpoint.FilePath = "state/checkpoints.json"

	// The env layer set this key this run: CWD semantics must survive even
	// though a config file IS in play, per the frozen #310 contract.
	resolveConfigPaths(c, "/etc/ts2otel/config.yaml", map[string]bool{"checkpoint.file_path": true})

	if c.Checkpoint.FilePath != "state/checkpoints.json" {
		t.Fatalf("an env-sourced relative path must keep CWD semantics, got %q", c.Checkpoint.FilePath)
	}
}

func TestResolveConfigPaths_NoConfigFileKeepsCWDSemantics(t *testing.T) {
	c := &Config{}
	c.Checkpoint.FilePath = "state/checkpoints.json"

	// No config file at all (env + defaults only): there is no directory to
	// resolve against, so CWD semantics apply.
	resolveConfigPaths(c, "", map[string]bool{})

	if c.Checkpoint.FilePath != "state/checkpoints.json" {
		t.Fatalf("with no config file, a relative path must keep CWD semantics, got %q", c.Checkpoint.FilePath)
	}
}

func TestResolveConfigPaths_EmptyPathUntouched(t *testing.T) {
	c := &Config{}
	// Checkpoint.FilePath left at its zero value ("").

	got := resolveConfigPaths(c, "/etc/ts2otel/config.yaml", map[string]bool{})

	if c.Checkpoint.FilePath != "" {
		t.Fatalf("an unset field must stay empty, got %q", c.Checkpoint.FilePath)
	}
	if pr := got["checkpoint.file_path"]; pr.Configured != "" || pr.Resolved != "" {
		t.Fatalf("pathResolution for an unset field = %+v, want both empty", pr)
	}
}

func TestResolveConfigPaths_ListEntryFileSourcedPathResolves(t *testing.T) {
	c := &Config{}
	c.Tailnets = []TailnetConfig{{Name: "acme"}}
	c.Tailnets[0].Auth.APIKeyFile = "secrets/acme.key"

	resolveConfigPaths(c, "/srv/ts2otel/config.yaml", map[string]bool{})

	want := filepath.Join("/srv/ts2otel", "secrets/acme.key")
	if c.Tailnets[0].Auth.APIKeyFile != want {
		t.Fatalf("tailnets[0].auth.apikey_file = %q, want %q", c.Tailnets[0].Auth.APIKeyFile, want)
	}
}

// --- envSetKeys --------------------------------------------------------

func TestEnvSetKeys_MatchesEnvironmentVariablePrefix(t *testing.T) {
	t.Setenv("TS2OTEL_CHECKPOINT__FILE_PATH", "/tmp/whatever.json")
	t.Setenv("NOT_TS2OTEL_SOMETHING", "irrelevant")

	set := envSetKeys()

	if !set["checkpoint.file_path"] {
		t.Fatalf("envSetKeys() = %v, want checkpoint.file_path present", set)
	}
	if len(set) != 1 {
		t.Fatalf("envSetKeys() = %v, want exactly one key (only TS2OTEL_-prefixed vars count)", set)
	}
}

// --- pathFields completeness guard -----------------------------------------

// isPathTag reports whether a yaml tag names a filesystem-path-bearing field
// by this project's convention: every such field's tag ends in "_file"
// (cert_file, key_file, ca_file, token_file, apikey_file, ...), except the
// two odd ones out, checkpoint's "file_path" and ingress_wal's "directory".
// HTTP route paths (webhook.path, streaming.path, the node-metrics scrape
// path) are a bare "path" and deliberately do not match.
func isPathTag(tag string) bool {
	return strings.HasSuffix(tag, "_file") || strings.HasSuffix(tag, "_database") ||
		tag == "file_path" || tag == "directory"
}

// expectedPathKeys derives the set of path-bearing dotted keys directly from
// the Config struct's yaml tags via reflection -- independent of pathFields'
// own hand-written list -- so TestPathFieldsCoversEveryPathBearingField can
// catch a field that was added to the struct but never registered in
// pathFields. A slice-of-struct field (tailnets, streaming.routes,
// webhook.routes, node_metrics.targets) contributes a "[*]" placeholder
// rather than a concrete index, since reflection over a bare type has no
// slice length to index into.
func expectedPathKeys(t reflect.Type, prefix string) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		ft := f.Type
		switch {
		case ft.Kind() == reflect.String:
			if isPathTag(tag) {
				keys = append(keys, prefix+tag)
			}
		case ft.Kind() == reflect.Struct:
			keys = append(keys, expectedPathKeys(ft, prefix+tag+".")...)
		case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct:
			keys = append(keys, expectedPathKeys(ft.Elem(), prefix+tag+".")...)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			keys = append(keys, expectedPathKeys(ft.Elem(), prefix+tag+"[*].")...)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Pointer && ft.Elem().Elem().Kind() == reflect.Struct:
			keys = append(keys, expectedPathKeys(ft.Elem().Elem(), prefix+tag+"[*].")...)
		}
	}
	return keys
}

// TestPathFieldsCoversEveryPathBearingField guards #310's "every path-bearing
// field follows the same contract" requirement: it independently derives the
// full set of path-bearing keys from the struct tags (expectedPathKeys) and
// fails if pathFields() (the hand-written list resolveConfigPaths and
// resolveSecretFiles both drive off) disagrees with it in either direction --
// a field present in one but not the other means either a newly added config
// field was forgotten here, or a stale/renamed entry lingers.
func TestPathFieldsCoversEveryPathBearingField(t *testing.T) {
	expected := expectedPathKeys(reflect.TypeOf(Config{}), "")
	sort.Strings(expected)

	c := Default()
	// Populate one element of every list-of-structs field (with its optional
	// pointer sub-struct set) so pathFields(), which only visits fields that
	// actually exist on the concrete value, produces the list-entry keys
	// too -- otherwise a zero-length slice would silently omit them and the
	// two sides would agree for the wrong reason.
	c.Tailnets = []TailnetConfig{{}}
	c.Streaming.Routes = []StreamingRoute{{}}
	c.Webhook.Routes = []WebhookRoute{{}}
	c.Collectors.NodeMetrics.Targets = []NodeMetricsTarget{{TLS: &NodeMetricsTargetTLS{}}}

	idxRe := regexp.MustCompile(`\[\d+\]`)
	got := make([]string, 0)
	for _, f := range c.pathFields() {
		got = append(got, idxRe.ReplaceAllString(f.key, "[*]"))
	}
	sort.Strings(got)

	if !reflect.DeepEqual(expected, got) {
		t.Fatalf("pathFields() is out of sync with Config's path-bearing struct fields (#310 requires "+
			"every one to follow the same resolution rule):\nexpected (from struct tags): %v\ngot (from pathFields()):     %v",
			expected, got)
	}
}

// --- end-to-end via Load() --------------------------------------------------

func TestLoad_RelativeCheckpointFilePathResolvesAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(
		"tailscale:\n  tailnet: acme.org\n  auth:\n    method: apikey\n    apikey: x\n"+
			"checkpoint:\n  store: file\n  file_path: state/checkpoints.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(dir, "state", "checkpoints.json")
	if cfg.Checkpoint.FilePath != want {
		t.Fatalf("Checkpoint.FilePath = %q, want %q (resolved against the config file's directory)",
			cfg.Checkpoint.FilePath, want)
	}
}

func TestLoad_EnvCheckpointFilePathKeepsCWDSemantics(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(
		"tailscale:\n  tailnet: acme.org\n  auth:\n    method: apikey\n    apikey: x\n"+
			"checkpoint:\n  store: file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TS2OTEL_CHECKPOINT__FILE_PATH", "state/checkpoints.json")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Checkpoint.FilePath != "state/checkpoints.json" {
		t.Fatalf("Checkpoint.FilePath = %q, want the unresolved env value (CWD semantics), a config "+
			"file was present but this value came from the environment, not it", cfg.Checkpoint.FilePath)
	}
}

func TestLoad_SecretFileErrorShowsConfiguredAndResolvedPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(
		"tailscale:\n  tailnet: acme.org\n  auth:\n    method: apikey\n    apikey_file: secrets/missing.key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("Load: want error for a missing secret file, got nil")
	}
	if !strings.Contains(err.Error(), "secrets/missing.key") {
		t.Fatalf("error %q must show the CONFIGURED path", err)
	}
	resolved := filepath.Join(dir, "secrets/missing.key")
	if !strings.Contains(err.Error(), resolved) {
		t.Fatalf("error %q must show the RESOLVED path %q", err, resolved)
	}
	if !strings.Contains(err.Error(), "resolved to") {
		t.Fatalf("error %q must say so explicitly (\"resolved to\")", err)
	}
}

// A TLS file error must name BOTH the path the operator wrote and the path
// that was actually opened. Since #310 those differ whenever a relative path
// is resolved against the config directory, and an error showing only one of
// them is the hardest kind to act on: the configured path looks correct, so
// the operator reads "no such file" about a file they can see is there.
func TestValidate_TLSFileErrorShowsConfiguredAndResolvedPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	y := "admin:\n  enabled: true\n  listen: \"127.0.0.1:9091\"\n" +
		"  tls:\n    cert_file: certs/missing.pem\n    key_file: certs/missing.key\n"
	if err := os.WriteFile(cfgPath, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("a missing TLS cert file must fail validation")
	}
	// Both checks quote the paths. Unquoted, the configured path is a SUBSTRING
	// of the resolved one, so a "contains" pair passes with only the resolved
	// path present — this assertion was vacuous until the quotes went in.
	msg := err.Error()
	if !strings.Contains(msg, `"certs/missing.pem"`) {
		t.Errorf("error does not name the CONFIGURED path: %v", msg)
	}
	if !strings.Contains(msg, `(resolved to "`+filepath.Join(dir, "certs/missing.pem")+`")`) {
		t.Errorf("error does not name the RESOLVED path: %v", msg)
	}
}
