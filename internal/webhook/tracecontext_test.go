package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// #367 closed with the poll path carrying trace context onto its log records,
// but the webhook receiver's emit() stayed on the context-free LogEvent — so a
// webhook record was orphaned from the "webhook.receive" span that produced it
// even though the span existed and was sampled. The assertion is on the
// record's NATIVE trace context (the LogRecord's own TraceID/SpanID, set by the
// log SDK from the emitting context), because that is what a backend joins on.

// tracedWebhookServer builds a receiver with an always-sampling tracer.
func tracedWebhookServer(t *testing.T) (*Server, *telemetrytest.Recorder, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	rec := telemetrytest.New()
	s := New(Options{
		Listen:    "127.0.0.1:0",
		Path:      "/webhook",
		Secret:    testSecret,
		Tolerance: 0,
	}, rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)), WithTracer(tp.Tracer("test")))
	return s, rec, sr
}

func TestWebhookHandle_LogRecordsCarryTraceContext(t *testing.T) {
	s, rec, sr := tracedWebhookServer(t)

	ts := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	resp := doPost(t, s.Handler(), "/webhook", twoEventBody, signBody(testSecret, ts, twoEventBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want exactly 1", len(spans))
	}
	want := spans[0].SpanContext()
	if !want.IsSampled() {
		t.Fatal("server span is not sampled; the test cannot prove context propagation")
	}

	logs := rec.LogRecords()
	if len(logs) != 2 {
		t.Fatalf("LogRecords len = %d, want 2", len(logs))
	}
	for _, l := range logs {
		if l.TraceID != want.TraceID().String() {
			t.Errorf("log %q TraceID = %q, want %q (the record is orphaned from the receiver span)",
				l.EventName, l.TraceID, want.TraceID().String())
		}
		if l.SpanID != want.SpanID().String() {
			t.Errorf("log %q SpanID = %q, want %q", l.EventName, l.SpanID, want.SpanID().String())
		}
	}
}

// ApplyDurable replays a stored body outside any HTTP request, so it must
// propagate the caller's context the same way the synchronous path does.
func TestWebhookApplyDurable_CarriesCallerTraceContext(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	rec := telemetrytest.New()
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook", Secret: testSecret},
		rec.Emitter(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, span := tp.Tracer("test").Start(context.Background(), "replay")
	if err := s.ApplyDurable(ctx, []byte(twoEventBody), time.Now()); err != nil {
		t.Fatalf("ApplyDurable: %v", err)
	}
	span.End()

	logs := rec.LogRecords()
	if len(logs) == 0 {
		t.Fatal("no log records emitted from the durable replay")
	}
	want := span.SpanContext()
	for _, l := range logs {
		if l.TraceID != want.TraceID().String() {
			t.Errorf("log %q TraceID = %q, want %q", l.EventName, l.TraceID, want.TraceID().String())
		}
	}
}
