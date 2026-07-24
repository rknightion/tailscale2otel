package flowlog

import (
	"fmt"
	"maps"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v3/internal/dedup"
	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v3/internal/portservice"
	"github.com/rknightion/tailscale2otel/v3/internal/rdns"
	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
)

// Exported metric names emitted by the processor.
const (
	MetricIO      = "tailscale.network.io"
	MetricPackets = "tailscale.network.packets"
	MetricFlows   = "tailscale.network.flows"
	// MetricLogsDropped counts flow LOG records suppressed by the
	// MaxLogRecordsPerWindow volume guard. Metrics are never dropped; only logs.
	MetricLogsDropped = "tailscale.network.flow.logs_dropped"
)

// unitRecord is the OTEL unit for MetricLogsDropped (a count of log records).
const unitRecord = "{record}"

// eventNameFlow is the OTEL LogRecord event name for a per-connection flow log.
const eventNameFlow = "tailscale.network.flow"

// attrNodeHostname is the log attribute carrying the originating node's short
// hostname, looked up from the device cache by the FlowLog's NodeID.
const attrNodeHostname = "tailscale.node.hostname"

// Log modes for Options.LogMode. Any other value (including "off") suppresses
// log emission while still producing metrics.
const (
	logPerConnection = "per_connection"
	logPerRecord     = "per_record"
)

// Options configures a Processor.
type Options struct {
	// LogMode selects how flow logs are emitted: "per_connection" (default),
	// "per_record", or "off". An empty value means "per_connection".
	LogMode string
	// IncludeSourcePort / IncludeDestinationPort independently add
	// source.port / destination.port to METRIC attributes. Flow LOGS always carry
	// both ports regardless of these toggles.
	IncludeSourcePort      bool
	IncludeDestinationPort bool
	// NodeDims adds tailscale.src.node/tailscale.dst.node to metric attributes.
	NodeDims bool
	// IdentityDims adds the per-flow endpoint identity (user, tags, OS) to METRIC
	// attributes, on the raw AND rollup families. Flow LOGS always carry it when
	// the record supplies it, regardless of this toggle. Off by default: the
	// values are low-cardinality but PII-adjacent, so they stay off the default
	// metric surface exactly as the ports do.
	//
	// Identity is node-derived, so it is only honored when NodeDims is on — see
	// newRollupAccumulator.
	IdentityDims bool
	// KeepExternalAddrs, when true, resolves an unrecognized address to its raw
	// host (IP) instead of collapsing it to "external"/"unknown". The zero value
	// (false) preserves the collapsing behavior.
	KeepExternalAddrs bool
	// RDNS, when non-nil, supplies reverse-DNS (PTR) names for EXTERNAL addresses:
	// a cached hit replaces "external"/raw-IP in src/dst node with the hostname.
	// It is consulted only for non-Tailscale addresses and never blocks.
	RDNS rdns.Resolver
	// Dedup, when non-nil, suppresses duplicate FlowLog window records that arrive
	// from both the poll flowlogs collector and the streaming receiver (which
	// share one Processor). A nil value (the default) disables cross-component
	// de-duplication.
	Dedup *dedup.Set
	// MaxLogRecordsPerWindow caps the number of flow LOG records emitted per poll
	// window (one ProcessAll call; standalone Process applies its own per-call
	// budget). Once the cap is reached, further flow logs are suppressed and
	// counted into MetricLogsDropped, but ALL metrics keep flowing. A zero value
	// (the default) means unlimited and preserves the current behavior exactly.
	MaxLogRecordsPerWindow int
	// FlowMetricsMode selects which flow metric families to emit: "all"
	// (per-connection raw io/packets), "rollup" (bounded top-N *.rollup families
	// only), or "both". An empty value means "all" — the safe library default; the
	// config layer supplies the product default "rollup". In rollup/both mode the
	// accumulated families are emitted by FlushRollup (driven on the export
	// interval); poll and stream feed the same accumulator.
	FlowMetricsMode string
	// RollupTopN bounds the number of busiest src/dst node pairs kept per flush in
	// rollup/both mode; the remainder folds into an __other__ series. A value <= 0
	// selects a default.
	RollupTopN int
	// Store, when non-nil, receives one Observation per processed connection for
	// the admin flow view. It is fed from inside the same path that emits the
	// metrics — after de-duplication, so it sees each connection exactly once —
	// and its Record must never block or fail (see the flowstore package doc).
	Store Recorder
	// Policy, when non-nil, supplies the tailnet's compiled network policy, and
	// every policy-governed connection is reconciled against it as it is
	// processed. The verdict rides on the Observation, so it has no effect
	// without Store. Reconciliation happens here rather than at query time
	// because the store retains only aggregates plus a bounded recent ring:
	// evaluating on the way in is what makes a verdict cover the whole retention
	// window.
	Policy PolicySource
	// ExitNodeAttribution emits the bounded tailscale.exit_node.io/packets
	// counters that attribute exit traffic to the reporting (exit) node. Default
	// on at the config layer. Independent of FlowMetricsMode — the cardinality is
	// intrinsically bounded by exit-node count, so it is emitted directly (not via
	// the rollup accumulator) in every mode.
	ExitNodeAttribution bool
}

// Recorder receives one observation per processed connection. It is the narrow
// view of the flow store the processor needs, so tests can fake it without
// standing up the real one.
type Recorder interface {
	Record(flowstore.Observation)
}

