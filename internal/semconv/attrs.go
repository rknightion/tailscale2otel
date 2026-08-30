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

// GeoIP enrichment attributes for EXTERNAL (non-Tailscale) endpoints, populated
// from local MaxMind databases when enrichment.geoip is on. Every one is omitted
// when the databases have no answer — never filled with an "unknown" sentinel,
// since an absent label is queryable as absent and a fabricated one is not.
//
// Two registries are in play here, deliberately:
//
//   - The geo half is OTEL-native. OTEL's semconv defines geo.country.iso_code
//     and geo.continent.code, and states that geo attributes are "typically used
//     under another namespace, such as client.*" — so the source./destination.
//     prefixed forms below are the sanctioned spelling, not an invention.
//   - The autonomous-system half is ECS. OTEL semconv has no AS namespace at
//     all (checked against the whole model/ tree), and ECS's source.as.number /
//     source.as.organization.name are the only widely-recognized names. Same
//     precedent as the ECS-aligned user.* keys above.
//
// Cardinality: the geo pair is bounded (~250 countries, 7 continents) and may go
// on flow METRICS via cardinality.flow.geo_dims. The AS pair is NOT bounded by
// anything useful and is confined to flow LOGS.
const (
	SourceGeoCountryISO      = "source.geo.country.iso_code"
	SourceGeoContinentCode   = "source.geo.continent.code"
	DestinationGeoCountryISO = "destination.geo.country.iso_code"
	DestGeoContinentCode     = "destination.geo.continent.code"

	// City-level keys, populated only when the operator supplies a City database
	// rather than a Country one. LOGS ONLY, like the AS pair: a city label would
	// be tens of thousands of values on a metric, but a log record is not a
	// series and carries the full-fidelity view by design.
	//
	// geo.locality.name, geo.region.iso_code, geo.location.lat and
	// geo.location.lon are all OTEL-native, under the same prefixing rule as the
	// country pair above.
	SourceGeoCity           = "source.geo.locality.name"
	SourceGeoRegionISO      = "source.geo.region.iso_code"
	SourceGeoLat            = "source.geo.location.lat"
	SourceGeoLon            = "source.geo.location.lon"
	DestinationGeoCity      = "destination.geo.locality.name"
	DestinationGeoRegionISO = "destination.geo.region.iso_code"
	DestinationGeoLat       = "destination.geo.location.lat"
	DestinationGeoLon       = "destination.geo.location.lon"

	SourceASNumber      = "source.as.number"
	SourceASOrg         = "source.as.organization.name"
	DestinationASNumber = "destination.as.number"
	DestinationASOrg    = "destination.as.organization.name"

	// DeviceGeoCountryISO and DeviceGeoContinentCode describe where a DEVICE is,
	// derived from the public magicsock endpoint it advertises. They ride the
	// fleet-rollup gauge (bounded by country count), never the per-device series
	// — adding a label to a live per-device gauge would change its identity and
	// break every existing query against it.
	DeviceGeoCountryISO    = "geo.country.iso_code"
	DeviceGeoContinentCode = "geo.continent.code"
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
	// AttrErrorType is the stable OTEL key for a BOUNDED error class (error.type)
	// — a closed enum, never free text, so it stays queryable and cardinality-safe
	// even when a free-text description is redacted.
	//
	// The KEY is shared; the value set is PER-SURFACE and deliberately not
	// unified, because the two surfaces classify unrelated failures:
	//   - collector scrapes (the scheduler's span + tailscale2otel.scrape.errors):
	//     "panic" | "timeout" | "error"   (internal/collector.scrapeErrorType)
	//   - OTLP export (tailscale2otel.export.failures):
	//     "export" | "timeout"            (internal/telemetry.errorType)
	// Each set stays closed and small; do not merge them into one global enum.
	AttrErrorType = "error.type"
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
	// AttrFlowWindowStart and AttrFlowWindowEnd bound the capture window a flow
	// record describes, as RFC3339Nano strings. The record's log Timestamp is the
	// window start, so the end is not otherwise recoverable; both are omitted
	// entirely when the record carries no window.
	AttrFlowWindowStart = "tailscale.flow.window.start"
	AttrFlowWindowEnd   = "tailscale.flow.window.end"
	// Per-flow endpoint identity, taken from the srcNode/dstNodes blocks the
	// control plane embeds in each flow record. Tags are joined with "," in the
	// order the record lists them. Each is omitted when the record does not carry
	// it — they vary per node (srcNode has no os; tag-owned nodes have no user).
	AttrSrcUser = "tailscale.src.user"
	AttrDstUser = "tailscale.dst.user"
	AttrSrcTags = "tailscale.src.tags"
	AttrDstTags = "tailscale.dst.tags"
	AttrSrcOS   = "tailscale.src.os"
	AttrDstOS   = "tailscale.dst.os"
	// AttrExitNode is the short hostname (or nodeId fallback) of the exit node
	// that relayed a flow's exit traffic; carried on tailscale.exit_node.io/packets.
	AttrExitNode  = "tailscale.exit_node"
	AttrUser      = "tailscale.user"
	AttrTags      = "tailscale.tags"
	AttrTailnet   = "tailscale.tailnet"
	AttrProvider  = "tailscale2otel.provider"
	AttrCollector = "tailscale.collector"
	AttrFeature   = "tailscale.feature"
	AttrACLETag   = "tailscale.acl.etag"

	// AttrHealthType is the tailscaled health-warning class carried on the
	// curated tailscale.node.health_messages gauge (from the scraped `type`
	// label). The value set is code-defined in tailscaled, not attacker
	// free text, so it is passed through as-is (no folding).
	AttrHealthType = "tailscale.health.type"
	// AttrPath is the data-plane path traffic was carried over. It appears on two
	// independent surfaces that deliberately share one vocabulary (the bounded set
	// below), so a single query spans both:
	//
	//   - the curated node-metrics counters (tailscale.node.io / .packets), where
	//     the scraped tailscaled `path` label
	//     (direct_ipv4|direct_ipv6|derp|peer_relay_ipv4|peer_relay_ipv6) is folded
	//     to that set to halve series cardinality;
	//   - the flow metrics, where it is read off the underlay endpoint of a
	//     physicalTraffic entry (see flowlog.classifyPath). Flow records cannot
	//     distinguish a peer relay from a direct endpoint, so that surface emits
	//     only PathDirect and PathDERP — a strict subset, never PathPeerRelay.
	//
	// Only physical traffic has a path at all; the overlay traffic types describe
	// what the tailnet carried rather than how, and carry the attribute not at all.
	AttrPath = "tailscale.path"
	// AttrDERPRegionID is the NUMERIC DERP region a relayed flow was carried
	// through, read from the port field beside the 127.3.3.40 relay marker. Present
	// only when AttrPath is PathDERP; a direct path omits it entirely rather than
	// carrying a sentinel.
	//
	// Deliberately NOT the same attribute as tailscale.derp.region on the devices
	// collector's latency metrics: that one is keyed by region NAME ("Frankfurt"),
	// because the devices API reports names. The two cannot be joined — the API
	// exposes no DERP map (it 404s), so there is nothing to translate an ID into a
	// name with. Sharing one attribute key would put two vocabularies under it.
	AttrDERPRegionID = "tailscale.derp.region_id"
	// AttrDropReason is why a packet was dropped on the curated
	// tailscale.node.packets.dropped counter (from the scraped `reason` label),
	// folded to a bounded admit-set (unrecognized -> DropReasonOther) since
	// scraped labels come from semi-trusted tailnet-member nodes.
	AttrDropReason = "tailscale.drop.reason"
)

