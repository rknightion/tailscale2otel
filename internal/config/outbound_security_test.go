package config

import (
	"strings"
	"testing"
)

func TestCredentialBearingOriginsRequireTLSOrLoopback(t *testing.T) {
	t.Run("headscale", func(t *testing.T) {
		c := Default()
		c.Provider = "headscale"
		c.Headscale.URL = "http://headscale.example.com"
		c.Headscale.APIKey = Secret("secret")
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("Validate = %v, want HTTPS error", err)
		}
	})

	t.Run("annotations", func(t *testing.T) {
		c := Default()
		c.GrafanaAnnotations.URL = "http://grafana.example.com"
		c.GrafanaAnnotations.Token = Secret("secret")
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("Validate = %v, want HTTPS error", err)
		}
	})

	t.Run("node metrics", func(t *testing.T) {
		c := Default()
		c.Collectors.NodeMetrics.Enabled = true
		c.Collectors.NodeMetrics.Targets = []NodeMetricsTarget{{
			URL: "http://node.example.com/metrics", BearerToken: Secret("secret"),
		}}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("Validate = %v, want HTTPS error", err)
		}
	})
}

func TestMalformedURLDiagnosticsOmitRawCredentialCanary(t *testing.T) {
	const canary = "URL-CREDENTIAL-CANARY"
	c := Default()
	c.GrafanaAnnotations.URL = "https://[" + canary
	c.GrafanaAnnotations.Token = Secret("secret")
	err := c.Validate()
	if err == nil {
		t.Fatal("malformed URL accepted")
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("validation error leaked raw URL: %v", err)
	}
}
