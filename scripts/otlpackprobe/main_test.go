package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func probeEnv(t *testing.T, endpoint string) map[string]string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("private-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"OTLP_ACK_PROBE_ENDPOINT": endpoint, "OTLP_ACK_PROBE_USER": "probe-user", "OTLP_ACK_PROBE_TOKEN_FILE": file}
}

func TestMissingEnvironment(t *testing.T) {
	for _, key := range []string{"OTLP_ACK_PROBE_ENDPOINT", "OTLP_ACK_PROBE_USER", "OTLP_ACK_PROBE_TOKEN_FILE"} {
		t.Run(key, func(t *testing.T) {
			env := probeEnv(t, "http://127.0.0.1:1")
			delete(env, key)
			var out bytes.Buffer
			if run(func(k string) string { return env[k] }, &out) == 0 {
				t.Fatal("missing environment succeeded")
			}
		})
	}
}

func TestResponsesAndRequests(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		body              []byte
		decoded           bool
	}{
		{"protobuf", "application/x-protobuf", []byte{0x0a, 0x00}, true},
		{"empty", "application/x-protobuf", nil, true},
		{"json", "application/json", []byte(`{"secret":"private-token"}`), false},
		{"missing-content-type", "", []byte{0x0a, 0x00}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				user, token, ok := r.BasicAuth()
				if !ok || user != "probe-user" || token != "private-token" {
					t.Error("wrong authentication")
				}
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-protobuf" {
					t.Error("wrong request headers or method")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				switch r.URL.Path {
				case "/otlp/v1/logs":
					var req logs.ExportLogsServiceRequest
					if err := proto.Unmarshal(body, &req); err != nil {
						t.Fatal(err)
					}
					if len(req.ResourceLogs) != 1 {
						t.Fatal("expected one resource")
					}
					resource := req.ResourceLogs[0]
					if len(resource.Resource.Attributes) != 1 || resource.Resource.Attributes[0].Key != "service.name" || resource.Resource.Attributes[0].Value.GetStringValue() != "tailscale2otel-ackprobe" {
						t.Error("wrong service")
					}
					if len(resource.ScopeLogs) != 1 || len(resource.ScopeLogs[0].LogRecords) != 1 {
						t.Error("expected one log")
					}
				case "/otlp/v1/metrics":
					var req metrics.ExportMetricsServiceRequest
					if err := proto.Unmarshal(body, &req); err != nil {
						t.Fatal(err)
					}
					if len(req.ResourceMetrics) != 1 {
						t.Fatal("expected one resource")
					}
					resource := req.ResourceMetrics[0]
					if len(resource.Resource.Attributes) != 1 || resource.Resource.Attributes[0].Key != "service.name" || resource.Resource.Attributes[0].Value.GetStringValue() != "tailscale2otel-ackprobe" {
						t.Error("wrong service")
					}
					if len(resource.ScopeMetrics) != 1 || len(resource.ScopeMetrics[0].Metrics) != 1 || len(resource.ScopeMetrics[0].Metrics[0].GetGauge().GetDataPoints()) != 1 {
						t.Error("expected one gauge point")
					}
				default:
					t.Errorf("wrong path: %s", r.URL.Path)
				}
				// A nil Content-Type slice suppresses net/http's automatic sniffing.
				w.Header()["Content-Type"] = nil
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tc.body)
			}))
			defer server.Close()
			env := probeEnv(t, server.URL+"/otlp/")
			var out bytes.Buffer
			if code := run(func(k string) string { return env[k] }, &out); code != 0 {
				t.Fatalf("exit=%d output=%s", code, &out)
			}
			want := ""
			for _, signal := range []string{"logs", "metrics"} {
				want += fmt.Sprintf("%s status=200 content-type=%q body-length=%d protobuf-decode=%t\n", signal, tc.contentType, len(tc.body), tc.decoded)
			}
			if out.String() != want {
				t.Fatalf("output=%q want=%q", out.String(), want)
			}
			if requests != 2 {
				t.Errorf("requests=%d", requests)
			}
			if strings.Contains(out.String(), "private-token") {
				t.Fatal("token leaked")
			}
		})
	}
}

func TestRedirectAndHeaderSecrecy(t *testing.T) {
	redirected := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/private-token; user=probe-user")
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	env := probeEnv(t, server.URL)
	var out bytes.Buffer
	if run(func(k string) string { return env[k] }, &out) == 0 {
		t.Fatal("redirect succeeded")
	}
	if redirected != 0 {
		t.Fatal("redirect followed")
	}
	if strings.Contains(out.String(), "private-token") || strings.Contains(out.String(), "probe-user") || strings.Contains(out.String(), target.URL) {
		t.Fatalf("credentials or redirect URL leaked: %q", out.String())
	}
	if !strings.Contains(out.String(), `status=307 content-type="application/[redacted]; user=[redacted]" body-length=0 protobuf-decode=true`) {
		t.Fatalf("unexpected fields: %q", out.String())
	}
}

