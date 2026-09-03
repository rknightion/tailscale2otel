package users

import (
	"github.com/rknightion/tailscale2otel/v5/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v5/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation: name, unit, instrument, description, and attribute keys. The
// emit sites (users.go) reference these descriptors so a description/unit cannot
// drift from what is documented; the doc generator (tools/metricscatalog, via
// internal/catalog) renders them into docs/metrics.md, and catalog_test.go
// asserts what the collector emits matches these declarations. user.last_seen is
// emitted only for users with a non-zero last-seen time; gating is documented in
// prose.
const groupUsers = "Users"

var (
	docUsersCount = metricdoc.Metric{
		Name:        MetricUsersCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "User count (a **count**), bucketed by role/status/type.",
		Attributes:  []string{attrRole, attrStatus, attrType},
		Group:       groupUsers,
	}
	docUserDevices = metricdoc.Metric{
		Name:        MetricUserDevices,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of devices owned by the user (a **count**).",
		Attributes:  []string{attrID, attrLogin},
		Group:       groupUsers,
	}
	docUserConnected = metricdoc.Metric{
		Name:        MetricUserConn,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if the user is currently connected, else `0`.",
		Attributes:  []string{attrID, attrLogin},
		Group:       groupUsers,
	}
	docUserLastSeen = metricdoc.Metric{
		Name:        MetricUserLastSeen,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp the user was last seen.",
		Attributes:  []string{attrID, attrLogin},
		Group:       groupUsers,
		TimeSource:  metricdoc.TimestampSource,
	}
	docUserInvites = metricdoc.Metric{
		Name:       MetricUserInvites,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "Outstanding open user invites (a **count**), by role and delivery method. The " +
			"list-user-invites endpoint returns only open (not yet accepted) invites, so this is a " +
			"snapshot of pending invitations — not accepted-invite history, which the API does not expose.",
		Attributes: []string{attrInviteRole, attrInviteDelivery},
		Group:      groupUsers,
	}
	docUserInvitePendingAge = metricdoc.Metric{
		Name:       MetricUserInvitePending,
		Unit:       semconv.UnitSeconds,
		Instrument: metricdoc.Histogram,
		Description: "Distribution of time since Tailscale last emailed each pending invite (a " +
			"**distribution**). Emitted only for emailed invites — manual-link invites have no delivery " +
			"timestamp to measure age from, so they're omitted rather than reported as age zero.",
		Attributes: []string{attrInviteRole},
		Group:      groupUsers,
	}
	docUsersAge = metricdoc.Metric{
		Name:       MetricUsersAge,
		Unit:       semconv.UnitSeconds,
		Instrument: metricdoc.Histogram,
		Description: "Distribution of user account age (a **distribution**), i.e. time since each " +
			"user was created. Users with no reported creation time are omitted rather than reported " +
			"as age zero.",
		Group: groupUsers,
	}
	docUserInviteObserved = metricdoc.LogEvent{
		Name:        EventUserInviteObserved,
		Severity:    "INFO",
		Description: "Emitted once when an open user invite is first observed in a successful snapshot. This is an observation, not proof of when the invite was created; the API exposes no invite-created timestamp. The invite URL is a bearer token and is never emitted. User identity attributes follow the PII filter.",
		Attributes:  []string{attrInviteID, attrInviteRole, attrLifecycleTransition, attrID, attrLogin},
		Group:       groupUsers,
	}
	docUserInviteNoLongerOpen = metricdoc.LogEvent{
		Name:        EventUserInviteNoLongerOpen,
		Severity:    "INFO",
		Description: "Emitted once when an invite present in an earlier successful open-invite snapshot is absent from a later successful snapshot. The API exposes no terminal reason, so absence is not classified as accepted, revoked, or canceled. The invite URL is never retained or emitted; user identity attributes follow the PII filter.",
		Attributes:  []string{attrInviteID, attrInviteRole, attrLifecycleTransition, attrID, attrLogin},
		Group:       groupUsers,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docUsersCount, docUserDevices, docUserConnected, docUserLastSeen,
		docUserInvites, docUserInvitePendingAge, docUsersAge,
	}
}

// LogCatalog returns the log events this package emits.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docUserInviteObserved, docUserInviteNoLongerOpen}
}
