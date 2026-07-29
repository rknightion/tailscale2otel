package annotations

import (
	"github.com/rknightion/tailscale2otel/v3/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
)

// Self-observability metric names. They live in the tailscale2otel.* namespace
// because they describe this process, not a tailnet.
const (
	// MetricPublished counts annotations Grafana accepted, by category.
	MetricPublished = "tailscale2otel.annotation.published"
	// MetricDropped counts annotations that did NOT reach Grafana, by reason.
	// One counter rather than one per failure mode: the reason set is closed
	// and small, and splitting it would multiply the dashboard surface without
	// telling an operator anything the label does not.
	MetricDropped = "tailscale2otel.annotation.dropped"
	// MetricDegraded is 1 while the most recent write failed and no later write
	// has succeeded.
	MetricDegraded = "tailscale2otel.annotation.degraded"
)

// unitAnnotation is the UCUM annotation unit for a count of annotations.
const unitAnnotation = "{annotation}"

// Descriptors for the emit sites in this package.
var (
	DocPublished = metricdoc.Metric{
		Name:       MetricPublished,
		Unit:       unitAnnotation,
		Instrument: metricdoc.Counter,
		Description: "Annotations accepted by the Grafana annotations API since process start, by " +
			"`category`. Only emitted when `grafana_annotations.url` is set; a deployment with " +
			"annotations off has no such series at all.",
		Attributes: []string{"category"},
		Group:      appcatalog.GroupSelfObs,
	}
	DocDropped = metricdoc.Metric{
		Name:       MetricDropped,
		Unit:       unitAnnotation,
		Instrument: metricdoc.Counter,
		Description: "Annotations that did not reach Grafana since process start, by bounded `reason`. " +
			"`duplicate` is the STEADY STATE on a snapshot-shaped source (a key stays inside its " +
			"expiry window for days and is re-observed every poll) and is not a fault; " +
			"`queue_full` and `local_rate_limited` are this process's own guards; " +
			"`unauthorized`, `rate_limited`, `rejected`, `server_error` and `transport` are " +
			"Grafana's verdict. A climbing `unauthorized` is an expired or under-scoped token.",
		Attributes: []string{"reason"},
		Group:      appcatalog.GroupSelfObs,
	}
	DocDegraded = metricdoc.Metric{
		Name:       MetricDegraded,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "Whether the most recent Grafana annotation write failed and has not been " +
			"recovered by a later success (`1` degraded, `0` healthy). The counterpart to " +
			"`tailscale2otel.annotation.dropped`: the counter says how much was lost, this says " +
			"whether it is still happening.",
		Group: appcatalog.GroupSelfObs,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{DocPublished, DocDropped, DocDegraded}
}
