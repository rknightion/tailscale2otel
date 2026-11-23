package config

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
)

func loadReplayCheckpointConfig(t *testing.T, store string, names []string, extra string) *Config {
	t.Helper()
	var yaml strings.Builder
	fmt.Fprintf(&yaml, "checkpoint:\n  store: %s\n", store)
	if store == "kubernetes" {
		yaml.WriteString("coordination:\n  mode: kubernetes\n")
	}
	for i, name := range names {
		if i == 0 {
			yaml.WriteString("tailnets:\n")
		}
		fmt.Fprintf(&yaml, "  - name: %s\n    auth:\n      method: oauth\n", strconv.Quote(name))
	}
	if len(names) == 0 {
		yaml.WriteString("tailscale:\n  tailnet: example.test\n")
	}
	yaml.WriteString(extra)
	cfg, err := Load(writeTempConfig(t, yaml.String()))
	if err != nil {
		t.Fatalf("load %s fixture: %v", store, err)
	}
	return cfg
}

func replayCheckpointRows(names []string, capacity int) map[string]time.Time {
	rows := make(map[string]time.Time, len(names)*(capacity+1))
	for runtime, name := range names {
		prefix := ""
		if len(names) > 1 {
			prefix = name + "/"
		}
		rows[prefix+"flowlogs"] = time.Date(2026, 11, 1, 23, 59, 59, 123456789, time.UTC)
		for i := range capacity {
			// Independent inputs per runtime and row, not a reused set of digests
			// (gzip can otherwise compress repeats across namespaces).
			digest := sha256.Sum256(fmt.Appendf(nil, "runtime=%d;row=%d", runtime, i))
			expiryNanos := int(digest[0])<<21 | int(digest[1])<<13 | int(digest[2])<<5 | 1
			rows[fmt.Sprintf("%sflowlogs/replay/seen/%x", prefix, digest)] = time.Date(2026, 11, 2, 23, 59, i%60, expiryNanos, time.UTC)
		}
	}
	return rows
}

