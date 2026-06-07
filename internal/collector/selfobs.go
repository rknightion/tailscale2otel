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
	// MetricScrapeStaleness is a gauge of the seconds elapsed since this
	// collector's last *successful* run. It counts up from process start until
	// the first success (so a collector that has never succeeded shows a growing,
	// alertable value rather than an absent series) and resets to ~0 on every
	// successful run. Explicit is friendlier than deriving freshness from
	// scrape.last_timestamp + scrape.success.
	MetricScrapeStaleness = "tailscale2otel.scrape.staleness"
	// MetricScrapeBudget is a gauge of the last run's duration as a fraction of
	// the collector's poll interval (duration ÷ interval). Values near or above
	// `1` mean a scrape is taking about as long as (or longer than) its interval
	// — little headroom, risk of overrun. The unit-`1` gauge normalizes to
	// tailscale2otel_scrape_budget_ratio under OTLP→Prometheus.
	MetricScrapeBudget = "tailscale2otel.scrape.budget"
	// MetricCheckpointPersistErrors is a monotonic counter incremented when a
	// window collector's high-water mark fails to persist to the checkpoint
	// store (e.g. a disk error). The window itself succeeded; only the durable
	// checkpoint write failed, so the next tick re-polls the same window.
	MetricCheckpointPersistErrors = "tailscale2otel.checkpoint.persist.errors"
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
	interval   time.Duration
	finishedAt time.Time
	staleness  time.Duration
	err        error
	panicked   bool
}

// emitScrapeMetrics records the per-collector scrape metrics for one run using
// the given emitter. It always emits the gauges; the errors counter is
// incremented only when the run failed.
func emitScrapeMetrics(e telemetry.Emitter, res scrapeResult) {
	attrs := telemetry.Attrs{semconv.AttrCollector: res.collector}

	e.Gauge(docScrapeDuration.Name, docScrapeDuration.Unit, docScrapeDuration.Description,
		res.duration.Seconds(), attrs)

	failed := res.err != nil || res.panicked
	success := 1.0
	if failed {
		success = 0
	}
	e.Gauge(docScrapeSuccess.Name, docScrapeSuccess.Unit, docScrapeSuccess.Description, success, attrs)

	e.Gauge(docScrapeLastTimestamp.Name, docScrapeLastTimestamp.Unit, docScrapeLastTimestamp.Description,
		float64(res.finishedAt.Unix()), attrs)

	e.Gauge(docScrapeStaleness.Name, docScrapeStaleness.Unit, docScrapeStaleness.Description,
		res.staleness.Seconds(), attrs)

	if res.interval > 0 { // guard: a zero/negative interval would make the ratio NaN (0/0) or Inf
		e.Gauge(docScrapeBudget.Name, docScrapeBudget.Unit, docScrapeBudget.Description,
			res.duration.Seconds()/res.interval.Seconds(), attrs)
	}

	if failed {
		errAttrs := telemetry.Attrs{
			semconv.AttrCollector: res.collector,
			"error.type":          scrapeErrorType(res.err, res.panicked),
		}
		e.Counter(docScrapeErrors.Name, docScrapeErrors.Unit, docScrapeErrors.Description, 1, errAttrs)
	}
}

// emitCheckpointPersistError records one MetricCheckpointPersistErrors increment
// for a collector whose checkpoint failed to persist, so a silently-failing
// checkpoint store (which stalls window progress on restart) is alertable.
func emitCheckpointPersistError(e telemetry.Emitter, collectorName string) {
	e.Counter(docCheckpointPersistErrors.Name, docCheckpointPersistErrors.Unit,
		docCheckpointPersistErrors.Description, 1,
		telemetry.Attrs{semconv.AttrCollector: collectorName})
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
