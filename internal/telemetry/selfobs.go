package telemetry

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel"
)

// InstallExportErrorHandler sets the global OpenTelemetry error handler so that
// every otel.Handle(err) increments the "tailscale2otel.export.failures"
// counter, attributed by a coarse error class, AND logs the full error via
// logger (when non-nil). The OTLP exporter surfaces the backend's HTTP response
// body in that error, so logging it makes a Grafana Cloud parse rejection (the
// exact metric/label Mimir refused) visible instead of being collapsed to an
// anonymous counter. It returns a restore func that reinstalls the previously-set
// handler, allowing callers (and tests) to undo the change and recover the prior
// global state.
func InstallExportErrorHandler(e Emitter, logger *slog.Logger) (restore func()) {
	prev := otel.GetErrorHandler()
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		if logger != nil {
			logger.Warn("OTLP export failed", "error.type", errorType(err), "error", err)
		}
		e.Counter(docExportFailures.Name, docExportFailures.Unit, docExportFailures.Description, 1, Attrs{
			"error.type": errorType(err),
		})
	}))
	return func() { otel.SetErrorHandler(prev) }
}

// errorType maps an error to a coarse class used as the "error.type" attribute.
func errorType(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "export"
}

// EmitBuildInfo records the "tailscale2otel.build_info" gauge with a constant
// value of 1, carrying the service and Go versions as attributes. Empty values
// are omitted so absent build metadata does not pollute the attribute set.
func EmitBuildInfo(e Emitter, version, goVersion string) {
	attrs := Attrs{}
	if version != "" {
		attrs["service.version"] = version
	}
	if goVersion != "" {
		attrs["go.version"] = goVersion
	}
	e.Gauge(docBuildInfo.Name, docBuildInfo.Unit, docBuildInfo.Description, 1, attrs)
}
