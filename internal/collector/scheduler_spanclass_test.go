package collector_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/rknightion/tailscale2otel/v5/internal/collector"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetry"
)

// --- #473 (security:SEC-10) ---
//
// The scrape span's status description is free text (a collector error string or
// a recovered panic value), so internal/telemetry's PII span exporter replaces it
// wholesale once free_text_details is disabled. A bounded, code-defined failure
// class must therefore live on the span as an ATTRIBUTE, so a redacted span is
// still diagnosable — the same three-value error.type enum the scrape.errors
// counter already carries.

func spanAttr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value, true
		}
	}
	return attribute.Value{}, false
}

func runTickWithRecorder(t *testing.T, c collector.SnapshotCollector) sdktrace.ReadOnlySpan {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	s := collector.NewScheduler(noopEmitter{}, collector.NewMemoryStore(),
		collector.WithTracer(tp.Tracer("test")),
		collector.WithStaggerWindow(0),
		collector.WithSelfObs(false),
	)
	last := time.Now()
	s.RunTick(context.Background(), collector.Entry{Collector: c, Interval: time.Minute}, &last)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want exactly 1", len(spans))
	}
	return spans[0]
}

func TestRunTick_SpanCarriesBoundedErrorClass(t *testing.T) {
	cases := []struct {
		name      string
		collector collector.SnapshotCollector
		want      string
	}{
		{"generic error", fakeErr("keys", errors.New("api unavailable")), "error"},
		{"timeout", fakeErr("devices", context.DeadlineExceeded), "timeout"},
		{"wrapped timeout", fakeErr("devices", errors.Join(errors.New("fetch"), context.DeadlineExceeded)), "timeout"},
		{"panic", fakePanicking("acl"), "panic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			span := runTickWithRecorder(t, tc.collector)
			if span.Status().Code != codes.Error {
				t.Fatalf("status code = %v, want Error", span.Status().Code)
			}
			got, ok := spanAttr(span, "error.type")
			if !ok {
				t.Fatalf("failing scrape span has no error.type attribute: %v", span.Attributes())
			}
			if got.AsString() != tc.want {
				t.Errorf("error.type = %q, want %q", got.AsString(), tc.want)
			}
		})
	}
}

// A successful run must not gain an error class: error.type on a healthy span
// would break the "attribute present => this scrape failed" reading.
func TestRunTick_SuccessSpanHasNoErrorClass(t *testing.T) {
	span := runTickWithRecorder(t, fakeOK("dev"))
	if _, ok := spanAttr(span, "error.type"); ok {
		t.Errorf("successful scrape span must not carry error.type: %v", span.Attributes())
	}
}

// A recovered panic must also be recorded as an exception EVENT, so the panic text
// sits on the governed free-text surface (exception.message) instead of only in
// the status description.
func TestRunTick_PanicRecordsExceptionEvent(t *testing.T) {
	span := runTickWithRecorder(t, fakePanicking("acl"))
	var found bool
	for _, ev := range span.Events() {
		if ev.Name != "exception" {
			continue
		}
		for _, a := range ev.Attributes {
			if string(a.Key) == "exception.message" && a.Value.AsString() != "" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("panicking scrape span has no exception event carrying exception.message: %v", span.Events())
	}
}

// fakePanicking returns a SnapshotCollector whose Collect panics, exercising the
// scheduler's recover path.
func fakePanicking(name string) collector.SnapshotCollector {
	return snapFunc{name: name, def: time.Minute, fn: func(context.Context, telemetry.Emitter) error {
		panic("boom: " + name)
	}}
}
