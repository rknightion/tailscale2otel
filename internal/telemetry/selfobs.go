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
// value of 1, carrying the Go runtime version as an attribute. An empty value is
// omitted so absent build metadata does not pollute the attribute set.
//
// The service version is deliberately NOT emitted as a data-point attribute: it
// already lives on the OTEL Resource (service.version), which Grafana Cloud
// promotes to a service_version label on every exported series — including this
// one. Emitting it here too produced a duplicate label that Mimir rejects as an
// otlp_parse_error (the Emitter's collision guard then dropped it, logging a WARN
// on startup). The resource copy carries identical information, so build_info
// keeps showing service_version with zero data loss and no warning.
func EmitBuildInfo(e Emitter, goVersion string) {
	attrs := Attrs{}
	if goVersion != "" {
		attrs["go.version"] = goVersion
	}
	e.Gauge(docBuildInfo.Name, docBuildInfo.Unit, docBuildInfo.Description, 1, attrs)
}

// EmitExportStats records the per-interval deltas for the OTLP export-volume
// counters: tailscale2otel.export.datapoints and .log_records. The caller (the
// app's export reporter) tracks the cumulative ExportStats and passes the
// difference since the previous tick; a zero delta emits nothing (avoids
// creating the series before any export has happened).
func EmitExportStats(e Emitter, datapointsDelta, logRecordsDelta float64) {
	if datapointsDelta > 0 {
		e.Counter(docExportDatapoints.Name, docExportDatapoints.Unit, docExportDatapoints.Description,
			datapointsDelta, nil)
	}
	if logRecordsDelta > 0 {
		e.Counter(docExportLogRecords.Name, docExportLogRecords.Unit, docExportLogRecords.Description,
			logRecordsDelta, nil)
	}
}
