// Package metricdoc is the single in-code source of truth for telemetry
// DOCUMENTATION metadata: each emitted metric and log event declares its name,
// unit, instrument, human description, and attribute keys here, and the emit
// sites reference those declarations so the description/unit cannot drift from
// what is documented. The catalog is rendered to docs/metrics.md by the
// generator in tools/metricscatalog (so the docs are derived from code, never
// hand-maintained), and validated against what the processors actually emit.
package metricdoc

import "strings"

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

// Metric declares one emitted metric's documentation metadata. Name/Unit/
// Description are exactly the values passed to the telemetry.Emitter at the emit
// site (reference these fields there so there is a single source of truth).
type Metric struct {
	Name        string     // dotted OTEL source name, e.g. "tailscale.network.io"
	Unit        string     // UCUM unit, e.g. "By", "s", "d", "1", "{flow}"
	Instrument  Instrument // counter | gauge | updowncounter
	Description string     // human description (also exported as OTLP metric metadata)
	Attributes  []string   // dotted OTEL attribute keys carried on the metric
	Group       string     // docs/metrics.md section heading this metric belongs under
}

// LogEvent declares one emitted log record's documentation metadata.
type LogEvent struct {
	Name        string   // OTLP LogRecord EventName, e.g. "tailscale.network.flow"
	Severity    string   // default severity text: INFO | WARN | ERROR
	Description string   // human description
	Attributes  []string // dotted OTEL attribute keys carried on the record
	Group       string   // docs/metrics.md section heading this event belongs under
}

// PromName returns the metric's Prometheus name after Grafana Cloud's
// OTLP→Prometheus normalization (see docs/metrics.md "Naming conventions"):
// dots→underscores; a known UCUM unit suffix (By→_bytes, s→_seconds, d→_days);
// a unit of "1" on a GAUGE→_ratio (annotation units in {curly braces} are
// dropped); and a monotonic counter→_total. Applied in that order.
func (m Metric) PromName() string {
	name := strings.ReplaceAll(m.Name, ".", "_")
	switch m.Unit {
	case "By":
		name += "_bytes"
	case "s":
		name += "_seconds"
	case "d":
		name += "_days"
	case "1":
		// A dimensionless "1" gets _ratio only on a gauge; on a counter it is a
		// plain count and gets no unit suffix (just _total below).
		if m.Instrument == Gauge {
			name += "_ratio"
		}
	default:
		// Annotation units like {packet}/{flow}/{route} are dropped entirely.
	}
	if m.Instrument == Counter {
		name += "_total"
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
