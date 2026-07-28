package telemetry

// Pyroscope span-profile correlation (#370). Owned by the #370 lane.

// ProfileOptions carries the opt-in Pyroscope span-profile correlation
// settings. A zero value must leave the TracerProvider unwrapped and add no
// dependency on the profiler being running.
type ProfileOptions struct{}
