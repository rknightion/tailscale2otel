package catalog_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/catalog"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

var writeCapabilityCounts = flag.Bool("write-capability-counts", false, "rewrite internal/catalog/capability_counts.json")

type capabilityCounts struct {
	AlertRules     int `json:"alert_rules"`
	Collectors     int `json:"collectors"`
	Dashboards     int `json:"dashboards"`
	LogEvents      int `json:"log_events"`
	Metrics        int `json:"metrics"`
	RecordingRules int `json:"recording_rules"`
}

func TestCapabilityCountsSourceInSync(t *testing.T) {
	root := repositoryRoot(t)
	got := deriveCapabilityCounts(t, root)
	want, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')

	path := filepath.Join(root, "internal", "catalog", "capability_counts.json")
	if *writeCapabilityCounts {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}

	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v; run scripts/regen-generated.sh counts", path, err)
	}
	if !bytes.Equal(actual, want) {
		t.Fatalf("%s is stale; run scripts/regen-generated.sh counts", path)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate capability count source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func deriveCapabilityCounts(t *testing.T, root string) capabilityCounts {
	t.Helper()
	counts := capabilityCounts{
		Collectors: reflect.TypeFor[config.Collectors]().NumField(),
		LogEvents:  len(catalog.LogEvents()),
		Metrics:    len(catalog.Metrics()),
	}

	for _, path := range matchingJSONFiles(t, filepath.Join(root, "deploy", "grafana")) {
		if manifestKind(t, path) == "Dashboard" {
			counts.Dashboards++
		}
	}
	for _, path := range matchingJSONFiles(t, filepath.Join(root, "deploy", "alerts", "grafana-managed")) {
		switch manifestKind(t, path) {
		case "AlertRule":
			counts.AlertRules++
		case "RecordingRule":
			counts.RecordingRules++
		}
	}
	return counts
}

func matchingJSONFiles(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no JSON manifests in %s", dir)
	}
	return paths
}

func manifestKind(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var manifest struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return manifest.Kind
}
