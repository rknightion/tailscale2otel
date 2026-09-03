package apistate

import (
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// GroupAPIState is the docs/metrics.md section these signals render under.
// They describe the exporter's own view of the upstream API, so they belong
// with the rest of the self-observability surface.
const GroupAPIState = "Self-observability"

// Metric source names.
const (
	MetricAvailability = "tailscale2otel.api.availability"
	MetricLastProbe    = "tailscale2otel.api.last_probe"

	MetricSubrequestAttempts = "tailscale2otel.subrequest.attempts"
	MetricSubrequestFailures = "tailscale2otel.subrequest.failures"
	MetricSubrequestCoverage = "tailscale2otel.subrequest.coverage"
)

var (
	docAvailability = metricdoc.Metric{
		Name:       MetricAvailability,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "`1` for the API operation's current availability state, `0` for every other state. " +
			"The full state set is always emitted (zero-seeded), so a state that stops occurring reads as `0` " +
			"rather than disappearing. States: `supported`, `disabled` (feature off or not configured — expected, " +
			"not a fault), `scope_denied` (HTTP 403, the credential lacks the scope), `credential_rejected` " +
			"(HTTP 401), `transient_failure` (429/5xx/network/timeout — retryable), `request_rejected` " +
			"(any other 4xx: the API refused the request this exporter built, so retrying it unchanged " +
			"cannot succeed — terminal and our fault), `unknown` (not yet probed). " +
			"**`disabled` and `scope_denied` are deliberately distinct** — alert on the latter, never the former. " +
			"**`request_rejected` and `transient_failure` are likewise distinct**: conflating them let a 400 " +
			"on every single tick masquerade as upstream flakiness (#523).",
		Attributes: []string{semconv.AttrCollector, semconv.AttrAPIOperation, semconv.AttrAPIState},
		Group:      GroupAPIState,
	}
	docLastProbe = metricdoc.Metric{
		Name:        MetricLastProbe,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp the API operation was last probed (dashboards subtract `time()`).",
		Attributes:  []string{semconv.AttrCollector, semconv.AttrAPIOperation},
		Group:       GroupAPIState,
		TimeSource:  metricdoc.TimestampProcessLocal,
	}
	docSubrequestAttempts = metricdoc.Metric{
		Name:        MetricSubrequestAttempts,
		Unit:        semconv.UnitRequests,
		Instrument:  metricdoc.Counter,
		Description: "Per-entity subrequests attempted, by bounded subrequest type (e.g. one posture-attributes call per device).",
		Attributes:  []string{semconv.AttrCollector, semconv.AttrSubrequest},
		Group:       GroupAPIState,
	}
	docSubrequestFailures = metricdoc.Metric{
		Name:       MetricSubrequestFailures,
		Unit:       semconv.UnitRequests,
		Instrument: metricdoc.Counter,
		Description: "Per-entity subrequests that failed, by bounded subrequest type and availability state. " +
			"A single entity's failure is non-fatal to the enclosing snapshot, so without this signal a " +
			"missing scope silently degrades coverage while the collector still reports a clean scrape.",
		Attributes: []string{semconv.AttrCollector, semconv.AttrSubrequest, semconv.AttrAPIState},
		Group:      GroupAPIState,
	}
	docSubrequestCoverage = metricdoc.Metric{
		Name:       MetricSubrequestCoverage,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "Fraction of per-entity subrequests that succeeded on the last pass, in `[0,1]` " +
			"(a genuine ratio, unlike most `_ratio` metrics here). `1` when nothing was attempted — " +
			"an empty tailnet has complete coverage of the entities it has.",
		Attributes: []string{semconv.AttrCollector, semconv.AttrSubrequest},
		Group:      GroupAPIState,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docAvailability, docLastProbe,
		docSubrequestAttempts, docSubrequestFailures, docSubrequestCoverage,
	}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent { return nil }
