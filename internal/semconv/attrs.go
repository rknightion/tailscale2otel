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

// Stable OTEL user/identity attribute keys (ECS-aligned user.* registry; the
// deprecated enduser.* namespace is no longer used). Carried on security-audit
// actors and the users collector's per-user gauges/log events.
const (
	AttrUserID       = "user.id"
	AttrUserName     = "user.name"
	AttrUserFullName = "user.full_name"

	// AttrErrorMessage is the stable OTEL key for a human-readable error string
	// (error.message); carried on error-bearing audit log records.
	AttrErrorMessage = "error.message"
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
	AttrProvider  = "tailscale2otel.provider"
	AttrCollector = "tailscale.collector"
	AttrFeature   = "tailscale.feature"

	// AttrHealthType is the tailscaled health-warning class carried on the
	// curated tailscale.node.health_messages gauge (from the scraped `type`
	// label). The value set is code-defined in tailscaled, not attacker
	// free text, so it is passed through as-is (no folding).
	AttrHealthType = "tailscale.health.type"
	// AttrPath is the data-plane path a node's curated throughput/packet counters
	// (tailscale.node.io / .packets) were carried over. The scraped tailscaled
	// `path` label (direct_ipv4|direct_ipv6|derp|peer_relay_ipv4|peer_relay_ipv6)
	// is folded to the bounded set below to halve series cardinality.
	AttrPath = "tailscale.path"
	// AttrDropReason is why a packet was dropped on the curated
	// tailscale.node.packets.dropped counter (from the scraped `reason` label),
	// folded to a bounded admit-set (unrecognized -> DropReasonOther) since
	// scraped labels come from semi-trusted tailnet-member nodes.
	AttrDropReason = "tailscale.drop.reason"
)

// tailscale.path values — the bounded, folded set of data-plane paths. The raw
// tailscaled `path` label splits direct/peer_relay by IP version; the curated
// metric collapses those so per-node path cardinality stays at most four.
const (
	PathDirect    = "direct"
	PathDERP      = "derp"
	PathPeerRelay = "peer_relay"
	PathOther     = "other"
)

// tailscale.drop.reason values — the bounded admit-set for curated dropped-packet
// reasons. tailscaled emits `acl` (blocked by the packet filter) and `error`
// (dropped due to an error); any other/future value folds to DropReasonOther so
// the label cardinality stays bounded (same posture as flowlog transportName).
const (
	DropReasonACL   = "acl"
	DropReasonError = "error"
	DropReasonOther = "other"
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
	// AttrExportSignal labels tailscale2otel.export.duration by OTLP signal type
	// ("metrics" | "logs"). A separate constant from AttrIngestSignal — kept
	// subsystem-scoped (like AttrIngestSource/AttrIngestSignal) so the export and
	// ingest call sites read self-evidently and can diverge later; the shared
	// "signal" wire key is intentional (both are the OTEL "signal" dimension).
	AttrExportSignal = "signal"
	// AttrExportOutcome labels tailscale2otel.export.duration by call result
	// ("success" | "failure").
	AttrExportOutcome = "outcome"
	// AttrMetricGroup names the docs/catalog group a metric belongs to, used on
	// tailscale2otel.series.by_group (e.g. "Devices", "Network", "Self-observability").
	AttrMetricGroup = "metric.group"
	// AttrCPUMode classifies process CPU time on the OTel-standard process.cpu.time
	// metric: "user" or "system". A CLOSED set so cardinality stays bounded.
	AttrCPUMode = "cpu.mode"
)

// process.cpu.time (AttrCPUMode) values — the CLOSED set of CPU modes.
const (
	CPUModeUser   = "user"
	CPUModeSystem = "system"
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