func TestTokenFileErrors(t *testing.T) {
	for _, content := range []string{"", " \n"} {
		env := probeEnv(t, "http://127.0.0.1:1")
		if err := os.WriteFile(env["OTLP_ACK_PROBE_TOKEN_FILE"], []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if run(func(k string) string { return env[k] }, &out) == 0 {
			t.Fatal("empty token succeeded")
		}
	}
	env := probeEnv(t, "http://127.0.0.1:1")
	env["OTLP_ACK_PROBE_TOKEN_FILE"] = filepath.Join(t.TempDir(), "missing-private-token")
	var out bytes.Buffer
	if run(func(k string) string { return env[k] }, &out) == 0 || strings.Contains(out.String(), "private-token") {
		t.Fatal("missing token file succeeded or leaked its path")
	}
}

// Exercise the actual command entry point, including both stdout and stderr.
func TestRecipeSecurityRegression(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	encoded := base64.StdEncoding.EncodeToString([]byte("probe-user:private-token"))
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		truncated    bool
		contentType  string
		wantRequests int32
	}{
		{"unauthorized", 401, "", false, "application/x-protobuf", 1},
		{"forbidden", 403, "", false, "application/x-protobuf", 1},
		{"unauthorized-oversize", 401, strings.Repeat("x", 1024*1024+1), false, "application/x-protobuf", 1},
		{"forbidden-truncated", 403, "x", true, "application/x-protobuf", 1},
		{"encoded-reflection", 200, "", false, "application/x-protobuf; reflected=" + encoded, 2},
		{"authorization-reflection", 200, "", false, "application/x-protobuf; reflected=Basic " + encoded, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", tc.contentType)
				if tc.truncated {
					w.Header().Set("Content-Length", "100")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			env := probeEnv(t, server.URL)
			cmd := exec.CommandContext(ctx, "just", "probe-otlp-ack")
			cmd.Dir = "../.."
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "OTLP_ACK_PROBE_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			for key, value := range env {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if (err != nil) != (tc.status == 401 || tc.status == 403) {
				t.Errorf("unexpected exit: %v", err)
			}
			if requests.Load() != tc.wantRequests {
				t.Errorf("requests=%d want=%d", requests.Load(), tc.wantRequests)
			}
			if !strings.Contains(stdout.String(), fmt.Sprintf("logs status=%d", tc.status)) {
				t.Errorf("status not printed: %q", stdout.String())
			}
			if tc.wantRequests == 1 && strings.Contains(stdout.String(), "metrics status=") {
				t.Error("second signal printed after auth rejection")
			}
			if tc.truncated || len(tc.body) > 1024*1024 {
				if !strings.Contains(stdout.String(), "protobuf-decode=false") {
					t.Error("incomplete body reported as decoded")
				}
			}
			for _, secret := range []string{"private-token", "probe-user", encoded, "Basic " + encoded} {
				if strings.Contains(stdout.String()+stderr.String(), secret) {
					t.Error("credential leaked to stdout/stderr")
				}
			}
		})
	}
}

func TestEndpointTransport(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		allowed  bool
	}{
		{"https://example.invalid/otlp", true},
		{"http://127.0.0.1:1234", true},
		{"http://127.42.0.1:1234", true},
		{"http://[::1]:1234", true},
		{"http://localhost:1234", false},
		{"http://example.invalid/otlp", false},
		{"http://192.0.2.1/otlp", false},
		{"http://127.0.0.1.example.invalid/otlp", false},
		{"ftp://127.0.0.1/otlp", false},
	} {
		endpoint, err := url.Parse(tc.endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if got := allowedTransport(endpoint); got != tc.allowed {
			t.Errorf("%s allowed=%t want=%t", tc.endpoint, got, tc.allowed)
		}
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	// localhost would reach this fixture if validation accepted DNS names.
	env := probeEnv(t, strings.Replace(server.URL, "127.0.0.1", "localhost", 1))
	var out bytes.Buffer
	if run(func(k string) string { return env[k] }, &out) == 0 || out.String() != "probe: invalid endpoint\n" || requests.Load() != 0 {
		t.Fatalf("nonliteral HTTP endpoint not rejected before I/O: %q", out.String())
	}
}

func TestErrorsDoNotLeak(t *testing.T) {
	for _, endpoint := range []string{"http://url-user:url-password@127.0.0.1:1", ":invalid-private-token"} {
		env := probeEnv(t, endpoint)
		var out bytes.Buffer
		if run(func(k string) string { return env[k] }, &out) == 0 {
			t.Fatal("invalid endpoint succeeded")
		}
		for _, secret := range []string{endpoint, "url-user", "url-password", "private-token"} {
			if strings.Contains(out.String(), secret) {
				t.Fatalf("secret leaked: %q", out.String())
			}
		}
	}
}
