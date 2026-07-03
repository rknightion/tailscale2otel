package config_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
)

// loadErr loads the YAML and returns the error (or nil) for assertion.
func loadErr(t *testing.T, y string) error {
	t.Helper()
	p := writeTemp(t, y)
	_, err := config.Load(p)
	return err
}

// TestWarnings_APIKeyMethodAdvises pins the non-fatal advisory steering operators
// toward OAuth: a personal API key (auth.method: apikey) is valid but expires in
// <=90 days and is tied to a user, so Warnings() should flag it; the default
// (oauth) produces no warning.
func TestWarnings_APIKeyMethodAdvises(t *testing.T) {
	c := config.Default()
	if w := c.Warnings(); len(w) != 0 {
		t.Fatalf("default (oauth) Warnings = %v, want none", w)
	}
	c.Tailscale.Auth.Method = "apikey"
	w := c.Warnings()
	if len(w) == 0 {
		t.Fatalf("apikey Warnings = none, want an advisory about expiry / OAuth")
	}
	joined := strings.Join(w, " ")
	if !strings.Contains(joined, "apikey") || !strings.Contains(joined, "OAuth") {
		t.Errorf("apikey advisory %q should reference both apikey and OAuth", joined)
	}
}

// TestWarnings_DualIngestionRisk pins the advisory that steers operators to a
// single log-ingestion method. When the stream receiver is enabled AND a log
// collector still polls, the same data can arrive twice; cross-source de-dup is
// only a best-effort failsafe, so Warnings() must flag the risk (and not flag a
// stream-only collector, nor anything when streaming is off).
func TestWarnings_DualIngestionRisk(t *testing.T) {
	// streaming OFF => no dual-ingestion warning even with source=both.
	c := config.Default()
	c.Streaming.Enabled = false
	c.Collectors.Flowlogs.Source = "both"
	for _, w := range c.Warnings() {
		if strings.Contains(strings.ToLower(w), "ingest") {
			t.Fatalf("unexpected dual-ingestion warning while streaming is off: %q", w)
		}
	}

	// streaming ON + auditlogs=both, flowlogs=stream => warn for audit only.
	c = config.Default()
	c.Streaming.Enabled = true
	c.Collectors.Auditlogs.Source = "both"
	c.Collectors.Flowlogs.Source = "stream"
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "auditlogs") {
		t.Fatalf("want a dual-ingestion warning naming auditlogs; got %q", w)
	}
	if !strings.Contains(strings.ToLower(w), "failsafe") {
		t.Errorf("dual-ingestion advisory should describe de-dup as a failsafe; got %q", w)
	}
	if strings.Contains(w, "flowlogs") {
		t.Errorf("flowlogs source=stream is single-method and must NOT warn; got %q", w)
	}

	// streaming ON + default (poll) sources => warn for BOTH flow and audit.
	c = config.Default()
	c.Streaming.Enabled = true
	w = strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "flowlogs") || !strings.Contains(w, "auditlogs") {
		t.Fatalf("streaming on + polling collectors should warn for both flow and audit; got %q", w)
	}
}

// TestWarnings_GrafanaCloudProfilesNeedsAuth pins the advisory that Grafana
// Cloud Profiles (a grafana.net pyroscope endpoint) needs basic-auth
// credentials: when pyroscope is enabled against a grafana.net server_address
// with no basic_auth_password set, Warnings() should flag it; a complete config
// (or a non-grafana.net endpoint) produces no such warning.
func TestWarnings_GrafanaCloudProfilesNeedsAuth(t *testing.T) {
	// grafana.net server with no password => advisory.
	c := config.Default()
	c.Profiling.Pyroscope.Enabled = true
	c.Profiling.Pyroscope.ServerAddress = "https://profiles-prod-001.grafana.net"
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(strings.ToLower(w), "basic-auth") && !strings.Contains(strings.ToLower(w), "basic auth") {
		t.Fatalf("want a Grafana Cloud Profiles basic-auth advisory; got %q", w)
	}

	// Same endpoint WITH a password => no such advisory.
	c.Profiling.Pyroscope.BasicAuthPassword = "glc_token"
	for _, msg := range c.Warnings() {
		if strings.Contains(strings.ToLower(msg), "basic-auth") || strings.Contains(strings.ToLower(msg), "basic auth") {
			t.Errorf("did not expect a basic-auth advisory once a password is set; got %q", msg)
		}
	}

	// A self-hosted (non-grafana.net) endpoint without a password => no advisory.
	c = config.Default()
	c.Profiling.Pyroscope.Enabled = true
	c.Profiling.Pyroscope.ServerAddress = "http://pyroscope.internal:4040"
	for _, msg := range c.Warnings() {
		if strings.Contains(strings.ToLower(msg), "basic-auth") || strings.Contains(strings.ToLower(msg), "basic auth") {
			t.Errorf("self-hosted endpoint should not trigger the Grafana Cloud advisory; got %q", msg)
		}
	}
}

// TestWarnings_AdminExposedWithoutToken pins the advisory that steers operators
// to protect the status page: when the admin server is enabled with the landing
// page on a wildcard (all-interfaces) bind and no admin.auth.token, the status
// page exposes internal state to anyone who can reach the port. Binding to a
// loopback/tailnet address OR setting a token clears the advisory.
func TestWarnings_AdminExposedWithoutToken(t *testing.T) {
	// Disabled admin => no advisory.
	c := config.Default()
	for _, w := range c.Warnings() {
		if strings.Contains(w, "admin.auth.token") {
			t.Fatalf("disabled admin should not warn about admin.auth.token; got %q", w)
		}
	}

	// Enabled, landing page on the default wildcard bind, no token => advisory.
	c = config.Default()
	c.Admin.Enabled = true // landing_page defaults true, listen defaults ":9090"
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "admin.auth.token") {
		t.Fatalf("admin exposed on a wildcard bind without a token should advise admin.auth.token; got %q", w)
	}

	// Same, but with a token set => no advisory.
	c.Admin.Auth.Token = "s3cret"
	for _, msg := range c.Warnings() {
		if strings.Contains(msg, "admin.auth.token") {
			t.Errorf("a configured token should clear the exposure advisory; got %q", msg)
		}
	}

	// Enabled on a loopback bind without a token => no advisory (restricted bind).
	c = config.Default()
	c.Admin.Enabled = true
	c.Admin.Listen = "127.0.0.1:9090"
	for _, msg := range c.Warnings() {
		if strings.Contains(msg, "admin.auth.token") {
			t.Errorf("a loopback bind should not trigger the exposure advisory; got %q", msg)
		}
	}
}

