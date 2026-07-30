package flowlog

import (
	"github.com/rknightion/tailscale2otel/v4/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this package's metric
// and log-event documentation: name, unit, instrument, description, and the
// attribute keys carried. The emit sites below reference these fields (so a
// description/unit cannot drift from what is documented), and tools/metricscatalog
// renders them into docs/metrics.md. A consistency test (processor_catalog_test.go)
// asserts what the processor actually emits matches these declarations.
//
// The flow node-dimension attributes (src/dst node) are gated by
// cardinality.flow.node_dims, the source/destination port attributes by
// cardinality.flow.source_port / cardinality.flow.destination_port; all are
// listed here as the full possible attribute set (gating is documented in
// prose). tailscale.dst.service is NOT gated — cardinality.flow.destination_service
// was removed in 0.13.0 and the attribute is now emitted unconditionally on both
// flow metric families. On flow LOGS the ports and tailscale.dst.service are
// always present (the latter when the destination port maps to a known service).
const groupNetwork = "Network / flow"

// Rollup + unique metric names, emitted only when cardinality.flow.metrics_mode
// is "rollup" or "both" (the bounded *.rollup families are the default metric
// path). The accumulator in rollup.go emits these; FlushRollup drives it.
const (
	MetricIORollup       = "tailscale.network.io.rollup"
	MetricPacketsRollup  = "tailscale.network.packets.rollup"
	MetricUniqueDstPeers = "tailscale.network.unique.dst_peers"
	MetricUniqueDstPorts = "tailscale.network.unique.dst_ports"
)

// Exit-node IO attribution metric names, emitted per exit-traffic connection
// when Options.ExitNodeAttribution is enabled. Cardinality is bounded by the
// number of exit nodes in the tailnet.
const (
	MetricExitNodeIO      = "tailscale.exit_node.io"
	MetricExitNodePackets = "tailscale.exit_node.packets"
	// MetricReporterObservations classifies reporter trust and consistency once
	// per processed flow record. It intentionally carries no reporter identity.
	MetricReporterObservations = "tailscale.network.reporter.observations"
	// MetricFieldObservations reports observed connection field completeness; it
	// does not infer control-plane logging configuration from missing fields.
	MetricFieldObservations = "tailscale.network.field.observations"
)

var (
	docIO = metricdoc.Metric{
		Name:        MetricIO,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Bytes transferred on the tailnet, by direction, transport, traffic type, and source/destination node. Emitted when `cardinality.flow.metrics_mode` is `all` or `both` — under the default `rollup` the bounded network.io.rollup family is emitted instead, and the `cardinality.flow.source_port`/`destination_port`/`identity_dims` toggles have no effect at all. `tailscale.path` (`direct`/`derp`) and, on a relayed connection, the numeric `tailscale.derp.region_id` are carried on **physical** traffic only — the overlay traffic types describe what the tailnet carried, not how, so they carry no path at all rather than one that reads as `direct`. `tailscale.derp.region_id` is NOT joinable with `tailscale.derp.region` on the device latency metrics: that one is a region NAME, this is the numeric ID the flow record supplies, and the API exposes no DERP map to translate between them. The endpoint identity attributes (`tailscale.{src,dst}.{user,tags,os}`) are **gated** by `cardinality.flow.identity_dims` (default off) and additionally require `cardinality.flow.node_dims`, since identity is node-derived.",
		Attributes: []string{
			semconv.NetworkIODirection, semconv.NetworkTransport, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.SourcePort, semconv.DestinationPort,
			semconv.AttrDstService, semconv.AttrPath, semconv.AttrDERPRegionID,
			semconv.AttrSrcUser, semconv.AttrSrcTags, semconv.AttrSrcOS,
			semconv.AttrDstUser, semconv.AttrDstTags, semconv.AttrDstOS,
			// Only when cardinality.flow.geo_dims is on (and enrichment.geoip is
			// configured). These two are the ONLY geo fields bounded enough for a
			// metric: ~250 countries and 7 continents. The AS number/organization
			// and any city-level detail stay on the flow logs.
			semconv.SourceGeoCountryISO, semconv.SourceGeoContinentCode,
			semconv.DestinationGeoCountryISO, semconv.DestGeoContinentCode,
		},
		Group: groupNetwork,
	}
	docPackets = metricdoc.Metric{
		Name:        MetricPackets,
		Unit:        semconv.UnitPackets,
		Instrument:  metricdoc.Counter,
		Description: "Packets transferred on the tailnet, with the same dimensions as network.io.",
		Attributes: []string{
			semconv.NetworkIODirection, semconv.NetworkTransport, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.SourcePort, semconv.DestinationPort,
			semconv.AttrDstService, semconv.AttrPath, semconv.AttrDERPRegionID,
			semconv.AttrSrcUser, semconv.AttrSrcTags, semconv.AttrSrcOS,
			semconv.AttrDstUser, semconv.AttrDstTags, semconv.AttrDstOS,
			// Only when cardinality.flow.geo_dims is on (and enrichment.geoip is
			// configured). These two are the ONLY geo fields bounded enough for a
			// metric: ~250 countries and 7 continents. The AS number/organization
			// and any city-level detail stay on the flow logs.
			semconv.SourceGeoCountryISO, semconv.SourceGeoContinentCode,
			semconv.DestinationGeoCountryISO, semconv.DestGeoContinentCode,
		},
		Group: groupNetwork,
	}
	docFlows = metricdoc.Metric{
		Name:        MetricFlows,
		Unit:        semconv.UnitFlows,
		Instrument:  metricdoc.Counter,
		Description: "Count of distinct flows observed (lower cardinality than network.io/packets).",
		Attributes:  []string{semconv.NetworkTransport, semconv.AttrTrafficType},
		Group:       groupNetwork,
	}
	docDedupConflicts = metricdoc.Metric{
		Name:        MetricDedupConflicts,
		Unit:        "{conflict}",
		Instrument:  metricdoc.Counter,
		Description: "Duplicate flow connections whose counters conflict with the first observation; first observed counters remain authoritative.",
		Attributes:  []string{"scope", semconv.AttrTrafficType},
		Group:       groupNetwork,
	}
	docStoreDropped = metricdoc.Metric{
		Name:        MetricStoreDropped,
		Unit:        unitRecord,
		Instrument:  metricdoc.Counter,
		Description: "Flow observations rejected from the local flow view (the in-memory ring, or the persistent store when flows.store.directory is set) because their timestamps are outside its retention or future-skew bounds. OTLP emission is unaffected.",
		Attributes:  []string{"reason"},
		Group:       groupNetwork,
	}
	docDataQuality = metricdoc.Metric{
		Name:        MetricDataQuality,
		Unit:        "{violation}",
		Instrument:  metricdoc.Counter,
		Description: "Semantically invalid flow records rejected before processor side effects, classified by a closed ingestion source and validation reason.",
		Attributes:  []string{"source", "reason"},
		Group:       groupNetwork,
	}
	docLogsDropped = metricdoc.Metric{
		Name:        MetricLogsDropped,
		Unit:        unitRecord,
		Instrument:  metricdoc.Counter,
		Description: "Flow LOG records suppressed by the per-window volume guard (collectors.flowlogs.max_log_records_per_window); 0 unless truncating. Metrics are never dropped, only logs.",
		Group:       groupNetwork,
	}
	docReporterObservations = metricdoc.Metric{
		Name:        MetricReporterObservations,
		Unit:        "{observation}",
		Instrument:  metricdoc.Counter,
		Description: "Flow-record reporter observations, classified by the configured trust policy and whether the verified reporter node ID agrees with the unverified embedded source reference. Carries no node ID.",
		Attributes:  []string{"trust", "consistency"},
		Group:       groupNetwork,
	}
	docFieldObservations = metricdoc.Metric{
		Name:        MetricFieldObservations,
		Unit:        "{observation}",
		Instrument:  metricdoc.Counter,
		Description: "Observed flow connection field completeness by traffic type, field class, and present or missing state. Missing fields are source evidence, not an inferred Destination Logging configuration setting.",
		Attributes:  []string{semconv.AttrTrafficType, "field_class", "state"},
		Group:       groupNetwork,
	}

	docIORollup = metricdoc.Metric{
		Name:        MetricIORollup,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Bytes transferred on the tailnet, bounded top-N rollup: the busiest source/destination node pairs by total bytes are kept per flush and the remainder is folded into a tailscale.src.node/tailscale.dst.node=\"__other__\" series per transport, traffic type, and destination service, so totals are preserved. Carries no L4 ports. Emitted when cardinality.flow.metrics_mode is rollup or both (the default). The `__other__` fold drops the endpoint identity with the node dimensions it derives from: the remainder is many nodes and so has no single user to report. `tailscale.path` (`direct`/`derp`) and, on a relayed connection, the numeric `tailscale.derp.region_id` are carried on **physical** traffic only — the overlay traffic types describe what the tailnet carried, not how, so they carry no path at all rather than one that reads as `direct`. `tailscale.derp.region_id` is NOT joinable with `tailscale.derp.region` on the device latency metrics: that one is a region NAME, this is the numeric ID the flow record supplies, and the API exposes no DERP map to translate between them. The endpoint identity attributes (`tailscale.{src,dst}.{user,tags,os}`) are **gated** by `cardinality.flow.identity_dims` (default off) and additionally require `cardinality.flow.node_dims`, since identity is node-derived.",
		Attributes: []string{
			semconv.NetworkIODirection, semconv.NetworkTransport, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.AttrDstService,
			semconv.AttrPath, semconv.AttrDERPRegionID,
			semconv.AttrSrcUser, semconv.AttrSrcTags, semconv.AttrSrcOS,
			semconv.AttrDstUser, semconv.AttrDstTags, semconv.AttrDstOS,
			// Only when cardinality.flow.geo_dims is on (and enrichment.geoip is
			// configured). These two are the ONLY geo fields bounded enough for a
			// metric: ~250 countries and 7 continents. The AS number/organization
			// and any city-level detail stay on the flow logs.
			semconv.SourceGeoCountryISO, semconv.SourceGeoContinentCode,
			semconv.DestinationGeoCountryISO, semconv.DestGeoContinentCode,
		},
		Group: groupNetwork,
	}
	docPacketsRollup = metricdoc.Metric{
		Name:        MetricPacketsRollup,
		Unit:        semconv.UnitPackets,
		Instrument:  metricdoc.Counter,
		Description: "Packets transferred on the tailnet, with the same bounded top-N rollup dimensions as network.io.rollup.",
		Attributes: []string{
			semconv.NetworkIODirection, semconv.NetworkTransport, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.AttrDstService,
			semconv.AttrPath, semconv.AttrDERPRegionID,
			semconv.AttrSrcUser, semconv.AttrSrcTags, semconv.AttrSrcOS,
			semconv.AttrDstUser, semconv.AttrDstTags, semconv.AttrDstOS,
			// Only when cardinality.flow.geo_dims is on (and enrichment.geoip is
			// configured). These two are the ONLY geo fields bounded enough for a
			// metric: ~250 countries and 7 continents. The AS number/organization
			// and any city-level detail stay on the flow logs.
			semconv.SourceGeoCountryISO, semconv.SourceGeoContinentCode,
			semconv.DestinationGeoCountryISO, semconv.DestGeoContinentCode,
		},
		Group: groupNetwork,
	}
	docUniqueDstPeers = metricdoc.Metric{
		Name:        MetricUniqueDstPeers,
		Unit:        semconv.UnitPeers,
		Instrument:  metricdoc.Gauge,
		Description: "Distinct destination nodes (peers) observed per source node in the last rollup flush interval (exact count, reset each flush). Emitted when cardinality.flow.metrics_mode is rollup or both and cardinality.flow.node_dims are on.",
		Attributes:  []string{semconv.AttrSrcNode},
		Group:       groupNetwork,
	}
	docUniqueDstPorts = metricdoc.Metric{
		Name:        MetricUniqueDstPorts,
		Unit:        semconv.UnitPorts,
		Instrument:  metricdoc.Gauge,
		Description: "Distinct destination ports observed per source node in the last rollup flush interval (exact count, reset each flush) — port-level visibility without per-port series.",
		Attributes:  []string{semconv.AttrSrcNode},
		Group:       groupNetwork,
	}

	docExitNodeIO = metricdoc.Metric{
		Name:        MetricExitNodeIO,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Bytes relayed through each exit node, by direction. Attributed to the reporting node of `traffic_type=exit` flow records (`tailscale.exit_node` = its hostname, or nodeId on a cache miss). Bounded by exit-node count. **Gated** by `cardinality.flow.exit_node_attribution` (default on); independent of the rollup/raw metric mode.",
		Attributes:  []string{semconv.AttrExitNode, semconv.NetworkIODirection},
		Group:       groupNetwork,
	}
	docExitNodePackets = metricdoc.Metric{
		Name:        MetricExitNodePackets,
		Unit:        semconv.UnitPackets,
		Instrument:  metricdoc.Counter,
		Description: "Packets relayed through each exit node, with the same dimensions as tailscale.exit_node.io.",
		Attributes:  []string{semconv.AttrExitNode, semconv.NetworkIODirection},
		Group:       groupNetwork,
	}

	docFlowLog = metricdoc.LogEvent{
		Name:        eventNameFlow,
		Severity:    "INFO",
		Description: "Per-connection (per_connection) or per-record (per_record) network-flow detail: the 5-tuple, transport, traffic type, source/destination node, and tx/rx bytes & packets. With `enrichment.geoip` on, external (non-Tailscale) endpoints also carry geolocation and autonomous-system attributes — the full set, including the ones that are deliberately never allowed onto a metric.",
		Attributes: []string{
			semconv.SourceAddress, semconv.SourcePort, semconv.DestinationAddress, semconv.DestinationPort,
			semconv.NetworkTransport, semconv.NetworkType, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.AttrDstService, semconv.AttrNodeID, attrNodeHostname,
			semconv.AttrFlowWindowStart, semconv.AttrFlowWindowEnd,
			semconv.AttrSrcUser, semconv.AttrDstUser, semconv.AttrSrcTags, semconv.AttrDstTags,
			semconv.AttrSrcOS, semconv.AttrDstOS,
			"tailscale.connections", // per_record summary only
			"tailscale.reporter.trust", "tailscale.reporter.consistency",
			"tailscale.tx.bytes", "tailscale.rx.bytes", "tailscale.tx.packets", "tailscale.rx.packets",
			// GeoIP enrichment (enrichment.geoip). Country and continent may also
			// reach flow METRICS via cardinality.flow.geo_dims; the autonomous
			// system and the city-level fields are LOGS ONLY, because neither is
			// bounded by anything useful and a log record is not a series. The
			// city/region/coordinate fields need a City database — a Country one
			// leaves them absent. Tailnet addresses are never geolocated.
			semconv.SourceGeoCountryISO, semconv.SourceGeoContinentCode,
			semconv.DestinationGeoCountryISO, semconv.DestGeoContinentCode,
			semconv.SourceGeoCity, semconv.SourceGeoRegionISO, semconv.SourceGeoLat, semconv.SourceGeoLon,
			semconv.DestinationGeoCity, semconv.DestinationGeoRegionISO, semconv.DestinationGeoLat, semconv.DestinationGeoLon,
			semconv.SourceASNumber, semconv.SourceASOrg,
			semconv.DestinationASNumber, semconv.DestinationASOrg,
		},
		Group: groupNetwork,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docIO, docPackets, docFlows, docDedupConflicts, docStoreDropped, docDataQuality, docLogsDropped,
		docReporterObservations, docFieldObservations,
		docIORollup, docPacketsRollup, docUniqueDstPeers, docUniqueDstPorts,
		docExitNodeIO, docExitNodePackets,
	}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docFlowLog}
}
