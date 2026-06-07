package devices

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// and log-event documentation: name, unit, instrument, description, and the
// attribute keys carried. The emit sites (devices.go) reference these
// descriptors so a description/unit cannot drift from what is documented, and
// the doc generator (tools/metricscatalog, via internal/catalog) renders them
// into docs/metrics.md. catalog_test.go asserts what the collector actually
// emits matches these declarations.
//
// The device.* gauges and devices.count belong to the "Devices" doc section;
// the enrich.cache_* gauges (also emitted here, after refreshing the shared
// device cache) belong to the cross-cutting "Self-observability" section. The
// last_seen/key.expiry gauges and the routes.* gauges (gated by collect_routes)
// and the posture log (gated by collect_posture) are documented unconditionally;
// gating is described in prose.
const (
	groupDevices = "Devices"
	groupSelfObs = "Self-observability"
)

// deviceIdentityAttrs is the common per-device identity attribute set carried by
// the per-device gauges. os.version is present only for devices that report a
// distro version, and tailscale.tags only for devices that carry ACL tags, so
// both appear here as part of the full possible set.
var deviceIdentityAttrs = []string{semconv.HostName, semconv.HostID, semconv.OSType, semconv.OSVersion, semconv.AttrUser, semconv.AttrTags}

// postureInfoAttrs is the curated label set carried by the posture info gauge:
// device identity (host.name/host.id) plus the curated subset of posture
// attributes. Each posture label is set only when the source key is present in
// the device's posture map, so this is the full possible set.
var postureInfoAttrs = []string{
	semconv.HostName, semconv.HostID,
	attrPostureOS, attrPostureOSVersion, attrPostureTSVersion,
	attrPostureAutoUpdate, attrPostureEncrypted, attrPostureTrack,
}

