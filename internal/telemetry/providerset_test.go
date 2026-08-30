package telemetry_test

import (
	"context"
	"io"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

func TestProviderSetPerTailnetResource(t *testing.T) {
	base := telemetry.Options{ServiceName: "tailscale2otel", Protocol: "stdout"}
	ps, err := telemetry.NewProviderSet(context.Background(), base, []telemetry.PerTailnetOptions{
		{Name: "acme.example.com", InstanceID: "host/acme.example.com"},
		{Name: "beta.example.com", InstanceID: "host/beta.example.com"},
	})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(context.Background()) })

	if ps.Process() == nil {
		t.Fatal("Process() provider is nil")
	}
	got := ps.TailnetNames()
	if len(got) != 2 || got[0] != "acme.example.com" || got[1] != "beta.example.com" {
		t.Fatalf("TailnetNames = %v", got)
	}
	if ps.Tailnet("acme.example.com") == nil {
		t.Fatal("Tailnet(acme) is nil")
	}
	if ps.Tailnet("missing") != nil {
		t.Fatal("Tailnet(missing) should be nil")
	}
	if ps.Process().Emitter() == nil {
		t.Fatal("process Emitter is nil")
	}
	if ps.Tailnet("acme.example.com").Emitter() == nil {
		t.Fatal("tailnet Emitter is nil")
	}
}

func TestNewProviderSetNoTailnets(t *testing.T) {
	base := telemetry.Options{ServiceName: "tailscale2otel", Protocol: "stdout"}
	ps, err := telemetry.NewProviderSet(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(context.Background()) })
	if ps.Process() == nil {
		t.Fatal("Process() provider is nil")
	}
	if len(ps.TailnetNames()) != 0 {
		t.Fatalf("TailnetNames = %v, want empty", ps.TailnetNames())
	}
}

// TestProviderSetPerTailnetCardinalityLimitsAreAttributable drives two real
// providers through their SDK limit and self-observation paths. Alpha reaches
// its own lower limit while beta remains below its higher limit; both limit and
// overflow self-observation series must retain the tailnet signal attribute.
func TestProviderSetPerTailnetCardinalityLimitsAreAttributable(t *testing.T) {
	ctx := context.Background()
	ps, err := telemetry.NewProviderSet(ctx, telemetry.Options{
		ServiceName:       "tailscale2otel",
		Provider:          "tailscale",
		Protocol:          "stdout",
		StdoutWriter:      io.Discard,
		PrometheusEnabled: true,
		SelfObsEnabled:    true,
		CardinalityLimit:  4,
	}, []telemetry.PerTailnetOptions{
		{Name: "alpha", InstanceID: "test/alpha", CardinalityLimit: 2},
		{Name: "beta", InstanceID: "test/beta"}, // zero inherits the base limit
	})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(ctx) })

	for _, name := range ps.TailnetNames() {
		points := make([]telemetry.GaugePoint, 0, 3)
		for _, id := range []string{"one", "two", "three"} {
			points = append(points, telemetry.GaugePoint{Value: 1, Attrs: telemetry.Attrs{"id": id}})
		}
		ps.Tailnet(name).Emitter().GaugeSnapshot("tailscale.test.cardinality", "1", "test", points)
		ps.Tailnet(name).Cardinality().Report(ps.Tailnet(name).Emitter())
	}

	mfs, err := ps.PromGatherer().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	limit := metricLabels(mfs, "tailscale2otel_series_limit")
	if limit["alpha"] != 2 || limit["beta"] != 4 {
		t.Fatalf("series.limit by tailnet = %v, want alpha=2 beta=4", limit)
	}
	overflow := metricLabelsBySource(mfs, "tailscale2otel_series_overflowing_ratio", "tailscale.test.cardinality")
	if overflow["alpha"] != 1 || overflow["beta"] != 0 {
		t.Fatalf("series.overflowing by tailnet = %v, want alpha=1 beta=0", overflow)
	}
}

// TestProviderSetNegativeCardinalityLimitIsUnlimited verifies that a negative
// per-tailnet value is not mistaken for inheritance: it suppresses the limit
// and keeps the overflowing self-observation series at zero.
func TestProviderSetNegativeCardinalityLimitIsUnlimited(t *testing.T) {
	ctx := context.Background()
	ps, err := telemetry.NewProviderSet(ctx, telemetry.Options{
		ServiceName:       "tailscale2otel",
		Provider:          "tailscale",
		Protocol:          "stdout",
		StdoutWriter:      io.Discard,
		PrometheusEnabled: true,
		SelfObsEnabled:    true,
		CardinalityLimit:  2,
	}, []telemetry.PerTailnetOptions{{Name: "unlimited", InstanceID: "test/unlimited", CardinalityLimit: -1}})
	if err != nil {
		t.Fatalf("NewProviderSet: %v", err)
	}
	t.Cleanup(func() { _ = ps.Shutdown(ctx) })

	points := make([]telemetry.GaugePoint, 0, 3)
	for _, id := range []string{"one", "two", "three"} {
		points = append(points, telemetry.GaugePoint{Value: 1, Attrs: telemetry.Attrs{"id": id}})
	}
	ps.Tailnet("unlimited").Emitter().GaugeSnapshot("tailscale.test.cardinality", "1", "test", points)
	ps.Tailnet("unlimited").Cardinality().Report(ps.Tailnet("unlimited").Emitter())

	mfs, err := ps.PromGatherer().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if limit := metricLabels(mfs, "tailscale2otel_series_limit"); len(limit) != 0 {
		t.Fatalf("series.limit = %v, want none for an unlimited tailnet", limit)
	}
	overflow := metricLabelsBySource(mfs, "tailscale2otel_series_overflowing_ratio", "tailscale.test.cardinality")
	if overflow["unlimited"] != 0 {
		t.Fatalf("series.overflowing for unlimited tailnet = %v, want 0", overflow)
	}
}

func metricLabels(mfs []*dto.MetricFamily, family string) map[string]float64 {
	out := map[string]float64{}
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, metric := range mf.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "tailscale_tailnet" {
					out[label.GetValue()] = metric.GetGauge().GetValue()
				}
			}
		}
	}
	return out
}

func metricLabelsBySource(mfs []*dto.MetricFamily, family, source string) map[string]float64 {
	out := map[string]float64{}
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, metric := range mf.Metric {
			var tailnet string
			matches := false
			for _, label := range metric.Label {
				switch label.GetName() {
				case "tailscale_tailnet":
					tailnet = label.GetValue()
				case "metric_name":
					matches = label.GetValue() == source
				}
			}
			if matches && tailnet != "" {
				out[tailnet] = metric.GetGauge().GetValue()
			}
		}
	}
	return out
}
