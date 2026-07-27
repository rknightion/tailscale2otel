package main

import (
	"os"
	"strings"
	"testing"
)

// loadFixture reads a testdata file, failing the test if it is missing.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestCheckDashboardGood(t *testing.T) {
	rep := newReport()
	if err := checkDashboard(rep, "testdata/dashboard_good.json", loadFixture(t, "dashboard_good.json")); err != nil {
		t.Fatalf("checkDashboard: %v", err)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("want no failures, got %d: %v", len(rep.Failures), rep.Failures)
	}
	if rep.Counts[LangPromQL] != 2 {
		t.Errorf("promql count = %d, want 2", rep.Counts[LangPromQL])
	}
	if rep.Counts[LangLogQL] != 1 {
		t.Errorf("logql count = %d, want 1", rep.Counts[LangLogQL])
	}
	if rep.Counts[LangTraceQL] != 1 {
		t.Errorf("traceql count = %d, want 1", rep.Counts[LangTraceQL])
	}
}

func TestCheckDashboardFailures(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantSub []string
	}{
		{
			name:    "syntactically broken promql",
			fixture: "dashboard_broken_expr.json",
			// The parser's own message, plus enough locator to find the panel.
			wantSub: []string{"grouping opts", "panel id=1", "Rate with a range token", "after template substitution"},
		},
		{
			name:    "reference to an undeclared dashboard variable",
			fixture: "dashboard_undeclared_var.json",
			wantSub: []string{"$hostname", "not declared"},
		},
		{
			name:    "datasource variable the dashboard never declares",
			fixture: "dashboard_unknown_datasource.json",
			wantSub: []string{"ds_mystery"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := newReport()
			if err := checkDashboard(rep, "testdata/"+tc.fixture, loadFixture(t, tc.fixture)); err != nil {
				t.Fatalf("checkDashboard: %v", err)
			}
			if len(rep.Failures) != 1 {
				t.Fatalf("want exactly 1 failure, got %d: %v", len(rep.Failures), rep.Failures)
			}
			got := rep.Failures[0].String()
			for _, sub := range tc.wantSub {
				if !strings.Contains(got, sub) {
					t.Errorf("failure %q does not contain %q", got, sub)
				}
			}
		})
	}
}
