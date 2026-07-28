package telemetry

import sdktrace "go.opentelemetry.io/otel/sdk/trace"

// Head-sampling policy. Split out of trace.go so sampler composition lives in
// one file with one owner.

// newSampler builds the head sampler for opts. It is the single seam
// NewProvider uses, so sampler composition (the global sampler, per-workload
// classes, and remote-parent trust policy) is decided here rather than at the
// call site.
func newSampler(opts Options) sdktrace.Sampler {
	return buildSampler(opts.TraceSampler, opts.TraceSamplerArg)
}

// buildSampler maps the config sampler name + arg to an sdktrace.Sampler.
// Unknown names fall back to the safe default (parentbased_always_on); the
// config layer validates the enum so this fallthrough is defensive only.
func buildSampler(name string, arg float64) sdktrace.Sampler {
	switch name {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(arg)
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(arg))
	default:
		// parentbased_always_on, "" (empty default), and any unvalidated name.
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}
