package audit

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// and log-event documentation: name, unit, instrument, description, and the
// attribute keys carried. The emit sites (processor.go) reference these
// descriptors so a description/unit cannot drift from what is documented; the
// doc generator (tools/metricscatalog, via internal/catalog) renders them into
// docs/metrics.md, and catalog_test.go asserts what the processor emits matches
// these declarations.
//
// These belong to the "Network / flow" doc section (alongside the flow metrics
// from internal/flowlog). Several audit log attributes (old/new/details/error)
// are conditional on the event content; they appear here as the full possible set.
const groupNetwork = "Network / flow"

var (
	docAuditEvents = metricdoc.Metric{
		Name:        MetricAuditEvents,
		Unit:        semconv.UnitEvents,
		Instrument:  metricdoc.Counter,
		Description: "Configuration-audit events, by action and origin.",
		Attributes:  []string{attrAction, attrOrigin},
		Group:       groupNetwork,
	}

	docAuditLog = metricdoc.LogEvent{
		Name:        auditEventName,
		Severity:    "INFO",
		Description: "Per configuration-audit event: actor, target, action, and (when present) the before/after change. Emitted at **WARN** when the event carries an error, otherwise INFO.",
		Attributes: []string{
			attrAction, attrOrigin, attrEventGroupID, attrEndUserID,
			attrActorLogin, attrActorDisplay,
			attrTargetID, attrTargetName, attrTargetType, attrTargetProperty,
			attrOld, attrNew, attrDetails, attrError,
		},
		Group: groupNetwork,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docAuditEvents}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docAuditLog}
}
