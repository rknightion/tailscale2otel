package stream_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/rknightion/tailscale2otel/v5/internal/audit"
	"github.com/rknightion/tailscale2otel/v5/internal/enrich"
	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/stream"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// #367 closed with the poll path carrying trace context onto flow and audit log
// records, but left the STREAMING path on the context-free variants — so a log
// record ingested via HEC was orphaned from the server span that produced it,
// which is exactly the correlation the issue exists to provide. These are the
// regression tests for the three receiver call sites.
//
// The assertion is on the record's NATIVE trace context (Recorder.TraceID /
// SpanID come from the LogRecord's own TraceID()/SpanID(), not from a
// trace_id attribute), because that is what a backend joins on.

// hecAudit is a minimal Splunk-HEC body whose single record classifies as an
// audit event: classify() requires a non-empty actor object and a non-empty
// action. Audit records always emit exactly one log record, which makes them the
// cheapest fixture for a trace-context assertion.
const hecAudit = `{"event":{"actor":{"loginName":"someone@example.com"},"action":"CREATE_NODE","eventGroupID":"g1"}}`

// hecFlow is a minimal HEC body whose record classifies as a flow log: a
// non-empty nodeId plus at least one traffic array entry.
const hecFlow = `{"event":{"nodeId":"nTEST","start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:01:00Z",` +
	`"virtualTraffic":[{"proto":6,"src":"100.64.0.1:1234","dst":"100.64.0.2:443","txPkts":1,"txBytes":100}]}}`

// tracingServer builds a receiver with an always-sampling tracer attached, plus
// the span recorder and telemetry recorder to assert against.
func tracingServer(t *testing.T) (*stream.Server, *telemetrytest.Recorder, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	rec := telemetrytest.New()
	cache := enrich.NewDeviceCache()
	flowProc := flowlog.NewProcessor(cache, flowlog.Options{})
	auditProc := audit.NewProcessor()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := stream.New(stream.Options{Listen: loopbackListen}, flowProc, auditProc,
		rec.Emitter(), logger, stream.WithTracer(tp.Tracer("test")))
	return s, rec, sr
}

// assertRecordsCarrySpan checks that every emitted log record shares the trace
// and span identity of the one recorded server span. Sharing the TRACE id alone
// would be satisfied by any descendant, so the span id is asserted too: the
// records are emitted directly under stream.receive, not under a child.
func assertRecordsCarrySpan(t *testing.T, rec *telemetrytest.Recorder, sr *tracetest.SpanRecorder) {
	t.Helper()
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want exactly 1", len(spans))
	}
	want := spans[0].SpanContext()
	if !want.IsSampled() {
		t.Fatal("server span is not sampled; the test cannot prove exemplar/context propagation")
	}
	logs := rec.LogRecords()
	if len(logs) == 0 {
		t.Fatal("no log records emitted; the fixture did not reach a processor")
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

func TestStreamHandle_AuditLogCarriesTraceContext(t *testing.T) {
	s, rec, sr := tracingServer(t)
	post(t, s.Handler(), http.MethodPost, "/services/collector/event",
		http.Header{}, strings.NewReader(hecAudit))
	assertRecordsCarrySpan(t, rec, sr)
}

func TestStreamHandle_FlowLogCarriesTraceContext(t *testing.T) {
	s, rec, sr := tracingServer(t)
	post(t, s.Handler(), http.MethodPost, "/services/collector/event",
		http.Header{}, strings.NewReader(hecFlow))
	assertRecordsCarrySpan(t, rec, sr)
}

// ApplyDurable replays a stored body outside any HTTP request, so its context is
// the caller's. A replay driven from a traced shutdown/startup path must
// propagate that context the same way, or durable replay silently loses the
// correlation the synchronous path has.
func TestApplyDurable_CarriesCallerTraceContext(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	rec := telemetrytest.New()
	cache := enrich.NewDeviceCache()
	flowProc := flowlog.NewProcessor(cache, flowlog.Options{})
	auditProc := audit.NewProcessor()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := stream.New(stream.Options{Listen: loopbackListen}, flowProc, auditProc,
		rec.Emitter(), logger)

	ctx, span := tp.Tracer("test").Start(context.Background(), "replay")
	if _, err := s.ApplyDurable(ctx, []byte(hecAudit), time.Now()); err != nil {
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
