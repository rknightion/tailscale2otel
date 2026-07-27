package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRoot builds a temporary repo root holding the dashboard and a
// grafana-managed rules directory, each populated from the named testdata
// fixtures. The rule manifests are written under their fixture basenames, which
// is enough: the tool globs the directory rather than expecting fixed names.
func fakeRoot(t *testing.T, dashboard string, ruleFixtures ...string) string {
	t.Helper()
	root := t.TempDir()

	dst := filepath.Join(root, filepath.FromSlash(dashboardPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dst, loadFixture(t, dashboard), 0o600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}

	dir := filepath.Join(root, filepath.FromSlash(rulesDir))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A folder manifest is always present alongside the rules and must be
	// skipped — it carries no expressions and is not a rule kind.
	folder := `{"apiVersion":"folder.grafana.app/v1beta1","kind":"Folder",` +
		`"metadata":{"name":"fixture"},"spec":{"title":"Fixture"}}`
	if err := os.WriteFile(filepath.Join(dir, folderManifest), []byte(folder), 0o600); err != nil {
		t.Fatalf("write folder manifest: %v", err)
	}
	for _, f := range ruleFixtures {
		if err := os.WriteFile(filepath.Join(dir, f), loadFixture(t, f), 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return root
}

func TestRunClean(t *testing.T) {
	root := fakeRoot(t, "dashboard_good.json", "grafana_rule_good.json")

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
		"checked 3 promql",
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
	root := fakeRoot(t, "dashboard_broken_expr.json", "grafana_rule_broken.json")

	var out bytes.Buffer
	failures, err := run(&out, root, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := 4; failures != want {
		t.Fatalf("want %d failures, got %d:\n%s", want, failures, out.String())
	}
	if !strings.Contains(out.String(), "4 failures") {
		t.Errorf("summary missing the failure count:\n%s", out.String())
	}
}

func TestRunVerboseListsEveryExpression(t *testing.T) {
	root := fakeRoot(t, "dashboard_good.json", "grafana_rule_good.json")

	var out bytes.Buffer
	if _, err := run(&out, root, true); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, sub := range []string{
		"tailscale_network_io_bytes_total", // a dashboard promql expr
		"panel id=1",                       // dashboard locator
		`fx-good "Good rule"`,              // rule-manifest locator
		"[logql]",                          // language tag on a skipped expr
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

// An empty rules directory must be an error rather than a clean run: the tool
// would otherwise report "0 failures" having parsed no rule at all, which is
// indistinguishable from everything passing.
func TestRunEmptyRulesDirIsAnError(t *testing.T) {
	root := fakeRoot(t, "dashboard_good.json")
	if _, err := run(&bytes.Buffer{}, root, false); err == nil {
		t.Fatal("want an error for a rules directory holding only the folder manifest, got nil")
	}
}
