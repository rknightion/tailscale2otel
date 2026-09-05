package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v5/internal/b0api"
	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/provider"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestPAMAPIOptionsCarryFrozenConnectionSeam(t *testing.T) {
	cfg := config.Default()
	cfg.PAM.APIURL = "https://pam.example.invalid/api/v1"
	cfg.PAM.Token = "static-token"

	got := pamAPIOptions(cfg, "v5.0.0")
	if got.BaseURL != cfg.PAM.APIURL || got.Token != "static-token" {
		t.Fatalf("PAM options = BaseURL %q Token %q", got.BaseURL, got.Token)
	}
	if got.UserAgent != "tailscale2otel/v5.0.0" {
		t.Fatalf("PAM User-Agent = %q", got.UserAgent)
	}
}

func TestPAMCollectorsRegisterOnConfiguredRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, configuredTailnet, wantTailnet string
	}{
		{name: "empty selects primary", wantTailnet: "alpha.example.invalid"},
		{name: "explicit selects matching runtime", configuredTailnet: "beta.example.invalid", wantTailnet: "beta.example.invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _ := pamTwoTailnetApp(t, tc.configuredTailnet)
			assertPAMRegistration(t, a, tc.wantTailnet)
		})
	}
}

// TestPAMTelemetryUsesSelectedRuntime drives the actual registered collectors
// through a Recorder. It catches the false-pass where PAM is registered on both
// runtimes: that would otherwise produce plausible per-tailnet series twice.
func TestPAMTelemetryUsesSelectedRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, configuredTailnet, wantTailnet string
	}{
		{name: "empty selects primary", wantTailnet: "alpha.example.invalid"},
		{name: "explicit selects matching runtime", configuredTailnet: "beta.example.invalid", wantTailnet: "beta.example.invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, recAlpha, recBeta := pamTwoTailnetApp(t, tc.configuredTailnet)
			collectRegisteredPAM(t, a)

			selected, unselected := recAlpha, recBeta
			if tc.wantTailnet == "beta.example.invalid" {
				selected, unselected = recBeta, recAlpha
			}
			assertPAMRecorderAttribution(t, selected, tc.wantTailnet)
			assertNoPAMTelemetry(t, unselected)
		})
	}
}

func pamTwoTailnetApp(t *testing.T, pamTailnet string) (*App, *telemetrytest.Recorder, *telemetrytest.Recorder) {
	t.Helper()
	srv := pamFixtureServer(t)
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.Collectors.PAM.Enabled = true
	cfg.Collectors.PAM.SnapshotEnabled = true // existing PAM log proves log attribution independently of TSO-0137.
	cfg.PAM.Tailnet = pamTailnet
	cfg.PAM.Token = "static-token"
	cfg.PAM.APIURL = srv.URL
	client, err := b0api.NewClient(pamAPIOptions(cfg, "vtest"))
	if err != nil {
		t.Fatal(err)
	}

	recAlpha, recBeta := telemetrytest.New(), telemetrytest.New()
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(),
		tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.pamClient = client
	a.buildProcessDeps()
	a.addRuntime("alpha.example.invalid", tailnetRecorderEmitter{Emitter: recAlpha.Emitter(), tailnet: "alpha.example.invalid"}, nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	a.addRuntime("beta.example.invalid", tailnetRecorderEmitter{Emitter: recBeta.Emitter(), tailnet: "beta.example.invalid"}, nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	return a, recAlpha, recBeta
}

func assertPAMRegistration(t *testing.T, a *App, wantTailnet string) {
	t.Helper()
	for _, rt := range a.runtimes {
		want := 0
		if rt.configuredName == wantTailnet {
			want = 1
		}
		var inventory, sessions int
		for _, entry := range rt.registry.Entries() {
			switch entry.Collector.Name() {
			case "pam":
				inventory++
			case "pam_sessions":
				sessions++
			}
		}
		if inventory != want || sessions != want {
			t.Errorf("tailnet %q PAM collectors = inventory %d sessions %d, want %d each", rt.configuredName, inventory, sessions, want)
		}
	}
}

func collectRegisteredPAM(t *testing.T, a *App) {
	t.Helper()
	for _, rt := range a.runtimes {
		for _, entry := range rt.registry.Entries() {
			if entry.Collector.Name() != "pam" && entry.Collector.Name() != "pam_sessions" {
				continue
			}
			snapshot, ok := entry.Collector.(collector.SnapshotCollector)
			if !ok {
				t.Fatalf("PAM collector %q is not a SnapshotCollector", entry.Collector.Name())
			}
			if err := snapshot.Collect(context.Background(), rt.emitter); err != nil {
				t.Fatalf("collect PAM %q on %q: %v", entry.Collector.Name(), rt.configuredName, err)
			}
		}
	}
}

func assertPAMRecorderAttribution(t *testing.T, rec *telemetrytest.Recorder, wantTailnet string) {
	t.Helper()
	points := 0
	series := make(map[string]struct{})
	for _, name := range rec.MetricNames() {
		if !strings.HasPrefix(name, "tailscale.pam.") {
			continue
		}
		for _, point := range rec.MetricPoints(name) {
			points++
			if got := point.Attrs["tailscale.tailnet"]; got != wantTailnet {
				t.Errorf("metric %q tailnet = %q, want %q", name, got, wantTailnet)
			}
			key := name + "\x00" + sortedAttrs(point.Attrs)
			if _, exists := series[key]; exists {
				t.Errorf("duplicate PAM metric series %q", key)
			}
			series[key] = struct{}{}
		}
	}
	if points == 0 {
		t.Fatal("no PAM metric points recorded")
	}

	logs := 0
	for _, record := range rec.LogRecords() {
		if !strings.HasPrefix(record.EventName, "tailscale.pam.") {
			continue
		}
		logs++
		if got := record.Attrs["tailscale.tailnet"]; got != wantTailnet {
			t.Errorf("PAM log %q tailnet = %q, want %q", record.EventName, got, wantTailnet)
		}
	}
	if logs == 0 {
		t.Fatal("no PAM log records recorded")
	}
	t.Logf("PAM telemetry on %q: %d metric series and %d log records", wantTailnet, len(series), logs)
}

func assertNoPAMTelemetry(t *testing.T, rec *telemetrytest.Recorder) {
	t.Helper()
	for _, name := range rec.MetricNames() {
		if strings.HasPrefix(name, "tailscale.pam.") {
			t.Errorf("unselected runtime recorded PAM metric %q", name)
		}
	}
	for _, record := range rec.LogRecords() {
		if strings.HasPrefix(record.EventName, "tailscale.pam.") {
			t.Errorf("unselected runtime recorded PAM log %q", record.EventName)
		}
	}
}

func sortedAttrs(attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+attrs[key])
	}
	return strings.Join(parts, "\x00")
}

