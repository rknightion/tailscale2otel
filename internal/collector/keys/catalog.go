package keys

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// and log-event documentation: name, unit, instrument, description, and
// attribute keys. The emit sites (keys.go) reference these descriptors so a
// description/unit cannot drift from what is documented; the doc generator
// (tools/metricscatalog, via internal/catalog) renders them into
// docs/metrics.md, and catalog_test.go asserts what the collector emits matches
// these declarations. The key.expiring log is emitted only when a key expires
// within the configured expiry_warn window; gating is documented in prose.
const groupKeys = "Keys"

var (
	docKeyExpiry = metricdoc.Metric{
		Name:        MetricKeyExpiry,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Gauge,
		Description: "Unix timestamp an auth/API key expires; one series per key.",
		Attributes:  []string{attrID, attrType, attrDescription},
		Group:       groupKeys,
	}
	docKeysCount = metricdoc.Metric{
		Name:        MetricKeysCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Key count (a **count**), bucketed by type/revoked/invalid.",
		Attributes:  []string{attrType, attrRevoked, attrInvalid},
		Group:       groupKeys,
	}

	docKeyExpiring = metricdoc.LogEvent{
		Name:        EventExpiring,
		Severity:    "WARN",
		Description: "Emitted when a key expires within the configured `expiry_warn` window. Carries `tailscale.key.expires_in_seconds` (seconds *until* expiry, a remaining duration — not an absolute timestamp).",
		Attributes:  []string{attrID, attrType, attrDescription, attrExpiresIn},
		Group:       groupKeys,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docKeyExpiry, docKeysCount}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docKeyExpiring}
}