// TestWarnings_ReceiverAuthDisabled pins the advisories that steer operators away
// from running an UNAUTHENTICATED ingestion receiver. The webhook receiver skips
// HMAC verification entirely when webhook.secret is empty, and the streaming (HEC)
// receiver disables token auth when streaming.token is empty — so an enabled
// receiver with an empty credential accepts forged/unauthenticated input. A
// credential left empty (unset, or a mistyped TS2OTEL_* env var name) lands here.
// A disabled receiver, or a credential that is set, must NOT warn.
func TestWarnings_ReceiverAuthDisabled(t *testing.T) {
	// Disabled receivers => no auth advisory.
	c := config.Default()
	for _, w := range c.Warnings() {
		if strings.Contains(w, "webhook.secret") || strings.Contains(w, "streaming.token") {
			t.Fatalf("disabled receivers should not warn about credentials; got %q", w)
		}
	}

	// webhook enabled with an empty secret => advisory naming webhook.secret + HMAC.
	c = config.Default()
	c.Webhook.Enabled = true
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "webhook.secret") {
		t.Fatalf("webhook enabled with empty secret should advise webhook.secret; got %q", w)
	}
	if !strings.Contains(strings.ToLower(w), "hmac") {
		t.Errorf("webhook advisory should explain HMAC verification is skipped; got %q", w)
	}

	// webhook secret set => no webhook advisory.
	c.Webhook.Secret = "whsec"
	for _, msg := range c.Warnings() {
		if strings.Contains(msg, "webhook.secret") {
			t.Errorf("a configured webhook.secret should clear the advisory; got %q", msg)
		}
	}

	// streaming enabled with an empty token => advisory naming streaming.token.
	c = config.Default()
	c.Streaming.Enabled = true
	w = strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "streaming.token") {
		t.Fatalf("streaming enabled with empty token should advise streaming.token; got %q", w)
	}

	// streaming token set => no streaming-auth advisory.
	c.Streaming.Token = "hec-token"
	for _, msg := range c.Warnings() {
		if strings.Contains(msg, "streaming.token") {
			t.Errorf("a configured streaming.token should clear the advisory; got %q", msg)
		}
	}
}

func TestValidateProfilingPprofRequiresAdmin(t *testing.T) {
	const y = "profiling:\n  pprof:\n    enabled: true\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: pprof enabled without admin")
	}
	if !strings.Contains(err.Error(), "pprof") || !strings.Contains(err.Error(), "admin") {
		t.Errorf("error %q should mention pprof + admin", err.Error())
	}
}

func TestValidateProfilingPprofValidWithAdmin(t *testing.T) {
	// pprof now also requires admin.auth.token (it can expose in-memory secrets
	// via heap dumps), so a token is part of the minimal valid config.
	const y = "admin:\n  enabled: true\n  auth:\n    token: \"s3cret\"\nprofiling:\n  pprof:\n    enabled: true\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("pprof with admin.enabled + auth.token should be valid: %v", err)
	}
}

// TestValidateProfilingPprofRequiresToken pins the stricter pprof gate: because
// the pprof handlers can leak in-memory secrets (heap/goroutine dumps), enabling
// them without admin.auth.token is a hard configuration error even though the
// status page itself only warns.
func TestValidateProfilingPprofRequiresToken(t *testing.T) {
	const y = "admin:\n  enabled: true\nprofiling:\n  pprof:\n    enabled: true\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: pprof enabled without admin.auth.token")
	}
	if !strings.Contains(err.Error(), "pprof") || !strings.Contains(err.Error(), "token") {
		t.Errorf("error %q should mention pprof + token", err.Error())
	}
}

func TestValidateProfilingPyroscopeRequiresServerAddress(t *testing.T) {
	const y = "profiling:\n  pyroscope:\n    enabled: true\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: pyroscope enabled without server_address")
	}
	if !strings.Contains(err.Error(), "pyroscope") || !strings.Contains(err.Error(), "server_address") {
		t.Errorf("error %q should mention pyroscope + server_address", err.Error())
	}
}

func TestValidateProfilingPyroscopeValidWithServerAddress(t *testing.T) {
	const y = "profiling:\n  pyroscope:\n    enabled: true\n    server_address: http://pyroscope.internal:4040\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("pyroscope with a server_address should be valid: %v", err)
	}
}

func TestValidateRejectsBadProtocol(t *testing.T) {
	err := loadErr(t, "otlp:\n  protocol: carrier-pigeon\n")
	if err == nil {
		t.Fatalf("expected error for bad otlp.protocol, got nil")
	}
	if !strings.Contains(err.Error(), "protocol") {
		t.Errorf("error %q should mention protocol", err.Error())
	}
}

func TestValidateAcceptsAllProtocols(t *testing.T) {
	// grpc requires a host:port endpoint (no scheme/path); http keeps the default
	// URL endpoint; stdout ignores the endpoint.
	cases := map[string]string{
		"http":   "otlp:\n  protocol: http\n",
		"grpc":   "otlp:\n  protocol: grpc\n  endpoint: \"otlp-gateway-prod-us-central-0.grafana.net:443\"\n",
		"stdout": "otlp:\n  protocol: stdout\n",
	}
	for proto, y := range cases {
		if err := loadErr(t, y); err != nil {
			t.Errorf("protocol %q should be valid: %v", proto, err)
		}
	}
}

