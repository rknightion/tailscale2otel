package config

import (
	"testing"
	"time"
)

func TestWave3ConfigFreezeDefaults(t *testing.T) {
	c := Default()
	if c.Tailscale.Organization != "" {
		t.Fatalf("tailscale.organization = %q, want empty", c.Tailscale.Organization)
	}
	if c.OTLP.MetricTemporality != "cumulative" || c.OTLP.OutageSummaryInterval.D() != 5*time.Minute {
		t.Fatalf("OTLP freeze defaults = %q/%v", c.OTLP.MetricTemporality, c.OTLP.OutageSummaryInterval.D())
	}
	if c.Collectors.Devices.SubrequestConcurrency != 1 || c.Collectors.Services.SubrequestConcurrency != 1 {
		t.Fatalf("subrequest concurrency = devices %d services %d, want 1/1",
			c.Collectors.Devices.SubrequestConcurrency, c.Collectors.Services.SubrequestConcurrency)
	}
	if c.Scheduler.InitialStaggerWindow.D() != 3*time.Second {
		t.Fatalf("scheduler.initial_stagger_window = %v, want 3s", c.Scheduler.InitialStaggerWindow.D())
	}
	if c.Collectors.LogStream.ConfigurationInterval.D() != 0 || c.Collectors.LogStream.NetworkInterval.D() != 0 {
		t.Fatal("per-type log-stream intervals must inherit by default")
	}
	if c.Checkpoint.WriteDebounce.D() != 0 {
		t.Fatal("checkpoint writes must remain synchronous by default")
	}
	if c.Streaming.PerRouteMaxConcurrentRequests != 0 || c.Webhook.PerRouteMaxConcurrentRequests != 0 {
		t.Fatal("receiver per-route admission must use automatic defaults")
	}
	if c.Admin.Auth.FailureLimit != 5 || c.Admin.SupportBundleLogTailRecords != 200 {
		t.Fatalf("admin freeze defaults = failure limit %d, log tail %d",
			c.Admin.Auth.FailureLimit, c.Admin.SupportBundleLogTailRecords)
	}
	if c.Enrichment.DeviceCacheStaleAfter.D() != 0 || c.Flows.Store.IncrementalVacuumInterval.D() != 0 {
		t.Fatal("staleness and vacuum must remain opt-in at freeze")
	}
}