// Stable attributes carried by every chunk emitted through the shared snapshot
// contract. Collector-specific revision aliases (for example an ACL ETag) may
// accompany these keys, but must not replace them.
const (
	AttrSnapshotKind       = "tailscale.snapshot.kind"
	AttrSnapshotReason     = "tailscale.snapshot.reason"
	AttrSnapshotRevision   = "tailscale.snapshot.revision"
	AttrSnapshotEmissionID = "tailscale.snapshot.emission_id"
	AttrSnapshotBytes      = "tailscale.snapshot.bytes"
	AttrSnapshotSeq        = "tailscale.snapshot.seq"
	AttrSnapshotTotal      = "tailscale.snapshot.total"
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
// reasons. These are the SEVEN reasons tailscaled documents at
// tailscale.com/docs/reference/tailscale-client-metrics; any other/future value
// folds to DropReasonOther so the label cardinality stays bounded (same posture
// as flowlog transportName).
//
// Only `acl` and `error` were admitted until #429. The other five are documented
// upstream values that have always existed, so folding them into `other` was
// discarding real diagnostic signal — "packets are being dropped" with no way to
// tell a policy block from a malformed frame. `other` is now reserved for values
// genuinely unknown to this build.
const (
	DropReasonACL              = "acl"
	DropReasonMulticast        = "multicast"
	DropReasonLinkLocalUnicast = "link_local_unicast"
	DropReasonTooShort         = "too_short"
	DropReasonFragment         = "fragment"
	DropReasonUnknownProtocol  = "unknown_protocol"
	DropReasonError            = "error"
	DropReasonOther            = "other"
)

// Peer-relay attribute keys (#429). tailscaled labels its peer-relay forwarding
// counters with BOTH an ingress and an egress transport on the same series, so
// they need two distinct keys — the generic NetworkTransport key above is the
// L4 transport of a flow and means something different.
const (
	AttrPeerRelayTransportIn  = "tailscale.peer_relay.transport_in"
	AttrPeerRelayTransportOut = "tailscale.peer_relay.transport_out"
	AttrPeerRelayState        = "tailscale.peer_relay.state"
)

// Peer-relay transport values — bounded admit-set. tailscaled documents udp4 and
// udp6; anything else folds to PeerRelayTransportOther, since these labels are
// scraped from semi-trusted tailnet-member nodes.
const (
	PeerRelayTransportUDP4  = "udp4"
	PeerRelayTransportUDP6  = "udp6"
	PeerRelayTransportOther = "other"
)

// Peer-relay endpoint state values — bounded admit-set (documented: connecting,
// open), unknown values fold to PeerRelayStateOther.
const (
	PeerRelayStateConnecting = "connecting"
	PeerRelayStateOpen       = "open"
	PeerRelayStateOther      = "other"
)

// API availability attribute keys (#420/#421/#425/#430). See internal/apistate
// for the bounded value sets and why an ambiguous 403 defaults to scope_denied
// rather than disabled.
const (
	// AttrAPIOperation is the upstream OpenAPI operationId, e.g.
	// "listNetworkFlowLogs". A closed set — it comes from the vendored spec, not
	// from a response.
	AttrAPIOperation = "tailscale.api.operation"
	// AttrAPIState carries an apistate.State value.
	AttrAPIState = "tailscale.api.state"
	// AttrSubrequest names a bounded per-entity subrequest type, e.g.
	// "device_invites" or "posture_attributes".
	AttrSubrequest = "tailscale.subrequest"
	// AttrCapability names an enabled collector's required upstream capability
	// on the configuration-to-capability matrix (#430).
	AttrCapability = "tailscale.capability"
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
	// "poll", "stream", "webhook", or "objectstore".
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
	IngestSourcePoll        = "poll"
	IngestSourceStream      = "stream"
	IngestSourceWebhook     = "webhook"
	IngestSourceObjectStore = "objectstore"

	IngestSignalFlow    = "flow"
	IngestSignalAudit   = "audit"
	IngestSignalWebhook = "webhook"
	// IngestSignalK8sAudit names tsrecorder's Kubernetes API-audit records. It is
	// baked into the object-store checkpoint namespace, so renaming it orphans
	// every deployment's durable cursor and seen set and cold-starts ingestion.
	IngestSignalK8sAudit = "k8s_audit"
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