// TestValidateRejectsGRPCURLEndpoint pins that protocol=grpc rejects a URL-shaped
// endpoint (scheme/path) — the gRPC exporter dials host:port — while accepting a
// bare host:port. protocol=http (the default) keeps accepting the full URL.
func TestValidateRejectsGRPCURLEndpoint(t *testing.T) {
	bad := []string{
		"otlp:\n  protocol: grpc\n  endpoint: \"https://example.test/otlp\"\n",
		"otlp:\n  protocol: grpc\n  endpoint: \"example.test:4317/otlp\"\n",
		"otlp:\n  protocol: grpc\n  endpoint: \"grpc://example.test:4317\"\n",
	}
	for _, y := range bad {
		err := loadErr(t, y)
		if err == nil {
			t.Errorf("grpc with URL-shaped endpoint should be rejected: %q", y)
			continue
		}
		if !strings.Contains(err.Error(), "otlp.endpoint") || !strings.Contains(err.Error(), "grpc") {
			t.Errorf("error %q should mention otlp.endpoint and grpc", err.Error())
		}
	}
	// A host:port grpc endpoint is accepted.
	if err := loadErr(t, "otlp:\n  protocol: grpc\n  endpoint: \"example.test:4317\"\n"); err != nil {
		t.Errorf("grpc with host:port endpoint should be valid: %v", err)
	}
	// The same URL endpoint is valid for http (unchanged behavior).
	if err := loadErr(t, "otlp:\n  protocol: http\n  endpoint: \"https://example.test/otlp\"\n"); err != nil {
		t.Errorf("http with URL endpoint should be valid: %v", err)
	}
}

// TestValidateReceiverPaths pins that a configured streaming/webhook path must be
// a rooted absolute path: the valid defaults pass, an empty path passes (the
// receiver fills in its own default), and a path without a leading slash (e.g.
// "tailscale/webhook") is rejected before it can be misregistered with ServeMux.
func TestValidateReceiverPaths(t *testing.T) {
	// Defaults (rooted) are valid.
	if err := loadErr(t, "streaming:\n  path: \"/services/collector/event\"\nwebhook:\n  path: \"/tailscale/webhook\"\n"); err != nil {
		t.Errorf("default receiver paths should be valid: %v", err)
	}
	// Empty paths are valid (receiver substitutes its default).
	if err := loadErr(t, "streaming:\n  path: \"\"\nwebhook:\n  path: \"\"\n"); err != nil {
		t.Errorf("empty receiver paths should be valid: %v", err)
	}
	// Missing-leading-slash is rejected for each receiver.
	for _, tc := range []struct{ y, field string }{
		{"webhook:\n  path: \"tailscale/webhook\"\n", "webhook.path"},
		{"streaming:\n  path: \"services/collector/event\"\n", "streaming.path"},
		{"webhook:\n  path: \"/has space\"\n", "webhook.path"},
	} {
		err := loadErr(t, tc.y)
		if err == nil {
			t.Errorf("path %q should be rejected", tc.y)
			continue
		}
		if !strings.Contains(err.Error(), tc.field) {
			t.Errorf("error %q should name %s", err.Error(), tc.field)
		}
	}
}

// TestValidateRequiresTailnet pins that single-tailnet mode needs a tailnet name:
// the default ("-") and an explicit name are accepted, but an explicit empty
// tailscale.tailnet is rejected at load time (rather than failing later in
// tsapi.NewClient).
func TestValidateRequiresTailnet(t *testing.T) {
	if err := loadErr(t, "tailscale:\n  tailnet: \"\"\n"); err == nil {
		t.Fatalf("empty tailscale.tailnet should be rejected")
	} else if !strings.Contains(err.Error(), "tailscale.tailnet") {
		t.Errorf("error %q should mention tailscale.tailnet", err.Error())
	}
	// Default ("-" from defaults) and an explicit name are fine.
	if err := loadErr(t, "log_level: info\n"); err != nil {
		t.Errorf("default tailnet (\"-\") should be valid: %v", err)
	}
	if err := loadErr(t, "tailscale:\n  tailnet: \"example.com\"\n"); err != nil {
		t.Errorf("explicit tailnet should be valid: %v", err)
	}
}

func TestValidateRejectsBadAuthMethod(t *testing.T) {
	err := loadErr(t, "tailscale:\n  auth:\n    method: magic\n")
	if err == nil {
		t.Fatalf("expected error for bad auth.method, got nil")
	}
	if !strings.Contains(err.Error(), "method") {
		t.Errorf("error %q should mention method", err.Error())
	}
}

func TestValidateRejectsBadCollectorSource(t *testing.T) {
	err := loadErr(t, "collectors:\n  flowlogs:\n    source: telepathy\n")
	if err == nil {
		t.Fatalf("expected error for bad collector source, got nil")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("error %q should mention source", err.Error())
	}
	if !strings.Contains(err.Error(), "flowlogs") {
		t.Errorf("error %q should name the offending collector (flowlogs)", err.Error())
	}
}

func TestValidateAcceptsAllCollectorSources(t *testing.T) {
	for _, src := range []string{"poll", "stream", "both"} {
		// source=stream needs a live ingestion path (#52a), so enable the receiver
		// for all three (poll/both just warn about dual ingestion, not error).
		y := "streaming:\n  enabled: true\ncollectors:\n  flowlogs:\n    source: " + src + "\n"
		if err := loadErr(t, y); err != nil {
			t.Errorf("source %q should be valid: %v", src, err)
		}
	}
}

func TestValidateRejectsBadCheckpointStore(t *testing.T) {
	err := loadErr(t, "checkpoint:\n  store: redis\n")
	if err == nil {
		t.Fatalf("expected error for bad checkpoint.store, got nil")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Errorf("error %q should mention store", err.Error())
	}
}

