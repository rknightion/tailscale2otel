package catalog

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rknightion/tailscale2otel/internal/metricdoc"
)

// Generated regions in docs/metrics.md are delimited by HTML comment markers:
//
//	<!-- BEGIN GENERATED: metrics groups="Devices" -->
//	| ...generated metric table... |
//	<!-- END GENERATED -->
//
// and exactly one log-events region:
//
//	<!-- BEGIN GENERATED: logs -->
//	| ...generated log table... |
//	<!-- END GENERATED -->
//
// A metrics region renders the metrics of one or more comma-separated groups
// (sorted by OTEL name); the logs region renders every log event (sorted by
// name). Everything OUTSIDE the markers — section headings, prose, the
// normalization rules, gating notes, query examples — is preserved verbatim.
const (
	beginPrefix = "<!-- BEGIN GENERATED:"
	endMarker   = "<!-- END GENERATED -->"
)

// regionRE matches one generated region, capturing the spec (group 1) and the
// existing body (group 2). (?s) lets the body span lines; the lazy body runs up
// to the FIRST end marker so each begin pairs with its own end, and an empty
// body (begin immediately followed by end on the next line) still matches.
var regionRE = regexp.MustCompile(`(?s)<!-- BEGIN GENERATED: (.*?) -->\n(.*?)<!-- END GENERATED -->`)

var groupsAttrRE = regexp.MustCompile(`groups="([^"]*)"`)

// Render fills every generated table in doc from the live, code-derived catalog
// and returns the new document. It is the rendering half of the doc generator;
// the tools/metricscatalog binary compares (or writes) the result. Prose outside
// the markers is preserved exactly. An error is returned if the markers are
// malformed or if any declared metric/log event would not be rendered (so a new
// metric cannot be silently left undocumented).
func Render(doc string) (string, error) {
	return render(doc, Metrics(), LogEvents())
}

// render is the dependency-injected core of Render, taking the catalog explicitly
// so it can be unit-tested against a synthetic catalog.
func render(doc string, metrics []metricdoc.Metric, logs []metricdoc.LogEvent) (string, error) {
	nBegin := strings.Count(doc, beginPrefix)
	nEnd := strings.Count(doc, endMarker)
	matches := regionRE.FindAllStringSubmatchIndex(doc, -1)
	if nBegin != nEnd || len(matches) != nBegin {
		return "", fmt.Errorf("malformed generated markers in docs: %d %q, %d %q, %d well-formed regions",
			nBegin, beginPrefix, nEnd, endMarker, len(matches))
	}

	byGroup := map[string][]metricdoc.Metric{}
	for _, m := range metrics {
		byGroup[m.Group] = append(byGroup[m.Group], m)
	}

	covered := map[string]string{} // group -> the region spec that renders it
	logRegions := 0

	var b strings.Builder
	last := 0
	for _, idx := range matches {
		specS, specE := idx[2], idx[3]
		bodyS, bodyE := idx[4], idx[5]
		spec := doc[specS:specE]

		kind, groups, err := parseSpec(spec)
		if err != nil {
			return "", err
		}

		var table string
		switch kind {
		case "metrics":
			var sel []metricdoc.Metric
			for _, g := range groups {
				ms := byGroup[g]
				if len(ms) == 0 {
					return "", fmt.Errorf("generated region requests group %q but no metric declares that group", g)
				}
				if prev, ok := covered[g]; ok {
					return "", fmt.Errorf("group %q is rendered by more than one region (%q and %q)", g, prev, spec)
				}
				covered[g] = spec
				sel = append(sel, ms...)
			}
			sort.Slice(sel, func(i, j int) bool { return sel[i].Name < sel[j].Name })
			table = metricdoc.RenderMetricTable(sel)
		case "logs":
			logRegions++
			sorted := append([]metricdoc.LogEvent(nil), logs...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
			table = metricdoc.RenderLogTable(sorted)
		}

		// Everything up to and including the BEGIN line's trailing newline, then
		// the freshly rendered table on its own lines, then resume at the END
		// marker (preserved). TrimRight + explicit "\n" keeps exactly one blank-free
		// line before the end marker, which makes Render idempotent.
		b.WriteString(doc[last:bodyS])
		b.WriteString(strings.TrimRight(table, "\n"))
		b.WriteString("\n")
		last = bodyE
	}
	b.WriteString(doc[last:])

	// Completeness: every declared metric's group must be rendered somewhere, so a
	// newly-added metric forces a docs update rather than being silently dropped.
	for _, m := range metrics {
		if _, ok := covered[m.Group]; !ok {
			return "", fmt.Errorf("metric %q (group %q) is not rendered by any generated region in docs/metrics.md (add a `metrics groups=%q` region)", m.Name, m.Group, m.Group)
		}
	}
	if len(logs) > 0 && logRegions == 0 {
		return "", fmt.Errorf("there are %d declared log events but no `logs` generated region in docs/metrics.md", len(logs))
	}
	if logRegions > 1 {
		return "", fmt.Errorf("more than one `logs` generated region in docs/metrics.md (found %d)", logRegions)
	}

	return b.String(), nil
}

// parseSpec parses a region spec such as `metrics groups="Settings,ACL,DNS"` or
// `logs` into its kind and (for metrics) the list of groups.
func parseSpec(spec string) (kind string, groups []string, err error) {
	spec = strings.TrimSpace(spec)
	fields := strings.Fields(spec)
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("empty generated-region spec")
	}
	kind = fields[0]
	switch kind {
	case "metrics":
		m := groupsAttrRE.FindStringSubmatch(spec)
		if m == nil {
			return "", nil, fmt.Errorf("metrics region missing groups=\"...\": %q", spec)
		}
		for _, part := range strings.Split(m[1], ",") {
			if part = strings.TrimSpace(part); part != "" {
				groups = append(groups, part)
			}
		}
		if len(groups) == 0 {
			return "", nil, fmt.Errorf("metrics region has empty groups list: %q", spec)
		}
	case "logs":
		// renders every log event; no group filter
	default:
		return "", nil, fmt.Errorf("unknown generated-region kind %q (want \"metrics\" or \"logs\")", kind)
	}
	return kind, groups, nil
}
