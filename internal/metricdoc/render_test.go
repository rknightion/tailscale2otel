package metricdoc_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/metricdoc"
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
