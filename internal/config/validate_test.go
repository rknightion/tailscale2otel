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
