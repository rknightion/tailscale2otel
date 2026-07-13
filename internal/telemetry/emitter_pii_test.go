package telemetry_test

import (
	"context"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetry/pii"
)

// newPIITestEmitter builds an Emitter wired to in-memory metric + log sinks, with
// the given PII categories applied.
func newPIITestEmitter(t *testing.T, cats pii.Categories) (telemetry.Emitter, *sdkmetric.ManualReader, *recordingLogExporter) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	exp := &recordingLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })
	e := telemetry.NewEmitterWithPII(mp.Meter("test"), lp.Logger("test"), cats)
	return e, reader, exp
}

// gaugeDataPoints collects and returns the data points for the named gauge metric,
// or nil if the metric is absent.
func gaugeDataPoints(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.DataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				g, ok := m.Data.(metricdata.Gauge[float64])
				if !ok {
					t.Fatalf("metric %q is %T, want Gauge", name, m.Data)
				}
				return g.DataPoints
			}
		}
	}
	return nil // metric not present
}

// counterDataPoints collects and returns the data points for the named counter metric.
func counterDataPoints(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.DataPoint[float64] {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				s, ok := m.Data.(metricdata.Sum[float64])
				if !ok {
					t.Fatalf("metric %q is %T, want Sum", name, m.Data)
				}
				return s.DataPoints
			}
		}
	}
	return nil
}

// allOnCats returns a Categories with every category enabled.
func allOnCats() pii.Categories {
	c := pii.Categories{}
	for _, cat := range pii.AllCategories {
		c[cat] = true
	}
	return c
}

// TestEmitterGaugeSuppressedWhenIdentityRedacted: when the sole identity attr
// (host.name → Hostnames) is redacted the datapoint must be suppressed entirely.
func TestEmitterGaugeSuppressedWhenIdentityRedacted(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatHostnames] = false

	e, reader, _ := newPIITestEmitter(t, cats)
	e.Gauge("tailscale.device.online", "1", "", 1, telemetry.Attrs{"host.name": "h1"})

	dps := gaugeDataPoints(t, reader, "tailscale.device.online")
	if len(dps) != 0 {
		t.Fatalf("expected gauge to be suppressed (0 datapoints), got %d", len(dps))
	}
}

// TestEmitterGaugeKeptWhenNonIdentityRedacted: when a non-identity attr (email)
// is redacted the gauge must still be emitted and the identity attrs kept.
func TestEmitterGaugeKeptWhenNonIdentityRedacted(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatEmails] = false

	e, reader, _ := newPIITestEmitter(t, cats)
	e.Gauge("tailscale.device.online", "1", "", 1, telemetry.Attrs{
		"host.name":      "h1",
		"host.id":        "n1",
		"tailscale.user": "a@b.com",
	})

	dps := gaugeDataPoints(t, reader, "tailscale.device.online")
	if len(dps) != 1 {
		t.Fatalf("expected 1 datapoint, got %d", len(dps))
	}
	dp := dps[0]
	if _, ok := dp.Attributes.Value("tailscale.user"); ok {
		t.Error("tailscale.user (email) must be dropped when emails category is off")
	}
	if _, ok := dp.Attributes.Value("host.name"); !ok {
		t.Error("host.name must be present (not an email)")
	}
	if _, ok := dp.Attributes.Value("host.id"); !ok {
		t.Error("host.id must be present (not an email)")
	}
}

// TestEmitterCounterMergesWhenLabelRedacted: with emails off, a counter with a
// tailscale.user attr must be emitted without that attr.
func TestEmitterCounterMergesWhenLabelRedacted(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatEmails] = false

	e, reader, _ := newPIITestEmitter(t, cats)
	e.Counter("tailscale.test.counter", "1", "", 1, telemetry.Attrs{
		"tailscale.user":       "a@b.com",
		"network.io.direction": "transmit",
	})

	dps := counterDataPoints(t, reader, "tailscale.test.counter")
	if len(dps) != 1 {
		t.Fatalf("expected 1 datapoint, got %d", len(dps))
	}
	dp := dps[0]
	if _, ok := dp.Attributes.Value("tailscale.user"); ok {
		t.Error("tailscale.user must be dropped when emails category is off")
	}
	if _, ok := dp.Attributes.Value("network.io.direction"); !ok {
		t.Error("network.io.direction (non-PII) must be kept")
	}
}

// TestEmitterLogDropsAttr: with emails off, a LogEvent with user.name
// must be emitted without that attr.
func TestEmitterLogDropsAttr(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatEmails] = false

	e, _, exp := newPIITestEmitter(t, cats)
	e.LogEvent(telemetry.Event{
		Name: "tailscale.audit.event",
		Body: "some action",
		Attrs: telemetry.Attrs{
			"user.name":              "a@b.com",
			"tailscale.audit.action": "update",
		},
	})

	recs := exp.all()
	if len(recs) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(recs))
	}
	attrs := logAttrs(recs[0])
	if _, ok := attrs["user.name"]; ok {
		t.Error("user.name (email) must be dropped when emails category is off")
	}
	if _, ok := attrs["tailscale.audit.action"]; !ok {
		t.Error("tailscale.audit.action (non-PII) must be kept")
	}
}

// TestEmitterDefaultAllOnUnchanged: nil categories (all-on) must pass labels through unchanged.
func TestEmitterDefaultAllOnUnchanged(t *testing.T) {
	e, reader, _ := newPIITestEmitter(t, nil)
	e.Gauge("tailscale.device.online", "1", "", 1, telemetry.Attrs{
		"host.name":      "h1",
		"tailscale.user": "a@b.com",
	})

	dps := gaugeDataPoints(t, reader, "tailscale.device.online")
	if len(dps) != 1 {
		t.Fatalf("expected 1 datapoint, got %d", len(dps))
	}
	if _, ok := dps[0].Attributes.Value("host.name"); !ok {
		t.Error("host.name must be kept when all categories are on")
	}
	if _, ok := dps[0].Attributes.Value("tailscale.user"); !ok {
		t.Error("tailscale.user must be kept when all categories are on")
	}
}
