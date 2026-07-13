package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

// TestLoad_S4PhaseCFields pins the new optional config keys added for the
// session-4 features: the nodemetrics per-target auth/TLS, the flow-log volume
// cap, the stream body cap, and the webhook<->audit cross-dedup toggle.
func TestLoad_S4PhaseCFields(t *testing.T) {
	const y = `
collectors:
  flowlogs:
    max_log_records_per_window: 500
  node_metrics:
    enabled: true
    targets:
      - url: https://node.ts.net:5252/metrics
        bearer_token: tok
        bearer_token_file: /run/secrets/token
        headers:
          X-Scope-OrgID: "42"
        tls:
          insecure_skip_verify: true
          ca_file: /etc/ca.pem
          cert_file: /etc/client.pem
          key_file: /etc/client.key
          server_name: node.ts.net
streaming:
  max_body_bytes: 1048576
webhook:
  dedup_audit_events: true
`
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got := cfg.Collectors.Flowlogs.MaxLogRecordsPerWindow; got != 500 {
		t.Errorf("flowlogs.max_log_records_per_window = %d, want 500", got)
	}
	if got := cfg.Streaming.MaxBodyBytes; got != 1<<20 {
		t.Errorf("streaming.max_body_bytes = %d, want %d", got, 1<<20)
	}
	if !cfg.Webhook.DedupAuditEvents {
		t.Error("webhook.dedup_audit_events = false, want true")
	}

	if len(cfg.Collectors.NodeMetrics.Targets) != 1 {
		t.Fatalf("node_metrics targets = %d, want 1", len(cfg.Collectors.NodeMetrics.Targets))
	}
	tg := cfg.Collectors.NodeMetrics.Targets[0]
	if tg.BearerToken != "tok" || tg.BearerTokenFile != "/run/secrets/token" {
		t.Errorf("bearer = %q / %q", tg.BearerToken, tg.BearerTokenFile)
	}
	if tg.Headers["X-Scope-OrgID"] != "42" {
		t.Errorf("headers = %v", tg.Headers)
	}
	if tg.TLS == nil {
		t.Fatal("target tls = nil, want parsed")
	}
	if !tg.TLS.InsecureSkipVerify || tg.TLS.CAFile != "/etc/ca.pem" || tg.TLS.CertFile != "/etc/client.pem" ||
		tg.TLS.KeyFile != "/etc/client.key" || tg.TLS.ServerName != "node.ts.net" {
		t.Errorf("tls = %+v", tg.TLS)
	}
}
