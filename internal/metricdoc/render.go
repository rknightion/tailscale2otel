package metricdoc

import (
	"fmt"
	"strings"
)

// RenderMetricTable renders metrics as the Markdown table used in docs/metrics.md
// (columns: OTEL name, Unit, Instrument, normalized Prometheus name, key
// attributes, description). It is the rendering half of the doc generator.
func RenderMetricTable(metrics []Metric) string {
	var b strings.Builder
	b.WriteString("| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, m := range metrics {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | `%s` | %s | %s |\n",
			m.Name, m.Unit, m.Instrument, m.PromName(), renderLabels(m.PromLabels()), cell(m.Description))
	}
	return b.String()
}

// RenderLogTable renders log events as a Markdown table (columns: event name,
// default severity, key attributes, description).
func RenderLogTable(events []LogEvent) string {
	var b strings.Builder
	b.WriteString("| Event name | Severity | Key attributes | Description |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, e := range events {
		labels := make([]string, len(e.Attributes))
		for i, a := range e.Attributes {
			labels[i] = strings.ReplaceAll(a, ".", "_")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", e.Name, e.Severity, renderLabels(labels), cell(e.Description))
	}
	return b.String()
}

// renderLabels backtick-joins normalized label names, or returns an em dash when
// there are none.
func renderLabels(labels []string) string {
	if len(labels) == 0 {
		return "—"
	}
	parts := make([]string, len(labels))
	for i, l := range labels {
		parts[i] = "`" + l + "`"
	}
	return strings.Join(parts, ", ")
}

// cell makes free text safe for a Markdown table cell (escape pipes, flatten
// newlines).
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
