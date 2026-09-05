package pam

import (
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

const groupPAM = "PAM"

var (
	docConnectors = metricdoc.Metric{Name: metricConnectors, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Configured Border0 PAM connector count.", Group: groupPAM}
	docConnectorConnected = metricdoc.Metric{Name: metricConnectorConnected, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "`1` when the named PAM connector currently reports connected, else `0`. Connector names are operator-supplied and subject to the configured metric-cardinality limit.", Attributes: []string{attrConnectorName}, Group: groupPAM}
	docConnectorLastSeenAge = metricdoc.Metric{Name: metricConnectorLastSeenAge, Unit: semconv.UnitSeconds, Instrument: metricdoc.Gauge,
		Description: "Seconds since the named PAM connector was last seen. Connector names are operator-supplied and subject to the configured metric-cardinality limit.", Attributes: []string{attrConnectorName}, Group: groupPAM}
	docConnectorSockets = metricdoc.Metric{Name: metricConnectorSockets, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "PAM service count reported by the named connector (a count, despite `_ratio`). This is connector-local Border0 state, not Tailscale Service port inventory.", Attributes: []string{attrConnectorName}, Group: groupPAM}
	docConnectorTokens = metricdoc.Metric{Name: metricConnectorArtifacts, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Active token metadata count reported by the named PAM connector (a count, despite `_ratio`).", Attributes: []string{attrConnectorName}, Group: groupPAM}
	docConnectorPlugins = metricdoc.Metric{Name: metricConnectorPlugins, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Active plugin count reported by the named PAM connector (a count, despite `_ratio`).", Attributes: []string{attrConnectorName}, Group: groupPAM}
	docConnectorInfo = metricdoc.Metric{Name: metricConnectorInfo, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Info gauge (constant `1`) carrying the named PAM connector's software version and build date.", Attributes: []string{attrConnectorName, attrVersion, attrBuiltDate}, Group: groupPAM}
	docServices = metricdoc.Metric{Name: metricServices, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Configured PAM service count by Border0 socket type (a count, despite `_ratio`); does not restate `tailscale.service.ports`.", Attributes: []string{attrServiceType}, Group: groupPAM}
	docServiceAlive = metricdoc.Metric{Name: metricServiceAlive, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "`1` when the named PAM service reports alive, else `0`; a Border0 health dimension, not Tailscale Service inventory. Service names are operator-supplied and subject to the configured metric-cardinality limit.", Attributes: []string{attrServiceName, attrServiceType}, Group: groupPAM}
	docServiceSettingEnabled = metricdoc.Metric{Name: metricServiceSettingEnabled, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "`1` when the stable named setting is enabled for the named PAM service, else `0`. Service names are operator-supplied and subject to the configured metric-cardinality limit.", Attributes: []string{attrServiceName, attrServiceType, attrSettingName}, Group: groupPAM}
	docPolicies = metricdoc.Metric{Name: metricPolicies, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Configured Border0 PAM policy count.", Group: groupPAM}
	docPolicySettingEnabled = metricdoc.Metric{Name: metricPolicySettingEnabled, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "`1` when the stable named boolean is enabled for the PAM policy, else `0`.", Attributes: []string{attrPolicyName, attrVersion, attrSettingName}, Group: groupPAM}
	docIdentities = metricdoc.Metric{Name: metricIdentities, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "PAM identity count by kind and role (a count, despite `_ratio`); service accounts are split by role so tag-mirrored client accounts remain distinguishable.", Attributes: []string{attrIdentityKind, attrIdentityRole}, Group: groupPAM}
	docOrgSettingEnabled = metricdoc.Metric{Name: metricOrgSettingEnabled, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "`1` when the stable named PAM organization setting is enabled, else `0`.", Attributes: []string{attrSettingName}, Group: groupPAM}
	docOrgPlanInfo = metricdoc.Metric{Name: metricOrgPlanInfo, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Info gauge (constant `1`) carrying the PAM organization's bounded plan slug.", Attributes: []string{attrPlan}, Group: groupPAM}
	docSubscriptionLimit = metricdoc.Metric{Name: metricSubscriptionLimit, Unit: semconv.UnitDimensionless, Instrument: metricdoc.Gauge,
		Description: "Configured PAM subscription limit keyed by its stable limit name (a count, despite `_ratio`).", Attributes: []string{attrLimitName}, Group: groupPAM}
	docSnapshot = metricdoc.LogEvent{Name: EventSnapshot, Severity: "INFO",
		Description: "Safe PAM inventory and configuration shape, emitted only when collectors.pam.snapshot_enabled is set and the content changes or its heartbeat is due. Upstream authentication objects, passwords, private keys, certificates, usernames and identity details are removed before serialization; large bodies are UTF-8-safe chunks grouped by tailscale.snapshot.emission_id.",
		Attributes:  []string{semconv.AttrSnapshotKind, semconv.AttrSnapshotReason, semconv.AttrSnapshotRevision, semconv.AttrSnapshotEmissionID, semconv.AttrSnapshotBytes, semconv.AttrSnapshotSeq, semconv.AttrSnapshotTotal}, Group: groupPAM}
	docSession = metricdoc.LogEvent{Name: EventSession, Severity: "INFO",
		Description: "One record for each newly accepted PAM session when `collectors.pam.session_log_enabled` is set. `tailscale.pam.session.authorization_result` is the authorization outcome, not connection health: a session can be authorized even if its upstream connection later fails. A tailnet grant-layer denial produces no session row and therefore no record. `tailscale.pam.session.duration_seconds` is a numeric string for complete sessions and `unknown` otherwise. Identity, address, device-name, and command attributes follow the configured `pii_filter` categories; `auth_info` and event metadata are never emitted.",
		Attributes: []string{attrSessionSocketName, attrSessionType, attrAuthorizationResult, attrSessionKilled, attrSessionRecordingType, attrSessionDuration,
			semconv.AttrUserName, semconv.AttrUserFullName, semconv.AttrUserID, attrSessionTailnetIP, attrSessionExternalIP, attrSessionClientPort, semconv.HostName, attrSessionCommand}, Group: groupPAMSessions}
)

// Catalog returns the metrics emitted by the PAM inventory collector.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docConnectors, docConnectorConnected, docConnectorLastSeenAge, docConnectorSockets,
		docConnectorTokens, docConnectorPlugins, docConnectorInfo, docServices, docServiceAlive,
		docServiceSettingEnabled, docPolicies, docPolicySettingEnabled, docIdentities,
		docOrgSettingEnabled, docOrgPlanInfo, docSubscriptionLimit,
	}
}

// LogCatalog returns the PAM log event declarations.
func LogCatalog() []metricdoc.LogEvent { return []metricdoc.LogEvent{docSnapshot, docSession} }
