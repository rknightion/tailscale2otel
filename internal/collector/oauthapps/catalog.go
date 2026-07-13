package oauthapps

import (
	"github.com/rknightion/tailscale2otel/v2/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// and log-event documentation: name, unit, instrument, description, and
// attribute keys. The emit sites (oauthapps.go) reference these descriptors so
// a description/unit cannot drift from what is documented; the doc generator
// (tools/metricscatalog, via internal/catalog) renders them into
// docs/metrics.md, and catalog_test.go asserts what the collector emits
// matches these declarations. Names and gating are frozen by the #167 seam
// (see the issue comment): default-on, a 403/404 from the alpha endpoint is
// feature-off idle (no error), covered by the isFeatureOff helper in
// oauthapps.go.
const groupOAuthApps = "OAuth Apps"

var (
	docAppsCount = metricdoc.Metric{
		Name:        MetricAppsCount,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of OAuth applications registered on the tailnet (a **count**).",
		Group:       groupOAuthApps,
	}

	docAppScopes = metricdoc.Metric{
		Name:        MetricAppScopes,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of OAuth scopes granted to an OAuth application (scope-sprawl signal); one series per app.",
		Attributes:  []string{attrID, attrName},
		Group:       groupOAuthApps,
	}

	docAppNodeAttributes = metricdoc.Metric{
		Name:        MetricAppNodeAttributes,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of custom node attributes an OAuth application is allowed to set; one series per app.",
		Attributes:  []string{attrID, attrName},
		Group:       groupOAuthApps,
	}

	docAppInfo = metricdoc.LogEvent{
		Name:        EventAppInfo,
		Severity:    "INFO",
		Description: "Emitted for each OAuth application on the tailnet. `tailscale.oauth_app.scope_values` is a comma-separated list of the granted scope strings; `tailscale.oauth_app.node_attribute_count` is the number of custom node attributes it may set.",
		Attributes:  []string{attrID, attrName, attrScopeValues, attrNodeAttrCount},
		Group:       groupOAuthApps,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docAppsCount, docAppScopes, docAppNodeAttributes}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docAppInfo}
}
