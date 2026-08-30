package settings

import (
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation: name, unit, instrument, description, and attribute keys. The
// emit sites (settings.go) reference these descriptors so a description/unit
// cannot drift from what is documented; the doc generator (tools/metricscatalog,
// via internal/catalog) renders them into docs/metrics.md, and catalog_test.go
// asserts what the collector emits matches these declarations.
const groupSettings = "Settings"

var (
	docSettingEnabled = metricdoc.Metric{
		Name:        metricEnabled,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if the named tailnet setting is enabled, else `0`.",
		Attributes:  []string{attrSettingName},
		Group:       groupSettings,
	}
	docSettingKeyDuration = metricdoc.Metric{
		Name:        metricKeyDuration,
		Unit:        semconv.UnitDays,
		Instrument:  metricdoc.Gauge,
		Description: "Configured device key expiry duration, in days.",
		Group:       groupSettings,
	}
	docSettingRole = metricdoc.Metric{
		Name:        metricSettingRole,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Info gauge (constant `1`); the user role allowed to join external tailnets, carried as the `tailscale.setting.role` label.",
		Attributes:  []string{attrSettingRole},
		Group:       groupSettings,
	}
	docSettingsSnapshot = metricdoc.LogEvent{
		Name:     EventSettingsSnapshot,
		Severity: "INFO",
		Description: "The complete safe tailnet-settings configuration as JSON, emitted only when " +
			"collectors.settings.snapshot_enabled is set and the configuration changes or " +
			"its daily heartbeat is due. The ACL external-link value is reduced to a " +
			"presence boolean; large bodies are UTF-8-safe chunks that MUST be grouped by " +
			"tailscale.snapshot.emission_id and sorted by tailscale.snapshot.seq before reassembly.",
		Attributes: []string{
			semconv.AttrSnapshotKind, semconv.AttrSnapshotReason,
			semconv.AttrSnapshotRevision, semconv.AttrSnapshotEmissionID,
			semconv.AttrSnapshotBytes, semconv.AttrSnapshotSeq,
			semconv.AttrSnapshotTotal,
		},
		Group: groupSettings,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docSettingEnabled, docSettingKeyDuration, docSettingRole}
}

// LogCatalog returns the log events this package emits.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docSettingsSnapshot}
}
