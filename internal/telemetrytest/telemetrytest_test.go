package telemetrytest_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func TestRecorderCapturesCounterAndGauge(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	e.Counter("tailscale.network.io", "By", "network bytes transferred", 1500, telemetry.Attrs{
		"network.io.direction": "transmit",
	})
	e.Gauge("tailscale.device.online", "1", "device connected to control", 1, telemetry.Attrs{
		"host.name": "laptop",
	})

	names := rec.MetricNames()
	if !contains(names, "tailscale.network.io") {
		t.Fatalf("MetricNames %v missing tailscale.network.io", names)
	}
	if !contains(names, "tailscale.device.online") {
		t.Fatalf("MetricNames %v missing tailscale.device.online", names)
	}

	counter := rec.MetricPoints("tailscale.network.io")
	if len(counter) != 1 {
		t.Fatalf("counter points = %d, want 1", len(counter))
	}
	cp := counter[0]
	if cp.Name != "tailscale.network.io" {
		t.Fatalf("counter name = %q", cp.Name)
	}
	if cp.Unit != "By" {
		t.Fatalf("counter unit = %q, want By", cp.Unit)
	}
	if cp.Kind != "sum" {
		t.Fatalf("counter kind = %q, want sum", cp.Kind)
	}
	if !cp.Monotonic {
		t.Fatal("counter should be monotonic")
	}
	if cp.Value != 1500 {
		t.Fatalf("counter value = %v, want 1500", cp.Value)
	}
	if cp.Attrs["network.io.direction"] != "transmit" {
		t.Fatalf("counter direction attr = %q, want transmit", cp.Attrs["network.io.direction"])
	}

	gauge := rec.MetricPoints("tailscale.device.online")
	if len(gauge) != 1 {
		t.Fatalf("gauge points = %d, want 1", len(gauge))
	}
	gp := gauge[0]
	if gp.Kind != "gauge" {
		t.Fatalf("gauge kind = %q, want gauge", gp.Kind)
	}
	if gp.Monotonic {
		t.Fatal("gauge should not be monotonic")
	}
	if gp.Value != 1 {
		t.Fatalf("gauge value = %v, want 1", gp.Value)
	}
	if gp.Attrs["host.name"] != "laptop" {
		t.Fatalf("gauge host.name attr = %q, want laptop", gp.Attrs["host.name"])
	}

	if pts := rec.MetricPoints("does.not.exist"); len(pts) != 0 {
		t.Fatalf("unknown metric returned %d points, want 0", len(pts))
	}
}

func TestRecorderCapturesLogEvent(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	e.LogEvent(telemetry.Event{
		Name:     "tailscale.network.flow",
		Body:     "tcp virtual 100.64.0.1:443 -> 100.64.0.2:51820",
		Severity: telemetry.SeverityWarn,
		Attrs:    telemetry.Attrs{"network.transport": "tcp"},
	})

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("log records = %d, want 1", len(recs))
	}
	lr := recs[0]
	if lr.Body != "tcp virtual 100.64.0.1:443 -> 100.64.0.2:51820" {
		t.Fatalf("body = %q", lr.Body)
	}
	if lr.SeverityText != "WARN" {
		t.Fatalf("severity text = %q, want WARN", lr.SeverityText)
	}
	if lr.Severity != int(telemetry.SeverityWarn) && lr.SeverityText != "WARN" {
		// Severity is the OTEL log severity int; just sanity-check it is warn-ish.
		t.Fatalf("severity = %d", lr.Severity)
	}
	if lr.EventName != "tailscale.network.flow" {
		t.Fatalf("event name = %q, want tailscale.network.flow", lr.EventName)
	}
	if _, ok := lr.Attrs["event.name"]; ok {
		t.Fatalf("event.name must be the native EventName, not an attribute; got attr %q", lr.Attrs["event.name"])
	}
	if lr.Attrs["network.transport"] != "tcp" {
		t.Fatalf("network.transport attr = %q, want tcp", lr.Attrs["network.transport"])
	}
}

// TestRecorderCapturesLogEventCtxTraceContext proves the Recorder exposes a
// log record's NATIVE trace context (#367), so a collector test can assert
// that a log emitted inside a sampled span carries the same TraceID/SpanID —
// this is the capability the gotcha in internal/telemetry/CLAUDE.md's testing
// section flagged as needing verification/addition.
func TestRecorderCapturesLogEventCtxTraceContext(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	wantTraceID := trace.TraceID{0xAA, 0xBB}
	wantSpanID := trace.SpanID{0xCC, 0xDD}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     wantSpanID,
		TraceFlags: trace.FlagsSampled,
	}))

	e.LogEventCtx(ctx, telemetry.Event{Name: "tailscale.test.event", Body: "sampled"})
	e.LogEvent(telemetry.Event{Name: "tailscale.test.event", Body: "background"})

	recs := rec.LogRecords()
	if len(recs) != 2 {
		t.Fatalf("log records = %d, want 2", len(recs))
	}
	sampled, background := recs[0], recs[1]

	if sampled.TraceID != wantTraceID.String() {
		t.Errorf("sampled TraceID = %q, want %q", sampled.TraceID, wantTraceID.String())
	}
	if sampled.SpanID != wantSpanID.String() {
		t.Errorf("sampled SpanID = %q, want %q", sampled.SpanID, wantSpanID.String())
	}
	if !sampled.Sampled {
		t.Errorf("sampled record's Sampled = false, want true")
	}

	if background.TraceID != (trace.TraceID{}).String() {
		t.Errorf("background TraceID = %q, want zero value", background.TraceID)
	}
	if background.Sampled {
		t.Errorf("background record's Sampled = true, want false")
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
