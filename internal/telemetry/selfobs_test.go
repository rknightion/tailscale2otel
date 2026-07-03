package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestInstallExportErrorHandler_IgnoresInstrumentNameError pins #62: an
// instrument-name validation error (the SDK still returns a usable instrument, so
// nothing is dropped) must NOT be counted as an OTLP export failure, while a
// genuine export error still is.
func TestInstallExportErrorHandler_IgnoresInstrumentNameError(t *testing.T) {
	rec := telemetrytest.New()
	restore := telemetry.InstallExportErrorHandler(rec.Emitter(), nil)
	defer restore()

	otel.Handle(fmt.Errorf("instrument name %q invalid: %w", "foo:bar", sdkmetric.ErrInstrumentName))
	if points := rec.MetricPoints("tailscale2otel.export.failures"); len(points) != 0 {
		t.Fatalf("instrument-name error counted as export failure: %d points", len(points))
	}

	otel.Handle(errors.New("real export boom"))
	points := rec.MetricPoints("tailscale2otel.export.failures")
	if len(points) != 1 || points[0].Value != 1 {
		t.Fatalf("genuine export error not counted: %+v", points)
	}
	if got := points[0].Attrs["error.type"]; got != "export" {
		t.Fatalf("error.type = %q, want export", got)
	}
}

func TestInstallExportErrorHandler_CountsHandledErrors(t *testing.T) {
	rec := telemetrytest.New()

	restore := telemetry.InstallExportErrorHandler(rec.Emitter(), nil)
	defer restore()

	otel.Handle(errors.New("export boom"))

	points := rec.MetricPoints("tailscale2otel.export.failures")
	if len(points) != 1 {
		t.Fatalf("got %d data points, want 1", len(points))
	}
	p := points[0]
	if p.Kind != "sum" || !p.Monotonic {
		t.Fatalf("kind=%q monotonic=%v, want monotonic sum", p.Kind, p.Monotonic)
	}
	if p.Unit != "1" {
		t.Fatalf("unit = %q, want %q", p.Unit, "1")
	}
	if p.Value != 1 {
		t.Fatalf("value = %v, want 1", p.Value)
	}
	if got := p.Attrs["error.type"]; got != "export" {
		t.Fatalf("error.type = %q, want export", got)
	}
}

func TestInstallExportErrorHandler_LogsErrorBody(t *testing.T) {
	rec := telemetrytest.New()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	restore := telemetry.InstallExportErrorHandler(rec.Emitter(), logger)
	defer restore()

	// The OTLP exporter surfaces Grafana Cloud's HTTP 400 body in the error it
	// hands to otel.Handle; the handler must log it so the offending metric/label
	// is visible instead of being collapsed to a coarse counter.
	otel.Handle(errors.New(`failed to upload metrics: 400 Bad Request: duplicate label "tailscale_node"`))

	// The slog TextHandler escapes the quotes in the wrapped error, so assert on
	// the (escaping-agnostic) substrings that prove the backend's body was logged.
	got := buf.String()
	if !strings.Contains(got, "duplicate label") || !strings.Contains(got, "tailscale_node") {
		t.Fatalf("export error body was not logged; got: %s", got)
	}
}

func TestInstallExportErrorHandler_ClassifiesTimeout(t *testing.T) {
	rec := telemetrytest.New()

	restore := telemetry.InstallExportErrorHandler(rec.Emitter(), nil)
	defer restore()

	otel.Handle(context.DeadlineExceeded)

	points := rec.MetricPoints("tailscale2otel.export.failures")
	if len(points) != 1 {
		t.Fatalf("got %d data points, want 1", len(points))
	}
	if got := points[0].Attrs["error.type"]; got != "timeout" {
		t.Fatalf("error.type = %q, want timeout", got)
	}
}

func TestInstallExportErrorHandler_RestoreReinstallsPrevious(t *testing.T) {
	var prevCalled bool
	prev := otel.ErrorHandlerFunc(func(error) { prevCalled = true })
	otel.SetErrorHandler(prev)

	rec := telemetrytest.New()
	restore := telemetry.InstallExportErrorHandler(rec.Emitter(), nil)

	otel.Handle(errors.New("during"))
	if prevCalled {
		t.Fatal("previous handler should not be called while our handler is installed")
	}
	if len(rec.MetricPoints("tailscale2otel.export.failures")) != 1 {
		t.Fatal("our handler should have counted the error")
	}

	restore()
	otel.Handle(errors.New("after restore"))
	if !prevCalled {
		t.Fatal("restore() should reinstall the previous handler")
	}
}

func TestEmitBuildInfo_EmitsGaugeWithGoVersion(t *testing.T) {
	rec := telemetrytest.New()

	telemetry.EmitBuildInfo(rec.Emitter(), "go1.26")

	points := rec.MetricPoints("tailscale2otel.build_info")
	if len(points) != 1 {
		t.Fatalf("got %d data points, want 1", len(points))
	}
	p := points[0]
	if p.Kind != "gauge" {
		t.Fatalf("kind = %q, want gauge", p.Kind)
	}
	if p.Unit != "1" {
		t.Fatalf("unit = %q, want %q", p.Unit, "1")
	}
	if p.Value != 1 {
		t.Fatalf("value = %v, want 1", p.Value)
	}
	if got := p.Attrs["go.version"]; got != "go1.26" {
		t.Fatalf("go.version = %q, want go1.26", got)
	}
	// The service version must NOT be emitted as a data-point attribute: it is
	// promoted from the resource and would otherwise collide as a duplicate
	// service_version label (otlp_parse_error).
	if _, ok := p.Attrs["service.version"]; ok {
		t.Fatalf("service.version should not be a data-point attribute, attrs=%v", p.Attrs)
	}
}

func TestEmitBuildInfo_SkipsEmptyGoVersion(t *testing.T) {
	rec := telemetrytest.New()

	telemetry.EmitBuildInfo(rec.Emitter(), "")

	points := rec.MetricPoints("tailscale2otel.build_info")
	if len(points) != 1 {
		t.Fatalf("got %d data points, want 1", len(points))
	}
	if _, ok := points[0].Attrs["go.version"]; ok {
		t.Fatalf("go.version should be absent for empty value, attrs=%v", points[0].Attrs)
	}
}

func TestEmitExportDuration(t *testing.T) {
	rec := telemetrytest.New()
	telemetry.EmitExportDuration(rec.Emitter(), "metrics", "success", 0.042)

	pts := rec.MetricPoints("tailscale2otel.export.duration")
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "histogram" {
		t.Errorf("kind = %q, want histogram", p.Kind)
	}
	if p.Unit != "s" {
		t.Errorf("unit = %q, want s", p.Unit)
	}
	if got := p.Attrs["signal"]; got != "metrics" {
		t.Errorf("signal = %q, want metrics", got)
	}
	if got := p.Attrs["outcome"]; got != "success" {
		t.Errorf("outcome = %q, want success", got)
	}
	if p.Count != 1 {
		t.Errorf("count = %d, want 1", p.Count)
	}
}
