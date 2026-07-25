package oauthapps

import (
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
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

	docAppRedirectURIs = metricdoc.Metric{
		Name:        MetricAppRedirectURIs,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Number of redirect URIs configured for an OAuth application (a **count** — the URI values are never emitted); one series per app with at least one configured. #419.",
		Attributes:  []string{attrID, attrName},
		Group:       groupOAuthApps,
	}

	docAppScopeClass = metricdoc.Metric{
		Name:       MetricAppScopeClass,
		Unit:       semconv.UnitDimensionless,
		Instrument: metricdoc.Gauge,
		Description: "OAuth application privilege class (info gauge, value 1 for the current class / 0 for the rest), the app-side analog of the keys collector's `tailscale.key.scope_class` (#415/#419): `none`|`read`|`all_read`|`write`|`all`, ranked by `internal/tsscope`. " +
			"Zero-seeded across every class for every app, including one with no scopes at all.",
		Attributes: []string{attrID, attrName, attrScopeClass},
		Group:      groupOAuthApps,
	}

	docAppsAge = metricdoc.Metric{
		Name:        MetricAppsAge,
		Unit:        semconv.UnitSeconds,
		Instrument:  metricdoc.Histogram,
		Description: "Fleet age distribution of OAuth applications, in seconds since `created` (#426). A single bounded histogram across every app with a known Created timestamp — not a per-entity series. Bucket bounds: `internal/entityage.BucketsSeconds()`.",
		Group:       groupOAuthApps,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docAppsCount, docAppScopes, docAppNodeAttributes,
		docAppRedirectURIs, docAppScopeClass, docAppsAge,
	}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docAppInfo}
}