// PolicySource supplies the current compiled network policy. *aclpolicy.Store
// satisfies it, and is what the app wires in.
//
// A nil return means no policy has been collected yet, which the processor
// treats as "do not evaluate" — never as "nothing is permitted". Reads happen
// once per connection on the emit path, so an implementation must not block.
type PolicySource interface {
	Policy() *aclpolicy.Policy
}

// Processor converts Tailscale flow logs into OTEL metrics and log records. It
// is stateless per call and safe to share between the polling collector and the
// streaming receiver; all mutable accumulation lives in the Emitter.
type Processor struct {
	cache        *enrich.DeviceCache
	logMode      string
	mode         string
	srcPort      bool
	dstPort      bool
	nodes        bool
	keepExternal bool
	identity     bool
	rdns         rdns.Resolver
	dedup        *dedup.Set
	maxLogs      int
	// rollup is non-nil in "rollup"/"both" mode; it accumulates per-connection
	// contributions and is drained by FlushRollup on the export interval.
	rollup *rollupAccumulator
	// exitNode enables per-exit-node IO/packets attribution (Options.ExitNodeAttribution).
	exitNode bool
	// store receives one observation per connection for the admin flow view; nil
	// when the view is disabled.
	store Recorder
	// policy supplies the compiled network policy each connection is reconciled
	// against; nil when reconciliation is off.
	policy PolicySource
}

// NewProcessor returns a Processor using cache for device-name resolution. A nil
// cache is tolerated; node resolution then yields "unknown".
func NewProcessor(cache *enrich.DeviceCache, opts Options) *Processor {
	logMode := opts.LogMode
	if logMode == "" {
		logMode = logPerConnection
	}
	flowMode := opts.FlowMetricsMode
	if flowMode == "" {
		flowMode = flowModeAll
	}
	p := &Processor{
		cache:        cache,
		logMode:      logMode,
		mode:         flowMode,
		srcPort:      opts.IncludeSourcePort,
		dstPort:      opts.IncludeDestinationPort,
		nodes:        opts.NodeDims,
		keepExternal: opts.KeepExternalAddrs,
		identity:     opts.IdentityDims,
		rdns:         opts.RDNS,
		dedup:        opts.Dedup,
		maxLogs:      opts.MaxLogRecordsPerWindow,
		exitNode:     opts.ExitNodeAttribution,
		store:        opts.Store,
		policy:       opts.Policy,
	}
	if flowMode == flowModeRollup || flowMode == flowModeBoth {
		p.rollup = newRollupAccumulator(opts.RollupTopN, opts.NodeDims, opts.IdentityDims)
	}
	return p
}

// logBudget gates flow LOG record emission for the volume guard. remaining < 0
// means unlimited (the cap is disabled). allow reports whether one more log
// record may be emitted, decrementing the remaining budget when it can or
// counting a drop when it cannot.
type logBudget struct {
	remaining int
	dropped   int
}

// newLogBudget returns a budget for max log records. max <= 0 yields an
// unlimited budget that never drops.
func newLogBudget(max int) *logBudget {
	if max <= 0 {
		return &logBudget{remaining: -1}
	}
	return &logBudget{remaining: max}
}

// allow reports whether one more flow log record may be emitted. An unlimited
// budget (remaining < 0) always allows; otherwise it consumes one unit when
// available and records a drop when exhausted.
func (b *logBudget) allow() bool {
	if b.remaining < 0 {
		return true
	}
	if b.remaining == 0 {
		b.dropped++
		return false
	}
	b.remaining--
	return true
}

// ProcessAll converts every flow log in resp. The MaxLogRecordsPerWindow cap (if
// set) applies across the whole call (the poll window): one shared budget gates
// every flow log record, and any suppressed records are flushed into
// MetricLogsDropped once the loop completes.
func (p *Processor) ProcessAll(resp NetworkResponse, e telemetry.Emitter) {
	budget := newLogBudget(p.maxLogs)
	for i := range resp.Logs {
		p.process(resp.Logs[i], e, budget)
	}
	p.flushDropped(budget, e)
}

// trafficSet pairs a ConnectionCounts slice with its traffic_type label.
type trafficSet struct {
	typ    string
	counts []ConnectionCounts
}

// Process converts a single FlowLog into metrics and (per LogMode) log records.
// When MaxLogRecordsPerWindow is set, this standalone entry point (used by the
// stream receiver) applies the cap per single call with its own budget and
// flushes any dropped count before returning.
func (p *Processor) Process(flow FlowLog, e telemetry.Emitter) {
	budget := newLogBudget(p.maxLogs)
	p.process(flow, e, budget)
	p.flushDropped(budget, e)
}

// FlushRollup emits the accumulated bounded *.rollup counters and the
// per-source-node unique gauges for the current interval, then resets the
// accumulator. It is a no-op in "all" mode (nil accumulator). The app's rollup
// flusher calls it once per export interval; the poll collector and the stream
// receiver share one Processor and feed the same accumulator, so a single flush
// drains both ingestion paths. Safe for concurrent use with Process/ProcessAll.
func (p *Processor) FlushRollup(e telemetry.Emitter) {
	p.rollup.Flush(e)
}

