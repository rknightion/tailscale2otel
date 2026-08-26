package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeOTLPServer answers every POST 200 OK, so -once (which uses the real
// export path) has somewhere safe and local to export to instead of the
// default config's real Grafana Cloud endpoint.
func fakeOTLPServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// headscaleDevicesYAML is a minimal config pointed at a fake Headscale server:
// only the devices collector is enabled, and every listener/checkpoint knob
// is deliberately turned ON so a passing test proves -preflight/-once
// actually suppress them rather than never having anything to suppress.
// otlpURL, when non-empty, replaces the default (real Grafana Cloud) OTLP
// endpoint with a local fake — required for any run that does NOT go through
// -preflight's discarding telemetry override (i.e. -once, or -preflight
// -preflight-export), or the test would otherwise attempt a real network call.
func headscaleDevicesYAML(serverURL, checkpointPath, otlpURL string) string {
	otlpBlock := ""
	if otlpURL != "" {
		otlpBlock = fmt.Sprintf(`
otlp:
  protocol: http
  endpoint: %q
  tls:
    insecure: true
`, otlpURL)
	}
	return fmt.Sprintf(`
provider: headscale
headscale:
  url: %q
  api_key: "k"
%s
collectors:
  devices:
    enabled: true
  users:
    enabled: false
  keys:
    enabled: false
  settings:
    enabled: false
  acl:
    enabled: false
  dns:
    enabled: false
  contacts:
    enabled: false
  webhooks:
    enabled: false
  posture_integrations:
    enabled: false
  log_stream:
    enabled: false
  services:
    enabled: false
  node_metrics:
    enabled: false
  oauth_apps:
    enabled: false
admin:
  enabled: true
  listen: "127.0.0.1:0"
prometheus:
  enabled: true
  listen: "127.0.0.1:0"
checkpoint:
  store: file
  file_path: %q
`, serverURL, otlpBlock, checkpointPath)
}

// fakeHeadscaleServer answers GET /api/v1/node with an empty node list, or
// (when unauthorized is true) a 401 for every request, so tests can exercise
// both the success and auth-failure exit-code paths without a real network
// call.
func fakeHeadscaleServer(t *testing.T, unauthorized bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "only GET is expected", http.StatusMethodNotAllowed)
			return
		}
		if unauthorized {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRun_PreflightAndOnce_MutuallyExclusive(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-preflight", "-once"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr = %q, want it to mention mutually exclusive", stderr.String())
	}
}

func TestRun_Preflight_InvalidConfig(t *testing.T) {
	path := writeTempConfig(t, invalidYAML)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-preflight", "-config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRun_Preflight_SucceedsAndReportsJSON(t *testing.T) {
	srv := fakeHeadscaleServer(t, false)
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	path := writeTempConfig(t, headscaleDevicesYAML(srv.URL, checkpointPath, ""))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-preflight", "-json", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var rep preflightReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; stdout=%s", err, stdout.String())
	}
	if rep.Mode != "preflight" {
		t.Errorf("mode = %q, want preflight", rep.Mode)
	}
	if !rep.OK || rep.ExitCode != 0 {
		t.Errorf("report = %+v, want ok=true exit_code=0", rep)
	}
	if rep.ExportAttempted {
		t.Error("export_attempted = true, want false (no -preflight-export)")
	}
	if len(rep.Results) != 1 || rep.Results[0].Collector != "devices" || !rep.Results[0].OK {
		t.Fatalf("results = %+v, want a single OK devices result", rep.Results)
	}

	// -preflight must never persist the checkpoint file, even though the
	// config points checkpoint.store=file at a real path.
	if _, err := os.Stat(checkpointPath); !os.IsNotExist(err) {
		t.Errorf("checkpoint file %s exists (or Stat errored: %v); -preflight must never persist it", checkpointPath, err)
	}
}

func TestRun_PrometheusCheck_DoesNotWriteAnnotationsOrOTLP(t *testing.T) {
	headscale := fakeHeadscaleServer(t, false)
	var annotationPosts atomic.Int32
	annotations := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		annotationPosts.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(annotations.Close)
	var otlpPosts atomic.Int32
	otlp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otlpPosts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(otlp.Close)

	path := writeTempConfig(t, headscaleDevicesYAML(headscale.URL, filepath.Join(t.TempDir(), "checkpoint.json"), "")+fmt.Sprintf(`
delivery:
  mode: prometheus
otlp:
  metrics:
    protocol: http
    endpoint: %q
    tls:
      insecure: true
grafana_annotations:
  url: %q
  token: "test-token"
`, otlp.URL, annotations.URL))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-prometheus-check", "-json", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := annotationPosts.Load(); got != 0 {
		t.Errorf("annotation POSTs = %d, want 0", got)
	}
	if got := otlpPosts.Load(); got != 0 {
		t.Errorf("OTLP POSTs = %d, want 0", got)
	}
}

