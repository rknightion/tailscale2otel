package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Head-sampling policy. Split out of trace.go so sampler composition lives in
// one file with one owner.

// newSampler builds the head sampler for opts. It is the single seam
// NewProvider uses, so sampler composition (the global sampler, per-workload
// classes, and remote-parent trust policy) is decided here rather than at the
// call site.
func newSampler(opts Options) sdktrace.Sampler {
	global := buildSampler(opts.TraceSampler, opts.TraceSamplerArg)
	so := opts.Sampling

	// Zero-value / no-override fast path: return the global sampler exactly as
	// built today, unwrapped. This is what "byte-for-byte compatible" means for
	// the #372/#373 acceptance — no classifying or parent-policy decorator is
	// introduced unless an operator actually configures one, so the default
	// deployment's Description() (and therefore its sampling behavior) is
	// identical to before this feature existed.
	if !so.hasClassOverrides() && (so.RemoteParent == "" || so.RemoteParent == RemoteParentTrust) {
		return global
	}

	root := &classifyingSampler{
		scrape:     classSampler(so.Scrape, global),
		receiver:   classSampler(so.Receiver, global),
		background: classSampler(so.Background, global),
		fallback:   global,
	}

	if so.RemoteParent == RemoteParentIgnore {
		// "ignore": a REMOTE parent's sampled bit (trusted or not) must never
		// be able to influence the local decision, in either direction. Routing
		// both remote cases back through the same root sampler used for
		// no-parent spans makes the remote bit inert: the per-class ratio (or
		// the fallback) decides exactly as if the span were a fresh root,
		// regardless of what an inbound sender's traceparent claimed.
		return sdktrace.ParentBased(root,
			sdktrace.WithRemoteParentSampled(root),
			sdktrace.WithRemoteParentNotSampled(root),
		)
	}

	// "trust" (today's behavior — remoteParentSampled defaults to AlwaysOn,
	// remoteParentNotSampled to AlwaysOff) and "link" (the remote span never
	// appears as a PARENT by the time this sampler runs — the receiver call
	// site converts it to a Link and starts a new root via RemoteParentContext,
	// so there is no parent context to make a remote-vs-local distinction on)
	// both use ParentBased's default remote behavior.
	return sdktrace.ParentBased(root)
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

// SpanClassKey is the Start-time attribute key receiver/scrape/background call
// sites use to declare which workload class a span belongs to, e.g.:
//
//	ctx, span := tracer.Start(ctx, "stream.receive",
//	    trace.WithAttributes(telemetry.SpanClassKey.String(telemetry.SpanClassReceiver)))
//
// This has to be a Start-time attribute (trace.WithAttributes), not something
// set after the span begins: verified against the pinned SDK
// (go.opentelemetry.io/otel/sdk@v1.44.0/trace/tracer.go:109-116) that
// tracer.Start builds sdktrace.SamplingParameters.Attributes directly from the
// SpanStartOption-configured attributes and passes that into
// Sampler.ShouldSample BEFORE the span exists — an attribute set via
// span.SetAttributes after Start is invisible to the sampler, which has
// already run.
const SpanClassKey attribute.Key = "tailscale2otel.span.class"

// Closed 3-value set of span classes (#372's scope: "stable root classes such
// as scrape, receiver and background"). Never derive a class from span name or
// any other unbounded value — a 4th ad hoc class would need a config schema
// change (tracing.samplers.<class>) to be configurable at all, so the set is
// deliberately fixed here rather than open-ended.
const (
	SpanClassScrape     = "scrape"
	SpanClassReceiver   = "receiver"
	SpanClassBackground = "background"
)

// Remote-parent trust policy values (#373), matching the frozen
// tracing.remote_parent enum from EPIC-04 (#480)'s seam-freeze comment.
const (
	// RemoteParentTrust is the default/compatibility policy: an inbound
	// traceparent's sampled bit is trusted exactly as today.
	RemoteParentTrust = "trust"
	// RemoteParentIgnore makes an inbound traceparent's sampled bit inert: the
	// local per-class (or global) sampler decides every time, regardless of
	// what an authenticated sender's header claims.
	RemoteParentIgnore = "ignore"
	// RemoteParentLink converts an inbound traceparent into a Link and starts
	// a new local root span instead of continuing the remote trace. Handled
	// entirely by RemoteParentContext at the call site, since links must be
	// set at span Start and cannot be expressed inside a Sampler.
	RemoteParentLink = "link"
)

// SamplerClassOptions is one workload class's sampler override. A zero value
// (empty Sampler) means "inherit the global TraceSampler/TraceSamplerArg" —
// the frozen #480 schema's "unset falls back to the existing global sampler".
type SamplerClassOptions struct {
	Sampler    string
	SamplerArg float64
}

// SamplingOptions carries per-workload head-sampling classes and the inbound
// traceparent trust policy (#372, #373). Owned by the #372/#373 lane. A zero
// value must resolve to the single global sampler built from TraceSampler /
// TraceSamplerArg, with remote parents trusted, exactly as today — see
// newSampler's fast path and TestNewSampler_ZeroValueMatchesGlobal.
type SamplingOptions struct {
	// Scrape, Receiver, and Background are the three frozen root classes
	// (tracing.samplers.{scrape,receiver,background}.{sampler,arg}). An unset
	// (zero) class falls back to the global TraceSampler/TraceSamplerArg.
	Scrape     SamplerClassOptions
	Receiver   SamplerClassOptions
	Background SamplerClassOptions

	// RemoteParent is one of RemoteParentTrust (default), RemoteParentIgnore,
	// or RemoteParentLink (tracing.remote_parent). Empty behaves as
	// RemoteParentTrust.
	RemoteParent string
}

// hasClassOverrides reports whether any of the three workload classes has its
// own sampler configured, rather than inheriting the global one.
func (so SamplingOptions) hasClassOverrides() bool {
	return so.Scrape.Sampler != "" || so.Receiver.Sampler != "" || so.Background.Sampler != ""
}

// classSampler resolves one workload class's effective Sampler: its own
// override if configured, else fallback (the global sampler).
func classSampler(opts SamplerClassOptions, fallback sdktrace.Sampler) sdktrace.Sampler {
	if opts.Sampler == "" {
		return fallback
	}
	return buildSampler(opts.Sampler, opts.SamplerArg)
}

// classifyingSampler routes a ROOT sampling decision (no parent, or a remote
// parent under RemoteParentIgnore) to the sampler for the span's declared
// SpanClassKey attribute, falling back to the global sampler when no
// recognized class attribute is present. It is never consulted for a LOCAL
// parent (sdktrace.ParentBased handles local-parent inheritance itself,
// independent of and prior to reaching this type), which is what gives every
// configuration — regardless of which sampler flavor a class uses — the
// "child of a sampled parent is still sampled" guarantee.
type classifyingSampler struct {
	scrape, receiver, background sdktrace.Sampler
	fallback                     sdktrace.Sampler
}

func (s *classifyingSampler) classFor(v string) sdktrace.Sampler {
	switch v {
	case SpanClassScrape:
		return s.scrape
	case SpanClassReceiver:
		return s.receiver
	case SpanClassBackground:
		return s.background
	default:
		return nil
	}
}

func (s *classifyingSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	for _, kv := range p.Attributes {
		if kv.Key == SpanClassKey {
			if cs := s.classFor(kv.Value.AsString()); cs != nil {
				return cs.ShouldSample(p)
			}
			break
		}
	}
	return s.fallback.ShouldSample(p)
}

func (s *classifyingSampler) Description() string {
	return fmt.Sprintf("classifying{scrape:%s,receiver:%s,background:%s,fallback:%s}",
		s.scrape.Description(), s.receiver.Description(), s.background.Description(), s.fallback.Description())
}

// RemoteParentContext resolves the context and SpanStartOptions a
// receiver-side tr.Start call site (internal/stream, internal/webhook) needs
// to apply the configured remote-parent trust policy, given a context already
// extracted from an inbound W3C traceparent (e.g. via a propagator's
// Extract). Only RemoteParentLink needs call-site help: a Link can only be
// attached at span Start, not from inside a Sampler, so this is the one part
// of #373 that cannot live in newSampler alone. RemoteParentTrust and
// RemoteParentIgnore need no call-site change — both are implemented entirely
// by the Sampler newSampler builds — so this returns ctx unchanged and no
// options for every policy except "link".
//
// When policy is RemoteParentLink and ctx carries a valid REMOTE parent span
// context, it returns ctx unchanged plus SpanStartOptions that make the
// upcoming span a new, independent root trace (trace.WithNewRoot) carrying a
// Link back to that remote span (trace.WithLinks) — so the local sampler's
// root/class decision governs this trace on its own, while the two traces
// stay correlatable via the link. When there is no valid remote parent (a
// local caller, or no parent at all), it is a no-op: there is nothing to link.
func RemoteParentContext(ctx context.Context, policy string) (context.Context, []trace.SpanStartOption) {
	if policy != RemoteParentLink {
		return ctx, nil
	}
	psc := trace.SpanContextFromContext(ctx)
	if !psc.IsValid() || !psc.IsRemote() {
		return ctx, nil
	}
	return ctx, []trace.SpanStartOption{
		trace.WithNewRoot(),
		trace.WithLinks(trace.Link{SpanContext: psc}),
	}
}
