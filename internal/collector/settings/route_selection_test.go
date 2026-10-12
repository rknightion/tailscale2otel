package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

func TestRouteSelectionSignals(t *testing.T) {
	tests := []struct {
		name, wire, mode string
		regional         *float64
	}{
		{"magicroute", `{"routeSelection":"magicroute","regionalRoutingOn":true}`, "magicroute", routeValue(0)},
		{"regional", `{"routeSelection":"regional-routing","regionalRoutingOn":false}`, "regional-routing", routeValue(1)},
		{"regional failover", `{"routeSelection":"regional-routing-failover","regionalRoutingOn":false}`, "regional-routing-failover", routeValue(1)},
		{"active passive", `{"routeSelection":"active-passive-failover","regionalRoutingOn":true}`, "active-passive-failover", routeValue(0)},
		{"future", `{"routeSelection":"future-mode","regionalRoutingOn":true}`, "unknown", nil},
		{"empty present", `{"routeSelection":"","regionalRoutingOn":true}`, "unknown", nil},
		{"legacy true", `{"regionalRoutingOn":true}`, "", routeValue(1)},
		{"legacy false", `{"regionalRoutingOn":false}`, "", routeValue(0)},
		{"legacy null", `{"regionalRoutingOn":null}`, "", nil},
		{"mode null legacy true", `{"routeSelection":null,"regionalRoutingOn":true}`, "", routeValue(1)},
		{"both null", `{"routeSelection":null,"regionalRoutingOn":null}`, "", nil},
		{"mode without legacy", `{"routeSelection":"regional-routing-failover"}`, "regional-routing-failover", routeValue(1)},
		{"mode with legacy null", `{"routeSelection":"magicroute","regionalRoutingOn":null}`, "magicroute", routeValue(0)},
		{"absent", `{}`, "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/tailnet/example.com/settings" {
					http.Error(w, "unexpected endpoint", http.StatusNotFound)
					return
				}
				_, _ = w.Write([]byte(tc.wire))
			}))
			defer srv.Close()
			client, err := tsapi.NewClient(tsapi.Options{
				Tailnet: "example.com", BaseURL: srv.URL, APIKey: "testkey",
				MaxAttempts: 1, Timeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			rec := telemetrytest.New()
			if err := New(client, 0).Collect(context.Background(), rec.Emitter()); err != nil {
				t.Fatal(err)
			}
			modes := rec.MetricPoints("tailscale.setting.route_selection")
			if tc.mode == "" {
				if len(modes) != 0 {
					t.Fatalf("unexpected mode points: %+v", modes)
				}
			} else {
				if len(modes) != 1 {
					t.Fatalf("mode points = %d, want 1", len(modes))
				}
				if modes[0].Value != 1 || modes[0].Attrs["tailscale.setting.route_selection"] != tc.mode {
					t.Fatalf("mode = %+v, want %q info gauge", modes[0], tc.mode)
				}
			}
			found := false
			for _, p := range rec.MetricPoints("tailscale.setting.enabled") {
				if p.Attrs["tailscale.setting.name"] == "regional_routing" {
					found = true
					if tc.regional == nil {
						t.Fatalf("unexpected regional point: %+v", p)
					}
					if p.Value != *tc.regional {
						t.Fatalf("regional = %v, want %v", p.Value, *tc.regional)
					}
				}
			}
			if !found && tc.regional != nil {
				t.Fatal("missing regional point")
			}
		})
	}
}

func routeValue(v float64) *float64 { return &v }

func TestRouteSelectionSnapshotTransitions(t *testing.T) {
	api := &fakeAPI{}
	rec := telemetrytest.New()
	clock := time.Unix(1_000_000, 0).UTC()
	c := New(api, 0, WithSnapshot(true), WithClock(func() time.Time { return clock }))
	tests := []struct {
		wire     string
		mode     any
		regional any
	}{
		{`{}`, nil, nil},
		{`{"routeSelection":"magicroute","regionalRoutingOn":true}`, "magicroute", true},
		{`{"routeSelection":"regional-routing","regionalRoutingOn":false}`, "regional-routing", false},
		{`{"routeSelection":"regional-routing-failover","regionalRoutingOn":null}`, "regional-routing-failover", nil},
		{`{"routeSelection":"active-passive-failover"}`, "active-passive-failover", nil},
		{`{"routeSelection":"future-mode","regionalRoutingOn":true}`, "unknown", true},
		{`{"routeSelection":null,"regionalRoutingOn":false}`, nil, false},
		{`{"regionalRoutingOn":null}`, nil, nil},
	}
	for i, tc := range tests {
		var s tsapi.TailnetSettings
		if err := json.Unmarshal([]byte(tc.wire), &s); err != nil {
			t.Fatal(err)
		}
		api.settings = &s
		clock = clock.Add(time.Minute)
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		logs := rec.LogRecords()
		if len(logs) != i+1 {
			t.Fatalf("transition %d: logs = %d, want %d", i, len(logs), i+1)
		}
		log := logs[i]
		if log.EventName != EventSettingsSnapshot || log.Attrs["tailscale.snapshot.reason"] != "change" {
			t.Fatalf("transition %d: unexpected snapshot event/reason: %+v", i, log)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(log.Body), &body); err != nil {
			t.Fatal(err)
		}
		mode, present := body["routeSelection"]
		if mode != tc.mode || present != (tc.mode != nil) {
			t.Fatalf("transition %d: snapshot mode = %v (present %v), want %v", i, mode, present, tc.mode)
		}
		regional, present := body["regionalRoutingOn"]
		if !present || regional != tc.regional {
			t.Fatalf("transition %d: snapshot legacy = %v (present %v), want %v", i, regional, present, tc.regional)
		}
		// An unchanged poll must not emit a second snapshot before its heartbeat.
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatal(err)
		}
		if got := len(rec.LogRecords()); got != i+1 {
			t.Fatalf("transition %d: unchanged poll emitted snapshot; logs = %d", i, got)
		}
	}
}
