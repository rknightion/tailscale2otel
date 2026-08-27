package appcatalog

import (
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

// Pyroscope upload-health metric names (#374). Continuous profiling used to be
// write-and-hope: the push agent turned every upload failure into a log line, so
// "are profiles actually arriving?" was unanswerable from telemetry and the
// admin page reported only that Pyroscope was CONFIGURED.
//
// These mirror the OTLP delivery metrics' split deliberately — attempts and
// failures as separate counters rather than one counter with a success/failure
// attribute — so the healthy path is a single, always-present series and the
// error dimension exists only where a failure actually occurred.
const (
	// MetricProfilingUploadAttempts counts every completed profile upload
	// attempt, successful or not.
	MetricProfilingUploadAttempts = "tailscale2otel.profiling.upload.attempts"
	// MetricProfilingUploadFailures counts failed attempts, by bounded error class.
	MetricProfilingUploadFailures = "tailscale2otel.profiling.upload.failures"
	// MetricProfilingUploadDuration is the per-attempt wall-clock latency histogram.
	MetricProfilingUploadDuration = "tailscale2otel.profiling.upload.duration"
	// MetricProfilingUploadLastSuccess is the Unix-seconds timestamp of the most
	// recent successful upload.
	MetricProfilingUploadLastSuccess = "tailscale2otel.profiling.upload.last_success"
	// MetricProfilingUploadConsecutiveFailures is the current unbroken failure
	// streak, reset to 0 by any success.
	MetricProfilingUploadConsecutiveFailures = "tailscale2otel.profiling.upload.consecutive_failures"
)

// ProfilingUploadErrorClasses is the CLOSED set of values the failures counter's
// error.type attribute may take.
//
// Closed for the same reason internal/telemetry's export-error classes are: a
// Pyroscope upload failure carries the server's response body, which is exactly
// where an echoed credential or a signed URL would appear. Classifying rather
// than forwarding means no server text ever reaches a metric label, the admin
// page, or a log line — and it bounds this counter to at most len(set) series.
//
// The classifier that produces them lives in internal/app (profilinghealth.go);
// only the value set is declared here, next to the descriptor whose cardinality
// it bounds.
func ProfilingUploadErrorClasses() []string {
	return []string{
		"timeout",
		"canceled",
		"unauthenticated",
		"rate_limited",
		"unavailable",
		"tls",
		"invalid",
		"other",
	}
}

// Pyroscope upload-health descriptors. Timestamps are Unix-seconds gauges,
// matching DocTLSCertReloadedAt's convention; the streak is a plain count gauge.
var (
	// DocProfilingUploadAttempts documents the total-attempts counter.
	DocProfilingUploadAttempts = metricdoc.Metric{
		Name:       MetricProfilingUploadAttempts,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Counter,
		Description: "Profile upload attempts to Pyroscope, successful or not (one per profile type per " +
			"`profiling.pyroscope.upload_rate` period). Emitted only when the Pyroscope push agent is " +
			"enabled. A flat line here with the agent enabled means the agent is not uploading at all, " +
			"which is a different fault from uploads being rejected.",
		Group: GroupSelfObs,
	}
	// DocProfilingUploadFailures documents the failure counter, keyed by class.
	DocProfilingUploadFailures = metricdoc.Metric{
		Name:       MetricProfilingUploadFailures,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Counter,
		Description: "Profile upload attempts that failed, by bounded `error.type` " +
			"(timeout|canceled|unauthenticated|rate_limited|unavailable|tls|invalid|other). The class set is " +
			"CLOSED and never contains any part of the server's response — a Pyroscope error body is a " +
			"credential-echo surface. `unauthenticated` means the basic-auth credential or tenant is wrong; " +
			"`tls` means the custom CA / client certificate did not satisfy the handshake.",
		Attributes: []string{semconv.AttrErrorType},
		Group:      GroupSelfObs,
	}
	// DocProfilingUploadDuration documents the per-attempt latency histogram.
	DocProfilingUploadDuration = metricdoc.Metric{
		Name:       MetricProfilingUploadDuration,
		Unit:       semconv.UnitSeconds,
		Instrument: metricdoc.Histogram,
		Description: "Wall-clock seconds per profile upload attempt, including failed ones. Rising latency " +
			"here is the early warning for the upload timeout that follows.",
		Group: GroupSelfObs,
	}
	// DocProfilingUploadLastSuccess documents the last-success timestamp gauge.
	DocProfilingUploadLastSuccess = metricdoc.Metric{
		Name:       MetricProfilingUploadLastSuccess,
		Unit:       semconv.UnitSeconds,
		Instrument: metricdoc.Gauge,
		Description: "Unix seconds of the most recent SUCCESSFUL profile upload; `0` until the first one " +
			"succeeds. Alert on `time() - this` exceeding several upload periods to catch profiles silently " +
			"stopping — the attempts counter keeps climbing during an outage, so it cannot tell you this.",
		Group:      GroupSelfObs,
		TimeSource: metricdoc.TimestampProcessLocal,
	}
	// DocProfilingUploadConsecutiveFailures documents the failure-streak gauge.
	DocProfilingUploadConsecutiveFailures = metricdoc.Metric{
		Name:       MetricProfilingUploadConsecutiveFailures,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "Current unbroken profile-upload failure streak, reset to `0` by any success (a " +
			"**count**, despite the `_ratio` Prometheus suffix). Distinguishes a blip from a sustained " +
			"outage without needing a rate window.",
		Group: GroupSelfObs,
	}
)

// ProfilingCatalog returns the Pyroscope upload-health descriptors.
//
// It is a separate function from Catalog() purely so it could be added without
// editing catalog.go while that file was owned by another change; the intended
// end state is one line in Catalog() appending these. Until then these metrics
// are emitted but undocumented in docs/metrics.md.
func ProfilingCatalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		DocProfilingUploadAttempts,
		DocProfilingUploadFailures,
		DocProfilingUploadDuration,
		DocProfilingUploadLastSuccess,
		DocProfilingUploadConsecutiveFailures,
	}
}
