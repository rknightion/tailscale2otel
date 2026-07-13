package catalog

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
)

// synthetic catalog used to exercise the marker engine in isolation.
func synthMetrics() []metricdoc.Metric {
	return []metricdoc.Metric{
		{Name: "a.two", Unit: "By", Instrument: metricdoc.Counter, Description: "Two.", Group: "Alpha"},
		{Name: "a.one", Unit: "1", Instrument: metricdoc.Gauge, Description: "One.", Group: "Alpha"},
		{Name: "b.metric", Unit: "s", Instrument: metricdoc.Gauge, Description: "Bee.", Group: "Beta"},
	}
}

func synthLogs() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{
		{Name: "evt.two", Severity: "WARN", Description: "Two.", Group: "Beta"},
		{Name: "evt.one", Severity: "INFO", Description: "One.", Group: "Alpha"},
	}
}

const fixtureDoc = `# Title

Intro prose.

## Alpha

Alpha prose.

<!-- BEGIN GENERATED: metrics groups="Alpha" -->
stale
<!-- END GENERATED -->

> a gating note that must be preserved

## Beta

<!-- BEGIN GENERATED: metrics groups="Beta" -->
<!-- END GENERATED -->

## Log events

<!-- BEGIN GENERATED: logs -->
old
<!-- END GENERATED -->

Trailing prose.
`

func TestRender_FillsAndPreservesProse(t *testing.T) {
	out, err := render(fixtureDoc, synthMetrics(), synthLogs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Prose outside markers preserved.
	for _, prose := range []string{"# Title", "Intro prose.", "Alpha prose.", "> a gating note that must be preserved", "Trailing prose."} {
		if !strings.Contains(out, prose) {
			t.Errorf("prose %q not preserved", prose)
		}
	}
	// Markers preserved.
	if strings.Count(out, beginPrefix) != 3 || strings.Count(out, endMarker) != 3 {
		t.Errorf("markers not preserved: %d begins, %d ends", strings.Count(out, beginPrefix), strings.Count(out, endMarker))
	}
	// Stale body replaced.
	if strings.Contains(out, "stale") || strings.Contains(out, "old") {
		t.Errorf("stale generated body not replaced:\n%s", out)
	}
	// Alpha metrics sorted by name (a.one before a.two) and present; Beta absent from Alpha region.
	ai := strings.Index(out, "`a.one`")
	aii := strings.Index(out, "`a.two`")
	if ai < 0 || aii < 0 || ai > aii {
		t.Errorf("Alpha metrics missing or unsorted (a.one idx=%d a.two idx=%d)", ai, aii)
	}
	if !strings.Contains(out, "`b.metric`") {
		t.Error("Beta metric not rendered")
	}
	// Log table rendered, sorted (evt.one before evt.two).
	li := strings.Index(out, "`evt.one`")
	lii := strings.Index(out, "`evt.two`")
	if li < 0 || lii < 0 || li > lii {
		t.Errorf("log events missing or unsorted (evt.one idx=%d evt.two idx=%d)", li, lii)
	}

	// Idempotent: rendering the output again yields the same output.
	out2, err := render(out, synthMetrics(), synthLogs())
	if err != nil {
		t.Fatalf("render (2nd pass): %v", err)
	}
	if out2 != out {
		t.Errorf("render is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out, out2)
	}
}

func TestRender_ErrorOnUncoveredGroup(t *testing.T) {
	// fixtureDoc covers Alpha+Beta; drop Beta's region by using a doc with only Alpha.
	doc := "## Alpha\n<!-- BEGIN GENERATED: metrics groups=\"Alpha\" -->\n<!-- END GENERATED -->\n## Logs\n<!-- BEGIN GENERATED: logs -->\n<!-- END GENERATED -->\n"
	_, err := render(doc, synthMetrics(), synthLogs())
	if err == nil || !strings.Contains(err.Error(), "b.metric") {
		t.Fatalf("want error naming the uncovered metric, got %v", err)
	}
}

func TestRender_ErrorOnUnknownGroupRegion(t *testing.T) {
	doc := "<!-- BEGIN GENERATED: metrics groups=\"Alpha\" -->\n<!-- END GENERATED -->\n<!-- BEGIN GENERATED: metrics groups=\"Beta\" -->\n<!-- END GENERATED -->\n<!-- BEGIN GENERATED: metrics groups=\"Ghost\" -->\n<!-- END GENERATED -->\n<!-- BEGIN GENERATED: logs -->\n<!-- END GENERATED -->\n"
	_, err := render(doc, synthMetrics(), synthLogs())
	if err == nil || !strings.Contains(err.Error(), "Ghost") {
		t.Fatalf("want error for region with no metrics, got %v", err)
	}
}

func TestRender_ErrorOnDuplicateGroupRegion(t *testing.T) {
	doc := "<!-- BEGIN GENERATED: metrics groups=\"Alpha\" -->\n<!-- END GENERATED -->\n<!-- BEGIN GENERATED: metrics groups=\"Alpha\" -->\n<!-- END GENERATED -->\n<!-- BEGIN GENERATED: metrics groups=\"Beta\" -->\n<!-- END GENERATED -->\n<!-- BEGIN GENERATED: logs -->\n<!-- END GENERATED -->\n"
	_, err := render(doc, synthMetrics(), synthLogs())
	if err == nil || !strings.Contains(err.Error(), "more than one region") {
		t.Fatalf("want duplicate-group error, got %v", err)
	}
}

func TestRender_ErrorOnUnbalancedMarkers(t *testing.T) {
	doc := "<!-- BEGIN GENERATED: metrics groups=\"Alpha\" -->\nno end here\n"
	_, err := render(doc, synthMetrics(), synthLogs())
	if err == nil || !strings.Contains(err.Error(), "malformed generated markers") {
		t.Fatalf("want malformed-markers error, got %v", err)
	}
}

func TestRender_ErrorOnMissingLogsRegion(t *testing.T) {
	doc := "<!-- BEGIN GENERATED: metrics groups=\"Alpha\" -->\n<!-- END GENERATED -->\n<!-- BEGIN GENERATED: metrics groups=\"Beta\" -->\n<!-- END GENERATED -->\n"
	_, err := render(doc, synthMetrics(), synthLogs())
	if err == nil || !strings.Contains(err.Error(), "no `logs` generated region") {
		t.Fatalf("want missing-logs-region error, got %v", err)
	}
}
