package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// alwaysSampledTraceID and alwaysDroppedTraceID give deterministic
// TraceIDRatioBased outcomes without relying on randomness (per #372's
// acceptance: "deterministic sampler tests"). traceIDRatioSampler computes
// binary.BigEndian.Uint64(traceID[8:16])>>1 and compares it against
// uint64(fraction*(1<<63)) (go.opentelemetry.io/otel/sdk@v1.44.0/trace/sampling.go);
// an all-zero tail is 0, below every positive threshold (always sampled at any
// ratio > 0); an all-0xff tail is the maximum representable value, above every
// threshold for a ratio < 1 (always dropped).
var (
	alwaysSampledTraceID = trace.TraceID{0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	alwaysDroppedTraceID = trace.TraceID{0x01, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
)

func classAttr(class string) attribute.KeyValue {
	return SpanClassKey.String(class)
}

// TestNewSampler_ZeroValueMatchesGlobal proves the #372 compatibility
// acceptance: a zero SamplingOptions resolves to EXACTLY today's single global
// sampler built from TraceSampler/TraceSamplerArg, byte-for-byte (same
// Description() string, meaning the same composed Sampler value) — no extra
// wrapping layer is introduced when no per-class/remote-parent override is
// configured.
func TestNewSampler_ZeroValueMatchesGlobal(t *testing.T) {
	cases := []struct {
		name       string
		sampler    string
		samplerArg float64
	}{
		{name: "default empty", sampler: "", samplerArg: 1},
		{name: "always_on", sampler: "always_on"},
		{name: "always_off", sampler: "always_off"},
		{name: "traceidratio", sampler: "traceidratio", samplerArg: 0.25},
		{name: "parentbased_traceidratio", sampler: "parentbased_traceidratio", samplerArg: 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := Options{TraceSampler: c.sampler, TraceSamplerArg: c.samplerArg}
			got := newSampler(opts).Description()
			want := buildSampler(c.sampler, c.samplerArg).Description()
			if got != want {
				t.Errorf("newSampler(%+v).Description() = %q, want %q (byte-for-byte match with buildSampler)", opts, got, want)
			}
		})
	}
}

// TestNewSampler_ZeroValueExplicitTrust proves an explicit RemoteParent:"trust"
// with no class overrides is equally a no-op — "trust" is documented as the
// default, so setting it explicitly must not introduce different behavior.
func TestNewSampler_ZeroValueExplicitTrust(t *testing.T) {
	opts := Options{TraceSampler: "traceidratio", TraceSamplerArg: 0.5, Sampling: SamplingOptions{RemoteParent: RemoteParentTrust}}
	got := newSampler(opts).Description()
	want := buildSampler("traceidratio", 0.5).Description()
	if got != want {
		t.Errorf("newSampler with explicit RemoteParent:trust = %q, want %q", got, want)
	}
}

// TestNewSampler_PerClassIndependentRatios drives root (no-parent) spans of
// each class through deterministic trace IDs and confirms each class's own
// sampler/arg governs its own decision independently of the others.
func TestNewSampler_PerClassIndependentRatios(t *testing.T) {
	opts := Options{
		TraceSampler:    "always_on", // fallback/global: never consulted below, every class is overridden
		TraceSamplerArg: 1,
		Sampling: SamplingOptions{
			Scrape:     SamplerClassOptions{Sampler: "traceidratio", SamplerArg: 0}, // always drop
			Receiver:   SamplerClassOptions{Sampler: "traceidratio", SamplerArg: 1}, // always sample
			Background: SamplerClassOptions{Sampler: "always_off"},                  // always drop
		},
	}
	sampler := newSampler(opts)

	cases := []struct {
		class   string
		traceID trace.TraceID
		want    sdktrace.SamplingDecision
	}{
		{class: SpanClassScrape, traceID: alwaysSampledTraceID, want: sdktrace.Drop},              // ratio 0 drops even a "would sample" trace ID
		{class: SpanClassReceiver, traceID: alwaysDroppedTraceID, want: sdktrace.RecordAndSample}, // ratio 1 samples even a "would drop" trace ID
		{class: SpanClassBackground, traceID: alwaysSampledTraceID, want: sdktrace.Drop},
	}
	for _, c := range cases {
		t.Run(c.class, func(t *testing.T) {
			res := sampler.ShouldSample(sdktrace.SamplingParameters{
				ParentContext: context.Background(),
				TraceID:       c.traceID,
				Attributes:    []attribute.KeyValue{classAttr(c.class)},
			})
			if res.Decision != c.want {
				t.Errorf("class %s: Decision = %v, want %v", c.class, res.Decision, c.want)
			}
		})
	}
}

// TestNewSampler_UnclassifiedFallsBackToGlobal proves a span with no
// recognized class attribute (or none at all) uses the fallback/global
// sampler rather than any of the per-class overrides.
func TestNewSampler_UnclassifiedFallsBackToGlobal(t *testing.T) {
	opts := Options{
		TraceSampler: "always_off", // global: always drop
		Sampling: SamplingOptions{
			Scrape: SamplerClassOptions{Sampler: "always_on"}, // would always sample, must NOT apply
		},
	}
	sampler := newSampler(opts)

	// No class attribute at all.
	res := sampler.ShouldSample(sdktrace.SamplingParameters{ParentContext: context.Background(), TraceID: alwaysSampledTraceID})
	if res.Decision != sdktrace.Drop {
		t.Errorf("no class attribute: Decision = %v, want Drop (fallback to global always_off)", res.Decision)
	}

	// An attribute present but not a recognized class value.
	res = sampler.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       alwaysSampledTraceID,
		Attributes:    []attribute.KeyValue{classAttr("something-else")},
	})
	if res.Decision != sdktrace.Drop {
		t.Errorf("unrecognized class value: Decision = %v, want Drop (fallback to global always_off)", res.Decision)
	}
}