func TestValidateRejectsBadLogMode(t *testing.T) {
	err := loadErr(t, "collectors:\n  flowlogs:\n    log_mode: per_galaxy\n")
	if err == nil {
		t.Fatalf("expected error for bad flowlogs.log_mode, got nil")
	}
	if !strings.Contains(err.Error(), "log_mode") {
		t.Errorf("error %q should mention log_mode", err.Error())
	}
}

func TestValidateAcceptsAllLogModes(t *testing.T) {
	for _, m := range []string{"per_connection", "per_record", "off"} {
		if err := loadErr(t, "collectors:\n  flowlogs:\n    log_mode: "+m+"\n"); err != nil {
			t.Errorf("log_mode %q should be valid: %v", m, err)
		}
	}
}

func TestValidateRejectsBadPostureLogMode(t *testing.T) {
	err := loadErr(t, "collectors:\n  devices:\n    posture_log_mode: hourly\n")
	if err == nil {
		t.Fatalf("expected error for bad devices.posture_log_mode, got nil")
	}
	if !strings.Contains(err.Error(), "posture_log_mode") {
		t.Errorf("error %q should mention posture_log_mode", err.Error())
	}
}

func TestValidateAcceptsAllPostureLogModes(t *testing.T) {
	for _, m := range []string{"changes", "always", "off"} {
		if err := loadErr(t, "collectors:\n  devices:\n    posture_log_mode: "+m+"\n"); err != nil {
			t.Errorf("posture_log_mode %q should be valid: %v", m, err)
		}
	}
}

func TestValidateRejectsBadDecompress(t *testing.T) {
	err := loadErr(t, "streaming:\n  decompress: bzip2\n")
	if err == nil {
		t.Fatalf("expected error for bad streaming.decompress, got nil")
	}
	if !strings.Contains(err.Error(), "decompress") {
		t.Errorf("error %q should mention decompress", err.Error())
	}
}

func TestValidateAcceptsAllDecompress(t *testing.T) {
	for _, d := range []string{"auto", "gzip", "zstd", "none"} {
		if err := loadErr(t, "streaming:\n  decompress: "+d+"\n"); err != nil {
			t.Errorf("decompress %q should be valid: %v", d, err)
		}
	}
}

// Sources are optional on collectors that don't use them; an empty source must
// be accepted (e.g. the users/keys/settings collectors).
func TestValidateAllowsEmptyCollectorSource(t *testing.T) {
	if err := loadErr(t, "collectors:\n  users:\n    interval: 120s\n"); err != nil {
		t.Errorf("empty source on users collector should be valid: %v", err)
	}
}

func TestValidateAutoConfigureRequiresEnabled(t *testing.T) {
	const y = "streaming:\n  enabled: false\n  auto_configure: true\n  public_url: https://recv.example\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: auto_configure without enabled")
	}
	if !strings.Contains(err.Error(), "auto_configure") || !strings.Contains(err.Error(), "enabled") {
		t.Errorf("error %q should mention auto_configure + enabled", err.Error())
	}
}

func TestValidateAutoConfigureRequiresPublicURL(t *testing.T) {
	const y = "streaming:\n  enabled: true\n  auto_configure: true\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: auto_configure without public_url")
	}
	if !strings.Contains(err.Error(), "public_url") {
		t.Errorf("error %q should mention public_url", err.Error())
	}
}

func TestValidateAutoConfigureValidWhenComplete(t *testing.T) {
	const y = "streaming:\n  enabled: true\n  auto_configure: true\n  public_url: https://recv.example/services/collector/event\n  token: tok\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("complete auto_configure should be valid: %v", err)
	}
}

func TestValidateNodeMetricsRequiresTargetURL(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    targets:\n      - instance: nodeA\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: enabled node_metrics target without url")
	}
	if !strings.Contains(err.Error(), "node_metrics") || !strings.Contains(err.Error(), "url") {
		t.Errorf("error %q should mention node_metrics + url", err.Error())
	}
}

func TestValidateNodeMetricsDisabledIgnoresTargets(t *testing.T) {
	// A disabled scraper with an empty-URL target must not fail validation.
	const y = "collectors:\n  node_metrics:\n    enabled: false\n    targets:\n      - instance: nodeA\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("disabled node_metrics should not validate targets: %v", err)
	}
}

func TestValidateNodeMetricsValidWithURL(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    targets:\n      - url: http://100.64.0.1:5252/metrics\n        instance: nodeA\n        labels: { role: relay }\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("node_metrics with a url should be valid: %v", err)
	}
}

func TestValidateNodeMetricsDiscoveryRejectsBadScheme(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    discovery:\n      enabled: true\n      scheme: ftp\n"
	err := loadErr(t, y)
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("err = %v, want a scheme error", err)
	}
}

func TestValidateNodeMetricsDiscoveryRejectsBadPort(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    discovery:\n      enabled: true\n      port: 70000\n"
	err := loadErr(t, y)
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("err = %v, want a port error", err)
	}
}

func TestValidateNodeMetricsDiscoveryRejectsBadAddressOrder(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    discovery:\n      enabled: true\n      address_order: ipv9\n"
	err := loadErr(t, y)
	if err == nil || !strings.Contains(err.Error(), "address_order") {
		t.Fatalf("err = %v, want an address_order error", err)
	}
}

func TestValidateNodeMetricsDiscoveryRejectsBadInstanceSource(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    discovery:\n      enabled: true\n      instance_source: fqdn\n"
	err := loadErr(t, y)
	if err == nil || !strings.Contains(err.Error(), "instance_source") {
		t.Fatalf("err = %v, want an instance_source error", err)
	}
}

func TestValidateNodeMetricsDiscoveryEnabledZeroTargetsValid(t *testing.T) {
	// Discovery enabled with NO static targets is valid: discovery is another way
	// to obtain targets.
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    targets: []\n    discovery:\n      enabled: true\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("discovery-enabled with zero static targets should be valid: %v", err)
	}
}