// process converts a single FlowLog, gating every flow LOG record through
// budget. Metrics are always emitted; only log records consume the budget. The
// caller owns the budget (one per ProcessAll window, or one per standalone
// Process call) and is responsible for flushing the dropped count.
func (p *Processor) process(flow FlowLog, e telemetry.Emitter, budget *logBudget) {
	// Seed the cache from the identity the record embeds BEFORE resolving any of
	// its connections, so a record enriches itself even when the devices
	// collector is disabled or has not yet run. Additive only — devices-collector
	// entries are never overwritten (see enrich.UpsertFromFlow).
	if p.cache != nil {
		p.cache.UpsertFromFlow(flow.nodeRefs())
	}

	sets := [...]trafficSet{
		{semconv.TrafficVirtual, flow.VirtualTraffic},
		{semconv.TrafficSubnet, flow.SubnetTraffic},
		{semconv.TrafficExit, flow.ExitTraffic},
		{semconv.TrafficPhysical, flow.PhysicalTraffic},
	}

	var totalConns int
	var totalTxBytes, totalRxBytes, totalTxPkts, totalRxPkts int64

	for _, set := range sets {
		for i := range set.counts {
			cc := set.counts[i]
			// Cross-source de-duplication at CONNECTION granularity (matching the
			// poll collector's boundary key). Per-connection — not per-window — so a
			// window re-delivered with a new connection still emits that connection
			// while the already-seen connections are skipped. The first sighting
			// (from poll or stream, which share this processor) wins.
			if p.dedup != nil && !p.dedup.Add(connDedupKey(flow, cc)) {
				continue
			}
			p.processConn(flow, set.typ, cc, e, budget)

			totalConns++
			totalTxBytes += cc.TxBytes
			totalRxBytes += cc.RxBytes
			totalTxPkts += cc.TxPkts
			totalRxPkts += cc.RxPkts
		}
	}

	// Emit the per-record summary when in per_record mode. With dedup on, suppress
	// it when every connection was a duplicate (nothing left to summarize); with
	// dedup off, preserve the original always-emit behavior. The summary log also
	// consumes the volume budget.
	if p.logMode == logPerRecord && (totalConns > 0 || p.dedup == nil) && budget.allow() {
		p.emitRecordLog(flow, totalConns, totalTxBytes, totalRxBytes, totalTxPkts, totalRxPkts, e)
	}
}

// flushDropped emits MetricLogsDropped with the budget's suppressed count when
// any flow log records were dropped. Nothing is emitted when none were dropped.
func (p *Processor) flushDropped(budget *logBudget, e telemetry.Emitter) {
	if budget.dropped > 0 {
		e.Counter(docLogsDropped.Name, docLogsDropped.Unit, docLogsDropped.Description,
			float64(budget.dropped), telemetry.Attrs{})
	}
}

// connDedupKey is the cross-source de-dup identity of one connection within a
// flow window: nodeId|start|end|proto|src|dst. It matches the flowlogs
// collector's per-connection boundary key so the two dedup layers are consistent.
func connDedupKey(fl FlowLog, cc ConnectionCounts) string {
	return fl.NodeID + "|" +
		fl.Start.UTC().Format(time.RFC3339Nano) + "|" +
		fl.End.UTC().Format(time.RFC3339Nano) + "|" +
		strconv.Itoa(cc.Proto) + "|" + cc.Src + "|" + cc.Dst
}

