package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// chartRenderedConfig reproduces what the chart renders as config.yaml: the
// `config:` block of values.yaml, passed through toYaml, with the install's
// overrides merged in (Helm merges maps key by key and keeps explicit nulls).
// It is the config file a chart install hands to Load.
func chartRenderedConfig(t *testing.T, overrides func(cfg map[string]any)) string {
	t.Helper()
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
	cfg := values.Config
	// A coordinated install: the Kubernetes checkpoint store, one tailnet.
	setChartValue(cfg, "checkpoint.store", "kubernetes")
	setChartValue(cfg, "coordination.mode", "kubernetes")
	// The chart template fills this in from the release namespace.
	setChartValue(cfg, "coordination.namespace", "monitoring")
	if overrides != nil {
		overrides(cfg)
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("render chart config: %v", err)
	}
	return string(out)
}

func setChartValue(cfg map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := cfg[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cfg[part] = next
		}
		cfg = next
	}
	cfg[parts[len(parts)-1]] = value
}

// The chart used to render replay_seen_capacity: 131072, which Load treats as an
// explicit operator choice, so no chart install ever reached the checkpoint
// clamp. The default chart config must leave the key unset.
func TestChartDefaultConfigReachesFlowReplayClamp(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, chartRenderedConfig(t, nil)))
	if err != nil {
		t.Fatalf("load default chart config: %v", err)
	}
	got := cfg.Collectors.Flowlogs.ReplaySeenCapacity
	if got < 1 || got >= Default().Collectors.Flowlogs.ReplaySeenCapacity {
		t.Fatalf("default chart config with the kubernetes store kept replay_seen_capacity=%d, "+
			"want a reduced positive value from the checkpoint clamp", got)
	}
	if warnings := replayCapacityWarnings(cfg); len(warnings) != 0 {
		t.Fatalf("the clamped default warned: %v", warnings)
	}
}

// A value an operator sets through the chart is still theirs: kept as written,
// and warned about when it exceeds what the checkpoint can hold.
func TestChartExplicitFlowReplayCapacityIsHonoured(t *testing.T) {
	clamped, err := Load(writeTempConfig(t, chartRenderedConfig(t, nil)))
	if err != nil {
		t.Fatalf("load default chart config: %v", err)
	}
	limit := clamped.Collectors.Flowlogs.ReplaySeenCapacity

	for _, tc := range []struct {
		name     string
		value    int
		wantWarn bool
	}{
		{name: "too large", value: 1048576, wantWarn: true},
		{name: "the old chart default", value: 131072, wantWarn: true},
		{name: "fits", value: limit, wantWarn: false},
		{name: "small", value: 1000, wantWarn: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rendered := chartRenderedConfig(t, func(cfg map[string]any) {
				setChartValue(cfg, flowReplayCapacityPath, tc.value)
			})
			cfg, err := Load(writeTempConfig(t, rendered))
			if err != nil {
				t.Fatalf("load chart config: %v", err)
			}
			if got := cfg.Collectors.Flowlogs.ReplaySeenCapacity; got != tc.value {
				t.Fatalf("explicit chart value %d became %d", tc.value, got)
			}
			if warnings := replayCapacityWarnings(cfg); (len(warnings) == 1) != tc.wantWarn || len(warnings) > 1 {
				t.Fatalf("warnings for %d = %v, want warned=%v", tc.value, warnings, tc.wantWarn)
			}
		})
	}
}

// A null in the file (how the chart renders an unset key) is the same as no key.
// It must not count as an operator override, and file and env installs keep
// treating a real value as one.
func TestFlowReplayCapacityNullIsUnset(t *testing.T) {
	null := loadReplayCheckpointConfig(t, "kubernetes", nil, "collectors:\n  flowlogs:\n    replay_seen_capacity: null\n")
	unset := loadReplayCheckpointConfig(t, "kubernetes", nil, "")
	if got, want := null.Collectors.Flowlogs.ReplaySeenCapacity, unset.Collectors.Flowlogs.ReplaySeenCapacity; got != want || got >= Default().Collectors.Flowlogs.ReplaySeenCapacity {
		t.Fatalf("null capacity = %d, unset = %d; want both clamped alike", got, want)
	}
}
