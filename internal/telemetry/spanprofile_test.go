package telemetry

import (
	"context"
	"runtime/pprof"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// collectPprofLabels returns the current runtime pprof labels attached to ctx,
// the same mechanism the CPU profiler reads from at sample time.
func collectPprofLabels(ctx context.Context) map[string]string {
	m := make(map[string]string)
	pprof.ForLabels(ctx, func(key, value string) bool {
		m[key] = value
		return true
	})
	return m
}

// TestWrapTracerProviderForProfiles_ZeroOptionsUnwrapped proves the #370
// acceptance criterion that a zero ProfileOptions leaves the TracerProvider
// completely unwrapped: the exact same value comes back, not merely an
// equivalent one, so no otel-profiling-go allocation or behavior is added
// when the feature is off.
func TestWrapTracerProviderForProfiles_ZeroOptionsUnwrapped(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	got := wrapTracerProviderForProfiles(tp, ProfileOptions{})

	if got != trace.TracerProvider(tp) {
		t.Fatalf("wrapTracerProviderForProfiles with zero ProfileOptions returned a different provider; want the original *sdktrace.TracerProvider unchanged")
	}
}

// TestWrapTracerProviderForProfiles_EnabledLabelsSpans proves that, once
// enabled, every started span carries span_id/trace_id as runtime pprof
// labels on its context — the mechanism the CPU profiler reads at sample
// time to correlate a profile back to the span that was executing.
func TestWrapTracerProviderForProfiles_EnabledLabelsSpans(t *testing.T) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	wrapped := wrapTracerProviderForProfiles(tp, ProfileOptions{Enabled: true})
	if wrapped == trace.TracerProvider(tp) {
		t.Fatalf("wrapTracerProviderForProfiles with Enabled:true returned the original provider unchanged; want it wrapped")
	}

	tracer := wrapped.Tracer(scopeName)
	ctx, span := tracer.Start(context.Background(), "op")
	defer span.End()

	labels := collectPprofLabels(ctx)
	wantSpanID := span.SpanContext().SpanID().String()
	wantTraceID := span.SpanContext().TraceID().String()

	if got := labels["span_id"]; got != wantSpanID {
		t.Errorf("span_id pprof label = %q, want %q", got, wantSpanID)
	}
	if got := labels["trace_id"]; got != wantTraceID {
		t.Errorf("trace_id pprof label = %q, want %q", got, wantTraceID)
	}
}

// TestWrapTracerProviderForProfiles_DisabledNoLabels proves the gate is on
// ProfileOptions.Enabled specifically (not merely "tp is non-nil"): an
// explicit zero-value Enabled:false must behave identically to the fully
// zero ProfileOptions{}, leaving spans unlabelled.
func TestWrapTracerProviderForProfiles_DisabledNoLabels(t *testing.T) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	wrapped := wrapTracerProviderForProfiles(tp, ProfileOptions{Enabled: false})
	tracer := wrapped.Tracer(scopeName)
	ctx, span := tracer.Start(context.Background(), "op")
	defer span.End()

	labels := collectPprofLabels(ctx)
	if _, ok := labels["span_id"]; ok {
		t.Fatalf("span_id pprof label present with Enabled:false; want no labeling")
	}
}
