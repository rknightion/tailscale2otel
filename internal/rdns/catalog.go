package rdns

import (
	"github.com/rknightion/tailscale2otel/v3/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// documentation: name, unit, instrument, description, and the attribute keys
// carried. report() references these fields (so a description/unit cannot drift
// from what is documented) and tools/metricscatalog renders them into
// docs/metrics.md. TestReport_EmitsMetrics asserts what report() actually emits
// matches these declarations.
//
// All series are gated on self_observability.enabled (the cache is handed an
// Emitter only then); the admin status page reads Stats() directly and therefore
// shows the same numbers even when self-obs is off.
const groupRDNS = "Reverse DNS"

// Metric names emitted by the reverse-DNS cache.
const (
	MetricLookups   = "tailscale.rdns.cache.lookups"
	MetricQueries   = "tailscale.rdns.queries"
	MetricRefreshes = "tailscale.rdns.refreshes"
	MetricEvictions = "tailscale.rdns.cache.evictions"
	MetricOverflows = "tailscale.rdns.cache.overflows"
	MetricEntries   = "tailscale.rdns.cache.entries"
	MetricCapacity  = "tailscale.rdns.cache.capacity"
)

// Attribute keys (closed sets keep cardinality bounded).
const (
	attrResult = "result"
	attrReason = "reason"

	resultHit      = "hit"
	resultMiss     = "miss"
	resultNegative = "negative"
	resultStale    = "stale"
	resultSuccess  = "success"
	resultFailure  = "failure"

	reasonExpired      = "expired"
	reasonPurge        = "purge"
	reasonStaleExpired = "stale_expired"
)

var (
	docLookups = metricdoc.Metric{
		Name:        MetricLookups,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Reverse-DNS cache hot-path lookups by result: hit (cached PTR name), stale (a positive entry past its TTL but still within enrichment.reverse_dns.stale_ttl, served while one background refresh runs, #297), negative (cached failed lookup), or miss (no cached entry; a background resolution is scheduled).",
		Attributes:  []string{attrResult},
		Group:       groupRDNS,
	}
	docQueries = metricdoc.Metric{
		Name:        MetricQueries,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Background PTR resolutions sent to the upstream resolver, by result (success or failure). This is the load the cache places on the resolver — it should stay low relative to lookups.",
		Attributes:  []string{attrResult},
		Group:       groupRDNS,
	}
	docRefreshes = metricdoc.Metric{
		Name:        MetricRefreshes,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Background PTR resolutions triggered by serving a stale name (#297), by result (success or failure). These are a subset of the resolver load already counted in tailscale.rdns.queries, broken out separately from first-sighting queries so a stale-refresh storm is visible on its own.",
		Attributes:  []string{attrResult},
		Group:       groupRDNS,
	}
	docEvictions = metricdoc.Metric{
		Name:        MetricEvictions,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Cache entries removed, by reason: expired (swept after their TTL), stale_expired (a positive entry swept after outliving its TTL plus enrichment.reverse_dns.stale_ttl, #297), or purge (manual purge via the admin endpoint).",
		Attributes:  []string{attrReason},
		Group:       groupRDNS,
	}
	docOverflows = metricdoc.Metric{
		Name:        MetricOverflows,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Counter,
		Description: "Hot-path misses for new addresses that could not be scheduled because the cache was at enrichment.reverse_dns.max_entries. A non-zero rate means the cache is too small.",
		Group:       groupRDNS,
	}
	docEntries = metricdoc.Metric{
		Name:        MetricEntries,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Current number of entries in the reverse-DNS cache (positive and negative).",
		Group:       groupRDNS,
	}
	docCapacity = metricdoc.Metric{
		Name:        MetricCapacity,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: "Configured maximum number of entries (enrichment.reverse_dns.max_entries).",
		Group:       groupRDNS,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docLookups, docQueries, docRefreshes, docEvictions, docOverflows, docEntries, docCapacity,
	}
}