// tailnetRecorderEmitter models the per-tailnet provider's signal-scoped
// attribute while retaining telemetrytest.Recorder's in-memory observations.
type tailnetRecorderEmitter struct {
	telemetry.Emitter
	tailnet string
}

func (e tailnetRecorderEmitter) Counter(name, unit, desc string, add float64, attrs telemetry.Attrs) {
	e.Emitter.Counter(name, unit, desc, add, e.attrs(attrs))
}

func (e tailnetRecorderEmitter) Gauge(name, unit, desc string, value float64, attrs telemetry.Attrs) {
	e.Emitter.Gauge(name, unit, desc, value, e.attrs(attrs))
}

func (e tailnetRecorderEmitter) GaugeSnapshot(name, unit, desc string, points []telemetry.GaugePoint) {
	copy := make([]telemetry.GaugePoint, len(points))
	for i := range points {
		copy[i] = telemetry.GaugePoint{Value: points[i].Value, Attrs: e.attrs(points[i].Attrs)}
	}
	e.Emitter.GaugeSnapshot(name, unit, desc, copy)
}

func (e tailnetRecorderEmitter) UpDownCounter(name, unit, desc string, value float64, attrs telemetry.Attrs) {
	e.Emitter.UpDownCounter(name, unit, desc, value, e.attrs(attrs))
}

func (e tailnetRecorderEmitter) Histogram(name, unit, desc string, value float64, bounds []float64, attrs telemetry.Attrs) {
	e.Emitter.Histogram(name, unit, desc, value, bounds, e.attrs(attrs))
}

func (e tailnetRecorderEmitter) HistogramCtx(ctx context.Context, name, unit, desc string, value float64, bounds []float64, attrs telemetry.Attrs) {
	e.Emitter.HistogramCtx(ctx, name, unit, desc, value, bounds, e.attrs(attrs))
}

func (e tailnetRecorderEmitter) LogEvent(ev telemetry.Event) {
	ev.Attrs = e.attrs(ev.Attrs)
	e.Emitter.LogEvent(ev)
}

func (e tailnetRecorderEmitter) LogEventCtx(ctx context.Context, ev telemetry.Event) {
	ev.Attrs = e.attrs(ev.Attrs)
	e.Emitter.LogEventCtx(ctx, ev)
}

func (e tailnetRecorderEmitter) attrs(attrs telemetry.Attrs) telemetry.Attrs {
	copy := make(telemetry.Attrs, len(attrs)+1)
	for key, value := range attrs {
		copy[key] = value
	}
	copy["tailscale.tailnet"] = e.tailnet
	return copy
}

func pamFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	fixtures := map[string]string{
		"/api/v1/organization":                       "pam_organization.json",
		"/api/v1/connectors":                         "pam_connectors.json",
		"/api/v1/sockets":                            "pam_sockets.json",
		"/api/v1/policies":                           "pam_policies.json",
		"/api/v1/organizations/iam/users":            "pam_iam_users.json",
		"/api/v1/organizations/iam/groups":           "pam_iam_groups.json",
		"/api/v1/organizations/iam/service_accounts": "pam_iam_service_accounts.json",
		"/api/v1/sessions":                           "pam_sessions.json",
		"/api/v1/socket/00000000-0000-4000-8000-000000000007/upstream_configurations": "pam_socket_upstream_config.json",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method must be GET", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/api/v1/socket/00000000-0000-4000-8000-000000000008/upstream_configurations" {
			_, _ = w.Write([]byte(`{"list":[]}`))
			return
		}
		fixture, ok := fixtures[r.URL.Path]
		if !ok {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, err := os.ReadFile(filepath.Join("..", "b0api", "testdata", fixture))
		if err != nil {
			http.Error(w, fmt.Sprintf("read fixture: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}