var (
	docOnline = metricdoc.Metric{
		Name:        metricOnline,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if the device is currently online, else `0`.",
		Attributes:  deviceIdentityAttrs,
		Group:       groupDevices,
	}
	docLastSeen = metricdoc.Metric{
		Name:        metricLastSeen,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp the device was last seen.",
		Attributes:  deviceIdentityAttrs,
		Group:       groupDevices,
	}
	docKeyExpiry = metricdoc.Metric{
		Name:        metricKeyExpiry,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp the device node key expires.",
		Attributes:  deviceIdentityAttrs,
		Group:       groupDevices,
	}
	docUpdateAvailable = metricdoc.Metric{
		Name:        metricUpdateAvailable,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if a Tailscale client update is available for the device.",
		Attributes:  deviceIdentityAttrs,
		Group:       groupDevices,
	}
	docDERPLatency = metricdoc.Metric{
		Name:        metricDERPLatency,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Latency from the device to a DERP region; one series per region.",
		Attributes:  []string{semconv.HostName, semconv.HostID, attrDERPRegion, attrDERPPreferred},
		Group:       groupDevices,
	}
	docRoutesAdvertised = metricdoc.Metric{
		Name:        metricRoutesAdvertised,
		Unit:        semconv.UnitRoutes,
		Instrument:  metricdoc.Gauge,
		Description: "Number of subnet routes the device advertises. **Gated** by `collect_routes`.",
		Attributes:  []string{semconv.HostName, semconv.HostID},
		Group:       groupDevices,
	}
	docRoutesEnabled = metricdoc.Metric{
		Name:        metricRoutesEnabled,
		Unit:        semconv.UnitRoutes,
		Instrument:  metricdoc.Gauge,
		Description: "Number of advertised routes that are enabled/approved. **Gated** by `collect_routes`.",
		Attributes:  []string{semconv.HostName, semconv.HostID},
		Group:       groupDevices,
	}
	docDevicesCount = metricdoc.Metric{
		Name:        metricDevicesCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Fleet device count (a **count**, despite `_ratio`), bucketed by OS/authorized/external.",
		Attributes:  []string{semconv.OSType, attrAuthorized, attrExternal},
		Group:       groupDevices,
	}
	docDeviceInvites = metricdoc.Metric{
		Name:        metricDeviceInvites,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Device-share invites (accepted and pending) (a **count**, despite `_ratio`), bucketed by accepted/pending and the exit-node / multi-use exposure flags. **Gated** by `collect_device_invites` (one API call per device).",
		Attributes:  []string{attrInviteAccepted, attrInviteAllowExitNode, attrInviteMultiUse},
		Group:       groupDevices,
	}

	docDevicesUntagged = metricdoc.Metric{
		Name:        metricDevicesUntagged,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of non-external devices with no ACL tags (a **count**, despite `_ratio`); a tagging-hygiene signal. External (shared-in) devices are excluded — they can't be tagged by this tailnet.",
		Group:       groupDevices,
	}
	docDevicesEphemeral = metricdoc.Metric{
		Name:        metricDevicesEphemeral,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of ephemeral devices in the tailnet (a **count**, despite `_ratio`).",
		Group:       groupDevices,
	}
	docDevicesByVersion = metricdoc.Metric{
		Name:        metricDevicesByVersion,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Device count per normalized Tailscale client version (`major.minor.patch`; unparseable→`unknown`); one series per version. Devices with no reported version (external) are excluded.",
		Attributes:  []string{attrClientVersion},
		Group:       groupDevices,
	}
	docDevicesByTag = metricdoc.Metric{
		Name:        metricDevicesByTag,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Device count per ACL tag (a device with N tags counts in N series). **Gated** by `collect_tag_rollup`; capped by `tag_rollup_limit` with overflow tags folded into `tailscale.tag=\"__other__\"`.",
		Attributes:  []string{attrTag},
		Group:       groupDevices,
	}
	docDevicesKeyExpiry = metricdoc.Metric{
		Name:        metricDevicesKeyExpiry,
		Unit:        semconv.UnitDays,
		Instrument:  metricdoc.Histogram,
		Description: "Distribution of days until each device's node key expires (negative = already expired; the `(-inf,0]` bucket). Excludes devices with key expiry disabled. Buckets (days): 0, 7, 30, 90, 180, 365.",
		Group:       groupDevices,
	}

	docDeviceVersionSkew = metricdoc.Metric{
		Name:        metricDeviceVersionSkew,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Minor releases this device's Tailscale client is behind the latest stable (`latest.minor − device.minor`, same major, clamped ≥0; patch-only drift is 0 — see `tailscale.device.update_available` for that). Per-device, gated by `cardinality.per_entity.device`. Emitted only when `version_checks.devices` is enabled, the upstream latest is known, and the device version parses.",
		Attributes:  deviceIdentityAttrs,
		Group:       groupDevices,
	}
	docFleetLatestVersion = metricdoc.Metric{
		Name:        metricFleetLatestVersion,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Always `1`; an info gauge whose `tailscale.client_version` label carries the latest Tailscale stable client version (`major.minor.patch`) as fetched from pkgs.tailscale.com. Emitted only when `version_checks.devices` is enabled and the upstream fetch has succeeded.",
		Attributes:  []string{attrClientVersion},
		Group:       groupDevices,
	}
	docDevicesOutdated = metricdoc.Metric{
		Name:        metricDevicesOutdated,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of devices at least `version_checks.devices.outdated_minor_threshold` minor releases behind the latest Tailscale stable (a **count**, despite `_ratio`). Fleet-wide, no labels. Emitted only when `version_checks.devices` is enabled and the upstream latest is known.",
		Group:       groupDevices,
	}

	docCacheAge = metricdoc.Metric{
		Name:        metricCacheAge,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Age of the device-enrichment cache (since last refresh).",
		Group:       groupSelfObs,
	}
	docCacheSize = metricdoc.Metric{
		Name:        metricCacheSize,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of devices in the enrichment cache (a **count**, despite `_ratio`).",
		Group:       groupSelfObs,
	}

	docPosture = metricdoc.LogEvent{
		Name:        eventPosture,
		Severity:    "INFO",
		Description: "Per-device posture/identity snapshot, carrying the device identity plus the posture attributes reported by the API. **Gated** by `collect_posture`; by default emitted only when a device's posture changes (see `posture_log_mode`).",
		Attributes:  []string{semconv.HostName, semconv.HostID},
		Group:       groupDevices,
	}

	docPostureInfo = metricdoc.Metric{
		Name:        eventPosture,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Per-device posture info gauge (constant `1`); device security posture — OS, Tailscale client version, auto-update, state-encrypted, release track — carried as labels. **Gated** by `collect_posture`.",
		Attributes:  postureInfoAttrs,
		Group:       groupDevices,
	}

	docAttribute = metricdoc.Metric{
		Name:        metricAttribute,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Numeric device posture attribute — boolean attributes as `0`/`1`, numeric attributes as their value (e.g. `intune:isEncrypted`, `custom:myScore`); one series per device per attribute, the namespaced posture key carried as the `attribute` label. **Gated** by `collect_posture` and the `attribute_namespaces` allow-list.",
		Attributes:  []string{semconv.HostName, semconv.HostID, attrAttribute},
		Group:       groupDevices,
	}
	docAttributeInfo = metricdoc.Metric{
		Name:        metricAttributeInfo,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "String/enum device posture attribute info gauge (constant `1`); the namespaced posture key is the `attribute` label and its string value the `value` label (e.g. `intune:complianceState`=`compliant`, `ip:country`=`GB`). **Gated** by `collect_posture` and the `attribute_namespaces` allow-list.",
		Attributes:  []string{semconv.HostName, semconv.HostID, attrAttribute, attrAttributeValue},
		Group:       groupDevices,
	}
)

