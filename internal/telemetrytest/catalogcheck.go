package telemetrytest

import (
	"sort"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
)

// AssertCatalogAttrs is the attribute-drift guard for a package's catalog_test.go
// (#126). For every metric point and log record captured by rec, it asserts each
// emitted attribute key is declared in the matching catalog entry's Attributes
// (metrics keyed by Metric.Name, logs by LogEvent.Name == LogRecord.EventName).
//
// An emitted-but-undeclared attribute is exactly the drift that silently rots
// docs/metrics.md (generated from the catalogs) — the class that let the
// tailscale.key.scopes attribute slip past the older Name/Unit/Instrument-only
// guards. It ignores any recorded signal with no catalog entry (those are covered
// by each catalog_test.go's existing name-membership check), so a collector can
// call this in addition to its current assertions.
//
// It intentionally does NOT require every declared attribute to be observed: a
// single test rarely exercises every conditional attribute, and a declared-only
// attribute does not corrupt the docs. Pass a nil logs slice for a metrics-only
// collector (and vice versa).
func AssertCatalogAttrs(t testing.TB, rec *Recorder, metrics []metricdoc.Metric, logs []metricdoc.LogEvent) {
	t.Helper()

	declaredMetric := make(map[string]map[string]struct{}, len(metrics))
	for _, m := range metrics {
		declaredMetric[m.Name] = toAttrSet(m.Attributes)
	}
	for _, name := range rec.MetricNames() {
		decl, ok := declaredMetric[name]
		if !ok {
			continue
		}
		for _, p := range rec.MetricPoints(name) {
			for _, k := range sortedKeys(p.Attrs) {
				if _, ok := decl[k]; !ok {
					t.Errorf("metric %q emits attribute %q not declared in its catalog Attributes "+
						"(docs/metrics.md will be missing it — add it to the Metric descriptor)", name, k)
				}
			}
		}
	}

	declaredLog := make(map[string]map[string]struct{}, len(logs))
	for _, e := range logs {
		declaredLog[e.Name] = toAttrSet(e.Attributes)
	}
	for _, r := range rec.LogRecords() {
		decl, ok := declaredLog[r.EventName]
		if !ok {
			continue
		}
		for _, k := range sortedKeys(r.Attrs) {
			if _, ok := decl[k]; !ok {
				t.Errorf("log event %q emits attribute %q not declared in its LogCatalog Attributes "+
					"(docs/metrics.md will be missing it — add it to the LogEvent descriptor)", r.EventName, k)
			}
		}
	}
}

func toAttrSet(keys []string) map[string]struct{} {
	s := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		s[k] = struct{}{}
	}
	return s
}

// sortedKeys makes the assertion order deterministic (stable test failures).
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
