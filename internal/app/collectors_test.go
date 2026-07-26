package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// TestWebhookOptionsPlumbsTolerance guards the config->receiver wiring for the
// replay-protection window: the webhook Tolerance must flow from config into the
// server's Options. Without it the staleness check is permanently disabled.
func TestWebhookOptionsPlumbsTolerance(t *testing.T) {
	got := webhookOptions(config.WebhookConfig{
		Listen:    ":8089",
		Path:      "/tailscale/webhook",
		Secret:    "s",
		Tolerance: config.Duration(7 * time.Minute),
		TLS: config.StreamingTLS{
			CertFile: "/run/tls/tls.crt",
			KeyFile:  "/run/tls/tls.key",
		},
	})
	if got.Tolerance != 7*time.Minute {
		t.Fatalf("webhookOptions Tolerance = %v, want 7m (webhook.tolerance must be plumbed)", got.Tolerance)
	}
	if got.TLSCertFile != "/run/tls/tls.crt" || got.TLSKeyFile != "/run/tls/tls.key" {
		t.Fatalf("webhookOptions TLS = (%q, %q), want configured cert/key",
			got.TLSCertFile, got.TLSKeyFile)
	}
}

func TestFlowOptionsPlumbsTrustedReporterPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.Collectors.Flowlogs.TrustedReporterNodeIDs = []string{"node-a"}
	cfg.Collectors.Flowlogs.TrustedReporterTags = []string{"tag:router"}

	got := flowOptions(cfg)
	if len(got.TrustedReporterNodeIDs) != 1 || got.TrustedReporterNodeIDs[0] != "node-a" {
		t.Fatalf("flowOptions TrustedReporterNodeIDs = %v, want [node-a]", got.TrustedReporterNodeIDs)
	}
	if len(got.TrustedReporterTags) != 1 || got.TrustedReporterTags[0] != "tag:router" {
		t.Fatalf("flowOptions TrustedReporterTags = %v, want [tag:router]", got.TrustedReporterTags)
	}
}

// registeredNames lists what registerCollectors put on a runtime's registry.
func registeredNames(a *App) []string {
	var out []string
	for _, e := range a.runtimes[0].registry.Entries() {
		out = append(out, e.Collector.Name())
	}
	return out
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// The objectstore collector is only registered when it is asked for. Registering
// it by default would have every deployment probing a bucket that does not
// exist.
func TestRegisterCollectors_ObjectStoreIsOptIn(t *testing.T) {
	a := baseTestApp(t, objectStoreTestConfig(nil), "http://127.0.0.1:0", telemetrytest.New())
	if names := registeredNames(a); hasName(names, "objectstore") {
		t.Errorf("collectors = %v, want no objectstore with source=poll", names)
	}
}

func TestRegisterCollectors_ObjectStoreSourceRegistersIt(t *testing.T) {
	cfg := objectStoreTestConfig(func(c *config.Config) { c.Collectors.Flowlogs.Source = "objectstore" })
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	names := registeredNames(a)
	if !hasName(names, "objectstore") {
		t.Errorf("collectors = %v, want objectstore registered", names)
	}
	// With the poller off, the feature gauge would otherwise disappear; the
	// lightweight probe keeps reporting it.
	if !hasName(names, "flowlogs-feature") {
		t.Errorf("collectors = %v, want the flow-log feature still reported", names)
	}
}

// objectStoreTestConfig is a valid single-tailnet config pointed at an export
// bucket.
func objectStoreTestConfig(tune func(*config.Config)) *config.Config {
	c := config.Default()
	c.Tailscale.Tailnet = "example.com"
	c.Collectors.Flowlogs.ObjectStore.Endpoint = "https://s3.eu-west-2.amazonaws.com"
	c.Collectors.Flowlogs.ObjectStore.Region = "eu-west-2"
	c.Collectors.Flowlogs.ObjectStore.Bucket = "flows"
	if tune != nil {
		tune(c)
	}
	return c
}
