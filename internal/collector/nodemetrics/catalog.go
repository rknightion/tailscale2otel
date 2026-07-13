package nodemetrics

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
)

// Catalog declarations are the SINGLE SOURCE OF TRUTH for this collector's own,
// statically-enumerable metric documentation. The scraper also FORWARDS every
// scraped tailscaled_* series VERBATIM (their names and units come from the
// scraped node at runtime), which is not statically enumerable and is documented
// as prose in docs/metrics.md, not in this catalog. The static metrics are the
// per-target tailscale.node.up gauge, the discovery-health gauges, and the
// CURATED tailscale.node.* families derived from specific tailscaled scrape
// families (see curated.go). The emit sites (nodemetrics.go, curated.go)
// reference these descriptors; catalog_test.go asserts them.
const groupNodeMetrics = "Node metrics"

// Curated metric names — first-class, catalog-documented series derived from
// specific tailscaled scrape families, emitted IN ADDITION to the verbatim raw
// forward (see curated.go for the source-family -> curated-metric mapping).
const (
	metricNodeIO             = "tailscale.node.io"
	metricNodePackets        = "tailscale.node.packets"
	metricNodePacketsDropped = "tailscale.node.packets.dropped"
	metricNodeHealthMessages = "tailscale.node.health_messages"
	metricNodeDERPHomeRegion = "tailscale.node.derp.home_region"

	metricNodePeerRelayIO        = "tailscale.node.peer_relay.io"
	metricNodePeerRelayPackets   = "tailscale.node.peer_relay.packets"
	metricNodePeerRelayEndpoints = "tailscale.node.peer_relay.endpoints"
)

// Curated metric descriptions (also passed to the emitter so the emitted
// signal can't drift from the declared one).
const (
	descNodeIO                 = "Bytes carried over the tailnet data plane, by direction and folded path. Curated from tailscaled_{inbound,outbound}_bytes_total (raw series still forwarded verbatim)."
	descNodePackets            = "Packets carried over the tailnet data plane, by direction and folded path. Curated from tailscaled_{inbound,outbound}_packets_total (raw series still forwarded verbatim)."
	descNodePacketsDropped     = "Packets dropped on the tailnet data plane, by direction and bounded reason. Curated from tailscaled_{inbound,outbound}_dropped_packets_total (raw series still forwarded verbatim)."
	descNodeHealthMessages     = "Active tailscaled client health-warning messages, by health type. Curated from tailscaled_health_messages (raw series still forwarded verbatim)."
	descNodeDERPHomeRegion     = "The node's current home DERP region ID (as the gauge value). Curated from tailscaled_home_derp_region_id (raw series still forwarded verbatim)."
	descNodePeerRelayIO        = "Bytes this node forwarded while acting as a peer relay. Curated from tailscaled_peer_relay_forwarded_bytes_total (raw series still forwarded verbatim)."
	descNodePeerRelayPackets   = "Packets this node forwarded while acting as a peer relay. Curated from tailscaled_peer_relay_forwarded_packets_total (raw series still forwarded verbatim)."
	descNodePeerRelayEndpoints = "Peer-relay endpoints currently configured on this node. Curated from tailscaled_peer_relay_endpoints (raw series still forwarded verbatim)."
)

var docNodeUp = metricdoc.Metric{
	Name:        metricUp,
	Unit:        "1",
	Instrument:  metricdoc.Gauge,
	Description: "Per-target scrape health: `1` if the last scrape of that node succeeded, else `0`.",
	Attributes:  []string{attrInstance},
	Group:       groupNodeMetrics,
}

// docDiscoverySuccess and docDiscoveredTargets are the discovery-health gauges,
// emitted every Collect only when dynamic discovery is enabled.
var docDiscoverySuccess = metricdoc.Metric{
	Name:        metricDiscoverySuccess,
	Unit:        semconv.UnitDimensionless,
	Instrument:  metricdoc.Gauge,
	Description: "1 if the last dynamic target-discovery refresh succeeded, else 0. Emitted only when discovery is enabled.",
	Group:       groupNodeMetrics,
}

