package flowlog

import (
	"fmt"
	"maps"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/dedup"
	"github.com/rknightion/tailscale2otel/v2/internal/enrich"
	"github.com/rknightion/tailscale2otel/v2/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v2/internal/portservice"
	"github.com/rknightion/tailscale2otel/v2/internal/rdns"
	"github.com/rknightion/tailscale2otel/v2/internal/semconv"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
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
	// IncludeDestinationService adds tailscale.dst.service (the IANA service name
	// for the destination port+transport) to METRIC attributes. It is a bounded,
	// low-cardinality stand-in for the destination port. Flow LOGS always carry it
	// when the port maps to a known service, regardless of this toggle.
	IncludeDestinationService bool
	// NodeDims adds tailscale.src.node/tailscale.dst.node to metric attributes.
	NodeDims bool
	// IdentityDims adds the per-flow endpoint identity (user, tags, OS) to METRIC
	// attributes. Flow LOGS always carry it when the record supplies it,
	// regardless of this toggle. Off by default: the values are low-cardinality
	// but PII-adjacent, so they stay off the default metric surface exactly as
	// ports and destination service do.
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
	// StorePII gates which identifiers reach Store. The store sits BEHIND the
	// emitter's redactor, so without this the admin page would show identifiers
	// the operator has switched off for telemetry. A nil map disables nothing.
	StorePII pii.Categories
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

// Processor converts Tailscale flow logs into OTEL metrics and log records. It
// is stateless per call and safe to share between the polling collector and the
// streaming receiver; all mutable accumulation lives in the Emitter.
type Processor struct {
	cache        *enrich.DeviceCache
	logMode      string
	mode         string
	srcPort      bool
	dstPort      bool
	dstService   bool
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
	// when the view is disabled. storeRedact applies the PII policy to what
	// reaches it, and is nil when nothing is disabled (the fast path).
	store       Recorder
	storeRedact *pii.Redactor
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
		dstService:   opts.IncludeDestinationService,
		nodes:        opts.NodeDims,
		keepExternal: opts.KeepExternalAddrs,
		identity:     opts.IdentityDims,
		rdns:         opts.RDNS,
		dedup:        opts.Dedup,
		maxLogs:      opts.MaxLogRecordsPerWindow,
		exitNode:     opts.ExitNodeAttribution,
		store:        opts.Store,
	}
	if opts.Store != nil {
		p.storeRedact = pii.New(opts.StorePII)
	}
	if flowMode == flowModeRollup || flowMode == flowModeBoth {
		p.rollup = newRollupAccumulator(opts.RollupTopN, opts.NodeDims)
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
	dstNode := p.resolveEndpoint(cc.Dst, dstAddr)
	dstService := serviceName(transport, dstPort)

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
		if p.dstService && dstService != "" {
			metricAttrs[semconv.AttrDstService] = dstService
		}
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
		p.rollup.record(transport, trafficType, srcNode, dstNode, dstService,
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
		p.store.Record(p.observation(flow, trafficType, cc, transport, srcAddr, dstAddr, srcNode, dstNode, dstPort, dstService))
	}

	if p.logMode == logPerConnection && budget.allow() {
		p.emitConnLog(flow, trafficType, cc, transport, netType, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode, dstService, e)
	}
}

// observation builds the flow-store view of one connection.
//
// Everything in it that could identify a person or a device passes through the
// SAME redactor the emitter applies to telemetry, because the store sits behind
// the emitter and would otherwise let the admin page display an identifier the
// operator switched off in pii_filter. A redacted field becomes empty, which the
// store already interprets as "the record did not carry this" — so it degrades
// into the absent-endpoint handling that already exists rather than needing a
// second notion of missing.
func (p *Processor) observation(flow FlowLog, trafficType string, cc ConnectionCounts,
	transport, srcAddr, dstAddr, srcNode, dstNode, dstPort, dstService string,
) flowstore.Observation {
	// The endpoints are classified by their bare address (that is what the
	// redactor knows how to grade), but retained with their ports.
	ident := telemetry.Attrs{}
	if srcNode != "" {
		ident[semconv.AttrSrcNode] = srcNode
	}
	if dstNode != "" {
		ident[semconv.AttrDstNode] = dstNode
	}
	if cc.Src != "" {
		ident[semconv.SourceAddress] = srcAddr
	}
	if cc.Dst != "" {
		ident[semconv.DestinationAddress] = dstAddr
	}
	addIdentityAttrs(ident, flow.refByAddr(srcAddr), flow.refByAddr(dstAddr))
	kept := telemetry.Attrs(p.storeRedact.Merge(ident))

	o := flowstore.Observation{
		Time:        logTimestamp(flow),
		TrafficType: trafficType,
		Transport:   transport,
		DstPort:     dstPort,
		DstService:  dstService,
		SrcNode:     attrString(kept, semconv.AttrSrcNode),
		DstNode:     attrString(kept, semconv.AttrDstNode),
		SrcUser:     attrString(kept, semconv.AttrSrcUser),
		DstUser:     attrString(kept, semconv.AttrDstUser),
		SrcTags:     attrString(kept, semconv.AttrSrcTags),
		DstTags:     attrString(kept, semconv.AttrDstTags),
		SrcOS:       attrString(kept, semconv.AttrSrcOS),
		DstOS:       attrString(kept, semconv.AttrDstOS),
		Counts: flowstore.Counts{
			TxBytes: cc.TxBytes,
			RxBytes: cc.RxBytes,
			TxPkts:  cc.TxPkts,
			RxPkts:  cc.RxPkts,
			Flows:   1,
		},
	}
	// Keep the full "addr:port" only when the address itself survived redaction.
	if attrString(kept, semconv.SourceAddress) != "" {
		o.SrcAddr = cc.Src
	}
	if attrString(kept, semconv.DestinationAddress) != "" {
		o.DstAddr = cc.Dst
	}
	return o
}

// attrString reads a string attribute, yielding "" when the key was redacted
// away or never present.
func attrString(attrs telemetry.Attrs, key string) string {
	s, _ := attrs[key].(string)
	return s
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
		if len(ref.Tags) > 0 {
			attrs[tagsKey] = strings.Join(ref.Tags, ",")
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
		attrs[semconv.AttrDstNode] = dstNode
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
