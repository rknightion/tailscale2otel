// Package metricdoc is the single in-code source of truth for telemetry
// DOCUMENTATION metadata: each emitted metric and log event declares its name,
// unit, instrument, human description, and attribute keys here, and the emit
// sites reference those declarations so the description/unit cannot drift from
// what is documented. The catalog is rendered to docs/metrics.md by the
// generator in tools/metricscatalog (so the docs are derived from code, never
// hand-maintained), and validated against what the processors actually emit.
package metricdoc

import (
	"strings"

	"github.com/prometheus/otlptranslator"
)

// promMetricNamer is the OTLP→Prometheus metric-name translator, configured with
// the SAME translation strategy the pull-path exporter pins
// (telemetry.PromTranslationStrategy — duplicated as a literal here because
// internal/telemetry imports this package, so the dependency cannot be
// reversed; internal/catalog's promname manifest test asserts the two agree).
//
// Delegating to prometheus/otlptranslator rather than hand-rolling the rules is
// deliberate (#379): this is the exact library used BOTH by the OTEL Prometheus
// exporter on the pull path AND by Mimir's OTLP ingest on the Grafana Cloud push
// path, so the documented Prometheus name cannot drift from either. The
// hand-rolled predecessor got three real catalog metrics wrong — it appended a
// unit suffix that the translator DEDUPES against tokens already in the name, so
// docs/metrics.md and the generated dashboard advertised
// `tailscale2otel_objectstore_bytes_bytes_total`, a series nothing emits.
var promMetricNamer = otlptranslator.NewMetricNamer("", otlptranslator.UnderscoreEscapingWithSuffixes)

// promMetricType maps a documented instrument onto the translator's metric type,
// mirroring what the OTEL Prometheus exporter derives from the aggregated
// metricdata (Sum monotonic→MonotonicCounter, Sum non-monotonic→
// NonMonotonicCounter, Gauge→Gauge, Histogram→Histogram).
func promMetricType(i Instrument) otlptranslator.MetricType {
	switch i {
	case Counter:
		return otlptranslator.MetricTypeMonotonicCounter
	case UpDownCounter:
		return otlptranslator.MetricTypeNonMonotonicCounter
	case Gauge:
		return otlptranslator.MetricTypeGauge
	case Histogram:
		return otlptranslator.MetricTypeHistogram
	default:
		return otlptranslator.MetricTypeUnknown
	}
}

// Instrument is the OTEL instrument kind backing a metric. It drives the
// OTLP→Prometheus name normalization (only monotonic counters get _total; only
// unit-"1" gauges get _ratio).
type Instrument string

const (
	// Counter is a monotonic cumulative sum (Prometheus: _total, use rate()).
	Counter Instrument = "counter"
	// Gauge is a point-in-time value.
	Gauge Instrument = "gauge"
	// UpDownCounter is a non-monotonic sum (no _total in Prometheus).
	UpDownCounter Instrument = "updowncounter"
	// Histogram is a distribution with explicit buckets (Prometheus: _bucket/
	// _sum/_count; no _total, and no _ratio even at unit "1").
	Histogram Instrument = "histogram"
)

// TimestampProvenance states where an absolute Unix-timestamp gauge gets its
// clock value. Durations and ages also use seconds but deliberately leave this
// empty because they do not claim an event happened at a particular instant.
type TimestampProvenance string

const (
	// TimestampSource is supplied by the observed source record or artifact.
	TimestampSource TimestampProvenance = "source"
	// TimestampPersistedObservation is the local time this exporter first
	// observed a condition, retained across restarts when checkpoints are durable.
	TimestampPersistedObservation TimestampProvenance = "persisted_observation"
	// TimestampProcessLocal records an event in the lifetime of this process.
	TimestampProcessLocal TimestampProvenance = "process_local"
)

// Metric declares one emitted metric's documentation metadata. Name/Unit/
// Description are exactly the values passed to the telemetry.Emitter at the emit
// site (reference these fields there so there is a single source of truth).
type Metric struct {
	Name        string              // dotted OTEL source name, e.g. "tailscale.network.io"
	Unit        string              // UCUM unit, e.g. "By", "s", "d", "1", "{flow}"
	Instrument  Instrument          // counter | gauge | updowncounter
	Description string              // human description (also exported as OTLP metric metadata)
	Attributes  []string            // dotted OTEL attribute keys carried on the metric
	Group       string              // docs/metrics.md section heading this metric belongs under
	TimeSource  TimestampProvenance // source | persisted_observation | process_local
}

// LogEvent declares one emitted log record's documentation metadata.
type LogEvent struct {
	Name        string   // OTLP LogRecord EventName, e.g. "tailscale.network.flow"
	Severity    string   // default severity text: INFO | WARN | ERROR
	Description string   // human description
	Attributes  []string // dotted OTEL attribute keys carried on the record
	Group       string   // docs/metrics.md section heading this event belongs under
}

// PromName returns the metric's Prometheus name after OTLP→Prometheus
// normalization — the same name on BOTH the pull path (`/metrics`, the OTEL
// Prometheus exporter) and the Grafana Cloud OTLP push path (Mimir), because
// both apply prometheus/otlptranslator with the UnderscoreEscapingWithSuffixes
// strategy, which this function delegates to. See docs/metrics.md "Naming
// conventions". The rules, in the order the translator applies them:
//
//  1. the name is split into tokens of [A-Za-z0-9:]; every other rune (notably
//     the dots) is a separator, so runs of them collapse to one underscore;
//  2. a known UCUM unit contributes a token (By→bytes, s→seconds, d→days);
//     annotation units in {curly braces} contribute nothing;
//  3. a monotonic counter contributes a `total` token;
//  4. a unit-"1" GAUGE contributes a `ratio` token — so a plain integer count
//     declared as a unit-"1" gauge becomes `*_ratio`. A unit-"1" counter does
//     NOT get `ratio`, only `total`. A histogram gets neither.
//  5. TOKEN DEDUPE: a suffix token already present anywhere in the name is not
//     appended again (`tailscale2otel.objectstore.bytes` + `By` stays
//     `tailscale2otel_objectstore_bytes_total`), and a `total`/`ratio` token
//     found mid-name is MOVED to the end rather than duplicated;
//  6. a leading digit is prefixed with `_`.
//
// This returns the empty string only if normalization degenerates (empty name,
// or a name made entirely of separators) — an input the catalog never contains,
// and one the per-package catalog consistency guards would reject.
func (m Metric) PromName() string {
	name, err := promMetricNamer.Build(otlptranslator.Metric{
		Name: m.Name,
		Unit: m.Unit,
		Type: promMetricType(m.Instrument),
	})
	if err != nil {
		return ""
	}
	return name
}

// PromLabels returns the metric's attribute keys normalized to Prometheus label
// names (dots→underscores), preserving order.
func (m Metric) PromLabels() []string {
	out := make([]string, len(m.Attributes))
	for i, a := range m.Attributes {
		out[i] = strings.ReplaceAll(a, ".", "_")
	}
	return out
}

// PromLabelName returns key normalized to the Prometheus label name that Grafana
// Cloud's OTLP ingestion assigns: every rune outside [A-Za-z0-9_] (notably the
// dots in OTEL attribute keys) becomes '_', and a digit-leading result is
// prefixed with '_' (Prometheus label names cannot start with a digit). Two
// distinct OTEL attribute keys that map to the same value here fold into a single
// Prometheus label after export — a duplicate label name Mimir rejects as
// otlp_parse_error. The Emitter's collision guard uses this to detect that case.
func PromLabelName(key string) string {
	if key == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(key) + 1)
	for i, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_') // a label name may not start with a digit
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