func replayCapacityWarnings(cfg *Config) []string {
	var warnings []string
	for _, w := range cfg.Warnings() {
		if strings.HasPrefix(w, "collectors.flowlogs.replay_seen_capacity=") {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

func replayCapacityYAML(capacity int) string {
	return fmt.Sprintf("collectors:\n  flowlogs:\n    replay_seen_capacity: %d\n", capacity)
}

func TestFlowReplayCheckpointKubernetesSharedShard(t *testing.T) {
	names := []string{"flowlogs/fixture-a", "flowlogs/fixture-b"}
	cfg := loadReplayCheckpointConfig(t, "kubernetes", names, "")
	capacity := cfg.Collectors.Flowlogs.ReplaySeenCapacity
	if capacity <= 0 || capacity >= Default().Collectors.Flowlogs.ReplaySeenCapacity {
		t.Fatalf("loaded capacity = %d, want a reduced positive value for a shared ConfigMap", capacity)
	}
	rows := replayCheckpointRows(names, capacity)
	for key := range rows {
		if collector.ShardKey(key) != "flowlogs" {
			t.Fatal("fixture does not exercise a single shared shard")
		}
	}
	data, _, err := collector.EncodeKubernetesCheckpointShard("flowlogs", rows)
	if err != nil || len(data) > collector.KubernetesCheckpointDataLimit {
		t.Fatalf("production shard at loaded capacity %d: %d bytes, err=%v", capacity, len(data), err)
	}
	if warnings := replayCapacityWarnings(cfg); len(warnings) > 0 {
		t.Fatalf("fitting implicit capacity warned: %v", warnings)
	}

	const explicit = 131072 // An explicit copy of the built-in value is still an override.
	cfg = loadReplayCheckpointConfig(t, "kubernetes", names, replayCapacityYAML(explicit))
	if got := cfg.Collectors.Flowlogs.ReplaySeenCapacity; got != explicit {
		t.Fatalf("explicit capacity changed to %d", got)
	}
	warnings := replayCapacityWarnings(cfg)
	if len(warnings) != 1 || !strings.Contains(warnings[0], fmt.Sprintf("replay_seen_capacity: %d or lower", capacity)) {
		t.Fatalf("want a recommendation for the verified capacity %d; got %v", capacity, warnings)
	}
	for _, name := range names {
		if strings.Contains(warnings[0], name) {
			t.Fatal("warning exposes a checkpoint namespace")
		}
	}
	cfg = loadReplayCheckpointConfig(t, "kubernetes", names, replayCapacityYAML(capacity))
	if got := cfg.Collectors.Flowlogs.ReplaySeenCapacity; got != capacity || len(replayCapacityWarnings(cfg)) != 0 {
		t.Fatalf("recommendation did not reload unchanged and without a replay capacity warning (capacity=%d)", got)
	}
}

// An explicit capacity that cannot fit a Kubernetes shard must say why, naming
// the 1 MiB per-shard ConfigMap limit in numbers so the operator can check it.
func TestFlowReplayCheckpointKubernetesWarningNamesShardLimit(t *testing.T) {
	limit := fmt.Sprintf("1 MiB (%d bytes)", collector.KubernetesCheckpointDataLimit)
	if collector.KubernetesCheckpointDataLimit != 1<<20 {
		t.Fatalf("shard limit is %d, the warning wording assumes 1 MiB", collector.KubernetesCheckpointDataLimit)
	}
	names := []string{"flowlogs/fixture-a", "flowlogs/fixture-b"}
	cfg := loadReplayCheckpointConfig(t, "kubernetes", names, replayCapacityYAML(131072))
	warnings := replayCapacityWarnings(cfg)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "shard limit") || !strings.Contains(warnings[0], limit) {
		t.Fatalf("explicit-capacity warning must name the %s Kubernetes shard limit; got %v", limit, warnings)
	}
	// The file store has no ConfigMap shard, so it must not claim one.
	file := loadReplayCheckpointConfig(t, "file", []string{"example-a.test", "example-b.test"}, replayCapacityYAML(1048576))
	for _, w := range replayCapacityWarnings(file) {
		if strings.Contains(w, "shard") {
			t.Fatalf("file store warning mentions a Kubernetes shard: %s", w)
		}
	}
}

func TestFlowReplayCheckpointFileFits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{name: "single"},
		{name: "many", names: []string{"example-a.test", "example-b.test", "<escaped>\t.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadReplayCheckpointConfig(t, "file", tc.names, "")
			capacity := cfg.Collectors.Flowlogs.ReplaySeenCapacity
			builtin := Default().Collectors.Flowlogs.ReplaySeenCapacity
			if len(tc.names) == 0 && capacity != builtin {
				t.Fatalf("single-file capacity = %d, want untouched built-in %d", capacity, builtin)
			}
			if capacity < 1 || (len(tc.names) > 0 && capacity >= builtin) {
				t.Fatalf("multi-tailnet file capacity = %d, want a reduced positive value", capacity)
			}
			names := tc.names
			if len(names) == 0 {
				names = []string{""}
			}
			path := filepath.Join(t.TempDir(), "checkpoints.json")
			store, err := collector.NewFileStore(path)
			if err != nil {
				t.Fatal(err)
			}
			rows := replayCheckpointRows(names, capacity)
			if err := collector.UpdateCheckpointBatch(store, rows, nil); err != nil {
				t.Fatalf("persist full production checkpoint at capacity %d: %v", capacity, err)
			}
			reloaded, err := collector.NewFileStore(path)
			if err != nil {
				t.Fatalf("reopen full production checkpoint: %v", err)
			}
			for key, want := range rows {
				if got, ok := reloaded.Get(key); !ok || !got.Equal(want) {
					t.Fatal("reopened checkpoint lost a row or changed its timestamp")
				}
			}
			if warnings := replayCapacityWarnings(cfg); len(warnings) != 0 {
				t.Fatalf("fitting implicit capacity warned: %v", warnings)
			}
		})
	}
}

func TestFlowReplayCheckpointKubernetesDistinctShards(t *testing.T) {
	names := []string{"example-a.test", "example-b.test"}
	cfg := loadReplayCheckpointConfig(t, "kubernetes", names, "")
	capacity := cfg.Collectors.Flowlogs.ReplaySeenCapacity
	shared := loadReplayCheckpointConfig(t, "kubernetes", []string{"flowlogs/a", "flowlogs/b"}, "").Collectors.Flowlogs.ReplaySeenCapacity
	if capacity <= shared {
		t.Fatalf("distinct shard capacity %d did not allow more retention than shared shard capacity %d", capacity, shared)
	}
	shards := map[string]map[string]time.Time{}
	for key, value := range replayCheckpointRows(names, capacity) {
		shard := collector.ShardKey(key)
		if shards[shard] == nil {
			shards[shard] = make(map[string]time.Time, capacity+1)
		}
		shards[shard][key] = value
	}
	if len(shards) != len(names) {
		t.Fatal("fixture does not exercise one production shard per tailnet")
	}
	for shard, rows := range shards {
		data, _, err := collector.EncodeKubernetesCheckpointShard(shard, rows)
		if err != nil || len(data) > collector.KubernetesCheckpointDataLimit {
			t.Fatalf("production shard at loaded capacity %d: %d bytes, err=%v", capacity, len(data), err)
		}
	}
}

