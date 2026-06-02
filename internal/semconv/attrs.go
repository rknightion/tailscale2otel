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

	// EventName carries the event type until the log SDK exposes a native
	// EventName field (see internal/telemetry).
	EventName = "event.name"
)

// Tailscale-specific attribute keys (namespaced under "tailscale.").
const (
	AttrTrafficType = "tailscale.traffic_type"
	AttrSrcNode     = "tailscale.src.node"
	AttrDstNode     = "tailscale.dst.node"
	AttrNodeID      = "tailscale.node.id"
	AttrUser        = "tailscale.user"
	AttrTags        = "tailscale.tags"
	AttrTailnet     = "tailscale.tailnet"
	AttrCollector   = "tailscale.collector"
	AttrFeature     = "tailscale.feature"
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
	UnitSeconds       = "s"
	UnitDays          = "d"
	UnitDimensionless = "1"
)
