package metricdoc_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/metricdoc"
)

// TestPromName pins the OTLP→Prometheus name normalization against the worked
// examples in docs/metrics.md (the doc generator and any drift check rely on it).
func TestPromName(t *testing.T) {
	cases := []struct {
		desc string
		m    metricdoc.Metric
		want string
	}{
		{"counter + By", metricdoc.Metric{Name: "tailscale.network.io", Unit: "By", Instrument: metricdoc.Counter}, "tailscale_network_io_bytes_total"},
		{"gauge flag unit 1 -> ratio", metricdoc.Metric{Name: "tailscale.device.online", Unit: "1", Instrument: metricdoc.Gauge}, "tailscale_device_online_ratio"},
		{"gauge + s", metricdoc.Metric{Name: "tailscale.device.last_seen", Unit: "s", Instrument: metricdoc.Gauge}, "tailscale_device_last_seen_seconds"},
		{"gauge count unit 1 -> ratio", metricdoc.Metric{Name: "tailscale.devices.count", Unit: "1", Instrument: metricdoc.Gauge}, "tailscale_devices_count_ratio"},
		{"gauge + d", metricdoc.Metric{Name: "tailscale.setting.devices_key_duration", Unit: "d", Instrument: metricdoc.Gauge}, "tailscale_setting_devices_key_duration_days"},
		{"counter annotation unit dropped", metricdoc.Metric{Name: "tailscale.network.packets", Unit: "{packet}", Instrument: metricdoc.Counter}, "tailscale_network_packets_total"},
		{"gauge annotation unit dropped, no total", metricdoc.Metric{Name: "tailscale.device.routes.advertised", Unit: "{route}", Instrument: metricdoc.Gauge}, "tailscale_device_routes_advertised"},
		{"counter unit 1 -> no ratio, just total", metricdoc.Metric{Name: "tailscale2otel.api.requests", Unit: "1", Instrument: metricdoc.Counter}, "tailscale2otel_api_requests_total"},
	}
	for _, c := range cases {
		if got := c.m.PromName(); got != c.want {
			t.Errorf("%s: PromName() = %q, want %q", c.desc, got, c.want)
		}
	}
}

// TestPromLabels pins attribute-key normalization (dots→underscores, order kept).
func TestPromLabels(t *testing.T) {
	m := metricdoc.Metric{Attributes: []string{"network.io.direction", "tailscale.src.node", "http.response.status_code"}}
	got := strings.Join(m.PromLabels(), ",")
	want := "network_io_direction,tailscale_src_node,http_response_status_code"
	if got != want {
		t.Errorf("PromLabels() = %q, want %q", got, want)
	}
}

// TestPromLabelName pins single-key Prometheus label normalization, the rule the
// Emitter's collision guard uses to decide when two OTEL attribute keys would
// fold into one Prometheus label (and get the sample rejected as
// otlp_parse_error). It must mirror Grafana Cloud's OTLP label normalization:
// non-[A-Za-z0-9_] runes (notably dots) become '_', and a digit-leading result
// is prefixed with '_'.
func TestPromLabelName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tailscale.node", "tailscale_node"}, // dotted identity ...
		{"tailscale_node", "tailscale_node"}, // ... folds onto the scraped spelling
		{"host.name", "host_name"},
		{"network.io.direction", "network_io_direction"},
		{"instance", "instance"},
		{"already_ok", "already_ok"},
		{"with-dash", "with_dash"}, // other punctuation also sanitized
		{"with:colon", "with_colon"},
		{"9lives", "_9lives"}, // digit-leading gets a '_' prefix
		{"", ""},
	}
	for _, c := range cases {
		if got := metricdoc.PromLabelName(c.in); got != c.want {
			t.Errorf("PromLabelName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