func TestValidateNodeMetricsRejectsBadLimits(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "max_response_bytes",
			yaml: "collectors:\n  node_metrics:\n    enabled: true\n    max_response_bytes: 0\n    targets:\n      - url: http://x:5252/metrics\n",
			want: "max_response_bytes",
		},
		{
			name: "max_samples",
			yaml: "collectors:\n  node_metrics:\n    enabled: true\n    max_samples: 0\n    targets:\n      - url: http://x:5252/metrics\n",
			want: "max_samples",
		},
		{
			name: "discovery_max_targets",
			yaml: "collectors:\n  node_metrics:\n    enabled: true\n    discovery:\n      enabled: true\n      max_targets: 0\n",
			want: "max_targets",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadErr(t, tc.yaml)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention %s", err, tc.want)
			}
		})
	}
}

func TestValidateNodeMetricsRejectsBadMetricAllowRegex(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    targets:\n      - url: http://x:5252/metrics\n    metric_allow:\n      - \"node_(\"\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: invalid regex in metric_allow")
	}
	if !strings.Contains(err.Error(), "metric_allow") {
		t.Errorf("error %q should mention metric_allow", err.Error())
	}
}

func TestValidateNodeMetricsRejectsBadMetricDenyRegex(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    targets:\n      - url: http://x:5252/metrics\n    metric_deny:\n      - \"*\"\n"
	err := loadErr(t, y)
	if err == nil {
		t.Fatal("expected error: invalid regex in metric_deny")
	}
	if !strings.Contains(err.Error(), "metric_deny") {
		t.Errorf("error %q should mention metric_deny", err.Error())
	}
}

func TestValidateNodeMetricsValidFilters(t *testing.T) {
	const y = "collectors:\n  node_metrics:\n    enabled: true\n    targets:\n      - url: http://x:5252/metrics\n    metric_allow:\n      - \"node_.*\"\n    metric_deny:\n      - \"node_secret_.*\"\n    drop_labels:\n      - region\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("valid metric filters should be accepted: %v", err)
	}
}

func TestValidateNodeMetricsDisabledIgnoresBadRegex(t *testing.T) {
	// Filter validation is gated on Enabled: a disabled scraper with an invalid
	// regex must not fail validation.
	const y = "collectors:\n  node_metrics:\n    enabled: false\n    metric_allow:\n      - \"node_(\"\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("disabled node_metrics should not validate metric_allow regex: %v", err)
	}
}

func TestValidateReverseDNSRejectsBadServer(t *testing.T) {
	const y = "enrichment:\n  reverse_dns:\n    enabled: true\n    server: not-an-ip\n"
	err := loadErr(t, y)
	if err == nil || !strings.Contains(err.Error(), "reverse_dns.server") {
		t.Fatalf("err = %v, want a reverse_dns.server error", err)
	}
}

func TestValidateReverseDNSAcceptsIPServers(t *testing.T) {
	for _, s := range []string{"", "10.0.0.53", "1.1.1.1:53", "2001:db8::1"} {
		y := "enrichment:\n  reverse_dns:\n    enabled: true\n    server: \"" + s + "\"\n"
		if err := loadErr(t, y); err != nil {
			t.Errorf("reverse_dns.server %q should be valid: %v", s, err)
		}
	}
}

func TestValidateReverseDNSRejectsZeroMaxEntries(t *testing.T) {
	const y = "enrichment:\n  reverse_dns:\n    enabled: true\n    max_entries: 0\n"
	err := loadErr(t, y)
	if err == nil || !strings.Contains(err.Error(), "max_entries") {
		t.Fatalf("err = %v, want a max_entries error", err)
	}
}

func TestValidateReverseDNSDisabledIgnoresBadServer(t *testing.T) {
	// Validation is gated on enabled: a disabled block with a bad server is fine.
	const y = "enrichment:\n  reverse_dns:\n    enabled: false\n    server: not-an-ip\n"
	if err := loadErr(t, y); err != nil {
		t.Errorf("disabled reverse_dns should not validate server: %v", err)
	}
}

// The advisory fires only when reverse_dns.enabled=true AND
// cardinality.flow.node_dims=true (the sole combination where PTR names inflate
// flow-METRIC cardinality). With node_dims=false the names land on flow LOGS
// only, so there must be no warning; and an operator who has sized metric_limit
// can set acknowledge_cardinality=true to silence it.
func TestWarnings_ReverseDNSCardinality(t *testing.T) {
	c := config.Default()
	if w := c.Warnings(); len(w) != 0 {
		t.Fatalf("default Warnings = %v, want none", w)
	}

	// enabled + node_dims=true (default) + not acknowledged => advisory.
	c.Enrichment.ReverseDNS.Enabled = true
	if !c.Cardinality.Flow.NodeDims {
		t.Fatal("precondition: node_dims should default to true")
	}
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "reverse_dns") || !strings.Contains(strings.ToLower(w), "cardinalit") {
		t.Errorf("reverse_dns warning %q should mention reverse_dns and cardinality", w)
	}

	// enabled + node_dims=false => names on logs only, no metric cardinality cost => no advisory.
	c = config.Default()
	c.Enrichment.ReverseDNS.Enabled = true
	c.Cardinality.Flow.NodeDims = false
	if w := strings.Join(c.Warnings(), "\n"); strings.Contains(w, "reverse_dns.enabled=true") {
		t.Errorf("reverse_dns with node_dims=false must NOT warn; got %q", w)
	}

	// enabled + node_dims=true + acknowledge_cardinality=true => silenced.
	c = config.Default()
	c.Enrichment.ReverseDNS.Enabled = true
	c.Enrichment.ReverseDNS.AcknowledgeCardinality = true
	if w := strings.Join(c.Warnings(), "\n"); strings.Contains(w, "reverse_dns.enabled=true") {
		t.Errorf("acknowledge_cardinality=true must silence the advisory; got %q", w)
	}
}

