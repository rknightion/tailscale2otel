package acl

import (
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
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

// Policy-validation metric source names (see validate.go for emission logic).
const (
	metricValidationOK           = "tailscale.acl.validation.ok"
	metricValidationErrors       = "tailscale.acl.validation.errors"
	metricValidationWarnings     = "tailscale.acl.validation.warnings"
	metricValidationTestFailures = "tailscale.acl.validation.test_failures"
)

// attrValidationKind labels a validation_issue log event with the bounded
// kind of issue found (error | warning | test_failure). Registered in the PII
// registry (internal/telemetry/pii) as a closed, non-identifying enum: the
// validator's free-text messages (rule text, usernames, addresses) are
// deliberately never decoded past a count, so nothing free-text can ever reach
// this or any other attribute (#428).
const attrValidationKind = "tailscale.acl.validation.kind"

// Bounded values for attrValidationKind.
const (
	validationKindError       = "error"
	validationKindWarning     = "warning"
	validationKindTestFailure = "test_failure"
)

// EventRiskyRule is the OTLP log event name emitted once per unrestricted rule
// (wildcard src AND wildcard dst in a non-deny rule). The event body names the
// offending src and dst entries so operators can identify the specific rule.
const EventRiskyRule = "tailscale.acl.risky_rule"

// EventPolicySnapshot carries an opt-in raw policy body. Chunks share the
// canonical tailscale.snapshot.emission_id and are reassembled by sequence.
const EventPolicySnapshot = "tailscale.acl.policy_snapshot"

// EventPolicyDiff carries the unified diff between the prior and current raw
// policy body on a revision change.
const EventPolicyDiff = "tailscale.acl.policy_diff"

// EventValidationIssue is the OTLP log event name emitted once per non-zero
// validation-issue kind found in the last policy validation. Unlike
// EventRiskyRule, it carries NO free text at all (not even under a droppable
// PII category) — only the bounded attrValidationKind attribute and a static,
// content-free body.
const EventValidationIssue = "tailscale.acl.validation_issue"

var (
	docACLLastChanged = metricdoc.Metric{
		Name:        metricLastChanged,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp when this exporter first observed the current ACL policy ETag. This is an approximate revision-observation time, not the policy modification time: the API exposes no last-modified field and polling can observe a revision late. It survives restarts when the checkpoint store is file-backed; an in-memory or failed checkpoint store resets it on restart.",
		Group:       groupACL,
		TimeSource:  metricdoc.TimestampPersistedObservation,
	}
	docACLLastAuditChange = metricdoc.Metric{
		Name:        metricLastAuditChange,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp of the newest classified ACL configuration-audit event, taken from the event's upstream source timestamp and persisted through the checkpoint store. Absent until an ACL audit event has been observed; delayed or backfilled events never move it backwards.",
		Group:       groupACL,
		TimeSource:  metricdoc.TimestampSource,
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

	docACLValidationOK = metricdoc.Metric{
		Name:       metricValidationOK,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "`1` if the tailnet's currently active ACL policy (including any tests embedded in its own " +
			"`tests` section) validated cleanly on the last check, else `0`. Absent entirely — not `0` — when the " +
			"validate call itself is unavailable (e.g. the credential lacks `policy_file:read`); see " +
			"`tailscale2otel.api.availability` for that state.",
		Group: groupACL,
	}
	docACLValidationErrors = metricdoc.Metric{
		Name:       metricValidationErrors,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "Count of generic validation errors on the last policy check (a **count**, despite `_ratio`). " +
			"Distinct from `tailscale.acl.validation.test_failures`: the documented API responses report embedded-test " +
			"failures separately, so this stays `0` in the common case.",
		Group: groupACL,
	}
	docACLValidationWarnings = metricdoc.Metric{
		Name:        metricValidationWarnings,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Count of validation warnings on the last policy check (e.g. a group not syncing from SCIM) (a **count**, despite `_ratio`).",
		Group:       groupACL,
	}
	docACLValidationTestFailures = metricdoc.Metric{
		Name:        metricValidationTestFailures,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Count of failed tests embedded in the policy's own `tests` section, evaluated against the policy's own rules on the last check (a **count**, despite `_ratio`).",
		Group:       groupACL,
	}

	docACLRiskyRule = metricdoc.LogEvent{
		Name:        EventRiskyRule,
		Severity:    "WARN",
		Description: "Emitted on a policy revision change for each policy-risk finding: unrestricted ACL/grant rules, wildcard SSH rules, or wildcard auto-approvers. Carries `tailscale.acl.risk_class`, `tailscale.acl.section`, and `tailscale.acl.rule` (the offending entries; a free-text attribute droppable via `pii_filter.free_text_details`).",
		Attributes:  []string{attrSection, attrRule, attrRiskClass},
		Group:       groupACL,
	}
	docACLPolicySnapshot = metricdoc.LogEvent{
		Name:        EventPolicySnapshot,
		Severity:    "INFO",
		Description: "Raw ACL policy body, emitted only when collectors.acl.snapshot_enabled is set and the revision changes or its heartbeat is due. Large bodies are UTF-8-safe chunks that MUST be grouped by tailscale.snapshot.emission_id, verified to have one matching tailscale.snapshot.revision, then sorted by tailscale.snapshot.seq before reassembly.",
		Attributes:  []string{"tailscale.acl.etag", "tailscale.snapshot.kind", "tailscale.snapshot.reason", "tailscale.snapshot.revision", "tailscale.snapshot.emission_id", "tailscale.snapshot.bytes", "tailscale.snapshot.seq", "tailscale.snapshot.total"},
		Group:       groupACL,
	}
	docACLPolicyDiff = metricdoc.LogEvent{
		Name:        EventPolicyDiff,
		Severity:    "INFO",
		Description: "Unified diff between the prior and current raw ACL policy bodies, emitted only with collectors.acl.snapshot_enabled on a changed policy revision. Large diffs use the same UTF-8-safe chunk contract as snapshots. The prior body is retained in the configured snapshot state store so the first post-restart revision still has a diff baseline.",
		Attributes:  []string{"tailscale.acl.etag", "tailscale.snapshot.kind", "tailscale.snapshot.reason", "tailscale.snapshot.revision", "tailscale.snapshot.emission_id", "tailscale.snapshot.bytes", "tailscale.snapshot.seq", "tailscale.snapshot.total"},
		Group:       groupACL,
	}
	docACLValidationIssue = metricdoc.LogEvent{
		Name:     EventValidationIssue,
		Severity: "WARN",
		Description: "Emitted once per validation-issue kind (`error`, `warning`, `test_failure`) whose count is " +
			"non-zero in the last policy validation. Carries ONLY the bounded `tailscale.acl.validation.kind` " +
			"attribute — the validator's free-text messages (rule text, usernames, addresses) are deliberately " +
			"never emitted, not even in the log body.",
		Attributes: []string{attrValidationKind},
		Group:      groupACL,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docACLLastChanged, docACLLastAuditChange, docACLSize, docACLRules,
		docACLWildcardRules, docACLUnrestricted, docACLAutoApprovers,
		docACLSSHWildcard, docACLPostureGated,
		docACLValidationOK, docACLValidationErrors, docACLValidationWarnings, docACLValidationTestFailures,
	}
}

// LogCatalog returns the log events this package emits.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docACLRiskyRule, docACLPolicySnapshot, docACLPolicyDiff, docACLValidationIssue}
}
