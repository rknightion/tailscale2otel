package catalog_test

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/catalog"
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
	grafanaRules  = "../../deploy/alerts/tailscale2otel.grafana-rules.yaml"
	promRules     = "../../deploy/alerts/tailscale2otel.rules.yaml"
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

// referencedMetrics extracts every catalog-owned metric name mentioned in a file.
func referencedMetrics(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := matcherValue.ReplaceAllString(string(b), "=<value>")
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

// recordingRuleNames are series the rule files DEFINE rather than consume, so a
// later expression referencing one is legitimate even though the catalog has no
// such metric.
func recordingRuleNames(t *testing.T, paths ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	record := regexp.MustCompile(`(?m)^\s*(?:-\s+)?record:\s*"?([a-zA-Z_:][a-zA-Z0-9_:]*)"?`)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		for _, m := range record.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
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
	assertReferencesExist(t, dashboardPath, recordingRuleNames(t, grafanaRules, promRules))
}

func TestGrafanaRulesQueryOnlyCatalogMetrics(t *testing.T) {
	assertReferencesExist(t, grafanaRules, recordingRuleNames(t, grafanaRules, promRules))
}

func TestPrometheusRulesQueryOnlyCatalogMetrics(t *testing.T) {
	assertReferencesExist(t, promRules, recordingRuleNames(t, grafanaRules, promRules))
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
