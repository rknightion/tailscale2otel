package metricdoc_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
)

func TestRenderMetricTable(t *testing.T) {
	got := metricdoc.RenderMetricTable([]metricdoc.Metric{
		{Name: "tailscale.network.io", Unit: "By", Instrument: metricdoc.Counter, Description: "Bytes transferred.", Attributes: []string{"network.io.direction", "tailscale.src.node"}},
		{Name: "tailscale.network.flows", Unit: "{flow}", Instrument: metricdoc.Counter, Description: "Flows.", Attributes: nil},
	})

	header := "| OTEL name | Unit | Instrument | Prometheus (normalized) name | Key attributes | Description |"
	if !strings.Contains(got, header) {
		t.Fatalf("missing header in:\n%s", got)
	}
	ioRow := "| `tailscale.network.io` | `By` | counter | `tailscale_network_io_bytes_total` | `network_io_direction`, `tailscale_src_node` | Bytes transferred. |"
	if !strings.Contains(got, ioRow) {
		t.Fatalf("missing io row.\n got:\n%s\nwant row:\n%s", got, ioRow)
	}
	flowsRow := "| `tailscale.network.flows` | `{flow}` | counter | `tailscale_network_flows_total` | — | Flows. |"
	if !strings.Contains(got, flowsRow) {
		t.Fatalf("missing flows row (no-attr => em dash).\n got:\n%s", got)
	}
}

// TestRenderEscapesPipesInCells guards against a stray `|` in any free-text cell
// injecting an extra Markdown table column (a doc generator must produce valid
// tables for arbitrary descriptors).
func TestRenderEscapesPipesInCells(t *testing.T) {
	mt := metricdoc.RenderMetricTable([]metricdoc.Metric{
		{Name: "x.y", Unit: "1", Instrument: metricdoc.Gauge, Description: "a | b"},
	})
	if strings.Contains(mt, "a | b") || !strings.Contains(mt, `a \| b`) {
		t.Errorf("metric description pipe not escaped:\n%s", mt)
	}

	lt := metricdoc.RenderLogTable([]metricdoc.LogEvent{
		{Name: "e.v", Severity: "INFO | WARN", Description: "d"},
	})
	if strings.Contains(lt, "INFO | WARN") || !strings.Contains(lt, `INFO \| WARN`) {
		t.Errorf("log severity pipe not escaped:\n%s", lt)
	}
}
