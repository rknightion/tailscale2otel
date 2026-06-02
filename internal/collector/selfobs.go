package collector

import (
	"context"
	"errors"
	"time"

	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Per-collector self-observability metric names. Each carries the
// semconv.AttrCollector attribute identifying the collector that produced it,
// letting operators see scrape health per data source.
const (
	// MetricScrapeDuration is a gauge of the wall-clock seconds a collector run
	// took (the snapshot Collect or window CollectWindow call).
	MetricScrapeDuration = "tailscale2otel.scrape.duration"
	// MetricScrapeSuccess is a gauge that is 1 when the run completed without
	// error and 0 otherwise (including recovered panics).
	MetricScrapeSuccess = "tailscale2otel.scrape.success"
	// MetricScrapeErrors is a monotonic counter incremented once per failed run,
	// carrying an "error.type" attribute classifying the failure.
	MetricScrapeErrors = "tailscale2otel.scrape.errors"
	// MetricScrapeLastTimestamp is a gauge of the unix time, in seconds, at which
	// the most recent run finished.
	MetricScrapeLastTimestamp = "tailscale2otel.scrape.last_timestamp"
)

// error.type values for MetricScrapeErrors.
const (
	scrapeErrorTimeout = "timeout"
	scrapeErrorPanic   = "panic"
	scrapeErrorGeneric = "error"
)

// scrapeResult captures the outcome of a single collector run for self-obs
// emission. A non-nil err marks a failure; panicked overrides err's
// classification with the "panic" error.type.
type scrapeResult struct {
	collector  string
	duration   time.Duration
	finishedAt time.Time
	err        error
	panicked   bool
}

// emitScrapeMetrics records the four per-collector scrape metrics for one run
// using the given emitter. It always emits duration, success, and
// last_timestamp; the errors counter is incremented only when the run failed.
func emitScrapeMetrics(e telemetry.Emitter, res scrapeResult) {
	attrs := telemetry.Attrs{semconv.AttrCollector: res.collector}

	e.Gauge(MetricScrapeDuration, semconv.UnitSeconds,
		"wall-clock seconds a collector scrape took", res.duration.Seconds(), attrs)

	failed := res.err != nil || res.panicked
	success := 1.0
	if failed {
		success = 0
	}
	e.Gauge(MetricScrapeSuccess, semconv.UnitDimensionless,
		"1 if the collector scrape succeeded, else 0", success, attrs)

	e.Gauge(MetricScrapeLastTimestamp, semconv.UnitSeconds,
		"unix time in seconds when the collector scrape finished",
		float64(res.finishedAt.Unix()), attrs)

	if failed {
		errAttrs := telemetry.Attrs{
			semconv.AttrCollector: res.collector,
			"error.type":          scrapeErrorType(res.err, res.panicked),
		}
		e.Counter(MetricScrapeErrors, semconv.UnitDimensionless,
			"collector scrape failures by error.type", 1, errAttrs)
	}
}

// scrapeErrorType classifies a failed run for the "error.type" attribute:
// "panic" for a recovered panic, "timeout" for a deadline-exceeded error, and
// "error" otherwise.
func scrapeErrorType(err error, panicked bool) string {
	if panicked {
		return scrapeErrorPanic
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return scrapeErrorTimeout
	}
	return scrapeErrorGeneric
}
