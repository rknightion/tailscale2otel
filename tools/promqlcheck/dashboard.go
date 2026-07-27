package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The Grafana dashboard **schema v2** subset this tool reads. The repo emits v2
// only (Grafana 13+), so there is deliberately no v1/Classic code path: panels
// live in a flat `spec.elements` map keyed by element name, NOT in a `panels[]`
// array, and each query's language is decided by its datasource rather than by
// the panel type.
type dashboardDoc struct {
	APIVersion string `json:"apiVersion"`
	Spec       struct {
		Variables []struct {
			Kind string `json:"kind"`
			Spec struct {
				Name     string `json:"name"`
				PluginID string `json:"pluginId"`
			} `json:"spec"`
		} `json:"variables"`
		Elements map[string]struct {
			Kind string `json:"kind"`
			Spec struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
				Data  struct {
					Spec struct {
						Queries []struct {
							Spec struct {
								RefID string `json:"refId"`
								Query struct {
									Group      string `json:"group"`
									Datasource struct {
										Name string `json:"name"`
										Type string `json:"type"`
										UID  string `json:"uid"`
									} `json:"datasource"`
									// Prometheus and Loki panels put the query in
									// `expr`; Tempo panels use `query` instead.
									// Reading only `expr` silently skips every
									// trace panel, which is exactly the kind of
									// quiet zero this tool must not produce.
									Spec struct {
										Expr  string `json:"expr"`
										Query string `json:"query"`
									} `json:"spec"`
								} `json:"query"`
							} `json:"spec"`
						} `json:"queries"`
					} `json:"spec"`
				} `json:"data"`
			} `json:"spec"`
		} `json:"elements"`
	} `json:"spec"`
}

// checkDashboard extracts and checks every panel query in a v2 dashboard.
//
// It returns an error only for problems with the FILE (unreadable, wrong
// schema); everything about an individual expression lands in the report as a
// failure, so one bad panel never hides the rest.
func checkDashboard(rep *Report, path string, data []byte) error {
	var doc dashboardDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s as JSON: %w", path, err)
	}
	if !strings.HasPrefix(doc.APIVersion, "dashboard.grafana.app/v2") {
		return fmt.Errorf("%s: apiVersion is %q, want dashboard.grafana.app/v2 — this tool has no v1/Classic code path by design", path, doc.APIVersion)
	}

	declared := map[string]bool{}
	dsPlugin := map[string]string{} // datasource-variable name -> plugin id
	for _, v := range doc.Spec.Variables {
		declared[v.Spec.Name] = true
		if v.Kind == "DatasourceVariable" {
			dsPlugin[v.Spec.Name] = v.Spec.PluginID
		}
	}
	ctx := dashboardInterpolation(declared)

	// Elements is a map, so iterate it in key order: CI output must be stable
	// between runs of an unchanged artifact.
	for _, key := range sortedKeys(doc.Spec.Elements) {
		el := doc.Spec.Elements[key]
		if el.Kind != "Panel" {
			continue
		}
		for _, q := range el.Spec.Data.Spec.Queries {
			raw := q.Spec.Query.Spec.Expr
			if strings.TrimSpace(raw) == "" {
				raw = q.Spec.Query.Spec.Query
			}
			if strings.TrimSpace(raw) == "" {
				continue
			}
			where := fmt.Sprintf("panel id=%d %q refId=%s", el.Spec.ID, el.Spec.Title, q.Spec.RefID)

			lang, err := dashboardQueryLang(q.Spec.Query.Group, q.Spec.Query.Datasource.Type, q.Spec.Query.Datasource.Name, dsPlugin)
			e := Expr{File: path, Where: where, Lang: lang, Raw: raw}
			rep.record(e)
			if err != nil {
				rep.fail(e, "%v", err)
				continue
			}
			checkExpr(rep, e, ctx)
		}
	}
	return nil
}

// dashboardQueryLang decides which language a panel query is written in.
//
// v2 carries the datasource type in `query.group` when it is known statically,
// but this repo's dashboards point every panel at a datasource VARIABLE
// (`${ds_prometheus}`) and leave `group` empty — so the authoritative source is
// the pluginId on the matching DatasourceVariable. A reference to a datasource
// variable the dashboard never declares is an error, not an unknown language:
// it is exactly the typo class this tool exists to catch.
func dashboardQueryLang(group, dsType, dsName string, dsPlugin map[string]string) (Lang, error) {
	plugin := group
	if plugin == "" {
		plugin = dsType
	}
	if plugin == "" {
		name := strings.Trim(dsName, "${}")
		if name == "" {
			return "", fmt.Errorf("query has no datasource: cannot tell which query language to validate it as")
		}
		p, ok := dsPlugin[name]
		if !ok {
			return "", fmt.Errorf("datasource %s refers to $%s, which is not a DatasourceVariable declared in spec.variables[]", dsName, name)
		}
		plugin = p
	}

	switch plugin {
	case "prometheus":
		return LangPromQL, nil
	case "loki":
		return LangLogQL, nil
	case "tempo":
		return LangTraceQL, nil
	default:
		return "", fmt.Errorf("unsupported datasource plugin %q: promqlcheck knows prometheus, loki and tempo, and refuses to silently skip anything else", plugin)
	}
}