func TestFlowReplayCheckpointWorkIsBoundedAcrossShards(t *testing.T) {
	cfg := Default()
	cfg.Checkpoint.Store = "kubernetes"
	cfg.Collectors.Flowlogs.ReplaySeenCapacity = 1
	cfg.Tailnets = make([]TailnetConfig, 2000)
	for _, prefix := range []string{"fixture-", "flowlogs/fixture-"} {
		for i := range cfg.Tailnets {
			cfg.Tailnets[i].Name = fmt.Sprintf("%s%d", prefix, i)
		}
		if warnings := replayCapacityWarnings(cfg); len(warnings) != 0 {
			t.Fatalf("a capacity of one for ordinary names fits even with 2000 runtimes: %v", warnings)
		}
		// Each shard by itself is affordable, but aggregate metadata is over
		// budget. Never treat an unexamined suffix as a verified fit.
		padding := strings.Repeat("x", 5000)
		for i := range cfg.Tailnets {
			cfg.Tailnets[i].Name = fmt.Sprintf("%s%d%s", prefix, i, padding)
		}
		warnings := replayCapacityWarnings(cfg)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "startup work budget") ||
			!strings.Contains(warnings[0], "no positive recommendation") || strings.Contains(warnings[0], padding) {
			t.Fatal("aggregate budget exhaustion did not warn without disclosing names or guessing a recommendation")
		}
	}
}

func TestFlowReplayCheckpointNoPositiveRecommendation(t *testing.T) {
	// The JSON escaping of this synthetic name makes one row exceed the raw
	// allowance even when no entry in the map is retained for another runtime.
	names := []string{"flowlogs/" + strings.Repeat("<", 170000), "other.test"}
	for _, extra := range []string{"", replayCapacityYAML(2)} {
		cfg := loadReplayCheckpointConfig(t, "kubernetes", names, extra)
		want := 1
		if extra != "" {
			want = 2
		}
		if got := cfg.Collectors.Flowlogs.ReplaySeenCapacity; got != want {
			t.Errorf("effective capacity = %d, want %d", got, want)
		}
		warnings := replayCapacityWarnings(cfg)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "no positive capacity fits") ||
			strings.Contains(warnings[0], "replay_seen_capacity: 0") || strings.Contains(warnings[0], "<<<<") {
			t.Fatal("impossible fit did not warn without disclosing the name or advising zero")
		}
	}
}

func TestFlowReplayCheckpointSkipsNonDurablePollModes(t *testing.T) {
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__REPLAY_SEEN_CAPACITY", "1048576")
	control := loadReplayCheckpointConfig(t, "kubernetes", nil, replayCapacityYAML(1))
	if got := control.Collectors.Flowlogs.ReplaySeenCapacity; got != 1048576 || len(replayCapacityWarnings(control)) != 1 {
		t.Fatalf("env override should supersede YAML and warn for durable polling, got capacity %d", got)
	}
	for _, tc := range []struct {
		name, store, extra string
	}{
		{name: "memory", store: "memory"},
		{name: "disabled", store: "kubernetes", extra: "collectors:\n  flowlogs:\n    enabled: false\n"},
		{name: "no overlap", store: "kubernetes", extra: "collectors:\n  flowlogs:\n    replay_overlap: 0s\n"},
		{name: "stream", store: "kubernetes", extra: "collectors:\n  flowlogs:\n    source: stream\nstreaming:\n  enabled: true\n  listen: 127.0.0.1:8080\n"},
		{name: "objectstore", store: "kubernetes", extra: "collectors:\n  flowlogs:\n    source: objectstore\n    objectstore:\n      endpoint: https://storage.example.test\n      region: fixture\n      bucket: fixture\n      max_seen_keys: 1\n"},
		{name: "headscale", store: "kubernetes", extra: "provider: headscale\nheadscale:\n  url: https://headscale.example.test\n  api_key: synthetic\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadReplayCheckpointConfig(t, tc.store, nil, tc.extra)
			if got := cfg.Collectors.Flowlogs.ReplaySeenCapacity; got != 1048576 || len(replayCapacityWarnings(cfg)) != 0 {
				t.Fatalf("non-durable or non-polling mode changed capacity or warned (capacity %d)", got)
			}
		})
	}
}
