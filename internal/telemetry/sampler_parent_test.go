package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// remoteParentContext builds a context carrying a REMOTE parent span context
// (as receiver.go's propagation.Extract would produce from an inbound W3C
// traceparent header) with the given trace ID and sampled bit.
func remoteParentContext(traceID trace.TraceID, sampled bool) context.Context {
	flags := trace.TraceFlags(0)
	if sampled {
		flags = trace.FlagsSampled
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{1},
		TraceFlags: flags,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(context.Background(), sc)
}

// TestNewSampler_RemoteParentTrust proves the default/compatibility policy
// (#373): an inbound (remote) traceparent's sampled bit is trusted exactly as
// today, for both a sampled and an unsampled remote parent. The class sampler
// is configured to always drop as a root decision, so any "sampled" result
// below can only come from trusting the remote bit.
func TestNewSampler_RemoteParentTrust(t *testing.T) {
	opts := Options{Sampling: SamplingOptions{
		Receiver:     SamplerClassOptions{Sampler: "always_off"},
		RemoteParent: RemoteParentTrust,
	}}
	sampler := newSampler(opts)

	sampledParent := remoteParentContext(alwaysDroppedTraceID, true)
	res := sampler.ShouldSample(sdktrace.SamplingParameters{ParentContext: sampledParent, TraceID: alwaysDroppedTraceID})
	if res.Decision != sdktrace.RecordAndSample {
		t.Errorf("trust, remote parent sampled: Decision = %v, want RecordAndSample (remote bit trusted)", res.Decision)
	}

	notSampledParent := remoteParentContext(alwaysDroppedTraceID, false)
	res2 := sampler.ShouldSample(sdktrace.SamplingParameters{ParentContext: notSampledParent, TraceID: alwaysDroppedTraceID})
	if res2.Decision != sdktrace.Drop {
		t.Errorf("trust, remote parent NOT sampled: Decision = %v, want Drop (remote bit trusted)", res2.Decision)
	}
}

// TestNewSampler_RemoteParentIgnore proves the #373 core acceptance:
// "untrusted mode cannot force sampling". With RemoteParent:"ignore" the
// remote parent's sampled bit — sampled OR not — must never be consulted;
// only the local per-class root decision governs, so both cases below must
// produce the SAME decision (driven purely by the class sampler).
func TestNewSampler_RemoteParentIgnore(t *testing.T) {
	// Class configured to always DROP as a root decision. If "ignore" worked,
	// a sampled remote parent would otherwise force sampling (as proven by the
	// trust test above using the identical trace ID) — it must not here.
	dropOpts := Options{Sampling: SamplingOptions{
		Receiver:     SamplerClassOptions{Sampler: "always_off"},
		RemoteParent: RemoteParentIgnore,
	}}
	dropSampler := newSampler(dropOpts)
	for _, sampled := range []bool{true, false} {
		parent := remoteParentContext(alwaysDroppedTraceID, sampled)
		res := dropSampler.ShouldSample(sdktrace.SamplingParameters{
			ParentContext: parent,
			TraceID:       alwaysDroppedTraceID,
			Attributes:    []attribute.KeyValue{classAttr(SpanClassReceiver)},
		})
		if res.Decision != sdktrace.Drop {
			t.Errorf("ignore, class always_off, remote sampled=%v: Decision = %v, want Drop (remote bit must be ignored)", sampled, res.Decision)
		}
	}

	// Symmetric check the other direction: class configured to always SAMPLE.
	// An UNSAMPLED remote parent must not be able to suppress it either —
	// "ignore" means the bit plays no part in the decision at all, not just
	// that it cannot force a positive result.
	sampleOpts := Options{Sampling: SamplingOptions{
		Receiver:     SamplerClassOptions{Sampler: "always_on"},
		RemoteParent: RemoteParentIgnore,
	}}
	sampleSampler := newSampler(sampleOpts)
	for _, sampled := range []bool{true, false} {
		parent := remoteParentContext(alwaysDroppedTraceID, sampled)
		res := sampleSampler.ShouldSample(sdktrace.SamplingParameters{
			ParentContext: parent,
			TraceID:       alwaysDroppedTraceID,
			Attributes:    []attribute.KeyValue{classAttr(SpanClassReceiver)},
		})
		if res.Decision != sdktrace.RecordAndSample {
			t.Errorf("ignore, class always_on, remote sampled=%v: Decision = %v, want RecordAndSample (remote bit must be ignored)", sampled, res.Decision)
		}
	}
}

// TestRemoteParentContext_LinkPolicy proves the "link" policy's call-site
// contract: given a context carrying a remote parent, RemoteParentContext
// returns SpanStartOptions that make tracer.Start begin a NEW root trace
// (independent trace ID, governed by local sampling) while recording a Link
// back to the original remote span — verified end-to-end against a real SDK
// TracerProvider, not just by inspecting the option slice.
func TestRemoteParentContext_LinkPolicy(t *testing.T) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	tracer := tp.Tracer("test")

	remoteSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    alwaysSampledTraceID,
		SpanID:     trace.SpanID{9},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	inCtx := trace.ContextWithSpanContext(context.Background(), remoteSC)

	outCtx, startOpts := RemoteParentContext(inCtx, RemoteParentLink)
	if len(startOpts) == 0 {
		t.Fatalf("RemoteParentContext(link) returned no SpanStartOptions for a valid remote parent")
	}

	_, span := tracer.Start(outCtx, "receiver.span", startOpts...)
	defer span.End()

	got := span.SpanContext()
	if got.TraceID() == remoteSC.TraceID() {
		t.Errorf("linked span shares the remote trace ID %s; want a new independent root trace", got.TraceID())
	}
	if got.IsRemote() {
		t.Errorf("linked span context reports Remote=true; want a local root")
	}

	rs, ok := span.(sdktrace.ReadOnlySpan)
	if !ok {
		t.Fatalf("span does not implement sdktrace.ReadOnlySpan")
	}
	links := rs.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want exactly 1", len(links))
	}
	if links[0].SpanContext.TraceID() != remoteSC.TraceID() || links[0].SpanContext.SpanID() != remoteSC.SpanID() {
		t.Errorf("link = %+v, want a link back to the original remote span context %+v", links[0].SpanContext, remoteSC)
	}
}

