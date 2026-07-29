package telemetry_test

import (
	"context"
	"io"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry/pii"
)

// promProviderSet builds a ProviderSet with the Prometheus pull reader enabled
// and one data point emitted per provider, so every registry has content.
func promProviderSet(t *testing.T, baseInstance string, tailnets []telemetry.PerTailnetOptions) *telemetry.ProviderSet {
	t.Helper()
	ctx := context.Background()
	ps, err := telemetry.NewProviderSet(ctx, telemetry.Options{
		ServiceName:       "tailscale2otel",
		Provider:          "tailscale",
		InstanceID:        baseInstance,
		PrometheusEnabled: true,
		Protocol:          "stdout",
		StdoutWriter:      io.Discard,
	}, tailnets)
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(ctx) })

	ps.Process().Emitter().Counter("tailscale2otel.export.datapoints", "1", "d", 1, nil)
	for _, tn := range tailnets {
		ps.Tailnet(tn.Name).Emitter().Gauge("tailscale.devices.count", "1", "d", 1, nil)
	}
	return ps
}

// targetInfoSeries returns the label sets of every target_info series in mfs.
func targetInfoSeries(mfs []*dto.MetricFamily) []map[string]string {
	var out []map[string]string
	for _, mf := range mfs {
		if mf.GetName() != "target_info" {
			continue
		}
		for _, m := range mf.Metric {
			labels := make(map[string]string, len(m.Label))
			for _, lp := range m.Label {
				labels[lp.GetName()] = lp.GetValue()
			}
			out = append(out, labels)
		}
	}
	return out
}

// TestPromGathererSingleTailnetGathersWithoutError pins #519. In single-tailnet
// mode instanceFor keeps the base service.instance.id for the one tailnet
// provider, so the process provider and the tailnet provider are built from a
// byte-identical Resource and each exporter emits an identical target_info.
// A plain prometheus.Gatherers merge reports that as a duplicate-series error on
// EVERY scrape; under the endpoint's ContinueOnError that is a permanent WARN
// that also buries any genuine collision behind it.
func TestPromGathererSingleTailnetGathersWithoutError(t *testing.T) {
	ps := promProviderSet(t, "host-1", []telemetry.PerTailnetOptions{
		{Name: "solo.example.com", InstanceID: "host-1"}, // instanceFor: single-tailnet keeps base
	})

	g := ps.PromGatherer()
	if g == nil {
		t.Fatal("PromGatherer() is nil with PrometheusEnabled")
	}
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if got := targetInfoSeries(mfs); len(got) != 1 {
		t.Fatalf("target_info series = %d, want 1: %v", len(got), got)
	}
}

// TestPromGathererMultiTailnetKeepsEveryTargetInfo is the other half of #519:
// the deduplication must collapse only IDENTICAL series. Multi-tailnet gives
// each tailnet a distinct service.instance.id, so its target_info rows differ
// and every one of them must survive the merge.
func TestPromGathererMultiTailnetKeepsEveryTargetInfo(t *testing.T) {
	ps := promProviderSet(t, "host-1", []telemetry.PerTailnetOptions{
		{Name: "alpha.example.com", InstanceID: "host-1/alpha.example.com"},
		{Name: "beta.example.com", InstanceID: "host-1/beta.example.com"},
	})

	mfs, err := ps.PromGatherer().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	got := targetInfoSeries(mfs)
	want := map[string]bool{
		"host-1":                   false,
		"host-1/alpha.example.com": false,
		"host-1/beta.example.com":  false,
	}
	if len(got) != len(want) {
		t.Fatalf("target_info series = %d, want %d: %v", len(got), len(want), got)
	}
	for _, labels := range got {
		id := labels["service_instance_id"]
		seen, known := want[id]
		if !known {
			t.Errorf("unexpected target_info instance %q", id)
			continue
		}
		if seen {
			t.Errorf("duplicate target_info for instance %q", id)
		}
		want[id] = true
	}
}

// TestPromGathererStillReportsRealCollisions pins the NARROWNESS of the #519
// fix. Collapsing target_info is safe because a duplicate carries no extra
// information. Every other duplicate does: with pii_filter.tailnet_name=false
// the tailscale.tailnet distinguisher is gone, so the process and tailnet
// providers emit the same self-obs counter under one identity and the second
// value is dropped (#103). That is real data loss, and the gather error is the
// only thing that says so — widening the dedupe to all metric names would
// silence it.
func TestPromGathererStillReportsRealCollisions(t *testing.T) {
	ctx := context.Background()
	ps, err := telemetry.NewProviderSet(ctx, telemetry.Options{
		ServiceName: "tailscale2otel", Provider: "tailscale", InstanceID: "host-1",
		PrometheusEnabled: true, Protocol: "stdout", StdoutWriter: io.Discard,
		PIIFilter: pii.Categories{pii.CatTailnetName: false},
	}, []telemetry.PerTailnetOptions{{Name: "solo.example.com", InstanceID: "host-1"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(ctx) })

	ps.Process().Emitter().Counter("tailscale2otel.export.datapoints", "1", "d", 1, nil)
	ps.Tailnet("solo.example.com").Emitter().Counter("tailscale2otel.export.datapoints", "1", "d", 7, nil)

	mfs, err := ps.PromGatherer().Gather()
	if err == nil {
		t.Fatal("colliding self-obs counters gathered without error; the #103 warning has been silenced")
	}
	if strings.Contains(err.Error(), "target_info") {
		t.Errorf("target_info is still reported as a duplicate: %v", err)
	}
	// The scrape must still serve what it could gather, including the one
	// surviving target_info — ContinueOnError depends on it.
	if got := targetInfoSeries(mfs); len(got) != 1 {
		t.Fatalf("target_info series = %d, want 1: %v", len(got), got)
	}
}

// TestPromGathererDisabled: no Prometheus reader means no gatherer at all, so
// app.New knows not to stand the pull endpoint up.
func TestPromGathererDisabled(t *testing.T) {
	ctx := context.Background()
	ps, err := telemetry.NewProviderSet(ctx, telemetry.Options{
		ServiceName: "tailscale2otel", Protocol: "stdout", StdoutWriter: io.Discard,
	}, []telemetry.PerTailnetOptions{{Name: "solo", InstanceID: "host-1"}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(ctx) })
	if g := ps.PromGatherer(); g != nil {
		t.Fatalf("PromGatherer() = %v, want nil when the Prometheus reader is off", g)
	}
}