// Tailnet-lock + per-DERP-region rollup descriptors (devices extension). The
// rollup gauges are gated by cardinality.derp_region_rollup; the tailnet-lock
// error count is unconditional (cheap, derived from the devices fetch).
var derpRegionAttr = []string{attrDERPRegion}

var (
	docTailnetLockErrors = metricdoc.Metric{
		Name:        metricTailnetLockErrors,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of devices with a non-empty tailnet-lock error (a **count**, despite `_ratio`); the only actionable tailnet-lock signal the API exposes (every node carries a lock key regardless of whether tailnet lock is enabled).",
		Group:       groupDevices,
	}
	docDerpRegionLatencyMin = metricdoc.Metric{
		Name:        metricDerpRegionLatencyMin,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Best (minimum) device→DERP-region latency across the tailnet; one series per region. **Gated** by `cardinality.derp_region_rollup`.",
		Attributes:  derpRegionAttr,
		Group:       groupDevices,
	}
	docDerpRegionDevices = metricdoc.Metric{
		Name:        metricDerpRegionDevices,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of devices reporting latency to a DERP region (a **count**). **Gated** by `cardinality.derp_region_rollup`.",
		Attributes:  derpRegionAttr,
		Group:       groupDevices,
	}
	docDerpRegionPreferred = metricdoc.Metric{
		Name:        metricDerpRegionPreferred,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of devices that prefer a DERP region (a **count**). **Gated** by `cardinality.derp_region_rollup`.",
		Attributes:  derpRegionAttr,
		Group:       groupDevices,
	}

	docTailnetLockError = metricdoc.LogEvent{
		Name:        eventTailnetLockError,
		Severity:    "ERROR",
		Description: "Emitted per device when its tailnet-lock error is non-empty (e.g. an unsigned node); the error text is the log body.",
		Attributes:  []string{semconv.HostName, semconv.HostID},
		Group:       groupDevices,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docOnline, docLastSeen, docKeyExpiry, docUpdateAvailable, docDERPLatency,
		docRoutesAdvertised, docRoutesEnabled, docDevicesCount, docDeviceInvites, docPostureInfo,
		docDevicesUntagged, docDevicesEphemeral, docDevicesByVersion, docDevicesByTag, docDevicesKeyExpiry,
		docDeviceVersionSkew, docFleetLatestVersion, docDevicesOutdated,
		docAttribute, docAttributeInfo,
		docTailnetLockErrors, docDerpRegionLatencyMin, docDerpRegionDevices, docDerpRegionPreferred,
		docCacheAge, docCacheSize,
	}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docPosture, docTailnetLockError}
}
