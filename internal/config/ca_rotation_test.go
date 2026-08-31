package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

func warningContains(warnings []string, substring string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, substring) {
			return true
		}
	}
	return false
}

// A watched CA bundle is picked up by the dynamic TLS reloader, but an active
// gRPC connection keeps using the old trust roots until it reconnects. The
// advisory must name the reconnection knob while leaving HTTP, disabled
// watching, and an explicitly configured period quiet.
func TestWarnings_GRPCCARotationNeedsReconnection(t *testing.T) {
	c := config.Default()
	c.OTLP.Protocol = "grpc"
	c.OTLP.TLS.CAFile = "/run/secrets/otlp-ca.pem"
	c.OTLP.CredentialReload.Enabled = true
	c.OTLP.GRPCReconnectionPeriod = config.Duration(0)

	warnings := c.Warnings()
	if !warningContains(warnings, "otlp.grpc_reconnection_period") {
		t.Fatalf("warnings = %v, want the gRPC CA-rotation reconnection advisory", warnings)
	}
	if !warningContains(warnings, "otlp.tls.ca_file") {
		t.Fatalf("warnings = %v, want the advisory to name the watched CA file", warnings)
	}

	c.OTLP.GRPCReconnectionPeriod = config.Duration(time.Minute)
	if warningContains(c.Warnings(), "otlp.grpc_reconnection_period") {
		t.Fatalf("warnings = %v, want no advisory once reconnection is configured", c.Warnings())
	}

	c.OTLP.GRPCReconnectionPeriod = config.Duration(0)
	c.OTLP.CredentialReload.Enabled = false
	if warningContains(c.Warnings(), "otlp.grpc_reconnection_period") {
		t.Fatalf("warnings = %v, want no advisory when CA watching is disabled", c.Warnings())
	}

	c.OTLP.CredentialReload.Enabled = true
	c.OTLP.Protocol = "http"
	if warningContains(c.Warnings(), "otlp.grpc_reconnection_period") {
		t.Fatalf("warnings = %v, want no gRPC advisory for HTTP", c.Warnings())
	}
}
