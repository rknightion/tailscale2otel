package settings

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// Exercise the real cumulative SDK and pull exporter across successive polls:
// a Recorder alone cannot detect synchronous gauges retaining old label sets.
func TestRouteSelectionGatherTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := telemetry.NewProvider(ctx, telemetry.Options{Protocol: "stdout", StdoutWriter: io.Discard, PrometheusEnabled: true, Provider: "tailscale", TailnetName: "example.com", MetricInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := p.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	api := &fakeAPI{}
	c := New(api, 0)
	cases := []struct {
		wire, mode string
		regional   *float64
	}{
		{`{"routeSelection":"regional-routing","regionalRoutingOn":false}`, "regional-routing", routeValue(1)},
		{`{"routeSelection":"magicroute","regionalRoutingOn":true}`, "magicroute", routeValue(0)},
		{`{"routeSelection":"future-mode","regionalRoutingOn":true}`, "unknown", nil},
		{`{}`, "", nil},
		{`{"regionalRoutingOn":true}`, "", routeValue(1)},
		{`{"regionalRoutingOn":null}`, "", nil},
		{`{"routeSelection":"active-passive-failover"}`, "active-passive-failover", routeValue(0)},
		{`{"routeSelection":null,"regionalRoutingOn":false}`, "", routeValue(0)},
		{`{}`, "", nil},
	}
	for i, tc := range cases {
		api.settings = &tsapi.TailnetSettings{}
		if err := json.Unmarshal([]byte(tc.wire), api.settings); err != nil {
			t.Fatal(err)
		}
		if err := c.Collect(ctx, p.Emitter()); err != nil {
			t.Fatal(err)
		}
		// Repeated scrapes must retain only the CURRENT snapshot, not consume it.
		for scrape := range 2 {
			families, err := p.PromGatherer().Gather()
			if err != nil {
				t.Fatal(err)
			}
			modes, regional := 0, 0
			for _, family := range families {
				for _, point := range family.Metric {
					switch family.GetName() {
					case "tailscale_setting_route_selection_ratio":
						modes++
						mode := ""
						for _, label := range point.Label {
							if label.GetName() == "tailscale_setting_route_selection" {
								mode = label.GetValue()
							}
						}
						if mode != tc.mode || tc.mode == "" || point.GetGauge().GetValue() != 1 {
							t.Errorf("tick %d scrape %d: mode %q value %v, want %q=1", i, scrape, mode, point.GetGauge().GetValue(), tc.mode)
						}
					case "tailscale_setting_enabled_ratio":
						for _, label := range point.Label {
							if label.GetName() == "tailscale_setting_name" && label.GetValue() == "regional_routing" {
								regional++
								if tc.regional == nil {
									t.Errorf("tick %d scrape %d: obsolete regional point", i, scrape)
								} else if point.GetGauge().GetValue() != *tc.regional {
									t.Errorf("tick %d: regional = %v, want %v", i, point.GetGauge().GetValue(), *tc.regional)
								}
							}
						}
					}
				}
			}
			wantModes, wantRegional := 0, 0
			if tc.mode != "" {
				wantModes = 1
			}
			if tc.regional != nil {
				wantRegional = 1
			}
			if modes != wantModes || regional != wantRegional {
				t.Errorf("tick %d scrape %d: mode/regional counts = %d/%d, want %d/%d", i, scrape, modes, regional, wantModes, wantRegional)
			}
		}
	}
}
