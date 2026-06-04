package app

import (
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
)

func TestInstanceID_ExplicitOverridesHostname(t *testing.T) {
	cfg := config.Default()
	cfg.SelfObservability.InstanceID = "inst-explicit"
	if got := instanceID(cfg); got != "inst-explicit" {
		t.Fatalf("instanceID = %q, want inst-explicit", got)
	}
}

func TestInstanceID_FallsBackToHostname(t *testing.T) {
	cfg := config.Default()
	cfg.SelfObservability.InstanceID = ""
	host, _ := os.Hostname()
	if got := instanceID(cfg); got != host {
		t.Fatalf("instanceID = %q, want hostname %q", got, host)
	}
}

func TestTelemetryOptions_InstanceIDWired(t *testing.T) {
	cfg := config.Default()
	cfg.SelfObservability.InstanceID = "inst-99"
	if got := telemetryOptions(cfg, "v1").InstanceID; got != "inst-99" {
		t.Fatalf("telemetryOptions InstanceID = %q, want inst-99", got)
	}
}

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

func TestFlowOptions_PortToggles(t *testing.T) {
	// Defaults: every metric port/service toggle off.
	if def := flowOptions(config.Default()); def.IncludeSourcePort || def.IncludeDestinationPort || def.IncludeDestinationService {
		t.Fatalf("defaults = src %v / dst %v / service %v, want all false",
			def.IncludeSourcePort, def.IncludeDestinationPort, def.IncludeDestinationService)
	}

	// Legacy flow_include_ports enables BOTH ports (back-compat).
	legacy := config.Default()
	legacy.Cardinality.FlowIncludePorts = true
	if got := flowOptions(legacy); !got.IncludeSourcePort || !got.IncludeDestinationPort {
		t.Fatalf("flow_include_ports=true => src %v / dst %v, want both true", got.IncludeSourcePort, got.IncludeDestinationPort)
	}

	// Independent source-only and destination-only.
	srcOnly := config.Default()
	srcOnly.Cardinality.FlowSourcePort = true
	if got := flowOptions(srcOnly); !got.IncludeSourcePort || got.IncludeDestinationPort {
		t.Fatalf("flow_source_port=true => src %v / dst %v, want true/false", got.IncludeSourcePort, got.IncludeDestinationPort)
	}
	dstOnly := config.Default()
	dstOnly.Cardinality.FlowDestinationPort = true
	if got := flowOptions(dstOnly); got.IncludeSourcePort || !got.IncludeDestinationPort {
		t.Fatalf("flow_destination_port=true => src %v / dst %v, want false/true", got.IncludeSourcePort, got.IncludeDestinationPort)
	}

	// Service toggle maps through.
	svc := config.Default()
	svc.Cardinality.FlowDestinationService = true
	if got := flowOptions(svc); !got.IncludeDestinationService {
		t.Fatal("flow_destination_service=true => IncludeDestinationService false, want true")
	}
}
