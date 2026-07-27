package main

import (
	"strings"
	"testing"
)

func TestCheckGrafanaRulesGood(t *testing.T) {
	rep := newReport()
	if err := checkGrafanaRules(rep, "testdata/grafana_rule_good.json", loadFixture(t, "grafana_rule_good.json")); err != nil {
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
	if rep.Counts[LangServerSide] != 2 {
		t.Errorf("server-side count = %d, want 2", rep.Counts[LangServerSide])
	}
}

func TestCheckGrafanaRulesBroken(t *testing.T) {
	rep := newReport()
	if err := checkGrafanaRules(rep, "testdata/grafana_rule_broken.json", loadFixture(t, "grafana_rule_broken.json")); err != nil {
		t.Fatalf("checkGrafanaRules: %v", err)
	}
	if len(rep.Failures) != 3 {
		t.Fatalf("want 3 failures, got %d: %v", len(rep.Failures), rep.Failures)
	}
	all := failuresString(rep)
	for _, sub := range []string{
		"unclosed left parenthesis",
		"fx-bad-promql", // metadata.name
		"Bad PromQL",    // rule title
		// A rule expression is evaluated verbatim — there is no dashboard and no
		// time picker — so a leaked dashboard variable is always a bug.
		"no templating",
		// The threshold node has an empty `type`, which Grafana rejects.
		"server-side expression",
	} {
		if !strings.Contains(all, sub) {
			t.Errorf("failures %q do not contain %q", all, sub)
		}
	}
}

// A manifest whose apiVersion is not the rules API is a hard error, not a
// per-expression failure: gcx would not route it to the rules endpoint at all,
// so checking its expressions would be beside the point.
func TestCheckGrafanaRulesRejectsWrongAPIVersion(t *testing.T) {
	rep := newReport()
	body := strings.Replace(
		string(loadFixture(t, "grafana_rule_good.json")),
		"rules.alerting.grafana.app/v0alpha1", "rules.alerting.grafana.app/v1", 1)
	err := checkGrafanaRules(rep, "testdata/synthetic.json", []byte(body))
	if err == nil {
		t.Fatal("want an error for an unexpected apiVersion, got nil")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("error %q does not mention apiVersion", err)
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
