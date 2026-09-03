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

	"github.com/rknightion/tailscale2otel/v5/internal/catalog"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

var writeCapabilityCounts = flag.Bool("write-capability-counts", false, "rewrite internal/catalog/capability_counts.json")

type capabilityCounts struct {
	AlertRules            int `json:"alert_rules"`
	Collectors            int `json:"collectors"`
	Dashboards            int `json:"dashboards"`
	LogEvents             int `json:"log_events"`
	Metrics               int `json:"metrics"`
	PanelLinkedAlertRules int `json:"panel_linked_alert_rules"`
	RecordingRules        int `json:"recording_rules"`
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
		t.Fatalf("read %s: %v; run just gen counts", path, err)
	}
	if !bytes.Equal(actual, want) {
		t.Fatalf("%s is stale; run just gen counts", path)
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
		manifest := readManifest(t, path)
		switch manifest.Kind {
		case "AlertRule":
			counts.AlertRules++
			if hasPanelIDAnnotation(t, path, manifest) {
				counts.PanelLinkedAlertRules++
			}
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
	return readManifest(t, path).Kind
}

type manifest struct {
	Kind string `json:"kind"`
	Spec struct {
		Annotations json.RawMessage `json:"annotations"`
	} `json:"spec"`
}

func hasPanelIDAnnotation(t *testing.T, path string, item manifest) bool {
	t.Helper()
	if len(item.Spec.Annotations) == 0 {
		return false
	}
	var annotations map[string]string
	if err := json.Unmarshal(item.Spec.Annotations, &annotations); err != nil {
		t.Fatalf("parse %s annotations: %v", path, err)
	}
	return annotations["__panelId__"] != ""
}

func readManifest(t *testing.T, path string) manifest {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var result manifest
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return result
}
