package postureintegrations

import (
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation; the emit site references these descriptors so a description/
// unit cannot drift, and catalog_test.go asserts the emission matches.
const groupPosture = "Posture"

var commonAttrs = []string{attrProvider, attrIntegration}

var (
	docCount = metricdoc.Metric{
		Name:        metricCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of configured device-posture integrations (a **count**, despite `_ratio`).",
		Group:       groupPosture,
	}
	docMatched = metricdoc.Metric{
		Name:        metricMatched,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Devices matched to a provider host by the posture integration (a **count**); one series per provider/integration.",
		Attributes:  commonAttrs,
		Group:       groupPosture,
	}
	docPossible = metricdoc.Metric{
		Name:        metricPossible,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Devices that could potentially be matched by the posture integration (a **count**).",
		Attributes:  commonAttrs,
		Group:       groupPosture,
	}
	docProviderHosts = metricdoc.Metric{
		Name:        metricProviderHosts,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Hosts known to the posture provider (a **count**).",
		Attributes:  commonAttrs,
		Group:       groupPosture,
	}
	docLastSync = metricdoc.Metric{
		Name:        metricLastSync,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp of the integration's last synchronization ATTEMPT (not necessarily successful — the API's `lastSync` advances on every attempt, so pair staleness with `tailscale.posture_integration.error` to detect a failing sync). Emitted only once a sync has occurred.",
		Attributes:  commonAttrs,
		Group:       groupPosture,
		TimeSource:  metricdoc.TimestampSource,
	}
	docError = metricdoc.Metric{
		Name:        metricError,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "`1` if the integration's last sync reported an error, else `0`; one series per provider/integration. The raw error text is deliberately not emitted as a label (unbounded/possibly sensitive). Pair with `last_sync` — `lastSync` advances even on a failed attempt, so this is the only failure signal.",
		Attributes:  commonAttrs,
		Group:       groupPosture,
	}
	docPostureIntegrationsSnapshot = metricdoc.LogEvent{
		Name:     EventPostureIntegrationsSnapshot,
		Severity: "INFO",
		Description: "The complete safe device-posture integration inventory as JSON, emitted only " +
			"when collectors.posture_integrations.snapshot_enabled is set and the configuration " +
			"changes or its daily heartbeat is due. Provider credentials and raw sync-error text " +
			"are never included; large bodies are UTF-8-safe chunks that MUST be grouped by " +
			"tailscale.snapshot.emission_id and sorted by tailscale.snapshot.seq before reassembly.",
		Attributes: []string{
			"tailscale.snapshot.kind", "tailscale.snapshot.reason",
			"tailscale.snapshot.revision", "tailscale.snapshot.emission_id",
			"tailscale.snapshot.bytes", "tailscale.snapshot.seq",
			"tailscale.snapshot.total",
		},
		Group: groupPosture,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docCount, docMatched, docPossible, docProviderHosts, docLastSync, docError}
}

// LogCatalog returns the log events this package emits.
func LogCatalog() []metricdoc.LogEvent { return []metricdoc.LogEvent{docPostureIntegrationsSnapshot} }
