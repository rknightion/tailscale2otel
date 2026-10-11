package nodemetrics_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// The DERP rollup panels are device-collector signals, not tailscaled samples.
// Exercise that emitter too, through the same real per-tailnet provider.
type dashboardDevices struct{}

func (dashboardDevices) DevicesRich(context.Context) ([]tsapi.RichDevice, error) {
	return []tsapi.RichDevice{{ID: "synthetic-node", Hostname: "node-a", DERPLatency: map[string]tsapi.DERPRegion{"region-a": {Preferred: true, LatencyMs: 12}}}}, nil
}
func (dashboardDevices) DevicePostureAttributes(context.Context, string) (tsapi.DeviceAttributes, error) {
	return tsapi.DeviceAttributes{}, nil
}
func (dashboardDevices) DeviceInvites(context.Context, string) ([]tsapi.DeviceInvite, error) {
	return nil, nil
}

// TestDashboardMetricDeploymentLabels observes the normalized Prometheus surface
// queried by Grafana, rather than assuming resource attributes become labels.
func TestDashboardMetricDeploymentLabels(t *testing.T) {
	data, err := os.ReadFile("testdata/dashboard_metrics.json")
	if err != nil {
		t.Fatal(err)
	}
	var queried []string
	if err := json.Unmarshal(data, &queried); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"tailscale", "headscale"} {
		t.Run(backend, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			provider, err := telemetry.NewProvider(ctx, telemetry.Options{Protocol: "stdout", StdoutWriter: io.Discard, PrometheusEnabled: true, Provider: backend, TailnetName: "synthetic-a", MetricInterval: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := provider.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			}()
			extra := "# TYPE tailscaled_advertised_routes gauge\ntailscaled_advertised_routes 1\n# TYPE tailscaled_approved_routes gauge\ntailscaled_approved_routes 1\n"
			body := curatedFixtureScrape1 + serviceFixture(t) + extra
			srv := serveText(&body)
			defer srv.Close()
			c := nodemetrics.New(nodemetrics.Options{Discoverer: &fakeDiscoverer{targets: []nodemetrics.Target{{URL: srv.URL, Instance: "node-a"}}}})
			if err := c.Collect(ctx, provider.Emitter()); err != nil {
				t.Fatal(err)
			}
			serviceSecond := strings.NewReplacer(" 100\n", " 110\n", " 200\n", " 220\n", " 300\n", " 330\n", " 400\n", " 440\n").Replace(serviceFixture(t))
			body = curatedFixtureScrape2 + serviceSecond + extra
			if err := c.Collect(ctx, provider.Emitter()); err != nil {
				t.Fatal(err)
			}
			if err := devices.New(dashboardDevices{}, enrich.NewDeviceCache(), 0, false, false).Collect(ctx, provider.Emitter()); err != nil {
				t.Fatal(err)
			}
			families, err := provider.PromGatherer().Gather()
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, family := range families {
				for _, name := range queried {
					if family.GetName() != name {
						continue
					}
					if len(family.Metric) == 0 {
						t.Errorf("%s has no samples", name)
					}
					for _, point := range family.Metric {
						attrs := map[string]string{}
						for _, label := range point.Label {
							attrs[label.GetName()] = label.GetValue()
						}
						if attrs["tailscale_tailnet"] != "synthetic-a" || attrs["tailscale2otel_provider"] != backend {
							t.Errorf("%s labels = %v", name, attrs)
						}
					}
					seen[name] = true
				}
			}
			for _, name := range queried {
				if !seen[name] {
					t.Errorf("queried metric %s was not emitted", name)
				}
			}
		})
	}
}