func TestRun_Once_SucceedsAndPersistsCheckpoint(t *testing.T) {
	srv := fakeHeadscaleServer(t, false)
	otlpSrv := fakeOTLPServer(t)
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	path := writeTempConfig(t, headscaleDevicesYAML(srv.URL, checkpointPath, otlpSrv.URL))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-once", "-json", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var rep preflightReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; stdout=%s", err, stdout.String())
	}
	if rep.Mode != "once" {
		t.Errorf("mode = %q, want once", rep.Mode)
	}
	if !rep.ExportAttempted {
		t.Error("export_attempted = false, want true for -once")
	}

	// -once keeps the operator's configured checkpoint behavior: the devices
	// collector is a SnapshotCollector (no window/checkpoint), so a
	// checkpoint file legitimately not existing here is expected — this
	// assertion instead pins that -once did NOT force the store to memory
	// (see PrepareConfig): re-running -preflight against the same config
	// would still refuse to persist, which the previous test already covers.
	if rep.Results[0].Collector != "devices" || !rep.Results[0].OK {
		t.Fatalf("results = %+v, want a single OK devices result", rep.Results)
	}
}

// TestRun_Preflight_AuthFailureExitCode drives a 401 from the fake Headscale
// server and asserts exit 3 (fix the credential), not 4 (the credential works,
// a collector doesn't). Those two codes exist to point an operator at
// different next actions, so misclassifying is worse than not classifying.
//
// This test previously asserted 4 and documented it as a KNOWN GAP: hsapi's
// getJSON wrapped every non-2xx in a bare fmt.Errorf, so isAuthFailure had
// nothing to type-assert against and EVERY Headscale deployment silently got
// the wrong code. hsapi.StatusError now carries the status the same way
// tsapi.StatusError does, so the gap is closed and the assertion inverted —
// it was the symptom in test form, not the specification.
func TestRun_Preflight_AuthFailureExitCode(t *testing.T) {
	srv := fakeHeadscaleServer(t, true)
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	path := writeTempConfig(t, headscaleDevicesYAML(srv.URL, checkpointPath, ""))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-preflight", "-json", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit code = %d, want 3 (auth failure: a Headscale 401 is a credential problem); stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	var rep preflightReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; stdout=%s", err, stdout.String())
	}
	if rep.OK {
		t.Error("ok = true, want false")
	}
	if len(rep.Results) != 1 || rep.Results[0].OK {
		t.Fatalf("results = %+v, want a single failed result", rep.Results)
	}
	if !rep.Results[0].AuthFailure {
		t.Error("auth_failure = false: a 401 from the control plane must be reported as one")
	}
}

func TestRun_Preflight_HumanReport(t *testing.T) {
	srv := fakeHeadscaleServer(t, false)
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	path := writeTempConfig(t, headscaleDevicesYAML(srv.URL, checkpointPath, ""))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-preflight", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "devices") || !strings.Contains(stdout.String(), "OK") {
		t.Errorf("human report = %q, want it to mention devices and OK", stdout.String())
	}
	if !strings.Contains(stdout.String(), "preflight: OK") {
		t.Errorf("human report = %q, want a preflight: OK summary line", stdout.String())
	}
}

func TestRun_PrometheusCheck_SucceedsWithSentinelAndValidExposition(t *testing.T) {
	srv := fakeHeadscaleServer(t, false)
	checkpointPath := filepath.Join(t.TempDir(), "checkpoint.json")
	path := writeTempConfig(t, headscaleDevicesYAML(srv.URL, checkpointPath, ""))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-prometheus-check", "-json", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
	if code != pfOK {
		t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, pfOK, stdout.String(), stderr.String())
	}

	var rep prometheusCheckReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("stdout is not valid JSON: %v; stdout=%s", err, stdout.String())
	}
	if !rep.OK || rep.ExitCode != pfOK {
		t.Errorf("report = %+v, want an OK report", rep)
	}
	if rep.Sentinel != prometheusCheckSentinel || !rep.ExpositionValid {
		t.Errorf("report sentinel/exposition = %q/%t, want %q/true", rep.Sentinel, rep.ExpositionValid, prometheusCheckSentinel)
	}
	if len(rep.FailureClasses) != 0 {
		t.Errorf("failure_classes = %v, want none", rep.FailureClasses)
	}
	if len(rep.Results) != 1 || !rep.Results[0].OK {
		t.Fatalf("results = %+v, want one successful collector", rep.Results)
	}
	if _, err := os.Stat(checkpointPath); !os.IsNotExist(err) {
		t.Errorf("checkpoint file %s exists (or Stat errored: %v); -prometheus-check must never persist it", checkpointPath, err)
	}
}

