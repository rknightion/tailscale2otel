package app

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/config"
)

// TestStatusPage_PerTailnetIdentityInListMode pins #116: in multi-tailnet mode the
// status page must read each runtime's tailnets[] auth method (not the unused
// top-level tailscale: block), attribute collectors to their tailnet, and the
// config summary must reflect the resolved runtime auth.
func TestStatusPage_PerTailnetIdentityInListMode(t *testing.T) {
	cfg := config.Default()
	cfg.OTLP.Protocol = "stdout"
	cfg.Tailscale.Auth.Method = "oauth" // top-level (ignored in multi mode)
	cfg.Tailnets = []config.TailnetConfig{
		{Name: "keyed.example.com", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k"}},
		{Name: "oauthed.example.com", Auth: config.TailscaleAuth{Method: "oauth",
			OAuth: config.OAuthConfig{ClientID: "id", ClientSecret: "sec"}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg.Validate: %v", err)
	}
	a, err := New(context.Background(), cfg, "v", slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if a.restore != nil {
			a.restore()
		}
		if a.shutdown != nil {
			_ = a.shutdown(context.Background())
		}
	})

	now := time.Now()
	byName := map[string]string{}
	for _, ts := range a.tailnetStatuses(now) {
		byName[ts.Name] = ts.AuthMethod
	}
	if byName["keyed.example.com"] != "apikey" {
		t.Errorf("keyed tailnet AuthMethod = %q, want apikey (read the tailnets[] entry, not top-level)", byName["keyed.example.com"])
	}
	if byName["oauthed.example.com"] != "oauth" {
		t.Errorf("oauthed tailnet AuthMethod = %q, want oauth", byName["oauthed.example.com"])
	}
	// Combined collector list carries tailnet attribution.
	sawTailnet := false
	for _, cs := range a.collectorStatuses(now) {
		if cs.Tailnet != "" {
			sawTailnet = true
			break
		}
	}
	if !sawTailnet {
		t.Error("CollectorStatus entries lack Tailnet attribution in multi-tailnet mode")
	}
	// Config summary reflects the resolved primary runtime's auth, not the top-level block.
	if got := a.redactedConfigSummary().AuthMethod; got != "apikey" {
		t.Errorf("config summary AuthMethod = %q, want apikey (primary runtime), not the top-level oauth", got)
	}
}

// TestNew_HeadscaleRuntimeHasNoAliasedSelfObs pins #54: under provider=headscale
// the single runtime shares the PROCESS provider's emitter, so it must be built
// with nil card/exportStats. Otherwise the per-runtime self-obs reporters ran a
// second pair over the same tracker/stats as the process-level reporters,
// inflating export.* ~2x and corrupting series.active. Exercised through the real
// New() path (not the newApp nil-hook seam).
func TestNew_HeadscaleRuntimeHasNoAliasedSelfObs(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "headscale"
	cfg.Headscale.URL = "https://headscale.invalid"
	cfg.Headscale.APIKey = "hs-key"
	cfg.OTLP.Protocol = "stdout" // no network egress during the test
	cfg.SelfObservability.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg.Validate: %v", err)
	}

	a, err := New(context.Background(), cfg, "v-test", slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatalf("New (headscale): %v", err)
	}
	t.Cleanup(func() {
		if a.restore != nil {
			a.restore()
		}
		if a.shutdown != nil {
			_ = a.shutdown(context.Background())
		}
	})

	if len(a.runtimes) != 1 {
		t.Fatalf("headscale runtimes = %d, want 1", len(a.runtimes))
	}
	rt := a.runtimes[0]
	if rt.card != nil {
		t.Error("headscale runtime.card must be nil (process provider covers its self-obs); a non-nil aliased tracker double-reports series.active")
	}
	if rt.exportStats != nil {
		t.Error("headscale runtime.exportStats must be nil; a non-nil aliased stats func double-reports export.datapoints/log_records")
	}
	// The process-level tracker still exists and is what the emitter feeds, so
	// self-obs is covered exactly once.
	if a.procCard == nil {
		t.Error("process cardinality tracker is nil with self_observability enabled")
	}
}

// discard is a no-op io.Writer for the test logger.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestNew_MultiTailnetStaticTargetsOnlyOnPrimary pins #59: a process-global static
// node_metrics target must be registered on the first runtime only in multi-tailnet
// mode, not once per tailnet (which would scrape + emit it N times).
func TestNew_MultiTailnetStaticTargetsOnlyOnPrimary(t *testing.T) {
	cfg := config.Default()
	cfg.OTLP.Protocol = "stdout"
	cfg.Tailnets = []config.TailnetConfig{
		{Name: "a.example.com", Auth: config.TailscaleAuth{Method: "oauth",
			OAuth: config.OAuthConfig{ClientID: "id", ClientSecret: "sec"}}},
		{Name: "b.example.com", Auth: config.TailscaleAuth{Method: "oauth",
			OAuth: config.OAuthConfig{ClientID: "id", ClientSecret: "sec"}}},
	}
	cfg.Collectors.NodeMetrics.Enabled = true
	cfg.Collectors.NodeMetrics.Targets = []config.NodeMetricsTarget{{URL: "http://192.0.2.10:5252/metrics"}}
	// discovery stays disabled (default), so non-primary runtimes have nothing to scrape.
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg.Validate: %v", err)
	}

	a, err := New(context.Background(), cfg, "v-test", slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if a.restore != nil {
			a.restore()
		}
		if a.shutdown != nil {
			_ = a.shutdown(context.Background())
		}
	})
	if len(a.runtimes) != 2 {
		t.Fatalf("runtimes = %d, want 2", len(a.runtimes))
	}
	if a.runtimes[0].nodeMetrics == nil {
		t.Error("primary runtime should own the static node_metrics collector")
	}
	if a.runtimes[1].nodeMetrics != nil {
		t.Error("non-primary runtime must NOT register the process-global static targets (discovery off)")
	}
}

// TestNew_MultiTailnetCredFailureNamesEntry pins #125: when a tailnets[] entry
// can't build its client (e.g. a mis-mounted secret), New's error must name the
// offending entry's index + name, not just a bare "no authentication configured".
func TestNew_MultiTailnetCredFailureNamesEntry(t *testing.T) {
	cfg := config.Default()
	cfg.OTLP.Protocol = "stdout"
	cfg.Tailnets = []config.TailnetConfig{
		{Name: "good.example.com", Auth: config.TailscaleAuth{Method: "oauth",
			OAuth: config.OAuthConfig{ClientID: "id", ClientSecret: "secret"}}},
		{Name: "bad.example.com", Auth: config.TailscaleAuth{Method: "oauth"}}, // no creds
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("cfg.Validate: %v", err)
	}

	_, err := New(context.Background(), cfg, "v-test", slog.New(slog.NewTextHandler(discard{}, nil)))
	if err == nil {
		t.Fatal("New should fail when a tailnets[] entry lacks credentials")
	}
	if !strings.Contains(err.Error(), "bad.example.com") || !strings.Contains(err.Error(), "tailnets[1]") {
		t.Errorf("error %q should name the offending entry (index 1, bad.example.com)", err)
	}
}