// processConn emits metrics (and, in per_connection mode, a log) for one
// ConnectionCounts entry. Metrics are always emitted; the per-connection log is
// gated through budget so the volume guard never suppresses metrics.
func (p *Processor) processConn(flow FlowLog, trafficType string, cc ConnectionCounts, e telemetry.Emitter, budget *logBudget) {
	transport := transportName(cc.Proto)
	srcAddr, srcPort := splitHostPort(cc.Src)
	dstAddr, dstPort := splitHostPort(cc.Dst)
	netType := networkType(srcAddr)
	// An endpoint the record does not carry is structurally ABSENT, not a failed
	// lookup, so it resolves to "" and every attribute derived from it is omitted
	// rather than filled with the "unknown" sentinel. Exit traffic is the case
	// that forces this: in a live capture all 504 exitTraffic entries carried no
	// dst and 234 carried no src, so resolving them produced dst_node="unknown"
	// on 100% of exit series — a label claiming a lookup that was never possible.
	// Use tailscale.exit_node.io/packets to measure exit traffic; they attribute
	// by reporting node, the only dimension exit records actually supply.
	srcNode := p.resolveEndpoint(cc.Src, srcAddr)
	// How the two nodes actually reached each other, read off the underlay
	// endpoint. Computed once here and shared by the raw metrics, the rollup and
	// the flow store, so the classification runs once per connection.
	storePath, derpRegion := classifyPath(trafficType, dstAddr, dstPort)
	metricPath := metricPathValue(storePath)
	// A relayed physical entry's destination is the DERP marker, not a peer, so
	// it yields neither a node nor a service — see relayedDestination.
	dstNode, dstService := "", ""
	if !relayedDestination(storePath) {
		dstNode = p.resolveEndpoint(cc.Dst, dstAddr)
		dstService = serviceName(transport, dstPort)
	}

	// Raw per-connection io/packets families (all/both mode). In rollup mode the
	// bounded *.rollup families are emitted by FlushRollup from the accumulator
	// instead; these high-cardinality raw families are suppressed.
	if p.mode == flowModeAll || p.mode == flowModeBoth {
		// Metric attributes shared by io + packets points.
		metricAttrs := telemetry.Attrs{
			semconv.NetworkTransport: transport,
			semconv.AttrTrafficType:  trafficType,
		}
		if p.nodes {
			if srcNode != "" {
				metricAttrs[semconv.AttrSrcNode] = srcNode
			}
			if dstNode != "" {
				metricAttrs[semconv.AttrDstNode] = dstNode
			}
		}
		if p.srcPort && cc.Src != "" {
			metricAttrs[semconv.SourcePort] = srcPort
		}
		if p.dstPort && cc.Dst != "" {
			metricAttrs[semconv.DestinationPort] = dstPort
		}
		// dst.service is the bounded stand-in for the destination port and is
		// emitted unconditionally, matching the rollup families (which have always
		// carried it ungated). It is the one L4-derived dimension whose value space
		// is a fixed registry rather than the ephemeral port range.
		if dstService != "" {
			metricAttrs[semconv.AttrDstService] = dstService
		}
		addPathAttrs(metricAttrs, metricPath, derpRegion)
		if p.identity {
			addIdentityAttrs(metricAttrs, flow.refByAddr(srcAddr), flow.refByAddr(dstAddr))
		}

		// MetricIO (bytes): transmit + receive. Name/unit/description come from the
		// catalog (catalog.go) so they cannot drift from the generated docs.
		e.Counter(docIO.Name, docIO.Unit, docIO.Description,
			float64(cc.TxBytes), dirAttrs(metricAttrs, semconv.DirectionTransmit))
		e.Counter(docIO.Name, docIO.Unit, docIO.Description,
			float64(cc.RxBytes), dirAttrs(metricAttrs, semconv.DirectionReceive))

		// MetricPackets: transmit + receive.
		e.Counter(docPackets.Name, docPackets.Unit, docPackets.Description,
			float64(cc.TxPkts), dirAttrs(metricAttrs, semconv.DirectionTransmit))
		e.Counter(docPackets.Name, docPackets.Unit, docPackets.Description,
			float64(cc.RxPkts), dirAttrs(metricAttrs, semconv.DirectionReceive))
	}

	// Bounded rollup accumulation (rollup/both mode); drained by FlushRollup. The
	// rollup deliberately carries no L4 ports — they stay in the flow logs and in
	// the per-source-node unique gauges.
	if p.rollup != nil {
		d := rollupDims{
			transport:   transport,
			trafficType: trafficType,
			srcNode:     srcNode,
			dstNode:     dstNode,
			dstService:  dstService,
			path:        metricPath,
			derpRegion:  derpRegion,
		}
		// Resolving the node blocks is only worth it when the accumulator will
		// actually key on identity — refByAddr walks the record's embedded blocks.
		if p.rollup.wantsIdentity() {
			d.identity = identityOf(flow.refByAddr(srcAddr), flow.refByAddr(dstAddr))
		}
		p.rollup.record(d,
			float64(cc.TxBytes), float64(cc.RxBytes), float64(cc.TxPkts), float64(cc.RxPkts))
		// A destination that does not exist is not a distinct peer: counting it
		// would add exactly one phantom peer per source node to the unique gauge.
		if dstNode != "" {
			p.rollup.observeUnique(srcNode, dstNode, dstPort)
		}
	}

	// MetricFlows: one flow observed (low cardinality; emitted in every mode).
	e.Counter(docFlows.Name, docFlows.Unit, docFlows.Description, 1, telemetry.Attrs{
		semconv.NetworkTransport: transport,
		semconv.AttrTrafficType:  trafficType,
	})

	// Per-exit-node IO attribution (bounded by exit-node count; all metric modes).
	if p.exitNode && trafficType == semconv.TrafficExit {
		node := p.exitNodeLabel(flow.NodeID)
		ioAttrs := telemetry.Attrs{semconv.AttrExitNode: node}
		e.Counter(docExitNodeIO.Name, docExitNodeIO.Unit, docExitNodeIO.Description,
			float64(cc.TxBytes), dirAttrs(ioAttrs, semconv.DirectionTransmit))
		e.Counter(docExitNodeIO.Name, docExitNodeIO.Unit, docExitNodeIO.Description,
			float64(cc.RxBytes), dirAttrs(ioAttrs, semconv.DirectionReceive))
		e.Counter(docExitNodePackets.Name, docExitNodePackets.Unit, docExitNodePackets.Description,
			float64(cc.TxPkts), dirAttrs(ioAttrs, semconv.DirectionTransmit))
		e.Counter(docExitNodePackets.Name, docExitNodePackets.Unit, docExitNodePackets.Description,
			float64(cc.RxPkts), dirAttrs(ioAttrs, semconv.DirectionReceive))
	}

	// Feed the admin flow view. This is the same connection the metrics above
	// describe, so it is fed from inside the dedup-guarded path and never from a
	// second traversal.
	if p.store != nil {
		p.store.Record(p.observation(flow, trafficType, cc, transport, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode, dstService))
	}

	if p.logMode == logPerConnection && budget.allow() {
		p.emitConnLog(flow, trafficType, cc, transport, netType, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode, dstService, e)
	}
}

