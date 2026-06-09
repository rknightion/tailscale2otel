package app

import (
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
)

// TestInstanceID_PIIFilter covers the three cases for the hostname-redaction
// logic in instanceID:
//  1. Explicit InstanceID always wins regardless of PII filter settings.
//  2. Hostnames PII category enabled (true) → bare hostname returned unchanged.
//  3. Hostnames PII category disabled (false) → a stable 12-char lowercase hex
//     digest is returned instead of the hostname (non-reversible, deterministic).
func TestInstanceID_PIIFilter(t *testing.T) {
	host, _ := os.Hostname()

	t.Run("explicit_id_wins", func(t *testing.T) {
		cfg := config.Default()
		cfg.SelfObservability.InstanceID = "explicit-id"
		cfg.PIIFilter.Hostnames = false // even with redaction on, explicit wins
		if got := instanceID(cfg); got != "explicit-id" {
			t.Fatalf("explicit InstanceID = %q, want explicit-id", got)
		}
	})

	t.Run("hostnames_enabled_returns_hostname", func(t *testing.T) {
		cfg := config.Default()
		cfg.SelfObservability.InstanceID = ""
		cfg.PIIFilter.Hostnames = true
		if got := instanceID(cfg); got != host {
			t.Fatalf("hostnames enabled: instanceID = %q, want hostname %q", got, host)
		}
	})

	t.Run("hostnames_disabled_returns_hash", func(t *testing.T) {
		cfg := config.Default()
		cfg.SelfObservability.InstanceID = ""
		cfg.PIIFilter.Hostnames = false
		got := instanceID(cfg)
		// Must be 12 lowercase hex chars (not the raw hostname).
		if len(got) != 12 {
			t.Fatalf("hashed ID length = %d, want 12; got %q", len(got), got)
		}
		for _, c := range got {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("hashed ID %q contains non-hex char %q", got, c)
			}
		}
		if got == host {
			t.Fatalf("hashed ID %q must not equal the raw hostname %q", got, host)
		}
		// Must be deterministic.
		if got2 := instanceID(cfg); got2 != got {
			t.Fatalf("instanceID not deterministic: first=%q second=%q", got, got2)
		}
	})
}

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
	tg := nodeMetricsOptions(nm, nil, nil).Targets[0]
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

	opts := nodeMetricsOptions(nm, &fakeDevicesAPI{}, nil)
	if opts.Discoverer == nil {
		t.Fatal("Discoverer = nil, want a discoverer when discovery is enabled")
	}
	if opts.DiscoveryInterval != 2*time.Minute {
		t.Fatalf("DiscoveryInterval = %v, want 2m", opts.DiscoveryInterval)
	}

	nm.Discovery.Enabled = false
	if got := nodeMetricsOptions(nm, &fakeDevicesAPI{}, nil); got.Discoverer != nil {
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

func TestFlowOptions_RollupMode(t *testing.T) {
	// Default: bounded rollup mode with a top-N of 500.
	def := flowOptions(config.Default())
	if def.FlowMetricsMode != "rollup" {
		t.Fatalf("default FlowMetricsMode = %q, want rollup", def.FlowMetricsMode)
	}
	if def.RollupTopN != 500 {
		t.Fatalf("default RollupTopN = %d, want 500", def.RollupTopN)
	}
	// Overrides propagate from cardinality.flow config.
	cfg := config.Default()
	cfg.Cardinality.Flow.MetricsMode = "both"
	cfg.Cardinality.Flow.RollupTopN = 42
	if got := flowOptions(cfg); got.FlowMetricsMode != "both" || got.RollupTopN != 42 {
		t.Fatalf("flowOptions mode/topN = %q/%d, want both/42", got.FlowMetricsMode, got.RollupTopN)
	}
}

func TestFlowOptions_PortToggles(t *testing.T) {
	// Defaults: every metric port/service toggle off.
	if def := flowOptions(config.Default()); def.IncludeSourcePort || def.IncludeDestinationPort || def.IncludeDestinationService {
		t.Fatalf("defaults = src %v / dst %v / service %v, want all false",
			def.IncludeSourcePort, def.IncludeDestinationPort, def.IncludeDestinationService)
	}

	// Both granular toggles on => both ports (the explicit replacement for the
	// removed legacy flow_include_ports umbrella).
	both := config.Default()
	both.Cardinality.Flow.SourcePort = true
	both.Cardinality.Flow.DestinationPort = true
	if got := flowOptions(both); !got.IncludeSourcePort || !got.IncludeDestinationPort {
		t.Fatalf("cardinality.flow.source_port+cardinality.flow.destination_port => src %v / dst %v, want both true", got.IncludeSourcePort, got.IncludeDestinationPort)
	}

	// Independent source-only and destination-only.
	srcOnly := config.Default()
	srcOnly.Cardinality.Flow.SourcePort = true
	if got := flowOptions(srcOnly); !got.IncludeSourcePort || got.IncludeDestinationPort {
		t.Fatalf("cardinality.flow.source_port=true => src %v / dst %v, want true/false", got.IncludeSourcePort, got.IncludeDestinationPort)
	}
	dstOnly := config.Default()
	dstOnly.Cardinality.Flow.DestinationPort = true
	if got := flowOptions(dstOnly); got.IncludeSourcePort || !got.IncludeDestinationPort {
		t.Fatalf("cardinality.flow.destination_port=true => src %v / dst %v, want false/true", got.IncludeSourcePort, got.IncludeDestinationPort)
	}

	// Service toggle maps through.
	svc := config.Default()
	svc.Cardinality.Flow.DestinationService = true
	if got := flowOptions(svc); !got.IncludeDestinationService {
		t.Fatal("cardinality.flow.destination_service=true => IncludeDestinationService false, want true")
	}
}

func TestTSAPIOptionsForResolvedTailnet(t *testing.T) {
	rt := config.ResolvedTailnet{
		Name: "acme.example.com",
		Auth: config.TailscaleAuth{Method: "apikey", APIKey: "secret-key"},
		HTTP: config.TailscaleHTTPConfig{RateLimit: 5},
	}
	o := tsapiOptionsFor(rt)
	if o.Tailnet != "acme.example.com" {
		t.Errorf("Tailnet = %q", o.Tailnet)
	}
	if o.APIKey != "secret-key" {
		t.Errorf("APIKey not mapped")
	}
	if o.RateLimit != 5 {
		t.Errorf("RateLimit = %v, want 5", o.RateLimit)
	}
}

func TestInstanceForMultiAndSingle(t *testing.T) {
	if got := instanceFor("host", "acme", false); got != "host" {
		t.Errorf("single mode: instanceFor = %q, want host", got)
	}
	if got := instanceFor("host", "acme", true); got != "host/acme" {
		t.Errorf("multi mode: instanceFor = %q, want host/acme", got)
	}
	if got := instanceFor("", "acme", true); got != "acme" {
		t.Errorf("multi mode empty base: instanceFor = %q, want acme", got)
	}
}