func TestValidateRejectsBadFlowMetricsMode(t *testing.T) {
	err := loadErr(t, "cardinality:\n  flow:\n    metrics_mode: telepathy\n")
	if err == nil || !strings.Contains(err.Error(), "metrics_mode") {
		t.Fatalf("err = %v, want a metrics_mode error", err)
	}
}

func TestValidateAcceptsAllFlowMetricsModes(t *testing.T) {
	for _, m := range []string{"all", "rollup", "both"} {
		if err := loadErr(t, "cardinality:\n  flow:\n    metrics_mode: "+m+"\n"); err != nil {
			t.Errorf("flow.metrics_mode %q should be valid: %v", m, err)
		}
	}
}

func TestValidateRejectsNegativeRollupTopN(t *testing.T) {
	err := loadErr(t, "cardinality:\n  flow:\n    rollup_top_n: -1\n")
	if err == nil || !strings.Contains(err.Error(), "rollup_top_n") {
		t.Fatalf("err = %v, want a rollup_top_n error", err)
	}
}

func TestValidateAcceptsZeroRollupTopN(t *testing.T) {
	// 0 is valid and selects the in-code default at construction time.
	if err := loadErr(t, "cardinality:\n  flow:\n    rollup_top_n: 0\n"); err != nil {
		t.Errorf("cardinality.flow.rollup_top_n: 0 should be valid (selects default): %v", err)
	}
}

func TestVersionChecksDefaults(t *testing.T) {
	c := config.Default()
	if !c.VersionChecks.Self.Enabled {
		t.Error("version_checks.self.enabled should default true")
	}
	if !c.VersionChecks.Devices.Enabled {
		t.Error("version_checks.devices.enabled should default true")
	}
	if c.VersionChecks.Devices.OutdatedMinorThreshold != 3 {
		t.Errorf("outdated_minor_threshold default = %d want 3", c.VersionChecks.Devices.OutdatedMinorThreshold)
	}
	if c.VersionChecks.CacheTTL.D() != time.Hour {
		t.Errorf("cache_ttl default = %s want 1h", c.VersionChecks.CacheTTL.D())
	}
	if c.VersionChecks.Timeout.D() != 10*time.Second {
		t.Errorf("timeout default = %s want 10s", c.VersionChecks.Timeout.D())
	}
}

func TestVersionChecksValidate(t *testing.T) {
	c := config.Default()
	c.VersionChecks.CacheTTL = config.Duration(time.Minute) // below 5m floor
	if err := c.Validate(); err == nil {
		t.Error("cache_ttl below floor: want error")
	}

	c = config.Default()
	c.VersionChecks.Devices.OutdatedMinorThreshold = 0
	if err := c.Validate(); err == nil {
		t.Error("outdated_minor_threshold < 1: want error")
	}

	c = config.Default()
	c.VersionChecks.Timeout = config.Duration(0)
	if err := c.Validate(); err == nil {
		t.Error("timeout <= 0: want error")
	}
}

func TestVersionChecksWarning(t *testing.T) {
	c := config.Default()
	c.Collectors.Devices.Enabled = false // devices check on but collector off
	got := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(got, "version_checks.devices") {
		t.Errorf("expected devices-check-without-collector warning, got: %s", got)
	}
}

// TestValidate_TracingSampler pins the validation of the tracing.sampler enum and
// tracing.sampler_arg range checks.
func TestValidate_TracingSampler(t *testing.T) {
	cfg := config.Default()
	cfg.Tracing.Enabled = true
	cfg.Tracing.Sampler = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid tracing.sampler")
	}
	cfg.Tracing.Sampler = "traceidratio"
	cfg.Tracing.SamplerArg = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for tracing.sampler_arg out of [0,1]")
	}
	cfg.Tracing.SamplerArg = 0.5
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid tracing config rejected: %v", err)
	}
}

// TestWarnings_TracingZeroRatio pins the advisory that fires when tracing is
// enabled with a ratio sampler set to 0 (no spans will be recorded).
func TestWarnings_TracingZeroRatio(t *testing.T) {
	c := config.Default()
	// Default (disabled tracing) should produce no tracing advisory.
	for _, w := range c.Warnings() {
		if strings.Contains(w, "tracing") {
			t.Fatalf("default config should not warn about tracing; got %q", w)
		}
	}
	// Enabled with parentbased_traceidratio + arg=0 should warn.
	c.Tracing.Enabled = true
	c.Tracing.Sampler = "parentbased_traceidratio"
	c.Tracing.SamplerArg = 0
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "no spans will be recorded") {
		t.Errorf("zero ratio should warn about no spans; got %q", w)
	}
	// Non-zero ratio: no warning.
	c.Tracing.SamplerArg = 0.5
	for _, warn := range c.Warnings() {
		if strings.Contains(warn, "no spans will be recorded") {
			t.Errorf("non-zero ratio should not warn about no spans; got %q", warn)
		}
	}
}

func TestValidateHeadscaleRequiresURLAndKey(t *testing.T) {
	c := config.Default()
	c.Provider = "headscale"
	c.Headscale = config.HeadscaleConfig{} // missing url + api_key
	if err := c.Validate(); err == nil {
		t.Fatal("expected error: headscale requires url + api_key")
	}
	c.Headscale.URL = "https://hs.example.org"
	c.Headscale.APIKey = "k"
	if err := c.Validate(); err != nil {
		t.Fatalf("valid headscale config should pass: %v", err)
	}
}

