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

// scopedDoc builds a two-tab dashboard where tab A declares `svc` and tab B does
// not, each tab holding one panel that references $svc. Only tab B's panel is
// wrong. Parameterised so the same shape can be built with the variable at
// dashboard level instead, which must make BOTH panels fine.
func scopedDoc(atDashboard bool) []byte {
	v := `{"kind":"QueryVariable","spec":{"name":"svc"}}`
	dashVars := `{"kind":"DatasourceVariable","spec":{"name":"ds_prometheus","pluginId":"prometheus"}}`
	tabVars := ""
	if atDashboard {
		dashVars += "," + v
	} else {
		tabVars = `"variables":[` + v + `],`
	}
	panel := func(name string) string {
		return `"` + name + `":{"kind":"Panel","spec":{"id":1,"title":"` + name + `","data":{"spec":{"queries":[` +
			`{"spec":{"refId":"A","query":{"group":"","datasource":{"name":"${ds_prometheus}"},` +
			`"spec":{"expr":"up{job=~\"$svc\"}"}}}}]}}}}`
	}
	tab := func(title, extraVars, element string) string {
		return `{"kind":"TabsLayoutTab","spec":{"title":"` + title + `",` + extraVars +
			`"layout":{"kind":"RowsLayout","spec":{"rows":[{"kind":"RowsLayoutRow","spec":{"title":"r",` +
			`"layout":{"kind":"GridLayout","spec":{"items":[{"kind":"GridLayoutItem","spec":{` +
			`"element":{"kind":"ElementReference","name":"` + element + `"}}}]}}}}]}}}}`
	}
	return []byte(`{"apiVersion":"dashboard.grafana.app/v2","kind":"Dashboard","spec":{` +
		`"variables":[` + dashVars + `],` +
		`"elements":{` + panel("panel-a") + `,` + panel("panel-b") + `},` +
		`"layout":{"kind":"TabsLayout","spec":{"tabs":[` +
		tab("A", tabVars, "panel-a") + `,` + tab("B", "", "panel-b") + `]}}}}`)
}

// A variable declared on a TAB is in scope for that tab's panels and nothing
// else. Checking every panel against the dashboard-level set alone is wrong in
// both directions, and #526 moved ~60 variables onto tabs, so the two halves of
// this test are the common case now — not an edge case.
func TestDashboardVariableScopeIsPerTab(t *testing.T) {
	t.Run("a tab-scoped variable satisfies its own tab", func(t *testing.T) {
		rep := newReport()
		if err := checkDashboard(rep, "scoped.json", scopedDoc(false)); err != nil {
			t.Fatalf("checkDashboard: %v", err)
		}
		for _, f := range rep.Failures {
			if strings.Contains(f.Where, "panel-a") || strings.Contains(f.Reason, `"panel-a"`) {
				t.Errorf("tab A's own variable was reported undeclared: %v", f)
			}
		}
	})

	// The half that matters. A walker that unioned every declaration in the
	// document — the obvious wrong implementation — would pass the case above
	// and silently accept this one, which renders as an unresolved $svc.
	t.Run("a sibling tab's variable does NOT satisfy this one", func(t *testing.T) {
		rep := newReport()
		if err := checkDashboard(rep, "scoped.json", scopedDoc(false)); err != nil {
			t.Fatalf("checkDashboard: %v", err)
		}
		if len(rep.Failures) != 1 {
			t.Fatalf("want exactly 1 failure (tab B borrowing tab A's variable), got %d: %v",
				len(rep.Failures), rep.Failures)
		}
		if !strings.Contains(rep.Failures[0].Reason, "$svc") {
			t.Errorf("failure does not name $svc: %v", rep.Failures[0])
		}
	})

	t.Run("the same variable at dashboard level satisfies both tabs", func(t *testing.T) {
		rep := newReport()
		if err := checkDashboard(rep, "scoped.json", scopedDoc(true)); err != nil {
			t.Fatalf("checkDashboard: %v", err)
		}
		if len(rep.Failures) != 0 {
			t.Fatalf("want no failures, got %d: %v", len(rep.Failures), rep.Failures)
		}
	})
}
