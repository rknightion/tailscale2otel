package flowlog_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func sampleFlow() flowlog.FlowLog {
	return flowlog.FlowLog{
		NodeID: "n1",
		VirtualTraffic: []flowlog.ConnectionCounts{
			{Proto: 6, Src: "100.64.0.1:1", Dst: "100.64.0.2:443", TxPkts: 3, TxBytes: 300, RxPkts: 2, RxBytes: 200},
		},
	}
}

// TestProcessCtxThreadsTraceContextOntoPerConnectionLog is the #367
// acceptance test for the flow processor's per-connection log path
// (emitConnLog): a sampled ctx passed via ProcessCtx must produce a log
// record carrying the SAME native TraceID/SpanID.
func TestProcessCtxThreadsTraceContextOntoPerConnectionLog(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_connection"})

	wantTraceID := trace.TraceID{0x01, 0x02}
	wantSpanID := trace.SpanID{0x03, 0x04}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     wantSpanID,
		TraceFlags: trace.FlagsSampled,
	}))

	p.ProcessCtx(ctx, sampleFlow(), rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("log records = %d, want 1", len(recs))
	}
	if recs[0].TraceID != wantTraceID.String() {
		t.Errorf("TraceID = %q, want %q", recs[0].TraceID, wantTraceID.String())
	}
	if recs[0].SpanID != wantSpanID.String() {
		t.Errorf("SpanID = %q, want %q", recs[0].SpanID, wantSpanID.String())
	}
}

// TestProcessCtxThreadsTraceContextOntoPerRecordLog covers the other log
// emit site (emitRecordLog, per_record mode).
func TestProcessCtxThreadsTraceContextOntoPerRecordLog(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_record"})

	wantTraceID := trace.TraceID{0x05, 0x06}
	wantSpanID := trace.SpanID{0x07, 0x08}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     wantSpanID,
		TraceFlags: trace.FlagsSampled,
	}))

	p.ProcessCtx(ctx, sampleFlow(), rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("log records = %d, want 1", len(recs))
	}
	if recs[0].TraceID != wantTraceID.String() {
		t.Errorf("TraceID = %q, want %q", recs[0].TraceID, wantTraceID.String())
	}
}

// TestProcessAllCtxThreadsTraceContext is the ProcessAll-side analog: every
// flow in the window batch gets the same ctx's trace context.
func TestProcessAllCtxThreadsTraceContext(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_connection"})

	wantTraceID := trace.TraceID{0x09, 0x0a}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    wantTraceID,
		SpanID:     trace.SpanID{0x0b},
		TraceFlags: trace.FlagsSampled,
	}))

	f1, f2 := sampleFlow(), sampleFlow()
	f2.NodeID = "n2"
	p.ProcessAllCtx(ctx, flowlog.NetworkResponse{Logs: []flowlog.FlowLog{f1, f2}}, rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 2 {
		t.Fatalf("log records = %d, want 2", len(recs))
	}
	for i, r := range recs {
		if r.TraceID != wantTraceID.String() {
			t.Errorf("record %d TraceID = %q, want %q", i, r.TraceID, wantTraceID.String())
		}
	}
}

// TestProcessBackgroundContextUnchanged pins the "unsampled/background
// behavior unchanged" #367 acceptance criterion for the ctx-free wrappers.
func TestProcessBackgroundContextUnchanged(t *testing.T) {
	rec := telemetrytest.New()
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{LogMode: "per_connection"})

	p.Process(sampleFlow(), rec.Emitter())

	recs := rec.LogRecords()
	if len(recs) != 1 {
		t.Fatalf("log records = %d, want 1", len(recs))
	}
	if recs[0].TraceID != (trace.TraceID{}).String() {
		t.Errorf("TraceID = %q, want zero value", recs[0].TraceID)
	}
}
