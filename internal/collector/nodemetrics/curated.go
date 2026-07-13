package nodemetrics

import (
	"strings"

	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Curation emits a small set of first-class, catalog-documented metrics DERIVED
// from specific tailscaled scrape families, IN ADDITION to (never instead of)
// the verbatim raw forward. Curation is:
//
//   - Additive. Every scraped sample is still forwarded byte-identically by
//     emitSample; curation only appends the extra curated series.
//   - Filter-bypassing. The metric_allow/metric_deny and drop_labels passthrough
//     filters apply ONLY to the raw forward — curated metrics are catalog metrics,
//     not passthrough, so they are emitted regardless of those filters.
//   - Delta-sharing for counters. A curated counter consumes the SAME per-series
//     delta the forwarder computes for its source family (see Collector.delta);
//     it never maintains a second baseline.
//
// The curated metric names/units are declared in catalog.go; the source-family ->
// curated-metric mapping is the two tables below.

// curatedCounter maps a cumulative tailscaled family to the curated counter it
// feeds. attrs derives the curated attribute set (excluding the tailscale.node
// identity, added at emit time) from the scraped sample's raw labels.
type curatedCounter struct {
	name  string
	unit  string
	desc  string
	attrs func(labels map[string]string) telemetry.Attrs
}

// curatedGauge maps a non-cumulative tailscaled family to the curated gauge it
// feeds. Curated gauges churn per node (and per health type), so they flow
// through a GaugeSnapshotBuilder (see Collector.curatedGauges) rather than a
// synchronous Gauge, matching tailscale.node.up: a node that leaves the fleet
// drops its curated series out instead of ghosting at its last value (#55).
type curatedGauge struct {
	name  string
	unit  string
	desc  string
	attrs func(labels map[string]string) telemetry.Attrs
}

// curatedCounters is the source-family -> curated-counter table. Inbound/outbound
// families map to one curated metric distinguished by network.io.direction.
var curatedCounters = map[string]curatedCounter{
	"tailscaled_inbound_bytes_total":    {metricNodeIO, semconv.UnitBytes, descNodeIO, ioAttrs(semconv.DirectionReceive)},
	"tailscaled_outbound_bytes_total":   {metricNodeIO, semconv.UnitBytes, descNodeIO, ioAttrs(semconv.DirectionTransmit)},
	"tailscaled_inbound_packets_total":  {metricNodePackets, semconv.UnitPackets, descNodePackets, ioAttrs(semconv.DirectionReceive)},
	"tailscaled_outbound_packets_total": {metricNodePackets, semconv.UnitPackets, descNodePackets, ioAttrs(semconv.DirectionTransmit)},

	"tailscaled_inbound_dropped_packets_total":  {metricNodePacketsDropped, semconv.UnitPackets, descNodePacketsDropped, dropAttrs(semconv.DirectionReceive)},
	"tailscaled_outbound_dropped_packets_total": {metricNodePacketsDropped, semconv.UnitPackets, descNodePacketsDropped, dropAttrs(semconv.DirectionTransmit)},

	"tailscaled_peer_relay_forwarded_bytes_total":   {metricNodePeerRelayIO, semconv.UnitBytes, descNodePeerRelayIO, noAttrs},
	"tailscaled_peer_relay_forwarded_packets_total": {metricNodePeerRelayPackets, semconv.UnitPackets, descNodePeerRelayPackets, noAttrs},
}

// curatedGauges is the source-family -> curated-gauge table.
var curatedGaugeSpecs = map[string]curatedGauge{
	"tailscaled_health_messages":      {metricNodeHealthMessages, semconv.UnitDimensionless, descNodeHealthMessages, healthAttrs},
	"tailscaled_home_derp_region_id":  {metricNodeDERPHomeRegion, semconv.UnitDimensionless, descNodeDERPHomeRegion, noAttrs},
	"tailscaled_peer_relay_endpoints": {metricNodePeerRelayEndpoints, semconv.UnitDimensionless, descNodePeerRelayEndpoints, noAttrs},
}

// noAttrs is the attribute deriver for curated series that carry only the
// tailscale.node identity (no source-derived dimensions).
func noAttrs(map[string]string) telemetry.Attrs { return telemetry.Attrs{} }

// ioAttrs builds the direction + folded-path attributes for the throughput/packet
// counters (tailscale.node.io / .packets).
func ioAttrs(direction string) func(map[string]string) telemetry.Attrs {
	return func(labels map[string]string) telemetry.Attrs {
		return telemetry.Attrs{
			semconv.NetworkIODirection: direction,
			semconv.AttrPath:           foldPath(labels["path"]),
		}
	}
}

// dropAttrs builds the direction + folded-reason attributes for the dropped-packet
// counter (tailscale.node.packets.dropped).
func dropAttrs(direction string) func(map[string]string) telemetry.Attrs {
	return func(labels map[string]string) telemetry.Attrs {
		return telemetry.Attrs{
			semconv.NetworkIODirection: direction,
			semconv.AttrDropReason:     foldDropReason(labels["reason"]),
		}
	}
}

// healthAttrs carries the tailscaled health-warning class through as-is (the type
// set is code-defined in tailscaled, not attacker-controlled, so it is not
// folded). A missing type label yields a single series carrying only node identity.
func healthAttrs(labels map[string]string) telemetry.Attrs {
	out := telemetry.Attrs{}
	if t := labels["type"]; t != "" {
		out[semconv.AttrHealthType] = t
	}
	return out
}

// foldPath collapses the raw tailscaled `path` label to the bounded curated set.
// tailscaled splits direct/peer_relay by IP version (direct_ipv4, direct_ipv6,
// peer_relay_ipv4, peer_relay_ipv6); the curated metric folds those to `direct`
// and `peer_relay` to halve per-node path cardinality. `derp` passes through; any
// unrecognized value folds to `other`.
func foldPath(raw string) string {
	switch {
	case raw == semconv.PathDERP:
		return semconv.PathDERP
	case strings.HasPrefix(raw, "direct"):
		return semconv.PathDirect
	case strings.HasPrefix(raw, "peer_relay"):
		return semconv.PathPeerRelay
	default:
		return semconv.PathOther
	}
}

// foldDropReason folds the raw tailscaled `reason` label to the bounded admit-set
// (acl, error), collapsing any other/future value to `other`. Scraped labels come
// from semi-trusted tailnet-member nodes, so bounding this keeps a misbehaving or
// newer node from minting unbounded reason cardinality.
func foldDropReason(raw string) string {
	switch raw {
	case semconv.DropReasonACL:
		return semconv.DropReasonACL
	case semconv.DropReasonError:
		return semconv.DropReasonError
	default:
		return semconv.DropReasonOther
	}
}
