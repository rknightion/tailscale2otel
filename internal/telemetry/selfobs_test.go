package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

func TestInstallExportErrorHandler_CountsHandledErrors(t *testing.T) {
	rec := telemetrytest.New()

	restore := telemetry.InstallExportErrorHandler(rec.Emitter())
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

func TestInstallExportErrorHandler_ClassifiesTimeout(t *testing.T) {
	rec := telemetrytest.New()

	restore := telemetry.InstallExportErrorHandler(rec.Emitter())
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
	restore := telemetry.InstallExportErrorHandler(rec.Emitter())

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

func TestEmitBuildInfo_EmitsGaugeWithVersions(t *testing.T) {
	rec := telemetrytest.New()

	telemetry.EmitBuildInfo(rec.Emitter(), "v1.2.3", "go1.26")

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
	if got := p.Attrs["service.version"]; got != "v1.2.3" {
		t.Fatalf("service.version = %q, want v1.2.3", got)
	}
	if got := p.Attrs["go.version"]; got != "go1.26" {
		t.Fatalf("go.version = %q, want go1.26", got)
	}
}

func TestEmitBuildInfo_SkipsEmptyValues(t *testing.T) {
	rec := telemetrytest.New()

	telemetry.EmitBuildInfo(rec.Emitter(), "", "go1.26")

	points := rec.MetricPoints("tailscale2otel.build_info")
	if len(points) != 1 {
		t.Fatalf("got %d data points, want 1", len(points))
	}
	if _, ok := points[0].Attrs["service.version"]; ok {
		t.Fatalf("service.version should be absent for empty value, attrs=%v", points[0].Attrs)
	}
	if got := points[0].Attrs["go.version"]; got != "go1.26" {
		t.Fatalf("go.version = %q, want go1.26", got)
	}
}