// observation builds the flow-store view of one connection.
//
// It carries the identity the record carried, unfiltered. pii_filter governs the
// telemetry this process EXPORTS — what reaches a third-party backend. The store
// is in memory, never leaves the process, and is readable only through the
// admin-authenticated /flows surface, so an operator who has narrowed what they
// send onward still sees their own tailnet in full here (#241). An empty field
// therefore means one thing only: the record did not carry it.
func (p *Processor) observation(flow FlowLog, trafficType string, cc ConnectionCounts,
	transport, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode, dstService string,
) flowstore.Observation {
	// Resolved once and used for both the identity fields and the policy
	// evaluation below: refByAddr walks the record's embedded node blocks, so a
	// second lookup per endpoint would be pure repeated work on the emit path.
	srcRef, dstRef := flow.refByAddr(srcAddr), flow.refByAddr(dstAddr)

	verdict, rule, reversed := p.reconcile(trafficType, transport, srcRef, dstRef, srcAddr, srcPort, dstAddr, dstPort)

	o := flowstore.Observation{
		Time:        logTimestamp(flow),
		TrafficType: trafficType,
		Transport:   transport,
		// The endpoints keep their ports; the nodes are the already-resolved
		// names, empty for an endpoint the record did not carry at all.
		SrcAddr:    cc.Src,
		DstAddr:    cc.Dst,
		DstPort:    dstPort,
		DstService: dstService,
		SrcNode:    srcNode,
		DstNode:    dstNode,
		Verdict:    verdict,
		Rule:       rule,
		Reversed:   reversed,
		Counts: flowstore.Counts{
			TxBytes: cc.TxBytes,
			RxBytes: cc.RxBytes,
			TxPkts:  cc.TxPkts,
			RxPkts:  cc.RxPkts,
			Flows:   1,
		},
	}
	if srcRef != nil {
		o.SrcUser, o.SrcTags, o.SrcOS = srcRef.User, joinTags(srcRef.Tags), srcRef.OS
	}
	if dstRef != nil {
		o.DstUser, o.DstTags, o.DstOS = dstRef.User, joinTags(dstRef.Tags), dstRef.OS
	}
	o.Path, o.DERPRegion = classifyPath(trafficType, dstAddr, dstPort)
	return o
}

// derpMarker is the loopback address Tailscale writes as a physical destination
// when a peer was reached through a DERP relay rather than directly. It is a
// marker, not a routable endpoint, and the value in the PORT field beside it is
// the relay's region ID.
const derpMarker = "127.3.3.40"

// classifyPath reads how two nodes actually reached each other off one physical
// connection's underlay endpoint.
//
// Only physicalTraffic carries this. The overlay traffic types describe what the
// tailnet carried, not how, so they get no path at all — and neither does a
// physical entry with no endpoint, since "we cannot tell" must not be folded into
// the direct column an operator would read as good news.
//
// This deliberately does not depend on the record's proto field: it is 0 on every
// one of 44,825 physical entries in a live capture.
func classifyPath(trafficType, dstAddr, dstPort string) (path, region string) {
	if trafficType != semconv.TrafficPhysical || dstAddr == "" {
		return "", ""
	}
	if dstAddr == derpMarker {
		// A marker with an unreadable region is still a relayed path. Demoting it
		// to direct would understate relaying, which is the number being asked for.
		return flowstore.PathDERP, dstPort
	}
	if strings.Contains(dstAddr, ":") {
		return flowstore.PathDirectIPv6, ""
	}
	return flowstore.PathDirectIPv4, ""
}

// relayedDestination reports whether a connection's destination is the DERP
// marker rather than a peer, and so carries no destination NAME of any kind.
//
// The marker is an endpoint the record genuinely carried, so the raw address and
// port are kept everywhere they were kept before — on the flow log, on the flow
// store, and under destination.port when that dimension is on. What it does not
// carry is a peer: 127.3.3.40 is not a device, and the value beside it is a DERP
// region ID, not a port. So the two dimensions that NAME something —
// tailscale.dst.node (a device) and tailscale.dst.service (an IANA service) —
// would both be inventions rather than readings, and are omitted the same way
// this processor omits every endpoint the record does not really carry.
//
// dst.service is the dormant half of that: physical entries report proto 0 on
// live data, so transportName yields "unknown", portservice.LookupName matches
// no registered transport and the region never reaches the registry. Should
// Tailscale start populating proto, region 22 would map through tcp/22 and mint
// "ssh" on a connection that contacted no SSH service — which is why this gate
// does not rely on that measurement holding.
//
// The counterparty is not lost by any of this. On a physical entry src is the
// PEER's overlay address and dst is the endpoint it was reached at, so the node
// an operator wants is on the src side, where it stays.
func relayedDestination(storePath string) bool { return storePath == flowstore.PathDERP }

// metricPathValue folds a store path onto the tailscale.path metric vocabulary.
//
// The store keeps the IP-version split (direct_ipv4 / direct_ipv6) because the
// /flows view has the room for it and an operator reading one tailnet wants it.
// The metric surface deliberately does not: semconv.AttrPath already carries the
// FOLDED set on the node-metrics counters, where direct_ipv4/direct_ipv6 collapse
// to semconv.PathDirect. Emitting the unfolded values here would put two
// vocabularies under one attribute key, so `sum by (tailscale_path)` across the
// two families would report `direct` and `direct_ipv4` as unrelated paths.
//
// An empty store path (overlay traffic, or physical traffic with no endpoint)
// stays empty, and the caller omits the attribute entirely.
func metricPathValue(storePath string) string {
	switch storePath {
	case flowstore.PathDERP:
		return semconv.PathDERP
	case flowstore.PathDirectIPv4, flowstore.PathDirectIPv6:
		return semconv.PathDirect
	default:
		return ""
	}
}

// joinTags renders a node block's tags the way every other surface does. An
// empty list yields an empty string, which the store reads as "untagged".
func joinTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return strings.Join(tags, ",")
}

