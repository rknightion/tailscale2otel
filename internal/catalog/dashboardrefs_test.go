package catalog_test

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/catalog"
)

// Dashboards and alert rules are the only artifacts that reference metric names
// from OUTSIDE the Go build, so nothing connects them to the catalog those names
// come from. A metric renamed in code leaves every panel querying it silently
// empty: the dashboard still loads, the panel just shows "No data", and no test,
// lint or drift check notices (#438).
//
// These tests close that loop by extracting every metric name the generated
// dashboard and rule files query and requiring it to exist in the in-code catalog
// under its NORMALIZED Prometheus spelling — which is the spelling a query has to
// use, and the thing most likely to be got wrong by hand (dots to underscores,
// `_total` only for monotonic counters, `_ratio` for a unit-"1" gauge).

const (
	dashboardPath = "../../deploy/grafana/tailscale2otel.json"
	// The shipped rules are Grafana-managed `rules.alerting.grafana.app/v0alpha1`
	// manifests, one JSON file per rule plus a folder manifest, pushed with
	// `gcx resources push`. The former provisioning-format and Prometheus-ruler
	// files are gone — see deploy/alerts/README.md.
	rulesDir = "../../deploy/alerts/grafana-managed"
)

// metricRef matches a bare Prometheus metric identifier in a PromQL expression.
// Anchored on the tailscale2otel prefixes so it never picks up a label name, a
// function, or a `tailscaled_*` passthrough series (those come from nodes, not
// from this exporter's catalog, so the catalog cannot vouch for them).
var metricRef = regexp.MustCompile(`\btailscale(?:2otel)?_[a-z0-9_]+`)

// catalogPromNames is every metric name the code can emit, in its normalized
// Prometheus spelling, plus the histogram/summary suffixes Prometheus derives.
func catalogPromNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, m := range catalog.Metrics() {
		base := m.PromName()
		out[base] = true
		// A histogram becomes _bucket/_sum/_count series; queries legitimately
		// reference those rather than the base name.
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			out[base+suffix] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("the in-code catalog reported no metrics; this test would pass vacuously")
	}
	return out
}

// catalogLabelNames is every LABEL name the code emits, normalized. Labels share
// the `tailscale_` prefix with metrics (`tailscale_collector`, `tailscale_node`,
// `tailscale_dst_service`, …), so a text scan cannot tell them apart by shape —
// it has to subtract the known label set. Deriving that set from the same catalog
// keeps it self-maintaining: a label added in code is recognized immediately, and
// a label REMOVED in code stops being recognized, which is the direction that
// should fail.
func catalogLabelNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, m := range catalog.Metrics() {
		for _, l := range m.PromLabels() {
			out[l] = true
		}
	}
	// LOG-event attributes too. The dashboard mixes PromQL panels with LogQL ones,
	// and a LogQL label filter looks identical to a metric name to a text scan —
	// tailscale_rx_bytes and tailscale_target_name are flow-log and node-metrics
	// LOG attributes, not metrics.
	for _, e := range catalog.LogEvents() {
		for _, a := range e.Attributes {
			out[strings.ReplaceAll(a, ".", "_")] = true
			// Loki labels carry the tailscale_ prefix that the OTEL attribute key
			// omits, e.g. attribute "rx.bytes" becomes label tailscale_rx_bytes.
			out["tailscale_"+strings.ReplaceAll(a, ".", "_")] = true
		}
	}
	// Resource attributes promoted onto every series by the OTLP exporter, so they
	// appear in queries without belonging to any single metric's attribute list.
	for _, l := range []string{
		"tailscale_tailnet",
		"tailscale2otel_provider",
	} {
		out[l] = true
	}
	if len(out) == 0 {
		t.Fatal("the in-code catalog reported no labels; the subtraction below would be vacuous")
	}
	return out
}

// matcherValue matches a label-matcher's quoted VALUE, including the JSON-escaped
// form the dashboard file stores (`category=\"tailscale_ips\"`). Values are
// stripped before extraction: a value happens to look exactly like an identifier,
// and treating one as a metric name reports a defect that is not there.
var matcherValue = regexp.MustCompile(`(?:=~|!~|!=|=)\s*\\?"[^"\\]*\\?"`)

