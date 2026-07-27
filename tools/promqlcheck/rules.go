package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ruleAPIVersion is the Grafana-managed rule manifest apiVersion this tool reads.
// The repo ships one JSON manifest per rule, pushed with `gcx resources push`.
const ruleAPIVersion = "rules.alerting.grafana.app/v0alpha1"

// grafanaRuleDoc is the subset of a `rules.alerting.grafana.app/v0alpha1`
// AlertRule/RecordingRule manifest this tool reads. `spec.expressions` is a small
// DAG keyed by refId: nodes pointing at a real datasource carry a query in
// `model.expr`, and nodes whose datasource is `__expr__` are Grafana SERVER-SIDE
// expressions (reduce / math / threshold / classic_conditions), not a query
// language at all.
type grafanaRuleDoc struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Title       string `json:"title"`
		Expressions map[string]struct {
			DatasourceUID string `json:"datasourceUID"`
			Model         struct {
				Expr       string `json:"expr"`
				Expression string `json:"expression"`
				Type       string `json:"type"`
				Datasource struct {
					Type string `json:"type"`
					UID  string `json:"uid"`
				} `json:"datasource"`
			} `json:"model"`
		} `json:"expressions"`
	} `json:"spec"`
}

// serverSideExprUID is the sentinel datasource uid Grafana uses for its
// server-side expression nodes.
const serverSideExprUID = "__expr__"

// checkGrafanaRules extracts and checks every query in one Grafana-managed rule
// manifest, and separately sanity-checks the server-side expression nodes.
//
// The manifests are `rules.alerting.grafana.app/v0alpha1`, one JSON file per
// rule. Note `spec.expressions` is a MAP keyed by refId, not the provisioning
// format's ordered `data` array, and the node key is `datasourceUID` (capital
// UID) rather than provisioning's `datasourceUid`.
func checkGrafanaRules(rep *Report, path string, data []byte) error {
	var doc grafanaRuleDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s as JSON: %w", path, err)
	}
	if doc.APIVersion != ruleAPIVersion {
		return fmt.Errorf("%s: apiVersion is %q, want %q", path, doc.APIVersion, ruleAPIVersion)
	}
	ctx := ruleInterpolation()

	// A map has no inherent order; sort so the report is stable run to run.
	refIDs := make([]string, 0, len(doc.Spec.Expressions))
	for refID := range doc.Spec.Expressions {
		refIDs = append(refIDs, refID)
	}
	sort.Strings(refIDs)

	name := doc.Metadata.Name
	for _, refID := range refIDs {
		node := doc.Spec.Expressions[refID]
		where := fmt.Sprintf("%s %q refId=%s", name, doc.Spec.Title, refID)

		if node.DatasourceUID == serverSideExprUID || node.Model.Datasource.Type == serverSideExprUID {
			e := Expr{File: path, Where: where, Lang: LangServerSide, Raw: serverSideSummary(node.Model.Type, node.Model.Expression)}
			rep.record(e)
			// Not PromQL, but still worth asserting: Grafana rejects a
			// server-side node with no `type`, and a node with no
			// `expression` references no upstream refId, so the rule
			// silently evaluates to nothing.
			switch {
			case node.Model.Type == "":
				rep.fail(e, "server-side expression node has no `type` (reduce/math/threshold/…)")
			case node.Model.Expression == "" && node.Model.Type != "classic_conditions":
				rep.fail(e, "server-side expression node of type %q has no `expression` referencing an upstream refId", node.Model.Type)
			}
			continue
		}

		raw := node.Model.Expr
		if strings.TrimSpace(raw) == "" {
			continue
		}
		lang := ruleQueryLang(node.DatasourceUID, node.Model.Datasource.Type)
		e := Expr{File: path, Where: where, Lang: lang, Raw: raw}
		rep.record(e)
		checkExpr(rep, e, ctx)
	}
	return nil
}

// serverSideSummary renders a server-side node compactly for the verbose
// listing, which prints Raw for every recorded expression.
func serverSideSummary(typ, expression string) string {
	if typ == "" {
		typ = "<no type>"
	}
	if expression == "" {
		expression = "<no expression>"
	}
	return fmt.Sprintf("__expr__ %s(%s)", typ, expression)
}

// ruleQueryLang maps an alert-rule datasource onto a query language. Unlike a
// dashboard there is no variable to resolve — the uid is concrete — so this
// falls back to the model's declared datasource type and finally to PromQL,
// which is what an unrecognized metrics datasource almost always is.
func ruleQueryLang(uid, modelType string) Lang {
	switch {
	case modelType == "loki" || strings.Contains(uid, "logs") || strings.Contains(uid, "loki"):
		return LangLogQL
	case modelType == "tempo" || strings.Contains(uid, "tempo") || strings.Contains(uid, "traces"):
		return LangTraceQL
	default:
		return LangPromQL
	}
}