// reconcile decides what the tailnet's network policy says about one
// connection. It returns an empty verdict when there is nothing to decide from:
// reconciliation is off, no policy has been collected yet, or the traffic is
// not policy-governed. That is deliberately not "undetermined", which means a
// policy WAS applied and could not decide.
//
// It runs once per connection on the emit path, so it neither allocates nor
// blocks: the policy is read through an atomic pointer, and the compiled
// Policy is immutable and evaluated in place.
//
// Note it evaluates the identity the record CARRIED, before the PII filter runs
// over it. The filter governs what the process discloses; a verdict discloses
// nothing about an endpoint, and deriving it from redacted input would silently
// turn an operator's privacy setting into wrong security findings. What the
// page can say ABOUT an unexplained connection still comes from the redacted
// fields.
func (p *Processor) reconcile(trafficType, transport string, srcRef, dstRef *NodeRef,
	srcAddr, srcPort, dstAddr, dstPort string,
) (verdict string, rule int, reversed bool) {
	if p.policy == nil {
		return "", -1, false
	}
	// physicalTraffic is the WireGuard underlay — the encrypted path between two
	// endpoints, not the tailnet connection a policy describes. Reconciling it
	// would report every peer-to-peer path as unexplained.
	if trafficType == semconv.TrafficPhysical {
		return "", -1, false
	}
	pol := p.policy.Policy()
	if pol == nil {
		return "", -1, false
	}

	c := aclpolicy.Conn{
		Src:    policyEndpoint(srcRef, srcAddr),
		Dst:    policyEndpoint(dstRef, dstAddr),
		Proto:  transport,
		IsExit: trafficType == semconv.TrafficExit,
	}
	// BOTH ports matter. A record carrying only the destination port reverses
	// into one carrying none, which makes every port-specific rule undecidable
	// against return traffic — and return traffic was 37% of a live capture.
	c.DstPort, c.HasPort = parsePort(dstPort)
	c.SrcPort, c.HasSrcPort = parsePort(srcPort)

	res := pol.Evaluate(c)
	return res.Verdict.String(), res.Rule, res.Reversed
}

// policyEndpoint builds the evaluator's view of one endpoint. The tag slice is
// the one the record decoded, not a copy: it is read-only from here on, and
// copying it per connection is the kind of cost that has no business on the
// emit path.
func policyEndpoint(ref *NodeRef, addr string) aclpolicy.Endpoint {
	ep := aclpolicy.Endpoint{}
	if ref != nil {
		ep.User = ref.User
		ep.Tags = ref.Tags
	}
	if addr != "" {
		if a, err := netip.ParseAddr(addr); err == nil {
			ep.Addr = a
		}
	}
	return ep
}

// parsePort reads the port half of an endpoint. A record that carried no port —
// ICMP, and the exit records that carry no destination at all — yields false,
// which the evaluator reads as "this cannot be decided against a rule naming
// specific ports" rather than as port zero.
func parsePort(s string) (uint16, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, false
	}
	return uint16(v), true
}

// identityOf collects the node-derived identity of both endpoints into the
// rollup's key half. It mirrors addIdentityAttrs, which builds the same values
// as loose attributes for the raw families and the logs; both omit a field the
// node block does not carry, so an absent field never becomes an empty label.
func identityOf(src, dst *NodeRef) identityKey {
	var k identityKey
	if src != nil {
		k.srcUser, k.srcTags, k.srcOS = src.User, joinTags(src.Tags), src.OS
	}
	if dst != nil {
		k.dstUser, k.dstTags, k.dstOS = dst.User, joinTags(dst.Tags), dst.OS
	}
	return k
}

// addPathAttrs adds the underlay-path dimensions to a metric attribute set.
// Both are omitted when absent rather than filled with a sentinel: overlay
// traffic has no path at all, and a direct path has no DERP region. Folding
// either into a placeholder would put "we cannot tell" in the same bucket an
// operator reads as a real answer.
func addPathAttrs(attrs telemetry.Attrs, path, derpRegion string) {
	if path == "" {
		return
	}
	attrs[semconv.AttrPath] = path
	if derpRegion != "" {
		attrs[semconv.AttrDERPRegionID] = derpRegion
	}
}

// addIdentityAttrs adds the per-flow endpoint identity (user, tags, OS) carried
// by the record's embedded srcNode/dstNodes blocks. Each attribute is omitted
// when the corresponding ref is absent or does not carry that field — the
// fields genuinely vary per node, so an absent attribute is the honest
// encoding. Tags are joined with "," in record order.
func addIdentityAttrs(attrs telemetry.Attrs, src, dst *NodeRef) {
	add := func(ref *NodeRef, userKey, tagsKey, osKey string) {
		if ref == nil {
			return
		}
		if ref.User != "" {
			attrs[userKey] = ref.User
		}
		if tags := joinTags(ref.Tags); tags != "" {
			attrs[tagsKey] = tags
		}
		if ref.OS != "" {
			attrs[osKey] = ref.OS
		}
	}
	add(src, semconv.AttrSrcUser, semconv.AttrSrcTags, semconv.AttrSrcOS)
	add(dst, semconv.AttrDstUser, semconv.AttrDstTags, semconv.AttrDstOS)
}

// dirAttrs clones base and adds the network.io.direction attribute. Cloning
// keeps each emitted point's attribute set independent and avoids mutating the
// shared base map.
func dirAttrs(base telemetry.Attrs, direction string) telemetry.Attrs {
	out := make(telemetry.Attrs, len(base)+1)
	maps.Copy(out, base)
	out[semconv.NetworkIODirection] = direction
	return out
}

