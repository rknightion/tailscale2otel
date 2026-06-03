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
// cardinality.flow_node_dims and the port attributes by cardinality.flow_include_ports;
// both are listed here as the full possible attribute set (gating is documented in prose).
const groupNetwork = "Network / flow"

var (
	docIO = metricdoc.Metric{
		Name:        MetricIO,
		Unit:        semconv.UnitBytes,
		Instrument:  metricdoc.Counter,
		Description: "Bytes transferred on the tailnet, by direction, transport, traffic type, and source/destination node.",
		Attributes: []string{
			semconv.NetworkIODirection, semconv.NetworkTransport, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.SourcePort, semconv.DestinationPort,
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

	docFlowLog = metricdoc.LogEvent{
		Name:        eventNameFlow,
		Severity:    "INFO",
		Description: "Per-connection (per_connection) or per-record (per_record) network-flow detail: the 5-tuple, transport, traffic type, source/destination node, and tx/rx bytes & packets.",
		Attributes: []string{
			semconv.SourceAddress, semconv.SourcePort, semconv.DestinationAddress, semconv.DestinationPort,
			semconv.NetworkTransport, semconv.NetworkType, semconv.AttrTrafficType,
			semconv.AttrSrcNode, semconv.AttrDstNode, semconv.AttrNodeID, attrNodeHostname,
			"tailscale.connections", // per_record summary only
			"tailscale.tx.bytes", "tailscale.rx.bytes", "tailscale.tx.packets", "tailscale.rx.packets",
		},
		Group: groupNetwork,
	}
)

// Catalog returns the metrics this package emits, for the doc generator.
func Catalog() []metricdoc.Metric {
	return []metricdoc.Metric{docIO, docPackets, docFlows, docLogsDropped}
}

// LogCatalog returns the log events this package emits, for the doc generator.
func LogCatalog() []metricdoc.LogEvent {
	return []metricdoc.LogEvent{docFlowLog}
}
