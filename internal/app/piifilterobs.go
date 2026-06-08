package app

import (
	"context"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/config"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
	"github.com/rknightion/tailscale2otel/internal/telemetry/pii"
)

// piiCategoryEnabled reports whether cat is enabled (emitted) in f.
// A true return means the category is NOT redacted; false means it is redacted.
func piiCategoryEnabled(f config.PIIFilterConfig, c pii.Category) bool {
	switch c {
	case pii.CatEmails:
		return f.Emails
	case pii.CatUserDisplayNames:
		return f.UserDisplayNames
	case pii.CatUserIDs:
		return f.UserIDs
	case pii.CatHostnames:
		return f.Hostnames
	case pii.CatNodeIDs:
		return f.NodeIDs
	case pii.CatTailscaleIPs:
		return f.TailscaleIPs
	case pii.CatInternalIPs:
		return f.InternalIPs
	case pii.CatExternalIPs:
		return f.ExternalIPs
	case pii.CatServiceAddrs:
		return f.ServiceAddrs
	case pii.CatEndpointPaths:
		return f.EndpointPaths
	case pii.CatNetworkTopology:
		return f.NetworkTopology
	case pii.CatTailnetName:
		return f.TailnetName
	case pii.CatFreeTextDetails:
		return f.FreeTextDetails
	default:
		// Unknown future category: default to emitted (safe for self-obs).
		return true
	}
}

// emitPIIFilterCategory records one tailscale2otel.pii_filter.category gauge
// datapoint per PII category. Value 1 means the category is emitted; 0 means
// it is redacted. The "category" attribute carries the canonical category name
// (e.g. "emails", "hostnames"). It is a pure function of f and e — safe to
// call from tests directly.
func emitPIIFilterCategory(e telemetry.Emitter, f config.PIIFilterConfig) {
	for _, cat := range pii.AllCategories {
		var val float64
		if piiCategoryEnabled(f, cat) {
			val = 1
		}
		e.Gauge(
			appcatalog.DocPIIFilterCategory.Name,
			appcatalog.DocPIIFilterCategory.Unit,
			appcatalog.DocPIIFilterCategory.Description,
			val,
			telemetry.Attrs{"category": string(cat)},
		)
	}
}

// runPIIFilterReporter emits pii_filter.category self-metrics immediately and then
// on each interval until ctx is canceled. If interval <= 0 it defaults to 60s.
func runPIIFilterReporter(ctx context.Context, f config.PIIFilterConfig, e telemetry.Emitter, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	emit := func() {
		emitPIIFilterCategory(e, f)
	}
	emit()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			emit()
		}
	}
}
