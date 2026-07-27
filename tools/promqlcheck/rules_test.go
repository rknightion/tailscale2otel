package main

import (
	"strings"
	"testing"
)

func TestCheckGrafanaRulesGood(t *testing.T) {
	rep := newReport()
	if err := checkGrafanaRules(rep, "testdata/grafana_rules_good.yaml", loadFixture(t, "grafana_rules_good.yaml")); err != nil {
		t.Fatalf("checkGrafanaRules: %v", err)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("want no failures, got %d: %v", len(rep.Failures), rep.Failures)
	}
	if rep.Counts[LangPromQL] != 1 {
		t.Errorf("promql count = %d, want 1", rep.Counts[LangPromQL])
	}
	if rep.Counts[LangLogQL] != 1 {
		t.Errorf("logql count = %d, want 1", rep.Counts[LangLogQL])
	}
	if rep.Counts[LangServerSide] != 3 {
		t.Errorf("server-side count = %d, want 3", rep.Counts[LangServerSide])
	}
}

func TestCheckGrafanaRulesBroken(t *testing.T) {
	rep := newReport()
	if err := checkGrafanaRules(rep, "testdata/grafana_rules_broken.yaml", loadFixture(t, "grafana_rules_broken.yaml")); err != nil {
		t.Fatalf("checkGrafanaRules: %v", err)
	}
	if len(rep.Failures) != 2 {
		t.Fatalf("want 2 failures, got %d: %v", len(rep.Failures), rep.Failures)
	}
	all := failuresString(rep)
	for _, sub := range []string{
		"unclosed left parenthesis",
		"fixture-broken",         // group name
		"fx-bad-promql",          // rule uid
		"Bad PromQL",             // rule title
		"server-side expression", // the malformed __expr__ node
	} {
		if !strings.Contains(all, sub) {
			t.Errorf("failures %q do not contain %q", all, sub)
		}
	}
}

func TestCheckPromRulesGood(t *testing.T) {
	rep := newReport()
	if err := checkPromRules(rep, "testdata/prom_rules_good.yaml", loadFixture(t, "prom_rules_good.yaml")); err != nil {
		t.Fatalf("checkPromRules: %v", err)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("want no failures, got %d: %v", len(rep.Failures), rep.Failures)
	}
	if rep.Counts[LangPromQL] != 2 {
		t.Errorf("promql count = %d, want 2", rep.Counts[LangPromQL])
	}
}

func TestCheckPromRulesBroken(t *testing.T) {
	rep := newReport()
	if err := checkPromRules(rep, "testdata/prom_rules_broken.yaml", loadFixture(t, "prom_rules_broken.yaml")); err != nil {
		t.Fatalf("checkPromRules: %v", err)
	}
	if len(rep.Failures) != 2 {
		t.Fatalf("want 2 failures, got %d: %v", len(rep.Failures), rep.Failures)
	}
	all := failuresString(rep)
	for _, sub := range []string{"BadSyntax", "LeakedDashboardVariable", "no templating"} {
		if !strings.Contains(all, sub) {
			t.Errorf("failures %q do not contain %q", all, sub)
		}
	}
}

// failuresString joins every failure so a test can assert on the whole set.
func failuresString(rep *Report) string {
	var b strings.Builder
	for _, f := range rep.Failures {
		b.WriteString(f.String())
		b.WriteString("\n")
	}
	return b.String()
}
