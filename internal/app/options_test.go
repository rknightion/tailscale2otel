package app

import (
	"encoding/base64"
	"testing"

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