// emitConnLog emits one per-connection flow log event.
func (p *Processor) emitConnLog(flow FlowLog, trafficType string, cc ConnectionCounts, transport, netType, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode, dstService string, e telemetry.Emitter) {
	body := fmt.Sprintf("%s %s %s -> %s tx=%dB rx=%dB", transport, trafficType, cc.Src, cc.Dst, cc.TxBytes, cc.RxBytes)
	attrs := telemetry.Attrs{
		semconv.NetworkTransport: transport,
		semconv.NetworkType:      netType,
		semconv.AttrTrafficType:  trafficType,
		semconv.AttrNodeID:       flow.NodeID,
		"tailscale.tx.bytes":     cc.TxBytes,
		"tailscale.rx.bytes":     cc.RxBytes,
		"tailscale.tx.packets":   cc.TxPkts,
		"tailscale.rx.packets":   cc.RxPkts,
	}
	// Endpoint attributes only for endpoints the record actually carries — see
	// resolveEndpoint. Exit records carry no destination at all, and half carry
	// no source; emitting empty addresses and "unknown" nodes for them would
	// describe a 5-tuple that was never in the data.
	if cc.Src != "" {
		attrs[semconv.SourceAddress] = srcAddr
		attrs[semconv.SourcePort] = srcPort
		attrs[semconv.AttrSrcNode] = srcNode
	}
	if cc.Dst != "" {
		attrs[semconv.DestinationAddress] = dstAddr
		attrs[semconv.DestinationPort] = dstPort
		// A destination the record carried but that names no device — the DERP
		// marker — keeps its address here and gets no node. The log is the
		// full-fidelity record of what the wire said, and the wire said an
		// endpoint, not a peer.
		if dstNode != "" {
			attrs[semconv.AttrDstNode] = dstNode
		}
	}
	// Logs always carry the mapped destination service when the port is known
	// (independent of the metric toggle); omit it entirely otherwise.
	if dstService != "" {
		attrs[semconv.AttrDstService] = dstService
	}
	p.addNodeHostname(attrs, flow.NodeID)
	addFlowWindow(attrs, flow)
	// Logs carry full detail by design, so identity is unconditional here — the
	// IdentityDims toggle governs the metric surface only.
	addIdentityAttrs(attrs, flow.refByAddr(srcAddr), flow.refByAddr(dstAddr))
	e.LogEvent(telemetry.Event{
		Name:              docFlowLog.Name,
		Body:              body,
		Severity:          telemetry.SeverityInfo,
		Timestamp:         logTimestamp(flow),
		ObservedTimestamp: observedTimestamp(flow),
		Attrs:             attrs,
	})
}

// emitRecordLog emits one summary log event for an entire FlowLog.
func (p *Processor) emitRecordLog(flow FlowLog, conns int, txBytes, rxBytes, txPkts, rxPkts int64, e telemetry.Emitter) {
	body := fmt.Sprintf("node %s: %d connections tx=%dB rx=%dB", flow.NodeID, conns, txBytes, rxBytes)
	attrs := telemetry.Attrs{
		semconv.AttrNodeID:      flow.NodeID,
		"tailscale.connections": int64(conns),
		"tailscale.tx.bytes":    txBytes,
		"tailscale.rx.bytes":    rxBytes,
		"tailscale.tx.packets":  txPkts,
		"tailscale.rx.packets":  rxPkts,
	}
	p.addNodeHostname(attrs, flow.NodeID)
	addFlowWindow(attrs, flow)
	e.LogEvent(telemetry.Event{
		Name:              docFlowLog.Name,
		Body:              body,
		Severity:          telemetry.SeverityInfo,
		Timestamp:         logTimestamp(flow),
		ObservedTimestamp: observedTimestamp(flow),
		Attrs:             attrs,
	})
}

// exitNodeLabel resolves the reporting node's hostname for the exit-node
// attribution metric, falling back to the raw nodeId on a nil/miss cache or an
// empty hostname, and to "unknown" only when there is no nodeId.
func (p *Processor) exitNodeLabel(nodeID string) string {
	if p.cache != nil {
		if meta, ok := p.cache.LookupNode(nodeID); ok && meta.Hostname != "" {
			return meta.Hostname
		}
	}
	if nodeID != "" {
		return nodeID
	}
	return "unknown"
}

// addNodeHostname adds tailscale.node.hostname to attrs when the cache has a
// device for nodeID with a non-empty Hostname. A nil cache, a cache miss, or an
// empty hostname leaves attrs untouched (the attribute is omitted entirely).
func (p *Processor) addNodeHostname(attrs telemetry.Attrs, nodeID string) {
	if p.cache == nil {
		return
	}
	if meta, ok := p.cache.LookupNode(nodeID); ok && meta.Hostname != "" {
		attrs[attrNodeHostname] = meta.Hostname
	}
}

// resolveEndpoint resolves an endpoint the record may not carry at all. An
// empty addrPort yields "", which callers treat as "omit every attribute
// derived from this endpoint". A present endpoint resolves exactly as before.
func (p *Processor) resolveEndpoint(addrPort, host string) string {
	if addrPort == "" {
		return ""
	}
	return p.resolve(addrPort, host)
}

