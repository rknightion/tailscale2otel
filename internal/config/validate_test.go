package config_test

import (
	"strings"
	"testing"

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
	for _, proto := range []string{"http", "grpc", "stdout"} {
		if err := loadErr(t, "otlp:\n  protocol: "+proto+"\n"); err != nil {
			t.Errorf("protocol %q should be valid: %v", proto, err)
		}
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
		if err := loadErr(t, "collectors:\n  flowlogs:\n    source: "+src+"\n"); err != nil {
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
