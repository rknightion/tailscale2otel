package users

import (
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
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
	}
	docUserInvites = metricdoc.Metric{
		Name:        MetricUserInvites,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Outstanding/processed user invites (a **count**), by role and accepted flag.",
		Attributes:  []string{attrInviteRole, attrInviteAccepted},
		Group:       groupUsers,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docUsersCount, docUserDevices, docUserConnected, docUserLastSeen, docUserInvites}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