// resolve maps an "addr:port" to a device hostname via the cache. A nil cache
// yields "unknown". host is the already-split host part of addrPort. When the
// address is EXTERNAL (non-Tailscale) and a reverse-DNS resolver is configured,
// a cached PTR name replaces the "external" sentinel. Otherwise, when
// keepExternal is set and the cache misses (collapsing to "external"/"unknown"),
// the raw host is returned instead of the collapsed sentinel.
func (p *Processor) resolve(addrPort, host string) string {
	if p.cache == nil {
		if p.keepExternal && host != "" {
			return host
		}
		return "unknown"
	}
	name := p.cache.ResolveName(addrPort)
	// Reverse DNS enriches only external addresses (never tailnet "unknown"), and
	// only when a name is already cached — the lookup itself never blocks here.
	if name == "external" && p.rdns != nil && host != "" {
		if a, err := netip.ParseAddr(host); err == nil {
			if ptr, ok := p.rdns.LookupName(a); ok && ptr != "" {
				return ptr
			}
		}
	}
	if p.keepExternal && (name == "external" || name == "unknown") && host != "" {
		return host
	}
	return name
}

// logTimestamp is when the traffic HAPPENED: the start of the record's capture
// window, falling back to the window end and finally to the capture time.
//
// It is deliberately not the record's logged time. logged is when the control
// plane captured the record, which trails the traffic by a variable amount — a
// live 3h capture measured a mean lag of 7.5s and a maximum of 852s. Using it as
// the event time misplaces every flow log on the time axis by an amount no
// downstream consumer can correct. logged is carried separately as the log
// record's ObservedTimestamp (see observedTimestamp), which is exactly the
// distinction OTEL models.
func logTimestamp(flow FlowLog) time.Time {
	if !flow.Start.IsZero() {
		return flow.Start
	}
	if !flow.End.IsZero() {
		return flow.End
	}
	return flow.Logged
}

// observedTimestamp is when the record was SEEN — the control plane's capture
// time. Zero when the record carries none, leaving the SDK to stamp it.
func observedTimestamp(flow FlowLog) time.Time { return flow.Logged }

// addFlowWindow adds the capture-window bounds to attrs, omitting either bound
// that the record does not carry. The window is not recoverable from the log
// timestamp alone once start and end differ.
func addFlowWindow(attrs telemetry.Attrs, flow FlowLog) {
	if !flow.Start.IsZero() {
		attrs[semconv.AttrFlowWindowStart] = flow.Start.UTC().Format(time.RFC3339Nano)
	}
	if !flow.End.IsZero() {
		attrs[semconv.AttrFlowWindowEnd] = flow.End.UTC().Format(time.RFC3339Nano)
	}
}

// serviceName maps a transport name and destination port string to its IANA
// service name (e.g. "tcp","443" -> "https"). It returns "" when the port is
// empty, unparseable, or has no registered service — callers omit the attribute
// entirely in that case.
func serviceName(transport, port string) string {
	if port == "" {
		return ""
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return ""
	}
	name, _ := portservice.LookupName(transport, uint16(p))
	return name
}

// splitHostPort splits an "addr:port" into host and port, tolerating a missing
// port (returns the whole input as host, empty port).
func splitHostPort(s string) (host, port string) {
	if s == "" {
		return "", ""
	}
	h, p, err := net.SplitHostPort(s)
	if err != nil {
		return s, ""
	}
	return h, p
}

// protoNames maps IANA protocol numbers the API returns to their lowercase
// transport names.
//
// 99 is the one entry that is not an IANA name. IANA reserves it for "any
// private encryption scheme", and Tailscale uses it for TSMP — its own ICMP-ish
// protocol, carried only between nodes inside the WireGuard tunnel, that
// communicates why something failed. Nothing else can put a 99 on the wire here,
// because tailscaled neither accepts these from the host stack nor sends them to
// it, so naming it tsmp is exact rather than a guess — and it is the more useful
// of the two names by a wide margin. See docs/flow-view.md: a TSMP flow is a
// REJECTION notice, so its source is the node that dropped something and its
// destination is the node whose traffic was dropped. That makes it the fastest
// way to read an ACL denial off the page, and unreadable as a bare "99".
var protoNames = map[int]string{
	1:   "icmp",
	2:   "igmp",
	6:   "tcp",
	17:  "udp",
	47:  "gre",
	50:  "esp",
	51:  "ah",
	58:  "ipv6-icmp",
	89:  "ospf",
	99:  "tsmp",
	132: "sctp",
}

// maxIANAProtocol is the highest valid IANA protocol number (a single octet on
// the wire). Anything outside [0, maxIANAProtocol] cannot be a real protocol
// number and is folded to "unknown" — see transportName.
const maxIANAProtocol = 255

// transportName maps an IANA protocol number to its transport name. Zero (the
// absent/null case) yields "unknown"; in-range (0-255) numbers without a known
// name yield their decimal string, bounding that fallback to at most 256
// distinct values. proto is an attacker-controlled JSON number on the
// streaming ingestion path (shared with poll via this same Processor), so any
// value outside the valid IANA range also folds to "unknown" instead of
// echoing the raw wire integer verbatim, which would otherwise let a
// misbehaving/attacking source mint unbounded transport-attribute cardinality
// (#77).
func transportName(proto int) string {
	if proto <= 0 || proto > maxIANAProtocol {
		return "unknown"
	}
	if name, ok := protoNames[proto]; ok {
		return name
	}
	return strconv.Itoa(proto)
}

// networkType classifies an address as ipv4 or ipv6. Unparseable addresses
// default to ipv4.
func networkType(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return semconv.NetworkTypeIPv4
	}
	if a.Is6() && !a.Is4In6() {
		return semconv.NetworkTypeIPv6
	}
	return semconv.NetworkTypeIPv4
}
