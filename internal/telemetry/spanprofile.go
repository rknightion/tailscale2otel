package telemetry

import (
	otelpyroscope "github.com/grafana/otel-profiling-go"
	"go.opentelemetry.io/otel/trace"
)

// Pyroscope span-profile correlation (#370). Owned by the #370 lane.

// ProfileOptions carries the opt-in Pyroscope span-profile correlation
// settings. A zero value must leave the TracerProvider unwrapped and add no
// dependency on the profiler being running.
type ProfileOptions struct {
	// Enabled turns on span<->profile correlation via
	// github.com/grafana/otel-profiling-go. The caller (internal/app) must
	// gate this to true only when BOTH tracing.enabled AND
	// profiling.pyroscope.enabled are true — this package has no notion of
	// either config block and does not re-check that cross-field rule
	// itself; wrapTracerProviderForProfiles trusts opts.Enabled as given.
	Enabled bool
}

// wrapTracerProviderForProfiles wraps tp with Grafana's OTel profiling
// bridge (github.com/grafana/otel-profiling-go) when opts.Enabled, so
// sampled CPU work is reachable from a Grafana trace-to-profile link.
//
// The bridge works by annotating the current goroutine's runtime pprof
// labels (span_id, trace_id, and — for the local root span only, its
// upstream default — span_name) for the duration of every started span, and
// by adding a `pyroscope.profile.id` attribute to spans that can carry a
// profile. Go's CPU profiler reads those pprof labels at sample time, which
// is how Pyroscope attributes a sampled stack back to the span that was
// running; see PROFILE TYPE COVERAGE in this lane's report for which
// Pyroscope profile types that reaches and which it does not.
//
// A zero/disabled ProfileOptions (opts.Enabled == false, the default)
// returns tp COMPLETELY UNCHANGED: no otelpyroscope.NewTracerProvider call,
// no wrapper value allocated, and therefore no dependency on a Pyroscope
// profiler actually running anywhere in the process.
//
// Ordering: there is no constraint relative to starting the Pyroscope agent
// (internal/app/profiling.go's startProfiling / pyroscope.Start). The bridge
// only ever touches per-goroutine pprof label state at span start/end; Go's
// CPU profiler consults whatever labels are attached to a goroutine at the
// moment it samples it, regardless of whether the profiler was started
// before or after this wrap call. Wrapping may therefore happen in either
// order relative to starting the profiler.
//
// Shutdown stays safe by construction: this function only changes what
// tp.Tracer(...) is called through to create spans. It has no effect on, and
// is never itself involved in, calling Shutdown on the underlying
// *sdktrace.TracerProvider — callers must keep shutting that down exactly as
// before, using the original (unwrapped) value they already hold.
func wrapTracerProviderForProfiles(tp trace.TracerProvider, opts ProfileOptions) trace.TracerProvider {
	if !opts.Enabled {
		return tp
	}
	return otelpyroscope.NewTracerProvider(tp)
}
