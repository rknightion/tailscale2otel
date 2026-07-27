package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRoot builds a temporary repo root containing the three artifact paths the
// tool walks, each populated from the named testdata fixture.
func fakeRoot(t *testing.T, dashboard, grafanaRules, promRules string) string {
	t.Helper()
	root := t.TempDir()
	for rel, fixture := range map[string]string{
		dashboardPath:    dashboard,
		grafanaRulesPath: grafanaRules,
		promRulesPath:    promRules,
	} {
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(dst, loadFixture(t, fixture), 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	return root
}

func TestRunClean(t *testing.T) {
	root := fakeRoot(t, "dashboard_good.json", "grafana_rules_good.yaml", "prom_rules_good.yaml")

	var out bytes.Buffer
	failures, err := run(&out, root, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if failures != 0 {
		t.Fatalf("want 0 failures, got %d:\n%s", failures, out.String())
	}

	got := out.String()
	// The skip counts must be impossible to miss: a run that silently ignored
	// LogQL and TraceQL would otherwise read as full coverage.
	for _, sub := range []string{
		"checked 5 promql",
		"2 logql",
		"1 traceql",
		"0 failures",
		"NOT PARSED",
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("output does not contain %q:\n%s", sub, got)
		}
	}
}

func TestRunReportsFailures(t *testing.T) {
	root := fakeRoot(t, "dashboard_broken_expr.json", "grafana_rules_broken.yaml", "prom_rules_broken.yaml")

	var out bytes.Buffer
	failures, err := run(&out, root, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := 5; failures != want {
		t.Fatalf("want %d failures, got %d:\n%s", want, failures, out.String())
	}
	if !strings.Contains(out.String(), "5 failures") {
		t.Errorf("summary missing the failure count:\n%s", out.String())
	}
}

func TestRunVerboseListsEveryExpression(t *testing.T) {
	root := fakeRoot(t, "dashboard_good.json", "grafana_rules_good.yaml", "prom_rules_good.yaml")

	var out bytes.Buffer
	if _, err := run(&out, root, true); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, sub := range []string{
		"tailscale_network_io_bytes_total",  // a dashboard promql expr
		"absent(tailscale2otel_up_ratio)",   // a plain-rules promql expr
		"panel id=1",                        // dashboard locator
		"fixture-health/fx-exporter-down",   // grafana-rules locator
		"fixture.health/alert=ExporterDown", // prom-rules locator
		"[logql]",                           // language tag on a skipped expr
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("verbose output does not contain %q:\n%s", sub, got)
		}
	}
}

func TestRunMissingArtifactIsAnError(t *testing.T) {
	if _, err := run(&bytes.Buffer{}, t.TempDir(), false); err == nil {
		t.Fatal("want an error for a root with no artifacts, got nil")
	}
}