// TestNewSampler_LocalParentSampledOverridesClassRatio proves the #372
// acceptance "a child of a sampled parent is still sampled regardless of its
// own class ratio": a local (in-process) sampled parent must force sampling
// even though the child's class is configured to always drop.
func TestNewSampler_LocalParentSampledOverridesClassRatio(t *testing.T) {
	opts := Options{
		Sampling: SamplingOptions{
			Scrape: SamplerClassOptions{Sampler: "always_off"}, // would always drop as a root
		},
	}
	sampler := newSampler(opts)

	parentSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    alwaysSampledTraceID,
		SpanID:     trace.SpanID{1},
		TraceFlags: trace.FlagsSampled, // local parent, sampled
	})
	ctx := trace.ContextWithSpanContext(context.Background(), parentSC)

	res := sampler.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: ctx,
		TraceID:       alwaysSampledTraceID,
		Attributes:    []attribute.KeyValue{classAttr(SpanClassScrape)},
	})
	if res.Decision != sdktrace.RecordAndSample {
		t.Errorf("child of sampled local parent: Decision = %v, want RecordAndSample despite always_off class sampler", res.Decision)
	}

	// And the inverse: a local NOT-sampled parent drops the child even if the
	// class sampler would always sample, confirming genuine parent-based
	// inheritance rather than an OR of the two.
	opts2 := Options{
		Sampling: SamplingOptions{
			Scrape: SamplerClassOptions{Sampler: "always_on"},
		},
	}
	sampler2 := newSampler(opts2)
	notSampledSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: alwaysSampledTraceID,
		SpanID:  trace.SpanID{1},
		// TraceFlags omitted: not sampled.
	})
	ctx2 := trace.ContextWithSpanContext(context.Background(), notSampledSC)
	res2 := sampler2.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: ctx2,
		TraceID:       alwaysSampledTraceID,
		Attributes:    []attribute.KeyValue{classAttr(SpanClassScrape)},
	})
	if res2.Decision != sdktrace.Drop {
		t.Errorf("child of unsampled local parent: Decision = %v, want Drop despite always_on class sampler", res2.Decision)
	}
}