func TestValidateRejectsUnknownProvider(t *testing.T) {
	c := config.Default()
	c.Provider = "wireguard"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestWarnsOnUnsupportedCollectorUnderHeadscale(t *testing.T) {
	c := config.Default()
	c.Provider = "headscale"
	c.Headscale = config.HeadscaleConfig{URL: "https://h", APIKey: "k"}
	c.Collectors.Flowlogs.Enabled = true // unsupported on headscale
	found := false
	for _, w := range c.Warnings() {
		if strings.Contains(w, "flowlogs") && strings.Contains(w, "headscale") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning about flowlogs unsupported under headscale; got %v", c.Warnings())
	}
}

func TestPrometheusOpenWildcardWarning(t *testing.T) {
	c := config.Default()
	c.Prometheus.Enabled = true
	c.Prometheus.Listen = ":2112" // wildcard (empty host)
	c.Prometheus.Auth.Token = ""
	found := false
	for _, w := range c.Warnings() {
		if strings.Contains(w, "prometheus.listen") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected open-wildcard prometheus warning, got %v", c.Warnings())
	}
	// A token silences it.
	c.Prometheus.Auth.Token = config.Secret("x")
	for _, w := range c.Warnings() {
		if strings.Contains(w, "prometheus.listen") {
			t.Errorf("token set but warning still present: %q", w)
		}
	}
}

func TestPrometheusListenConflict(t *testing.T) {
	c := config.Default()
	c.Admin.Enabled = true
	c.Admin.Listen = ":9090"
	c.Prometheus.Enabled = true
	c.Prometheus.Listen = ":9090"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "prometheus.listen") {
		t.Fatalf("expected admin/prometheus listen-conflict error, got %v", err)
	}
	// Distinct listeners: the conflict error must be gone (other unrelated Validate
	// rules are out of scope here, so assert only that THIS error disappears).
	c.Prometheus.Listen = ":2112"
	if err := c.Validate(); err != nil && strings.Contains(err.Error(), "prometheus.listen") {
		t.Fatalf("distinct listeners still report a prometheus.listen conflict: %v", err)
	}
}

func TestWarnsOnNodeMetricsWithUnlimitedCardinality(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.NodeMetrics.Enabled = true
	cfg.Cardinality.MetricLimit = 0 // unlimited
	if !slices.ContainsFunc(cfg.Warnings(), func(w string) bool {
		return strings.Contains(w, "node_metrics") && strings.Contains(w, "metric_limit")
	}) {
		t.Errorf("want node_metrics/metric_limit advisory, got %q", cfg.Warnings())
	}
	cfg.Cardinality.MetricLimit = 10000
	if slices.ContainsFunc(cfg.Warnings(), func(w string) bool {
		return strings.Contains(w, "node_metrics") && strings.Contains(w, "metric_limit")
	}) {
		t.Errorf("limit set: advisory must not fire")
	}
}

// TestValidateOTLPMetricIntervalMustBePositive pins the hard error that fires
// when otlp.metric_interval is zero or negative. A zero-duration interval
// passed to time.NewTicker panics at runtime, so Validate() must catch it.
func TestValidateOTLPMetricIntervalMustBePositive(t *testing.T) {
	cases := []struct {
		name  string
		value time.Duration
	}{
		{"zero", 0},
		{"negative", -1 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.OTLP.MetricInterval = config.Duration(tc.value)
			err := c.Validate()
			if err == nil {
				t.Fatalf("metric_interval=%v: expected Validate() error, got nil", tc.value)
			}
			if !strings.Contains(err.Error(), "metric_interval") {
				t.Errorf("error %q should mention metric_interval", err.Error())
			}
		})
	}

	// A positive value must not error.
	c := config.Default()
	c.OTLP.MetricInterval = config.Duration(30 * time.Second)
	if err := c.Validate(); err != nil {
		t.Errorf("positive metric_interval should be valid: %v", err)
	}
}

// --- Issue #52: config validation gaps ---

// TestValidate_StreamSourceNeedsStreaming pins #52(a): source: stream with the
// stream receiver disabled has no ingestion path, and source: stream is dead in
// multi-tailnet mode. Both must be rejected; source: both stays valid.
func TestValidate_StreamSourceNeedsStreaming(t *testing.T) {
	c := config.Default()
	c.Collectors.Auditlogs.Source = "stream"
	c.Streaming.Enabled = false
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "auditlogs") {
		t.Fatalf("source=stream + streaming disabled: want error naming auditlogs, got %v", err)
	}
	// With streaming enabled it is valid.
	c.Streaming.Enabled = true
	if err := c.Validate(); err != nil {
		t.Errorf("source=stream + streaming enabled: unexpected error %v", err)
	}
	// Multi-tailnet: source=stream rejected even though streaming.enabled can't be on.
	// (Leave the default top-level tailscale block — Tailnet "-" coexists with the
	// list — so the top-level auth-method check passes and we reach the source rule.)
	c = config.Default()
	c.Tailnets = []config.TailnetConfig{
		{Name: "a.example.com", Auth: config.TailscaleAuth{Method: "oauth"}},
		{Name: "b.example.com", Auth: config.TailscaleAuth{Method: "oauth"}},
	}
	c.Collectors.Flowlogs.Source = "stream"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "multi-tailnet") {
		t.Fatalf("source=stream in multi mode: want multi-tailnet error, got %v", err)
	}
	// source: both is not affected.
	c = config.Default()
	c.Collectors.Flowlogs.Source = "both"
	if err := c.Validate(); err != nil {
		t.Errorf("source=both: unexpected error %v", err)
	}
}

// TestValidate_OTLPHTTPEndpointShape pins #52(b): under protocol: http a
// scheme-less host:port endpoint silently zeroes the exporter, so it must be
// rejected the way the grpc shape is.
func TestValidate_OTLPHTTPEndpointShape(t *testing.T) {
	c := config.Default()
	c.OTLP.Protocol = "http"
	c.OTLP.Endpoint = "otlp-gateway-prod-us-central-0.grafana.net:443" // grpc shape
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "otlp.endpoint") {
		t.Fatalf("grpc-shaped endpoint under http: want otlp.endpoint error, got %v", err)
	}
	c.OTLP.Endpoint = "https://otlp-gateway-prod-us-central-0.grafana.net/otlp"
	if err := c.Validate(); err != nil {
		t.Errorf("valid http URL endpoint: unexpected error %v", err)
	}
	c.OTLP.Endpoint = ""
	if err := c.Validate(); err == nil {
		t.Errorf("empty endpoint under http: want error, got nil")
	}
}

