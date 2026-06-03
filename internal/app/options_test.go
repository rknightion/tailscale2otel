package app

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
)

func TestTelemetryOptions_GrafanaCloudBasicAuth(t *testing.T) {
	cfg := config.Default()
	cfg.OTLP.Protocol = "http"
	cfg.OTLP.GrafanaCloud.InstanceID = "12345"
	cfg.OTLP.GrafanaCloud.Token = "secrettoken"

	opts := telemetryOptions(cfg, "v1.2.3")

	if opts.Protocol != "http" {
		t.Fatalf("protocol = %q, want http", opts.Protocol)
	}
	if opts.ServiceVersion != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3", opts.ServiceVersion)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("12345:secrettoken"))
	if got := opts.Headers["Authorization"]; got != want {
		t.Fatalf("auth header = %q, want %q", got, want)
	}
}

func TestTsapiOptions_APIKey(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Tailscale.Auth.Method = "apikey"
	cfg.Tailscale.Auth.APIKey = "tskey-api-xxx"

	o := tsapiOptions(cfg)
	if o.Tailnet != "example.com" || o.APIKey != "tskey-api-xxx" {
		t.Fatalf("options = %+v", o)
	}
	if o.OAuthClientID != "" {
		t.Fatal("oauth client id should be empty for apikey method")
	}
}

func TestTsapiOptions_OAuth(t *testing.T) {
	cfg := config.Default()
	cfg.Tailscale.Auth.Method = "oauth"
	cfg.Tailscale.Auth.OAuth.ClientID = "cid"
	cfg.Tailscale.Auth.OAuth.ClientSecret = "csec"

	o := tsapiOptions(cfg)
	if o.OAuthClientID != "cid" || o.OAuthClientSecret != "csec" {
		t.Fatalf("options = %+v", o)
	}
	if o.APIKey != "" {
		t.Fatal("apikey should be empty for oauth method")
	}
}

func TestNodeMetricsOptions_AuthAndTLS(t *testing.T) {
	nm := config.NodeMetricsConfig{
		Targets: []config.NodeMetricsTarget{{
			URL:             "https://n:5252/metrics",
			BearerToken:     "tok",
			BearerTokenFile: "/f",
			Headers:         map[string]string{"X-Scope-OrgID": "1"},
			TLS: &config.NodeMetricsTargetTLS{
				InsecureSkipVerify: true, CAFile: "/ca", CertFile: "/c", KeyFile: "/k", ServerName: "n",
			},
		}},
	}
	tg := nodeMetricsOptions(nm, nil).Targets[0]
	if tg.BearerToken != "tok" || tg.BearerTokenFile != "/f" {
		t.Errorf("bearer = %q/%q", tg.BearerToken, tg.BearerTokenFile)
	}
	if tg.Headers["X-Scope-OrgID"] != "1" {
		t.Errorf("headers = %v", tg.Headers)
	}
	if tg.TLS == nil || !tg.TLS.InsecureSkipVerify || tg.TLS.CAFile != "/ca" ||
		tg.TLS.CertFile != "/c" || tg.TLS.KeyFile != "/k" || tg.TLS.ServerName != "n" {
		t.Errorf("tls = %+v", tg.TLS)
	}
}

func TestNodeMetricsOptions_DiscoveryWired(t *testing.T) {
	nm := config.Default().Collectors.NodeMetrics
	nm.Discovery.Enabled = true
	nm.Discovery.Interval = config.Duration(2 * time.Minute)

	opts := nodeMetricsOptions(nm, &fakeDevicesAPI{})
	if opts.Discoverer == nil {
		t.Fatal("Discoverer = nil, want a discoverer when discovery is enabled")
	}
	if opts.DiscoveryInterval != 2*time.Minute {
		t.Fatalf("DiscoveryInterval = %v, want 2m", opts.DiscoveryInterval)
	}

	nm.Discovery.Enabled = false
	if got := nodeMetricsOptions(nm, &fakeDevicesAPI{}); got.Discoverer != nil {
		t.Fatal("Discoverer != nil, want nil when discovery is disabled")
	}
}

func TestFlowOptions_MaxLogRecordsPerWindow(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.Flowlogs.MaxLogRecordsPerWindow = 250
	if got := flowOptions(cfg).MaxLogRecordsPerWindow; got != 250 {
		t.Fatalf("flowOptions MaxLogRecordsPerWindow = %d, want 250", got)
	}
}
