package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/collector/nodemetrics"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// adminLoopbackHost is the Host header a tokenless admin request must carry:
// with no admin.auth.token the gate only serves requests addressed to loopback,
// and httptest.NewRequest defaults the Host to "example.com".
const adminLoopbackHost = "127.0.0.1:9091"

func TestStatusPage_HTMLRenders(t *testing.T) {
	cfg := config.Default()             // landing_page defaults to true
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"<!DOCTYPE html>", serviceName, "vtest", "Collectors", "Metrics catalog",
		`id="healthBadge"`, // at-a-glance health verdict in the header
		`id="collBody"`,    // collectors table body that the poller live-rebuilds
		`id="apiBody"`,     // API-health section table body
		`data-tab="api"`,   // the API tab
		`id="staleBanner"`, // freshness indicator shown on poll failure
		"drawSpark",        // inline-SVG sparkline renderer
		"drawChart",        // full zero-dependency line chart
	} {
		if !strings.Contains(body, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func TestStatusPage_UnknownPath404(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /nope = %d, want 404 (only / renders the page)", w.Code)
	}
}

func TestStatusJSON_Shape(t *testing.T) {
	cfg := config.Default()
	cfg.OTLP.MetricExportBatchSize = 4321
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	a.checkpointEffective = "memory"
	a.checkpointReason = "checkpoint path is not writable"
	a.evidenceEffective = "file"
	a.evidencePath = "/state/checkpoints.json"
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/status.json = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got statusdata.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status json: %v", err)
	}
	if got.Service.Name != serviceName || got.Service.Version != "vtest" {
		t.Errorf("service = %+v, want name=%s version=vtest", got.Service, serviceName)
	}
	if got.Telemetry.MetricExportBatchSize != 4321 {
		t.Errorf("metric export batch size = %d, want 4321", got.Telemetry.MetricExportBatchSize)
	}
	if len(got.DurableState.Stores) != 2 || !got.DurableState.Degraded {
		t.Errorf("durable_state = %+v, want two stores with cursor degradation", got.DurableState)
	}
	if len(got.Collectors) != len(a.runtimes[0].registry.Entries()) {
		t.Errorf("collectors = %d, want %d (one per registered collector)", len(got.Collectors), len(a.runtimes[0].registry.Entries()))
	}
	if len(got.Metrics) == 0 {
		t.Errorf("metrics catalog is empty")
	}
	// Self-obs is off in the default config, so live cardinality is unavailable.
	if got.Cardinality.Available {
		t.Errorf("cardinality should be unavailable when self-observability is off")
	}
	// buildStatus must always set a valid health verdict (deriveHealth's logic is
	// covered exhaustively in health_test.go).
	switch got.Health {
	case healthHealthy, healthDegraded, healthStarting:
	default:
		t.Errorf("health = %q, want one of healthy/degraded/starting", got.Health)
	}
}

// TestStatusJSON_UpdateInfoIsWired asserts /api/status.json actually carries
// the update-check state built from the REAL composition root
// (buildProcessDeps -> a.selfRelease), not a value hand-assigned in the test.
// version_checks.self.enabled defaults to true (config.Default()), so
// baseTestApp's newApp -> buildProcessDeps constructs a real *release.Fetcher;
// it has never run a fetch in this test (no goroutine started), so the
// trustworthy end-to-end assertion is State=="checking" with Enabled==true —
// anything else means the field isn't actually connected to a.selfRelease.
func TestStatusJSON_UpdateInfoIsWired(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	if a.selfRelease == nil {
		t.Fatal("test premise broken: version_checks.self.enabled should default true")
	}
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = adminLoopbackHost
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	var got statusdata.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status json: %v", err)
	}
	if !got.Update.Enabled {
		t.Errorf("update.enabled = false, want true (version_checks.self.enabled defaults true)")
	}
	if got.Update.State != "checking" {
		t.Errorf("update.state = %q, want %q (never fetched yet in this test)", got.Update.State, "checking")
	}
	if got.Update.ReleaseURL == "" {
		t.Errorf("update.release_url is empty, want the static release link")
	}
}

