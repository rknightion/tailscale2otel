package catalog_test

import (
	"context"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/otlptranslator"
	"github.com/rknightion/tailscale2otel/v5/internal/catalog"
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// This file is the pull-path NAME CONTRACT for the whole catalog (#379).
//
// Before it existed, the only /metrics name assertions in the tree were prefix
// matches in internal/telemetry that deliberately refused to pin a unit suffix,
// and metricdoc.PromName() — the function that generates the "Prometheus
// (normalized) name" column in docs/metrics.md and that dashboardrefs_test.go
// validates the dashboards and alert rules against — was a hand-rolled
// reimplementation of the translation rules that had never been executed against
// the real exporter. It was wrong for three metrics.
//
// TestPromNameManifestMatchesLiveExporter closes that loop empirically: it emits
// EVERY metric in the in-code catalog through a real telemetry.ProviderSet with
// the Prometheus reader enabled, gathers the registry, and requires the family
// names the exporter actually produced to equal metricdoc.PromName() for every
// one of them. A mass rename — from a translation-strategy flip, an exporter
// upgrade, or an edit to PromName — cannot pass this.

// promEmit drives one catalog descriptor through the Emitter using the
// instrument its descriptor declares, because the instrument is what decides the
// _total / _ratio suffixes. Returns false for a descriptor with no emittable
// instrument (which the per-package catalog guards already reject).
func promEmit(e telemetry.Emitter, m metricdoc.Metric) bool {
	switch m.Instrument {
	case metricdoc.Counter:
		e.Counter(m.Name, m.Unit, m.Description, 1, nil)
	case metricdoc.UpDownCounter:
		e.UpDownCounter(m.Name, m.Unit, m.Description, 1, nil)
	case metricdoc.Gauge:
		e.Gauge(m.Name, m.Unit, m.Description, 1, nil)
	case metricdoc.Histogram:
		e.Histogram(m.Name, m.Unit, m.Description, 1, []float64{1, 10}, nil)
	default:
		return false
	}
	return true
}

// livePromFamilies emits every catalog metric and returns source-OTEL-name →
// exposed Prometheus family name, read from the registry rather than parsed out
// of the text exposition (so a histogram reports its base family name, and
// nothing depends on text-format quirks).
func livePromFamilies(t *testing.T, metrics []metricdoc.Metric) map[string]string {
	t.Helper()
	ctx := context.Background()
	ps, err := telemetry.NewProviderSet(ctx, telemetry.Options{
		ServiceName:       "tailscale2otel",
		Provider:          "tailscale",
		PrometheusEnabled: true,
		Protocol:          "stdout",
		StdoutWriter:      io.Discard,
	}, []telemetry.PerTailnetOptions{{Name: "solo", InstanceID: "i"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	defer func() { _ = ps.Shutdown(ctx) }()

	// One emitter for everything: the process vs per-tailnet split changes which
	// LABELS a series carries, never its name, and this test is about names.
	e := ps.Tailnet("solo").Emitter()
	emitted := map[string]bool{}
	for _, m := range metrics {
		if emitted[m.Name] {
			continue // a descriptor listed by two packages; one emit is enough
		}
		if !promEmit(e, m) {
			t.Errorf("metric %q declares instrument %q, which cannot be emitted", m.Name, m.Instrument)
			continue
		}
		emitted[m.Name] = true
	}

	families, err := ps.PromGatherer().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	// The exporter's family name is post-translation, so recover the mapping by
	// matching each exposed family back to the source name it came from. The
	// translation is deterministic, so ask the translator itself which source
	// produced which family instead of guessing from string shapes.
	namer := otlptranslator.NewMetricNamer("", telemetry.PromTranslationStrategy)
	bySource := map[string]string{}
	exposed := map[string]bool{}
	for _, f := range families {
		exposed[f.GetName()] = true
	}
	for _, m := range metrics {
		if bySource[m.Name] != "" {
			continue
		}
		want, err := namer.Build(otlptranslator.Metric{Name: m.Name, Unit: m.Unit, Type: promMetricTypeFor(m)})
		if err != nil {
			t.Errorf("metric %q: translator rejected it: %v", m.Name, err)
			continue
		}
		// A counter's registry family name carries the _total suffix; the
		// translator returns the same string, so a direct hit is expected. Assert
		// the family really is on the registry so this cannot pass vacuously
		// against a name the exporter never produced.
		if !exposed[want] {
			t.Errorf("metric %q: translator says family %q but the registry exposed no such family", m.Name, want)
			continue
		}
		bySource[m.Name] = want
	}
	return bySource
}

func promMetricTypeFor(m metricdoc.Metric) otlptranslator.MetricType {
	switch m.Instrument {
	case metricdoc.Counter:
		return otlptranslator.MetricTypeMonotonicCounter
	case metricdoc.UpDownCounter:
		return otlptranslator.MetricTypeNonMonotonicCounter
	case metricdoc.Gauge:
		return otlptranslator.MetricTypeGauge
	case metricdoc.Histogram:
		return otlptranslator.MetricTypeHistogram
	default:
		return otlptranslator.MetricTypeUnknown
	}
}

// TestPromNameManifestMatchesLiveExporter is the catalog-wide regression: every
// metric's documented Prometheus name must equal the family name the real
// exporter puts on /metrics. This is what makes a silent mass rename impossible.
func TestPromNameManifestMatchesLiveExporter(t *testing.T) {
	metrics := catalog.Metrics()
	if len(metrics) == 0 {
		t.Fatal("the in-code catalog reported no metrics; this test would pass vacuously")
	}
	live := livePromFamilies(t, metrics)

	checked := 0
	seen := map[string]bool{}
	for _, m := range metrics {
		if seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		got, ok := live[m.Name]
		if !ok {
			continue // already reported by livePromFamilies
		}
		if want := m.PromName(); got != want {
			t.Errorf("metric %q (unit %q, %s): /metrics exposes %q but metricdoc.PromName() documents %q\n"+
				"  the exporter is authoritative; PromName drives docs/metrics.md and dashboardrefs_test.go",
				m.Name, m.Unit, m.Instrument, got, want)
		}
		checked++
	}
	if checked < len(seen) {
		t.Errorf("only %d of %d distinct catalog metrics were checked", checked, len(seen))
	}
	t.Logf("verified %d distinct catalog metric names against the live Prometheus exporter", checked)
}

// TestPromNameStrategyAgreesWithExporter pins the ONE duplicated constant in this
// contract. metricdoc cannot import internal/telemetry (telemetry imports
// metricdoc), so metricdoc.PromName hardcodes the same translation strategy the
// pull-path exporter pins. If they ever diverge, docs/metrics.md and the
// dashboards start advertising names /metrics does not serve — exactly the class
// of bug #379 exists to prevent — and PromName has no way to notice on its own.
func TestPromNameStrategyAgreesWithExporter(t *testing.T) {
	if telemetry.PromTranslationStrategy != otlptranslator.UnderscoreEscapingWithSuffixes {
		t.Fatalf("telemetry.PromTranslationStrategy = %q; update metricdoc.promMetricNamer to match",
			telemetry.PromTranslationStrategy)
	}
	// Behavioral cross-check rather than a second constant comparison: run a
	// name through PromName that only this strategy produces. NoTranslation would
	// return the dotted name; a *WithoutSuffixes strategy would drop `_ratio`.
	m := metricdoc.Metric{Name: "tailscale.devices.count", Unit: "1", Instrument: metricdoc.Gauge}
	if got := m.PromName(); got != "tailscale_devices_count_ratio" {
		t.Errorf("PromName() = %q, want tailscale_devices_count_ratio — metricdoc's strategy no longer matches the exporter's", got)
	}
}

// TestPromNameManifestSnapshot prints the full source→Prometheus name manifest on
// -v. It is the artifact to diff by hand when a rename IS intended, and it fails
// if two distinct source metrics normalize to the same Prometheus name — a
// collision that silently merges two series families on both the pull and push
// paths and that no other test in the tree looks for.
func TestPromNameManifestSnapshot(t *testing.T) {
	owner := map[string]string{}
	var lines []string
	for _, m := range catalog.Metrics() {
		name := m.PromName()
		if name == "" {
			t.Errorf("metric %q normalizes to an empty Prometheus name", m.Name)
			continue
		}
		if prev, ok := owner[name]; ok && prev != m.Name {
			t.Errorf("Prometheus name collision: %q and %q both normalize to %q", prev, m.Name, name)
			continue
		}
		if _, ok := owner[name]; !ok {
			owner[name] = m.Name
			lines = append(lines, m.Name+"\t"+string(m.Instrument)+"\t"+m.Unit+"\t"+name)
		}
	}
	sort.Strings(lines)
	t.Logf("pull-path name manifest (%d metrics):\n%s", len(lines), strings.Join(lines, "\n"))
}