// TestRemoteParentContext_NonLinkPoliciesAreNoOps proves trust/ignore/unknown
// policies need no call-site intervention: the sampler alone implements them,
// so the helper must return the context unchanged and no SpanStartOptions.
func TestRemoteParentContext_NonLinkPoliciesAreNoOps(t *testing.T) {
	remoteSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: alwaysSampledTraceID, SpanID: trace.SpanID{9}, TraceFlags: trace.FlagsSampled, Remote: true,
	})
	inCtx := trace.ContextWithSpanContext(context.Background(), remoteSC)

	for _, policy := range []string{RemoteParentTrust, RemoteParentIgnore, "", "unknown-future-value"} {
		outCtx, startOpts := RemoteParentContext(inCtx, policy)
		if outCtx != inCtx {
			t.Errorf("policy %q: context was modified, want unchanged", policy)
		}
		if len(startOpts) != 0 {
			t.Errorf("policy %q: got %d SpanStartOptions, want 0", policy, len(startOpts))
		}
	}
}

// TestRemoteParentContext_LinkPolicyNoRemoteParent proves the helper is a
// no-op when there is no remote parent to link to (e.g. a local caller, or no
// parent at all) — it must not fabricate a link from nothing.
func TestRemoteParentContext_LinkPolicyNoRemoteParent(t *testing.T) {
	outCtx, startOpts := RemoteParentContext(context.Background(), RemoteParentLink)
	if outCtx != context.Background() {
		t.Errorf("no parent: context was modified, want unchanged")
	}
	if len(startOpts) != 0 {
		t.Errorf("no parent: got %d SpanStartOptions, want 0", len(startOpts))
	}

	localSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: alwaysSampledTraceID, SpanID: trace.SpanID{9}, TraceFlags: trace.FlagsSampled, Remote: false,
	})
	localCtx := trace.ContextWithSpanContext(context.Background(), localSC)
	outCtx2, startOpts2 := RemoteParentContext(localCtx, RemoteParentLink)
	if outCtx2 != localCtx {
		t.Errorf("local parent: context was modified, want unchanged")
	}
	if len(startOpts2) != 0 {
		t.Errorf("local parent: got %d SpanStartOptions, want 0 (link policy only applies to REMOTE parents)", len(startOpts2))
	}
}
