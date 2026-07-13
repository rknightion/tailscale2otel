package flowlogs

import (
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this collector's own
// metric documentation. This is the flow-logs POLL collector; the bytes/packets/
// flows metrics and flow log records come from the shared flowlog.Processor
// (see internal/flowlog) and are cataloged there. The only metric this package
// emits directly is the feature.enabled gauge. The emit site (flowlogs.go)
// references this descriptor so the unit/description cannot drift from what is
// documented; catalog_test.go asserts it. feature.enabled is emitted only when a
// feature check runs (or a 403 self-disables the feature); gating is documented
// in prose.
const groupFeatures = "Features"

var docFeatureEnabled = metricdoc.Metric{
	Name:        metricFeatureEnabled,
	Unit:        semconv.UnitDimensionless,
	Instrument:  metricdoc.Gauge,
	Description: "`1` if the named tailnet feature is enabled, else `0`; one series per feature.",
	Attributes:  []string{semconv.AttrFeature},
	Group:       groupFeatures,
}

// Catalog returns the metrics this package emits directly, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docFeatureEnabled}
}

// LogCatalog returns the log events this package emits (none; flow log records
// are emitted by the shared flowlog.Processor).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
