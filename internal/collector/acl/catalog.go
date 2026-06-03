package acl

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation: name, unit, instrument, description, and attribute keys. The
// emit sites (acl.go) reference these descriptors so a description/unit cannot
// drift from what is documented; the doc generator (tools/metricscatalog, via
// internal/catalog) renders them into docs/metrics.md, and catalog_test.go
// asserts what the collector emits matches these declarations. acl.rules is
// emitted once per recognized policy section that is present; gating is
// documented in prose.
const groupACL = "ACL"

var (
	docACLLastChanged = metricdoc.Metric{
		Name:        metricLastChanged,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp the ACL policy last changed (detected by ETag).",
		Group:       groupACL,
	}
	docACLSize = metricdoc.Metric{
		Name:        metricSize,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Gauge,
		Description: "Size of the current ACL policy document, in bytes.",
		Group:       groupACL,
	}
	docACLRules = metricdoc.Metric{
		Name:        metricRules,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of rules per ACL section (a **count**, despite `_ratio`).",
		Attributes:  []string{attrSection},
		Group:       groupACL,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docACLLastChanged, docACLSize, docACLRules}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
