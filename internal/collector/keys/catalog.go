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
		Description: "Unix timestamp a Tailscale key expires; one series per key.",
		Attributes:  []string{attrID, attrType, attrAuthKind, attrDescription},
		Group:       groupKeys,
	}
	docKeysCount = metricdoc.Metric{
		Name:        MetricKeysCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Key count (a **count**), bucketed by type/auth_kind/revoked/invalid.",
		Attributes:  []string{attrType, attrAuthKind, attrRevoked, attrInvalid},
		Group:       groupKeys,
	}

	docKeyExpiring = metricdoc.LogEvent{
		Name:        EventExpiring,
		Severity:    "WARN",
		Description: "Emitted when a key expires within the configured `expiry_warn` window. Carries `tailscale.key.expires_in_seconds` (seconds *until* expiry, a remaining duration — not an absolute timestamp).",
		Attributes:  []string{attrID, attrType, attrAuthKind, attrDescription, attrExpiresIn},
		Group:       groupKeys,
	}

	docKeyScopes = metricdoc.Metric{
		Name:        MetricKeyScopes,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of OAuth scopes granted to a credential (scope-sprawl signal); one series per OAuth-client/API credential. Gated by `cardinality.per_entity.key`.",
		Attributes:  []string{attrID, attrType, attrDescription},
		Group:       groupKeys,
	}

	docKeyPreauthorized = metricdoc.Metric{
		Name:        MetricKeyPreauthorized,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Whether an auth key is preauthorized (1) or not (0); one series per auth key. Gated by `cardinality.per_entity.key`.",
		Attributes:  []string{attrID, attrType, attrDescription},
		Group:       groupKeys,
	}

	docKeyScopesLog = metricdoc.LogEvent{
		Name:        MetricKeyScopes,
		Severity:    "INFO",
		Description: "Emitted for each OAuth-client/API credential that carries scopes (scope-sprawl audit log). `tailscale.key.scope_values` is a comma-separated list of the granted capability strings. Gated by `cardinality.per_entity.key`.",
		Attributes:  []string{attrID, attrScopeValues},
		Group:       groupKeys,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docKeyExpiry, docKeysCount, docKeyScopes, docKeyPreauthorized}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docKeyExpiring, docKeyScopesLog}
}