var docDiscoveredTargets = metricdoc.Metric{
	Name:        metricDiscoveredTargets,
	Unit:        semconv.UnitTargets,
	Instrument:  metricdoc.Gauge,
	Description: "Active node-metrics scrape targets after the last refresh (static plus discovered). Emitted only when discovery is enabled.",
	Group:       groupNodeMetrics,
}

// Curated metric descriptors. Counters ride the shared per-series delta pipeline;
// gauges flow through the churn-safe GaugeSnapshot path. Each is emitted IN
// ADDITION to the verbatim raw forward of its source family.
var (
	docNodeIO = metricdoc.Metric{
		Name:        metricNodeIO,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: descNodeIO,
		Attributes:  []string{attrInstance, semconv.NetworkIODirection, semconv.AttrPath},
		Group:       groupNodeMetrics,
	}
	docNodePackets = metricdoc.Metric{
		Name:        metricNodePackets,
		Unit:        semconv.UnitPackets,
		Instrument:  metricdoc.Counter,
		Description: descNodePackets,
		Attributes:  []string{attrInstance, semconv.NetworkIODirection, semconv.AttrPath},
		Group:       groupNodeMetrics,
	}
	docNodePacketsDropped = metricdoc.Metric{
		Name:        metricNodePacketsDropped,
		Unit:        semconv.UnitPackets,
		Instrument:  metricdoc.Counter,
		Description: descNodePacketsDropped,
		Attributes:  []string{attrInstance, semconv.NetworkIODirection, semconv.AttrDropReason},
		Group:       groupNodeMetrics,
	}
	docNodeHealthMessages = metricdoc.Metric{
		Name:        metricNodeHealthMessages,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: descNodeHealthMessages,
		Attributes:  []string{attrInstance, semconv.AttrHealthType},
		Group:       groupNodeMetrics,
	}
	docNodeDERPHomeRegion = metricdoc.Metric{
		Name:        metricNodeDERPHomeRegion,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: descNodeDERPHomeRegion,
		Attributes:  []string{attrInstance},
		Group:       groupNodeMetrics,
	}
	docNodePeerRelayIO = metricdoc.Metric{
		Name:        metricNodePeerRelayIO,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: descNodePeerRelayIO,
		Attributes:  []string{attrInstance},
		Group:       groupNodeMetrics,
	}
	docNodePeerRelayPackets = metricdoc.Metric{
		Name:        metricNodePeerRelayPackets,
		Unit:        semconv.UnitPackets,
		Instrument:  metricdoc.Counter,
		Description: descNodePeerRelayPackets,
		Attributes:  []string{attrInstance},
		Group:       groupNodeMetrics,
	}
	docNodePeerRelayEndpoints = metricdoc.Metric{
		Name:        metricNodePeerRelayEndpoints,
		Unit:        semconv.UnitDimensionless,
		Instrument:  metricdoc.Gauge,
		Description: descNodePeerRelayEndpoints,
		Attributes:  []string{attrInstance},
		Group:       groupNodeMetrics,
	}
)

// Catalog returns the statically-enumerable metrics this package emits, for the
// doc generator. The forwarded tailscaled_* series are runtime-named and not
// included; the curated tailscale.node.* families derived from them are.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docNodeUp, docDiscoverySuccess, docDiscoveredTargets,
		docNodeIO, docNodePackets, docNodePacketsDropped,
		docNodeHealthMessages, docNodeDERPHomeRegion,
		docNodePeerRelayIO, docNodePeerRelayPackets, docNodePeerRelayEndpoints,
	}
}

// LogCatalog returns the log events this package emits (none).
func LogCatalog() []metricdoc.LogEvent {
	return nil
}
