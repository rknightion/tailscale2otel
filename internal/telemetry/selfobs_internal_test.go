package telemetry

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
)

// TestInstallExportErrorHandler_AttributesSignal pins #359: an error tagged by
// one of the three exporter wrappers (export_counting.go, delivery_trace.go, via
// withExportSignal) carries its signal into the export.failures counter. This is
// a white-box (package telemetry) test because withExportSignal is unexported —
// the black-box behavior (an untagged error omits the attribute) is covered by
// selfobs_test.go's TestInstallExportErrorHandler_OmitsSignalWhenUntagged.
//
// It deliberately uses the package's own newReaderEmitter helper rather than
// internal/telemetrytest.Recorder: telemetrytest imports this package, so an
// in-package test that imported it back would be an import cycle. The
// ManualReader gives the same "assert the emitted metric, not internals"
// guarantee.
func TestInstallExportErrorHandler_AttributesSignal(t *testing.T) {
	emitter, reader := newReaderEmitter(t, nil)
	restore := InstallExportErrorHandler(emitter, nil)
	defer restore()

	otel.Handle(withExportSignal(SignalLogs, errors.New("boom")))

	attrs := collectAttrs(t, reader, "tailscale2otel.export.failures")
	got, ok := attrs.Value("signal")
	if !ok {
		t.Fatalf("export.failures carries no signal attribute; attrs=%v", attrs.ToSlice())
	}
	if got.AsString() != "logs" {
		t.Fatalf("signal = %q, want logs", got.AsString())
	}
}
