// Package semconv centralizes the OpenTelemetry attribute keys, UCUM units, and
// enumerated values shared across collectors and processors. Collector-specific
// metric names live alongside their collectors; this package holds the shared
// vocabulary so naming stays consistent.
package semconv

// Stable OTEL network attribute keys.
const (
	NetworkIODirection  = "network.io.direction"
	NetworkTransport    = "network.transport"
	NetworkType         = "network.type"
	NetworkProtocolName = "network.protocol.name"

	SourceAddress      = "source.address"
	SourcePort         = "source.port"
	DestinationAddress = "destination.address"
	DestinationPort    = "destination.port"

	HostName  = "host.name"
	HostID    = "host.id"
	OSType    = "os.type"
	OSVersion = "os.version"
)

// Tailscale-specific attribute keys (namespaced under "tailscale.").
const (
	AttrTrafficType = "tailscale.traffic_type"
	AttrSrcNode     = "tailscale.src.node"
	AttrDstNode     = "tailscale.dst.node"
	// AttrDstService is the IANA service name inferred from the destination port
	// and transport (e.g. tcp/443 -> "https"). Port-inferred, not DPI-confirmed.
	AttrDstService = "tailscale.dst.service"
	AttrNodeID     = "tailscale.node.id"
	// AttrExitNode is the short hostname (or nodeId fallback) of the exit node
	// that relayed a flow's exit traffic; carried on tailscale.exit_node.io/packets.
	AttrExitNode  = "tailscale.exit_node"
	AttrUser      = "tailscale.user"
	AttrTags      = "tailscale.tags"
	AttrTailnet   = "tailscale.tailnet"
	AttrCollector = "tailscale.collector"
	AttrFeature   = "tailscale.feature"
)

// Self-observability attribute keys.
const (
	AttrMetricName = "metric.name"
	// AttrComponent classifies a non-collector subsystem failure
	// (tailscale2otel.component.errors): "stream", "webhook", "admin",
	// "auto_configure".
	AttrComponent = "component"
	// AttrDedupSet names the de-duplication set a metric describes
	// (tailscale2otel.dedup.*): "flow", "audit", "webhook_cross".
	AttrDedupSet = "dedup.set"
	// AttrIngestSource names the ingestion path on tailscale2otel.ingest.*:
	// "poll", "stream", or "webhook".
	AttrIngestSource = "source"
	// AttrIngestSignal names the record type on tailscale2otel.ingest.records:
	// "flow", "audit", or "webhook".
	AttrIngestSignal = "signal"
	// AttrMetricGroup names the docs/catalog group a metric belongs to, used on
	// tailscale2otel.series.by_group (e.g. "Devices", "Network", "Self-observability").
	AttrMetricGroup = "metric.group"
)

// Ingestion-path (tailscale2otel.ingest.*) attribute values. A CLOSED set so the
// source/signal label cardinality stays bounded.
const (
	IngestSourcePoll    = "poll"
	IngestSourceStream  = "stream"
	IngestSourceWebhook = "webhook"

	IngestSignalFlow    = "flow"
	IngestSignalAudit   = "audit"
	IngestSignalWebhook = "webhook"
)

// network.io.direction values (stable).
const (
	DirectionTransmit = "transmit"
	DirectionReceive  = "receive"
)

// Tailscale network flow traffic_type values.
const (
	TrafficVirtual  = "virtual"
	TrafficSubnet   = "subnet"
	TrafficExit     = "exit"
	TrafficPhysical = "physical"
)

// RollupOther is the sentinel tailscale.src.node / tailscale.dst.node value for
// the folded-remainder series in the bounded *.rollup metrics: node pairs beyond
// the configured top-N are aggregated under this value so per-(transport,
// traffic_type, dst.service) totals stay exact.
const RollupOther = "__other__"

// network.type values (stable).
const (
	NetworkTypeIPv4 = "ipv4"
	NetworkTypeIPv6 = "ipv6"
)

// UCUM units.
const (
	UnitBytes         = "By"
	UnitPackets       = "{packet}"
	UnitFlows         = "{flow}"
	UnitEvents        = "{event}"
	UnitRoutes        = "{route}"
	UnitConnections   = "{connection}"
	UnitTargets       = "{target}"
	UnitSeries        = "{series}"
	UnitPeers         = "{peer}"
	UnitPorts         = "{port}"
	UnitRequests      = "{request}"
	UnitRecords       = "{record}"
	UnitDataPoints    = "{datapoint}"
	UnitSeconds       = "s"
	UnitDays          = "d"
	UnitDimensionless = "1"
)