// readArtifact returns an artifact's bytes. When path is a DIRECTORY it returns
// every rule manifest in it concatenated, so the rules read as one artifact the
// way the single provisioning file used to. Per-file extraction would be wrong
// here: a Loki-backed rule references no `tailscale_*` metric at all, and the
// empty-result guard below would fire on it.
func readArtifact(t *testing.T, path string) []byte {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		b, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative artifact path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return b
	}
	var buf []byte
	for _, m := range ruleManifests(t, path) {
		b, err := os.ReadFile(m) //nolint:gosec // fixed repo-relative artifact path
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	return buf
}

// referencedMetrics extracts every catalog-owned metric name mentioned in a file
// or, for the rule manifests, in a directory.
func referencedMetrics(t *testing.T, path string) []string {
	t.Helper()
	body := matcherValue.ReplaceAllString(string(readArtifact(t, path)), "=<value>")
	seen := map[string]bool{}
	for _, m := range metricRef.FindAllString(body, -1) {
		seen[m] = true
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatalf("%s references no tailscale metrics at all; either the file moved or this "+
			"extraction is broken, and every assertion built on it is vacuous", path)
	}
	return out
}

// recordingRuleNames are series the rules DEFINE rather than consume, so a later
// expression referencing one is legitimate even though the catalog has no such
// metric. In the Grafana-managed manifests a RecordingRule names its output series
// in `spec.metric`, not in the Prometheus formats' `record:` key.
func recordingRuleNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, path := range ruleManifests(t, rulesDir) {
		b, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative artifact path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc struct {
			Kind string `json:"kind"`
			Spec struct {
				Metric string `json:"metric"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if doc.Kind == "RecordingRule" && doc.Spec.Metric != "" {
			out[doc.Spec.Metric] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("no RecordingRule manifest declares a spec.metric; either the manifests moved " +
			"or the shape changed, and every downstream reference check is over-strict")
	}
	return out
}

func assertReferencesExist(t *testing.T, path string, extra map[string]bool) {
	t.Helper()
	known := catalogPromNames(t)
	labels := catalogLabelNames(t)
	var unknown []string
	for _, ref := range referencedMetrics(t, path) {
		if known[ref] || labels[ref] || extra[ref] {
			continue
		}
		unknown = append(unknown, ref)
	}
	if len(unknown) > 0 {
		t.Errorf("%s queries %d metric name(s) the in-code catalog cannot emit:\n  %s\n\n"+
			"Each one renders as an empty panel or a rule that never fires, with no error anywhere. "+
			"Either the metric was renamed/removed in code and the generator was not updated, or the "+
			"query has a normalization mistake (dots→underscores, `_total` only for monotonic "+
			"counters, `_ratio` for a unit-\"1\" gauge).",
			path, len(unknown), strings.Join(unknown, "\n  "))
	}
}

func TestFlagshipDashboardQueriesOnlyCatalogMetrics(t *testing.T) {
	assertReferencesExist(t, dashboardPath, recordingRuleNames(t))
}

func TestGrafanaRulesQueryOnlyCatalogMetrics(t *testing.T) {
	assertReferencesExist(t, rulesDir, recordingRuleNames(t))
}

// A duplicate panel ID makes Grafana's behavior undefined — links, repeats and
// library-panel references all key off it — and a generator that assigns IDs by
// hand or by counter is exactly where a collision appears.
func TestFlagshipDashboardPanelIDsAreUnique(t *testing.T) {
	b, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the generated dashboard is not valid JSON: %v", err)
	}

	seen := map[float64][]string{}
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch v := node.(type) {
		case map[string]any:
			if id, ok := v["id"].(float64); ok {
				if _, isPanel := v["vizConfig"]; isPanel {
					seen[id] = append(seen[id], path)
				} else if _, isPanel := v["type"]; isPanel {
					if _, hasTitle := v["title"]; hasTitle {
						seen[id] = append(seen[id], path)
					}
				}
			}
			for k, child := range v {
				walk(child, path+"."+k)
			}
		case []any:
			for i, child := range v {
				walk(child, path+"["+itoa(i)+"]")
			}
		}
	}
	walk(doc, "$")

	for id, paths := range seen {
		if len(paths) > 1 {
			t.Errorf("panel id %v is used %d times (%s). Grafana keys links, repeats and "+
				"library-panel references off the id, so a collision makes those undefined.",
				id, len(paths), strings.Join(paths, ", "))
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// Every panel's datasource must be a variable the dashboard actually declares.
// A typo'd or removed variable renders the panel against Grafana's default
// datasource — or nothing — with no error, the same silent-wrong-output failure
// as an unknown metric name (#438).
//
// "-- Grafana --" is the built-in pseudo-datasource used by annotation queries and
// is legitimately not a declared variable.
func TestFlagshipDashboardDatasourceRefsAreDeclared(t *testing.T) {
	b, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the generated dashboard is not valid JSON: %v", err)
	}

	declared := map[string]bool{}
	refs := map[string]int{}
	var walk func(node any, inVariables bool)
	walk = func(node any, inVariables bool) {
		switch v := node.(type) {
		case map[string]any:
			if ds, ok := v["datasource"].(map[string]any); ok {
				if name, ok := ds["name"].(string); ok {
					refs[name]++
				}
			}
			// A dashboard's variable list declares each one by name; the v2 schema
			// nests them under "variables", the classic schema under "templating".
			if name, ok := v["name"].(string); ok && inVariables {
				declared[name] = true
			}
			for k, child := range v {
				walk(child, inVariables || k == "variables" || k == "templating")
			}
		case []any:
			for _, child := range v {
				walk(child, inVariables)
			}
		}
	}
	walk(doc, false)

	if len(refs) == 0 {
		t.Fatal("the dashboard references no datasources at all; this extraction is broken and " +
			"the assertion below would be vacuous")
	}
	if len(declared) == 0 {
		t.Fatal("no dashboard variables were discovered; the assertion below would reject everything")
	}

	for ref, count := range refs {
		if ref == "-- Grafana --" {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")
		if name == ref {
			t.Errorf("datasource %q (%d panel(s)) is a literal, not a ${variable}: the dashboard "+
				"would be pinned to one stack's datasource UID and unusable anywhere else", ref, count)
			continue
		}
		if !declared[name] {
			t.Errorf("datasource variable %q (%d panel(s)) is referenced but never declared. Those "+
				"panels fall back to Grafana's default datasource silently.", ref, count)
		}
	}
}

// The README states rule counts, and prose counts drift the moment a rule is
// added — exactly how it came to claim 79 Grafana-managed rules against a
// generated file holding 85 (#438). Derive them from the files instead.
func TestReadmeRuleCountsMatchTheShippedFiles(t *testing.T) {
	alerts, recordings := countRules(t)

	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(readme)

	for _, want := range []struct {
		count int
		what  string
	}{
		{alerts, "alert rules"},
		{recordings, "recording rules"},
		{alerts + recordings, "total shipped rules"},
	} {
		if !strings.Contains(body, itoa(want.count)) {
			t.Errorf("README never states %d, the actual number of %s. A count stated in prose "+
				"drifts the moment a rule is added, so it must be the real figure.",
				want.count, want.what)
		}
	}
}

// countRules totals the shipped rule manifests by kind. One JSON file per rule,
// so the count is a file count split on `kind` — there are no groups any more.
func countRules(t *testing.T) (alerts, recordings int) {
	t.Helper()
	for _, path := range ruleManifests(t, rulesDir) {
		b, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative artifact path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		switch doc.Kind {
		case "AlertRule":
			alerts++
		case "RecordingRule":
			recordings++
		}
	}
	if alerts == 0 || recordings == 0 {
		t.Fatalf("counted %d alert and %d recording manifests; the shape changed and every "+
			"count here is vacuous", alerts, recordings)
	}
	return alerts, recordings
}

// Structural validation of the shipped Grafana-managed rule manifests.
//
// deploy/alerts/gen/validate_manifests.py is the stricter, primary gate and it
// rides the Python unittest CI step. This is deliberate defense in depth in the
// language that always runs: the checks below are the ones whose failure mode is
// SILENT — a manifest that parses, validates client-side, and is simply never
// applied or never fires.
//
// `gcx resources validate` cannot substitute for either. It reports
// "does not support server-side dry-run ... It did not validate the spec", and
// all 25 noDataState x execErrState casing combinations pass through it.
func TestGrafanaRuleManifestsAreStructurallyValid(t *testing.T) {
	// The casing is asymmetric and neither half announces a mistake: a rule with
	// noDataState "OK" is rejected on apply while every local check passes.
	validNoData := map[string]bool{"NoData": true, "Ok": true, "Alerting": true, "KeepLast": true}
	// CORRECTED 2026-07-27 by a real `gcx resources push`: BOTH state fields spell the
	// OK value "Ok". This map previously allowed "OK" for execErrState, matching the same
	// wrong belief held by the Python generator, its validator and three docs — so every
	// offline gate agreed with itself while 19 rules were rejected at push time.
	validExecErr := map[string]bool{"Ok": true, "Error": true, "Alerting": true, "KeepLast": true}

	names := map[string]string{}
	titles := map[string]bool{}
	var alerts int
	for _, path := range ruleManifests(t, rulesDir) {
		b, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative artifact path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Title        string            `json:"title"`
				Metric       string            `json:"metric"`
				NoDataState  string            `json:"noDataState"`
				ExecErrState string            `json:"execErrState"`
				Annotations  map[string]string `json:"annotations"`
				Expressions  map[string]any    `json:"expressions"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		if doc.APIVersion != "rules.alerting.grafana.app/v0alpha1" {
			t.Errorf("%s has apiVersion %q, which gcx will not route to the rules API",
				path, doc.APIVersion)
		}
		// Grafana caps rule UIDs at 40 characters and restricts the charset. Over
		// the cap the apply fails; the generator compacts recording-rule slugs for
		// exactly this reason.
		if n := doc.Metadata.Name; n == "" || len(n) > 40 || !ruleUID.MatchString(n) {
			t.Errorf("%s has metadata.name %q, which is not a legal Grafana rule uid "+
				"(<=40 chars, letters/digits/hyphen/underscore)", path, n)
		}
		if prev, dup := names[doc.Metadata.Name]; dup {
			t.Errorf("uid %q is used by both %s and %s; the second silently overwrites the first",
				doc.Metadata.Name, prev, path)
		}
		names[doc.Metadata.Name] = path
		if doc.Spec.Title == "" {
			t.Errorf("%s has no spec.title", path)
		}
		if titles[doc.Spec.Title] {
			t.Errorf("rule title %q appears twice; alert notifications become ambiguous",
				doc.Spec.Title)
		}
		titles[doc.Spec.Title] = true
		if len(doc.Spec.Expressions) == 0 {
			t.Errorf("%s has no spec.expressions, so it references nothing", path)
		}

		switch doc.Kind {
		case "AlertRule":
			alerts++
			if !validNoData[doc.Spec.NoDataState] {
				t.Errorf("%s has noDataState %q. Valid: NoData, Ok, Alerting, KeepLast — note "+
					"\"Ok\", NOT \"OK\". The wrong casing makes the rule un-deployable while "+
					"every offline check passes.", path, doc.Spec.NoDataState)
			}
			if !validExecErr[doc.Spec.ExecErrState] {
				t.Errorf("%s has execErrState %q. Valid: Ok, Error, Alerting, KeepLast — "+
					"\"OK\" is rejected by the API, same as for noDataState.",
					path, doc.Spec.ExecErrState)
			}
			// A responder reads the summary first; without one they get a title.
			if doc.Spec.Annotations["summary"] == "" {
				t.Errorf("alert rule %q has no summary annotation", doc.Spec.Title)
			}
			// Runbook links are generated for every rule (#387), so a missing one
			// means the generator silently skipped it.
			if doc.Spec.Annotations["runbook_url"] == "" {
				t.Errorf("alert rule %q has no runbook_url annotation", doc.Spec.Title)
			}
			// Grafana requires the pair. One without the other is a dead link that
			// renders as a broken panel reference rather than an error.
			_, hasDash := doc.Spec.Annotations["__dashboardUid__"]
			_, hasPanel := doc.Spec.Annotations["__panelId__"]
			if hasDash != hasPanel {
				t.Errorf("alert rule %q sets only one of __dashboardUid__/__panelId__; Grafana "+
					"needs both or neither", doc.Spec.Title)
			}
		case "RecordingRule":
			if doc.Spec.Metric == "" {
				t.Errorf("recording rule %q names no target metric", doc.Spec.Title)
			}
			// The v0alpha1 RecordingRule spec is additionalProperties:false and has
			// no such fields; emitting them fails the apply.
			if doc.Spec.NoDataState != "" || doc.Spec.ExecErrState != "" {
				t.Errorf("recording rule %q carries alert-only state fields", doc.Spec.Title)
			}
		default:
			t.Errorf("%s has kind %q, expected AlertRule or RecordingRule", path, doc.Kind)
		}
	}
	if alerts == 0 {
		t.Fatal("no AlertRule manifests parsed; every assertion above is vacuous")
	}
}

// ruleUID is Grafana's accepted rule-uid charset.
var ruleUID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