// TestValidate_WindowTiming pins #52(c)/(d): a zero/negative initial_lookback
// permanently stalls a window collector, and a negative lag skips records.
func TestValidate_WindowTiming(t *testing.T) {
	c := config.Default()
	c.Collectors.Flowlogs.InitialLookback = config.Duration(0)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "initial_lookback") {
		t.Fatalf("initial_lookback=0: want error, got %v", err)
	}
	c = config.Default()
	c.Collectors.Auditlogs.Lag = config.Duration(-1)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "lag") {
		t.Fatalf("negative lag: want error, got %v", err)
	}
}

// TestWarnings_MaxWindowLEInterval pins #52(e): a positive max_window <= interval
// never catches up; a zero max_window (no-cap sentinel) must NOT warn.
func TestWarnings_MaxWindowLEInterval(t *testing.T) {
	c := config.Default()
	c.Collectors.Flowlogs.Interval = config.Duration(2 * time.Hour)
	c.Collectors.Flowlogs.MaxWindow = config.Duration(1 * time.Hour)
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "flowlogs") || !strings.Contains(w, "max_window") {
		t.Fatalf("max_window <= interval: want a flowlogs max_window warning, got %q", w)
	}
	// max_window=0 (no cap) must not warn.
	c.Collectors.Flowlogs.MaxWindow = config.Duration(0)
	for _, ww := range c.Warnings() {
		if strings.Contains(ww, "max_window") {
			t.Errorf("max_window=0 (no cap) should not warn; got %q", ww)
		}
	}
}

// TestValidate_ListenerCollisions pins #52(f): all four HTTP listeners are
// bind-collision checked, not just admin/prometheus.
func TestValidate_ListenerCollisions(t *testing.T) {
	c := config.Default()
	c.Streaming.Enabled = true
	c.Webhook.Enabled = true
	c.Streaming.Listen = ":8088"
	c.Webhook.Listen = ":8088"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "streaming.listen") || !strings.Contains(err.Error(), "webhook.listen") {
		t.Fatalf("streaming/webhook listen collision: want error naming both, got %v", err)
	}
}

// TestWarnings_PartialTailnetCredential pins #52(g): a tailnet with exactly one
// half of an OAuth pair set gets an advisory; a both-empty block does not (creds
// legitimately come from env at runtime).
func TestWarnings_PartialTailnetCredential(t *testing.T) {
	c := config.Default()
	c.Tailscale.Auth.Method = "oauth"
	c.Tailscale.Auth.OAuth.ClientID = "id-only"
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "client_secret") {
		t.Fatalf("client_id set, secret empty: want advisory about client_secret, got %q", w)
	}
	// Both empty: no advisory (env-supplied at runtime).
	c = config.Default()
	c.Tailscale.Auth.Method = "oauth"
	for _, ww := range c.Warnings() {
		if strings.Contains(ww, "client_secret") || strings.Contains(ww, "client_id") {
			t.Errorf("both-empty oauth should not warn; got %q", ww)
		}
	}
}

// TestWarnings_HeadscaleReceiversUnsupported pins #117: under provider=headscale,
// streaming/webhook/auto_configure are unsupported and must be flagged, and the
// single-tailnet receiver guard is no longer skipped for headscale.
func TestWarnings_HeadscaleReceiversUnsupported(t *testing.T) {
	c := config.Default()
	c.Provider = "headscale"
	c.Headscale.URL = "https://hs.example.com"
	c.Headscale.APIKey = "hs-key"
	c.Streaming.Enabled = true
	c.Streaming.AutoConfigure = true
	c.Streaming.PublicURL = "https://public.example.com" // auto_configure needs it to pass Validate
	c.Webhook.Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatalf("headscale + receivers should validate (warn, not error): %v", err)
	}
	w := strings.Join(c.Warnings(), "\n")
	for _, want := range []string{"streaming.enabled", "webhook.enabled", "streaming.auto_configure"} {
		if !strings.Contains(w, want) {
			t.Errorf("headscale warnings missing %q; got:\n%s", want, w)
		}
	}
}

// TestValidate_LogLevelEnum pins issue #52-adjacent #106: log_level is documented
// and framed as a validated enum, so Validate() must reject a value outside
// debug/info/warn/error (matching the page's stated Convention) rather than
// silently failing open to info in parseLevel.
func TestValidate_LogLevelEnum(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error"} {
		c := config.Default()
		c.LogLevel = lvl
		if err := c.Validate(); err != nil {
			t.Errorf("log_level=%q: unexpected error %v", lvl, err)
		}
	}
	c := config.Default()
	c.LogLevel = "warning" // the classic wrong spelling
	err := c.Validate()
	if err == nil {
		t.Fatalf("log_level=warning: want a validation error, got nil")
	}
	if !strings.Contains(err.Error(), "log_level") {
		t.Errorf("error %q should name log_level", err)
	}
}

// TestWarnings_FlowMetricsModeBoth pins the advisory that both-mode emits the raw
// AND rollup families, so summing them in PromQL double-counts. The default
// (rollup) and all-mode do not warn.
func TestWarnings_FlowMetricsModeBoth(t *testing.T) {
	c := config.Default()
	for _, w := range c.Warnings() {
		if strings.Contains(w, "metrics_mode") {
			t.Fatalf("default (rollup) should not warn about flow.metrics_mode; got %q", w)
		}
	}
	c.Cardinality.Flow.MetricsMode = "both"
	w := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(w, "flow.metrics_mode=both") {
		t.Fatalf("both mode should warn naming flow.metrics_mode=both; got %q", w)
	}
	if !strings.Contains(strings.ToLower(w), "double-count") {
		t.Errorf("both-mode warning should explain double-counting; got %q", w)
	}
}
