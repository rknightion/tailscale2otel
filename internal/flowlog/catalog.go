package flowlog

import (
	"github.com/rknightion/tailscale2otel/internal/metricdoc"
	"github.com/rknightion/tailscale2otel/internal/semconv"
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
// cardinality.flow.source_port / cardinality.flow.destination_port, and tailscale.dst.service
// by cardinality.flow.destination_service; all are listed here as the full
// possible attribute set (gating is documented in prose). On flow LOGS the ports
// and tailscale.dst.service are always present (the latter when the destination
// port maps to a known service).
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

var (
	docIO = metricdoc.Metric{
		Name:        MetricIO,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Bytes transferred on the tailnet, by direction, transport, traffic type, and source/destination node.",
		Attributes: []string{
			semconv.NetworkIODirection, semconv.NetworkTransport, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.SourcePort, semconv.DestinationPort,
			semconv.AttrDstService,
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
			semconv.AttrDstService,
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
	docLogsDropped = metricdoc.Metric{
		Name:        MetricLogsDropped,
		Unit:        unitRecord,
		Instrument:  metricdoc.Counter,
		Description: "Flow LOG records suppressed by the per-window volume guard (collectors.flowlogs.max_log_records_per_window); 0 unless truncating. Metrics are never dropped, only logs.",
		Group:       groupNetwork,
	}

	docIORollup = metricdoc.Metric{
		Name:        MetricIORollup,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Bytes transferred on the tailnet, bounded top-N rollup: the busiest source/destination node pairs by total bytes are kept per flush and the remainder is folded into a tailscale.src.node/tailscale.dst.node=\"__other__\" series per transport, traffic type, and destination service, so totals are preserved. Carries no L4 ports. Emitted when cardinality.flow.metrics_mode is rollup or both (the default).",
		Attributes: []string{
			semconv.NetworkIODirection, semconv.NetworkTransport, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.AttrDstService,
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

	docFlowLog = metricdoc.LogEvent{
		Name:        eventNameFlow,
		Severity:    "INFO",
		Description: "Per-connection (per_connection) or per-record (per_record) network-flow detail: the 5-tuple, transport, traffic type, source/destination node, and tx/rx bytes & packets.",
		Attributes: []string{
			semconv.SourceAddress, semconv.SourcePort, semconv.DestinationAddress, semconv.DestinationPort,
			semconv.NetworkTransport, semconv.NetworkType, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.AttrDstService, semconv.AttrNodeID, attrNodeHostname,
			"tailscale.connections", // per_record summary only
			"tailscale.tx.bytes", "tailscale.rx.bytes", "tailscale.tx.packets", "tailscale.rx.packets",
		},
		Group: groupNetwork,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{
		docIO, docPackets, docFlows, docLogsDropped,
		docIORollup, docPacketsRollup, docUniqueDstPeers, docUniqueDstPorts,
	}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docFlowLog}
}
