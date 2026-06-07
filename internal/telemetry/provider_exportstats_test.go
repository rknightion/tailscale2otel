package telemetry

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestProviderExportStats(t *testing.T) {
	ctx := context.Background()

	off, err := NewProvider(ctx, Options{Protocol: "stdout", StdoutWriter: io.Discard, ServiceName: "t"})
	if err != nil {
		t.Fatalf("NewProvider off: %v", err)
	}
	if s := off.ExportStats(); s.Datapoints != 0 || s.LogRecords != 0 {
		t.Errorf("ExportStats with self-obs off = %+v, want zero", s)
	}
	_ = off.Shutdown(ctx)

	on, err := NewProvider(ctx, Options{Protocol: "stdout", StdoutWriter: io.Discard, ServiceName: "t", SelfObsEnabled: true, MetricInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewProvider on: %v", err)
	}
	on.Emitter().Counter("t.x", "1", "d", 1, Attrs{"a": "b"})
	on.Emitter().LogEvent(Event{Body: "hello"})
	on.Emitter().LogEvent(Event{Body: "hello2"})
	on.Emitter().LogEvent(Event{Body: "hello3"})
	if err := on.Shutdown(ctx); err != nil { // Shutdown flushes -> Export runs
		t.Fatalf("Shutdown: %v", err)
	}
	s := on.ExportStats()
	if s.Datapoints == 0 {
		t.Errorf("Datapoints = 0 after flush, want > 0")
	}
	if s.LogRecords == 0 {
		t.Errorf("LogRecords = 0 after flush, want > 0")
	}
}