// TestStatusJSON_ThroughputAndFleetFields asserts the throughput and
// collector-fleet sections are present in the served JSON under their exact
// field names — the page's chart registrations read these keys, so a rename
// silently blanks the charts.
func TestStatusJSON_ThroughputAndFleetFields(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	// One sample so the trend series carry data rather than a bare null.
	a.runtimeHist.sample(time.Now(), samplerTick{
		emit:  telemetry.EmitStats{MetricPoints: 3, LogRecords: 1},
		fleet: fleetStats{active: 1, failing: 0, meanDurationMs: 12},
	})
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/status.json = %d, want 200", w.Code)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode status json: %v", err)
	}
	for section, fields := range map[string][]string{
		"throughput": {
			"metric_points", "log_records",
			"metric_points_per_sec", "log_records_per_sec",
			"metric_points_per_sec_series", "log_records_per_sec_series",
		},
		"fleet": {
			"active", "failing", "mean_duration_ms",
			"failing_series", "mean_duration_ms_series",
		},
	} {
		body, ok := raw[section]
		if !ok {
			t.Errorf("status json missing %q section", section)
			continue
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode %q section: %v", section, err)
			continue
		}
		for _, f := range fields {
			if _, ok := got[f]; !ok {
				t.Errorf("status json %s missing field %q", section, f)
			}
		}
	}
}

func TestStatusPage_RedactsSecrets(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	cfg.Tailscale.Auth.APIKey = "tskey-SECRETAPIKEY"
	cfg.Tailscale.Auth.OAuth.ClientSecret = "SECRETOAUTH"
	cfg.OTLP.GrafanaCloud.Token = "SECRETGCTOKEN"
	cfg.Streaming.Token = "SECRETSTREAM"
	cfg.Webhook.Secret = "SECRETWEBHOOK"
	cfg.Profiling.Pyroscope.BasicAuthPassword = "SECRETPYRO"
	cfg.OTLP.Headers = map[string]config.Secret{"Authorization": "Basic SECRETHEADER", "X-Scope-OrgID": "tenant-1"}

	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	secrets := []string{"SECRETAPIKEY", "SECRETOAUTH", "SECRETGCTOKEN", "SECRETSTREAM", "SECRETWEBHOOK", "SECRETPYRO", "SECRETHEADER", "tskey-SECRETAPIKEY", "Basic SECRETHEADER"}
	for _, path := range []string{"/", "/api/status.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		body := w.Body.String()
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaked secret %q", path, secret)
			}
		}
	}

	// The JSON must still report that the secrets ARE configured (booleans), and
	// header KEYS (not values) may appear.
	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	var got statusdata.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cs := got.Config
	if !cs.APIKeySet || !cs.OAuthSecretSet || !cs.GCloudTokenSet || !cs.StreamTokenSet || !cs.WebhookSecretSet || !cs.PyroscopeAuthSet {
		t.Errorf("expected all *Set booleans true, got %+v", cs)
	}
	if !strings.Contains(strings.Join(cs.OTLPHeaderKeys, ","), "Authorization") {
		t.Errorf("OTLP header KEYS should include Authorization, got %v", cs.OTLPHeaderKeys)
	}
}

// TestAdminServer_HealthzUnconditional verifies /healthz on the FULL app
// server (buildAdminServer, not the probe-only newAdminServer scaffold) is
// unconditional process liveness: always 200 "ok", regardless of whether
// collectors have run yet (#57).
func TestAdminServer_HealthzUnconditional(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("GET /healthz = %d %q, want 200 ok", w.Code, w.Body.String())
	}
}

// TestAdminServer_ReadyzNotReadyUntilFirstTick verifies a freshly built app
// (default config: several collectors enabled, none has run yet) reports
// /readyz as not-ready with a 503 and a reason mentioning the pending
// collectors (#57 acceptance: non-200 before first successful tick).
func TestAdminServer_ReadyzNotReadyUntilFirstTick(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz on a fresh app = %d, want 503 (starting)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "starting") {
		t.Errorf("GET /readyz body = %q, want it to mention starting", w.Body.String())
	}
}

// TestAdminServer_ReadyzReadyWithNoCollectors verifies /readyz reports ready
// (200 ok) once there is nothing left pending its first run — here achieved
// by disabling every collector so the health verdict has no "starting" reason.
func TestAdminServer_ReadyzReadyWithNoCollectors(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.Devices.Enabled = false
	cfg.Collectors.Flowlogs.Enabled = false
	cfg.Collectors.Auditlogs.Enabled = false
	cfg.Collectors.Users.Enabled = false
	cfg.Collectors.Keys.Enabled = false
	cfg.Collectors.Settings.Enabled = false
	cfg.Collectors.Acl.Enabled = false
	cfg.Collectors.Dns.Enabled = false
	cfg.Collectors.Contacts.Enabled = false
	cfg.Collectors.Webhooks.Enabled = false
	cfg.Collectors.PostureIntegrations.Enabled = false
	cfg.Collectors.LogStream.Enabled = false
	cfg.Collectors.OAuthApps.Enabled = false
	cfg.Collectors.Services.Enabled = false
	cfg.Collectors.NodeMetrics.Enabled = false

	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	if got := len(a.runtimes[0].registry.Entries()); got != 0 {
		t.Fatalf("registered collectors = %d, want 0 (all disabled)", got)
	}
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("GET /readyz with no collectors registered = %d %q, want 200 ok", w.Code, w.Body.String())
	}
}

