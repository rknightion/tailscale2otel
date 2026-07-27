package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// grafanaRulesDoc is the Grafana provisioning rule-file subset this tool reads
// (`apiVersion: 1`). Each rule is a small DAG of `data[]` nodes: nodes pointing
// at a real datasource carry a query in `model.expr`, and nodes whose
// `datasourceUid` is `__expr__` are Grafana SERVER-SIDE expressions (reduce /
// math / threshold / classic_conditions) that are not a query language at all.
type grafanaRulesDoc struct {
	APIVersion int `yaml:"apiVersion"`
	Groups     []struct {
		Name   string `yaml:"name"`
		Folder string `yaml:"folder"`
		Rules  []struct {
			UID   string `yaml:"uid"`
			Title string `yaml:"title"`
			Data  []struct {
				RefID         string `yaml:"refId"`
				DatasourceUID string `yaml:"datasourceUid"`
				Model         struct {
					Expr       string `yaml:"expr"`
					Expression string `yaml:"expression"`
					Type       string `yaml:"type"`
					Datasource struct {
						Type string `yaml:"type"`
						UID  string `yaml:"uid"`
					} `yaml:"datasource"`
				} `yaml:"model"`
			} `yaml:"data"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

// promRulesDoc is the plain Prometheus rule-group format used by the
// hand-maintained rules file: `groups[].rules[]` with `record:` or `alert:`.
type promRulesDoc struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Record string `yaml:"record"`
			Alert  string `yaml:"alert"`
			Expr   string `yaml:"expr"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

// serverSideExprUID is the sentinel datasource uid Grafana uses for its
// server-side expression nodes.
const serverSideExprUID = "__expr__"

// checkGrafanaRules extracts and checks every query in a Grafana provisioning
// rule file, and separately sanity-checks the server-side expression nodes.
func checkGrafanaRules(rep *Report, path string, data []byte) error {
	var doc grafanaRulesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s as YAML: %w", path, err)
	}
	if doc.APIVersion != 1 {
		return fmt.Errorf("%s: apiVersion is %d, want 1 (Grafana provisioning)", path, doc.APIVersion)
	}
	ctx := ruleInterpolation()

	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			for _, node := range r.Data {
				where := fmt.Sprintf("%s/%s %q refId=%s", g.Name, r.UID, r.Title, node.RefID)

				if node.DatasourceUID == serverSideExprUID {
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
		}
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

// checkPromRules extracts and checks every expr in a plain Prometheus rule file.
func checkPromRules(rep *Report, path string, data []byte) error {
	var doc promRulesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s as YAML: %w", path, err)
	}
	ctx := ruleInterpolation()

	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			kind := "record=" + r.Record
			if r.Alert != "" {
				kind = "alert=" + r.Alert
			}
			where := fmt.Sprintf("%s/%s", g.Name, kind)
			if strings.TrimSpace(r.Expr) == "" {
				rep.fail(Expr{File: path, Where: where, Lang: LangPromQL}, "rule has no `expr`")
				continue
			}
			e := Expr{File: path, Where: where, Lang: LangPromQL, Raw: r.Expr}
			rep.record(e)
			checkExpr(rep, e, ctx)
		}
	}
	return nil
}
