package pam

import (
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

const groupPAMSessions = "PAM sessions"

var (
	docPAMSessions = metricdoc.Metric{
		Name:        metricSessions,
		Unit:        "{session}",
		Instrument:  metricdoc.Counter,
		Description: "PAM sessions authorized to reach a connector; this is authorization outcome telemetry, not connection-health or access-attempt telemetry.",
		Attributes:  []string{attrServiceName, attrSessionType, attrAuthorizationResult},
		Group:       groupPAMSessions,
	}
	docPAMSessionDuration = metricdoc.Metric{
		Name:        metricSessionDuration,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Histogram,
		Description: "Duration of completed PAM sessions, in seconds.",
		Attributes:  []string{attrSessionType},
		Group:       groupPAMSessions,
	}
	docPAMSessionsKilled = metricdoc.Metric{
		Name:        metricSessionsKilled,
		Unit:        "{session}",
		Instrument:  metricdoc.Counter,
		Description: "PAM sessions reported as killed by Border0.",
		Attributes:  []string{attrServiceName, attrSessionType},
		Group:       groupPAMSessions,
	}
	docPAMSessionsActive = metricdoc.Metric{
		Name:        metricSessionsActive,
		Unit:        "{session}",
		Instrument:  metricdoc.Gauge,
		Description: "Active PAM sessions visible in the newest-first polling prefix, identified by an absent end time.",
		Attributes:  []string{attrServiceName, attrSessionType},
		Group:       groupPAMSessions,
	}
	docPAMSessionEvents = metricdoc.Metric{
		Name:        metricSessionEvents,
		Unit:        semconv.UnitEvents,
		Instrument:  metricdoc.Counter,
		Description: "Bounded PAM session events observed on newly accepted session records, by event type and status; event metadata is never emitted.",
		Attributes:  []string{attrSessionEventType, attrSessionEventStatus},
		Group:       groupPAMSessions,
	}
)

// SessionCatalog returns the metric declarations emitted by the independently
// scheduled PAM session collector.
func SessionCatalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docPAMSessions,
		docPAMSessionDuration,
		docPAMSessionsKilled,
		docPAMSessionsActive,
		docPAMSessionEvents,
	}
}