func TestAdminServer_CardinalityJSON(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/cardinality.json", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/cardinality.json = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var got statusdata.CardinalityInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body does not round-trip into CardinalityInfo: %v", err)
	}

	// Non-GET is rejected.
	req = httptest.NewRequest(http.MethodPost, "/api/cardinality.json", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w = httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/cardinality.json = %d, want 405", w.Code)
	}
}

func TestAdminServer_ConfigJSON(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/config.json", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/config.json = %d, want 200", w.Code)
	}
	var got statusdata.ConfigSummary
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body does not round-trip into ConfigSummary: %v", err)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("content-disposition = %q, want an attachment", cd)
	}

	// The complete effective-config projection (#320) must actually be ATTACHED
	// to this endpoint, not merely correct in isolation. internal/configexport
	// carries thorough completeness and leak tests, but every one of them calls
	// configexport.Build directly — so deleting the assignment in
	// redactedConfigSummary left the whole suite green while /api/config.json
	// silently reverted to the hand-picked subset #320 exists to replace.
	// Verified by mutation: this is the only assertion that fails when the
	// projection is not wired in.
	if len(got.Full) == 0 {
		t.Fatal("/api/config.json carries no `full` projection — configexport.Build is not wired into redactedConfigSummary (#320)")
	}
	for _, key := range []string{"otlp.endpoint", "admin.listen", "log_level"} {
		if _, ok := got.Full[key]; !ok {
			t.Errorf("`full` is missing the key %q; got %d keys", key, len(got.Full))
		}
	}
}

func TestAdminServer_PprofGatedByConfig(t *testing.T) {
	t.Run("disabled -> 404", func(t *testing.T) {
		cfg := config.Default()
		cfg.Admin.Listen = "127.0.0.1:9091" // loopback: reach handleIndex's 404, not the auth gate (#227)
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		srv := a.buildAdminServer()
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
		req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("pprof disabled: GET /debug/pprof/ = %d, want 404", w.Code)
		}
	})
	t.Run("enabled -> 200", func(t *testing.T) {
		cfg := config.Default()
		cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
		cfg.Profiling.Pprof.Enabled = true
		a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
		srv := a.buildAdminServer()
		req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
		req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("pprof enabled: GET /debug/pprof/ = %d, want 200", w.Code)
		}
	})
}

// TestStatusJSON_NoURLCredentials is the end-to-end guard for
// GHSA-qch3-gwff-r6pf / GHSA-jp5c-3282-6882 / GHSA-h5p7-qj62-m8qx: whatever the
// DTO construction does internally, the bytes actually served by /api/status.json
// must not contain a credential embedded in any configured URL.
func TestStatusJSON_NoURLCredentials(t *testing.T) {
	const secret = "sUpErSeCrEtCrEdEnTiAl"
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: stays open with no token (#227)
	cfg.OTLP.Protocol = "http"
	cfg.OTLP.Endpoint = "https://12345:" + secret + "@otlp.example.com/otlp"
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://profiles.example.com?api_key=" + secret
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	a.runtimes[0].nodeMetrics = nodemetrics.New(nodemetrics.Options{
		Targets: []nodemetrics.Target{{URL: "https://n:" + secret + "@node.example.com/metrics", Instance: "n1"}},
	})
	srv := a.buildAdminServer()

	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/status.json = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Errorf("status JSON leaks a URL-embedded credential")
	}

	// ...and neither may the HTML page, which renders the same DTO.
	reqHTML := httptest.NewRequest(http.MethodGet, "/", nil)
	reqHTML.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
	wHTML := httptest.NewRecorder()
	srv.Handler.ServeHTTP(wHTML, reqHTML)
	if wHTML.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", wHTML.Code)
	}
	if strings.Contains(wHTML.Body.String(), secret) {
		t.Errorf("status HTML leaks a URL-embedded credential")
	}
}
