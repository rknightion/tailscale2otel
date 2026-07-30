package app

import (
	"context"
	"testing"
	"time"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/rknightion/tailscale2otel/v4/internal/collector"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/provider"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
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

// Configuration logs read from object storage register their OWN collector, under
// a distinct name so its scrape metrics and status row do not merge with flow's
// (#288). The flow poller/probe wiring is untouched.
func TestRegisterCollectors_AuditObjectStoreSourceRegistersItsOwnCollector(t *testing.T) {
	cfg := objectStoreTestConfig(func(c *config.Config) {
		c.Collectors.Auditlogs.Source = "objectstore"
		c.Collectors.Auditlogs.ObjectStore.Endpoint = "https://s3.eu-west-2.amazonaws.com"
		c.Collectors.Auditlogs.ObjectStore.Region = "eu-west-2"
		c.Collectors.Auditlogs.ObjectStore.Bucket = "config-logs"
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	names := registeredNames(a)
	if !hasName(names, "objectstore-audit") {
		t.Errorf("collectors = %v, want objectstore-audit registered", names)
	}
	// The audit POLLER must be gone: source selection is exclusive per signal, and
	// polling alongside the bucket would double-count every event.
	if hasName(names, "auditlogs") {
		t.Errorf("collectors = %v, want no auditlogs poller alongside the object store", names)
	}
}

// In multi-tailnet mode each runtime registers an objectstore collector against
// ITS OWN destination, on its own interval — one global feed read twice is the
// cross-attribution hazard #283 closed.
func TestRegisterCollectors_ObjectStorePerTailnetDestinations(t *testing.T) {
	cfg := multiTailnetObjectStoreCfg(nil)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	a := newAppShell(cfg, "vtest", nil, telemetrytest.New().Emitter(),
		tracenoop.NewTracerProvider().Tracer("test"),
		func(context.Context) error { return nil }, collector.NewMemoryStore())
	a.buildProcessDeps()
	a.addRuntime("alpha.example.com", telemetrytest.New().Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)
	a.addRuntime("beta.example.com", telemetrytest.New().Emitter(), nil, nil,
		provider.Tailscale(newTestClient(t, "http://127.0.0.1:0")), true)

	wantIntervals := map[string]time.Duration{
		"alpha.example.com": 90 * time.Second,
		"beta.example.com":  150 * time.Second,
	}
	for _, rt := range a.runtimes {
		var got time.Duration
		found := false
		for _, e := range rt.registry.Entries() {
			if e.Collector.Name() == "objectstore" {
				got, found = e.Interval, true
			}
		}
		if !found {
			t.Fatalf("tailnet %q registered no objectstore collector", rt.configuredName)
		}
		if want := wantIntervals[rt.configuredName]; got != want {
			t.Errorf("tailnet %q objectstore interval = %v, want %v from its own entry",
				rt.configuredName, got, want)
		}
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

// Every knob under enrichment.reverse_dns has to reach rdns.Options, or the
// setting is inert: the config validates, the docs describe it, the status page
// reports it, and it changes nothing. stale_ttl in particular fails silently —
// a zero StaleTTL is a legal value meaning "disabled", so a missed wiring looks
// exactly like an operator who turned it off (#297).
func TestRDNSOptions_CarriesEveryConfiguredValue(t *testing.T) {
	cfg := config.Default()
	cfg.Enrichment.ReverseDNS.Server = "10.0.0.53"
	cfg.Enrichment.ReverseDNS.Timeout = config.Duration(3 * time.Second)
	cfg.Enrichment.ReverseDNS.CacheTTL = config.Duration(12 * time.Hour)
	cfg.Enrichment.ReverseDNS.NegativeTTL = config.Duration(7 * time.Minute)
	cfg.Enrichment.ReverseDNS.StaleTTL = config.Duration(90 * time.Minute)
	cfg.Enrichment.ReverseDNS.MaxEntries = 1234

	got := rdnsOptions(cfg)
	for _, c := range []struct {
		key       string
		got, want any
	}{
		{"Server", got.Server, "10.0.0.53"},
		{"Timeout", got.Timeout, 3 * time.Second},
		{"TTL", got.TTL, 12 * time.Hour},
		{"NegativeTTL", got.NegativeTTL, 7 * time.Minute},
		{"StaleTTL", got.StaleTTL, 90 * time.Minute},
		{"MaxEntries", got.MaxEntries, 1234},
	} {
		if c.got != c.want {
			t.Errorf("rdns.Options.%s = %v, want %v", c.key, c.got, c.want)
		}
	}
}