func TestRun_PrometheusCheck_ClassifiesAuthenticationAndCollectionFailures(t *testing.T) {
	tests := []struct {
		name      string
		server    *httptest.Server
		wantCode  int
		wantClass prometheusCheckFailureClass
	}{
		{
			name:      "authentication",
			server:    fakeHeadscaleServer(t, true),
			wantCode:  pfAuthFailure,
			wantClass: prometheusCheckAuthentication,
		},
		{
			name: "collection",
			server: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "upstream unavailable", http.StatusInternalServerError)
			})),
			wantCode:  pfCollectFailure,
			wantClass: prometheusCheckCollection,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "collection" {
				t.Cleanup(tc.server.Close)
			}
			path := writeTempConfig(t, headscaleDevicesYAML(tc.server.URL, filepath.Join(t.TempDir(), "checkpoint.json"), ""))

			var stdout, stderr bytes.Buffer
			code := run([]string{"-prometheus-check", "-json", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			var rep prometheusCheckReport
			if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
				t.Fatalf("stdout is not valid JSON: %v; stdout=%s", err, stdout.String())
			}
			if !slices.Contains(rep.FailureClasses, tc.wantClass) {
				t.Errorf("failure_classes = %v, want %q", rep.FailureClasses, tc.wantClass)
			}
		})
	}
}

func TestRun_PrometheusCheck_ClassifiesGatherAndAccessPostureFailures(t *testing.T) {
	srv := fakeHeadscaleServer(t, false)
	tests := []struct {
		name      string
		config    string
		wantCode  int
		wantClass prometheusCheckFailureClass
	}{
		{
			name:      "missing documented sentinel",
			config:    headscaleDevicesYAML(srv.URL, filepath.Join(t.TempDir(), "checkpoint.json"), "") + "\nself_observability:\n  enabled: false\n",
			wantCode:  pfPrometheusGatherFailure,
			wantClass: prometheusCheckGather,
		},
		{
			name: "unauthenticated network bind",
			config: strings.Replace(
				headscaleDevicesYAML(srv.URL, filepath.Join(t.TempDir(), "checkpoint.json"), ""),
				"prometheus:\n  enabled: true\n  listen: \"127.0.0.1:0\"",
				"prometheus:\n  enabled: true\n  listen: \":2112\"",
				1,
			),
			wantCode:  pfPrometheusAccessFailure,
			wantClass: prometheusCheckAccessPosture,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempConfig(t, tc.config)
			var stdout, stderr bytes.Buffer
			code := run([]string{"-prometheus-check", "-json", "-preflight-timeout", "10s", "-config", path}, &stdout, &stderr)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			var rep prometheusCheckReport
			if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
				t.Fatalf("stdout is not valid JSON: %v; stdout=%s", err, stdout.String())
			}
			if !slices.Contains(rep.FailureClasses, tc.wantClass) {
				t.Errorf("failure_classes = %v, want %q", rep.FailureClasses, tc.wantClass)
			}
		})
	}
}

func TestRun_PrometheusCheck_ClassifiesConfigurationAndReportsHumanAccessPosture(t *testing.T) {
	t.Run("configuration", func(t *testing.T) {
		path := writeTempConfig(t, invalidYAML)
		var stdout, stderr bytes.Buffer
		code := run([]string{"-prometheus-check", "-json", "-config", path}, &stdout, &stderr)
		if code != pfConfigInvalid {
			t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, pfConfigInvalid, stdout.String(), stderr.String())
		}
		var rep prometheusCheckReport
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatalf("stdout is not valid JSON: %v; stdout=%s", err, stdout.String())
		}
		if !slices.Contains(rep.FailureClasses, prometheusCheckConfiguration) {
			t.Errorf("failure_classes = %v, want %q", rep.FailureClasses, prometheusCheckConfiguration)
		}
	})

	t.Run("human access posture", func(t *testing.T) {
		srv := fakeHeadscaleServer(t, false)
		cfg := strings.Replace(
			headscaleDevicesYAML(srv.URL, filepath.Join(t.TempDir(), "checkpoint.json"), ""),
			"prometheus:\n  enabled: true\n  listen: \"127.0.0.1:0\"",
			"prometheus:\n  enabled: true\n  listen: \":2112\"",
			1,
		)
		path := writeTempConfig(t, cfg)
		var stdout, stderr bytes.Buffer
		code := run([]string{"-prometheus-check", "-config", path}, &stdout, &stderr)
		if code != pfPrometheusAccessFailure {
			t.Fatalf("exit code = %d, want %d; stdout=%s stderr=%s", code, pfPrometheusAccessFailure, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "failure_classes=access_posture") {
			t.Errorf("human output = %q, want access posture classification", stdout.String())
		}
	})
}
