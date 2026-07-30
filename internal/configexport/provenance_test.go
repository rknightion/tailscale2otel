package configexport_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/configexport"
)

// TestBuildWithProvenance_MatchesBuildPlusSource asserts BuildWithProvenance
// (#309) is purely additive over Build (#320): every key Build produces is
// present with the identical ConfigFieldValue, plus a Provenance field
// sourced from config.Provenance. Build's own signature/behavior must not
// change — this is why the new capability is a second function, not a
// parameter added to Build.
func TestBuildWithProvenance_MatchesBuildPlusSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("log_level: debug\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("TS2OTEL_LOG_FORMAT", "json")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	plain := configexport.Build(cfg)
	withProv, err := configexport.BuildWithProvenance(cfg, path)
	if err != nil {
		t.Fatalf("BuildWithProvenance: %v", err)
	}

	if len(withProv) != len(plain) {
		t.Fatalf("BuildWithProvenance has %d keys, Build has %d — should be the exact same key set", len(withProv), len(plain))
	}
	for key, plainVal := range plain {
		got, ok := withProv[key]
		if !ok {
			t.Errorf("key %q missing from BuildWithProvenance", key)
			continue
		}
		if !reflect.DeepEqual(got.ConfigFieldValue, plainVal) {
			t.Errorf("key %q: ConfigFieldValue = %+v, want %+v (must match Build exactly)", key, got.ConfigFieldValue, plainVal)
		}
	}

	if got := withProv["log_level"].Provenance; got != config.SourceFile {
		t.Errorf("log_level provenance = %q, want %q", got, config.SourceFile)
	}
	if got := withProv["log_format"].Provenance; got != config.SourceEnv {
		t.Errorf("log_format provenance = %q, want %q", got, config.SourceEnv)
	}
	if got := withProv["admin.enabled"].Provenance; got != config.SourceDefault {
		t.Errorf("admin.enabled provenance = %q, want %q", got, config.SourceDefault)
	}
}

// TestBuildWithProvenance_SecretFieldsNeverLeakValue is the same sentinel
// leak test TestBuildFullConfigMap_SecretFieldsNeverLeakValue runs against
// Build, run again against BuildWithProvenance's JSON encoding — the whole
// point of freezing Build's signature and adding provenance alongside it is
// that the redaction guarantee must survive unchanged in the new surface too.
func TestBuildWithProvenance_SecretFieldsNeverLeakValue(t *testing.T) {
	cfg := config.Default()
	cfg.Tailnets = append(cfg.Tailnets, config.TailnetConfig{Name: "planted"})
	cfg.Streaming.Routes = append(cfg.Streaming.Routes, config.StreamingRoute{Tailnet: "planted"})
	cfg.Webhook.Routes = append(cfg.Webhook.Routes, config.WebhookRoute{Tailnet: "planted"})
	cfg.Collectors.NodeMetrics.Targets = append(cfg.Collectors.NodeMetrics.Targets, config.NodeMetricsTarget{URL: "http://planted"})

	sentinels, shown := plantSentinels(cfg)
	if len(sentinels) == 0 {
		t.Fatal("plantSentinels planted nothing — vacuous test")
	}

	withProv, err := configexport.BuildWithProvenance(cfg, "")
	if err != nil {
		t.Fatalf("BuildWithProvenance: %v", err)
	}
	body, err := json.Marshal(withProv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for sentinel, path := range sentinels {
		if strings.Contains(string(body), sentinel) {
			t.Errorf("sentinel for %s leaked into BuildWithProvenance output: %q found", path, sentinel)
		}
	}
	for sentinel, path := range shown {
		if !strings.Contains(string(body), sentinel) {
			t.Errorf("sentinel for %s did not appear, but MUST be shown: %q missing", path, sentinel)
		}
	}
}

// TestBuildWithProvenance_Deterministic guards the #309 acceptance line
// "output is deterministic": two independent calls (fresh config.Load,
// fresh BuildWithProvenance) against the same file+env inputs must produce
// byte-identical JSON — no map-iteration-order leakage.
func TestBuildWithProvenance_Deterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const yaml = `
log_level: debug
tailnets:
  - name: one
    tailnet: one.example.com
    auth:
      method: apikey
      apikey: "tskey-one"
  - name: two
    tailnet: two.example.com
    auth:
      method: apikey
      apikey: "tskey-two"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	run := func() []byte {
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}
		out, err := configexport.BuildWithProvenance(cfg, path)
		if err != nil {
			t.Fatalf("BuildWithProvenance: %v", err)
		}
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}

	a := run()
	b := run()
	if string(a) != string(b) {
		t.Fatalf("BuildWithProvenance output not deterministic across runs:\na=%s\nb=%s", a, b)
	}
}
