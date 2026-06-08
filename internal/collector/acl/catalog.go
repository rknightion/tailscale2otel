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

// Risk metric source names (see risk.go for emission logic).
const (
	metricWildcardRules = "tailscale.acl.wildcard_rules"
	metricUnrestricted  = "tailscale.acl.unrestricted_rules"
	metricAutoApprovers = "tailscale.acl.autoapprovers"
	metricSSHWildcard   = "tailscale.acl.ssh_wildcard"
	metricPostureGated  = "tailscale.acl.posture_gated_rules"
)

// EventRiskyRule is the OTLP log event name emitted once per unrestricted rule
// (wildcard src AND wildcard dst in a non-deny rule). The event body names the
// offending src and dst entries so operators can identify the specific rule.
const EventRiskyRule = "tailscale.acl.risky_rule"

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
	docACLWildcardRules = metricdoc.Metric{
		Name:        metricWildcardRules,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of non-deny ACL/grant rules with a wildcard (`*`) source or destination, per section and position (a **count**, despite `_ratio`).",
		Attributes:  []string{attrSection, attrPosition},
		Group:       groupACL,
	}
	docACLUnrestricted = metricdoc.Metric{
		Name:        metricUnrestricted,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of non-deny rules matching any source to any destination (wildcard `src` and `dst`), per section (a **count**, despite `_ratio`).",
		Attributes:  []string{attrSection},
		Group:       groupACL,
	}
	docACLAutoApprovers = metricdoc.Metric{
		Name:        metricAutoApprovers,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of auto-approver entries by kind (routes, exit_node, services) (a **count**, despite `_ratio`).",
		Attributes:  []string{attrApproverKind},
		Group:       groupACL,
	}
	docACLSSHWildcard = metricdoc.Metric{
		Name:        metricSSHWildcard,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of Tailscale SSH rules with a wildcard (`*`) source or destination (a **count**, despite `_ratio`).",
		Group:       groupACL,
	}
	docACLPostureGated = metricdoc.Metric{
		Name:        metricPostureGated,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of rules gated by a device-posture condition (`srcPosture`), per section (a **count**, despite `_ratio`).",
		Attributes:  []string{attrSection},
		Group:       groupACL,
	}

	docACLRiskyRule = metricdoc.LogEvent{
		Name:        EventRiskyRule,
		Severity:    "WARN",
		Description: "Emitted once per unrestricted ACL/grant rule (wildcard `src` **and** wildcard `dst` in a non-deny rule). Carries `tailscale.acl.section` and `tailscale.acl.rule` (the offending src/dst entries; a free-text attribute droppable via `pii_filter.free_text_details`). The log body also names the rule for readability.",
		Attributes:  []string{attrSection, attrRule},
		Group:       groupACL,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docACLLastChanged, docACLSize, docACLRules,
		docACLWildcardRules, docACLUnrestricted, docACLAutoApprovers,
		docACLSSHWildcard, docACLPostureGated,
	}
}

// LogCatalog returns the log events this package emits.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docACLRiskyRule}
}
