package flowlog

import (
	"fmt"
	"maps"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/enrich"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Exported metric names emitted by the processor.
const (
	MetricIO      = "tailscale.network.io"
	MetricPackets = "tailscale.network.packets"
	MetricFlows   = "tailscale.network.flows"
)

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
	// IncludePorts adds source.port/destination.port to METRIC attributes.
	IncludePorts bool
	// NodeDims adds tailscale.src.node/tailscale.dst.node to metric attributes.
	NodeDims bool
	// KeepExternalAddrs, when true, resolves an unrecognized address to its raw
	// host (IP) instead of collapsing it to "external"/"unknown". The zero value
	// (false) preserves the collapsing behavior.
	KeepExternalAddrs bool
	// Dedup, when non-nil, suppresses duplicate FlowLog window records that arrive
	// from both the poll flowlogs collector and the streaming receiver (which
	// share one Processor). A nil value (the default) disables cross-component
	// de-duplication.
	Dedup *dedup.Set
}

// Processor converts Tailscale flow logs into OTEL metrics and log records. It
// is stateless per call and safe to share between the polling collector and the
// streaming receiver; all mutable accumulation lives in the Emitter.
type Processor struct {
	cache        *enrich.DeviceCache
	logMode      string
	ports        bool
	nodes        bool
	keepExternal bool
	dedup        *dedup.Set
}

// NewProcessor returns a Processor using cache for device-name resolution. A nil
// cache is tolerated; node resolution then yields "unknown".
func NewProcessor(cache *enrich.DeviceCache, opts Options) *Processor {
	mode := opts.LogMode
	if mode == "" {
		mode = logPerConnection
	}
	return &Processor{
		cache:        cache,
		logMode:      mode,
		ports:        opts.IncludePorts,
		nodes:        opts.NodeDims,
		keepExternal: opts.KeepExternalAddrs,
		dedup:        opts.Dedup,
	}
}

// ProcessAll converts every flow log in resp.
func (p *Processor) ProcessAll(resp NetworkResponse, e telemetry.Emitter) {
	for i := range resp.Logs {
		p.Process(resp.Logs[i], e)
	}
}

// trafficSet pairs a ConnectionCounts slice with its traffic_type label.
type trafficSet struct {
	typ    string
	counts []ConnectionCounts
}

// Process converts a single FlowLog into metrics and (per LogMode) log records.
func (p *Processor) Process(flow FlowLog, e telemetry.Emitter) {
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
			p.processConn(flow, set.typ, cc, e)

			totalConns++
			totalTxBytes += cc.TxBytes
			totalRxBytes += cc.RxBytes
			totalTxPkts += cc.TxPkts
			totalRxPkts += cc.RxPkts
		}
	}

	// Emit the per-record summary when in per_record mode. With dedup on, suppress
	// it when every connection was a duplicate (nothing left to summarize); with
	// dedup off, preserve the original always-emit behavior.
	if p.logMode == logPerRecord && (totalConns > 0 || p.dedup == nil) {
		p.emitRecordLog(flow, totalConns, totalTxBytes, totalRxBytes, totalTxPkts, totalRxPkts, e)
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
// ConnectionCounts entry.
func (p *Processor) processConn(flow FlowLog, trafficType string, cc ConnectionCounts, e telemetry.Emitter) {
	transport := transportName(cc.Proto)
	srcAddr, srcPort := splitHostPort(cc.Src)
	dstAddr, dstPort := splitHostPort(cc.Dst)
	netType := networkType(srcAddr)
	srcNode := p.resolve(cc.Src, srcAddr)
	dstNode := p.resolve(cc.Dst, dstAddr)

	// Metric attributes shared by io + packets points.
	metricAttrs := telemetry.Attrs{
		semconv.NetworkTransport: transport,
		semconv.AttrTrafficType:  trafficType,
	}
	if p.nodes {
		metricAttrs[semconv.AttrSrcNode] = srcNode
		metricAttrs[semconv.AttrDstNode] = dstNode
	}
	if p.ports {
		metricAttrs[semconv.SourcePort] = srcPort
		metricAttrs[semconv.DestinationPort] = dstPort
	}

	// MetricIO (bytes): transmit + receive.
	e.Counter(MetricIO, semconv.UnitBytes, "Tailscale network bytes transferred",
		float64(cc.TxBytes), dirAttrs(metricAttrs, semconv.DirectionTransmit))
	e.Counter(MetricIO, semconv.UnitBytes, "Tailscale network bytes transferred",
		float64(cc.RxBytes), dirAttrs(metricAttrs, semconv.DirectionReceive))

	// MetricPackets: transmit + receive.
	e.Counter(MetricPackets, semconv.UnitPackets, "Tailscale network packets transferred",
		float64(cc.TxPkts), dirAttrs(metricAttrs, semconv.DirectionTransmit))
	e.Counter(MetricPackets, semconv.UnitPackets, "Tailscale network packets transferred",
		float64(cc.RxPkts), dirAttrs(metricAttrs, semconv.DirectionReceive))

	// MetricFlows: one flow observed.
	e.Counter(MetricFlows, semconv.UnitFlows, "Tailscale network flows observed", 1, telemetry.Attrs{
		semconv.NetworkTransport: transport,
		semconv.AttrTrafficType:  trafficType,
	})

	if p.logMode == logPerConnection {
		p.emitConnLog(flow, trafficType, cc, transport, netType, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode, e)
	}
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
func (p *Processor) emitConnLog(flow FlowLog, trafficType string, cc ConnectionCounts, transport, netType, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode string, e telemetry.Emitter) {
	body := fmt.Sprintf("%s %s %s -> %s tx=%dB rx=%dB", transport, trafficType, cc.Src, cc.Dst, cc.TxBytes, cc.RxBytes)
	attrs := telemetry.Attrs{
		semconv.SourceAddress:      srcAddr,
		semconv.SourcePort:         srcPort,
		semconv.DestinationAddress: dstAddr,
		semconv.DestinationPort:    dstPort,
		semconv.NetworkTransport:   transport,
		semconv.NetworkType:        netType,
		semconv.AttrTrafficType:    trafficType,
		semconv.AttrSrcNode:        srcNode,
		semconv.AttrDstNode:        dstNode,
		semconv.AttrNodeID:         flow.NodeID,
		"tailscale.tx.bytes":       cc.TxBytes,
		"tailscale.rx.bytes":       cc.RxBytes,
		"tailscale.tx.packets":     cc.TxPkts,
		"tailscale.rx.packets":     cc.RxPkts,
	}
	p.addNodeHostname(attrs, flow.NodeID)
	e.LogEvent(telemetry.Event{
		Name:      eventNameFlow,
		Body:      body,
		Severity:  telemetry.SeverityInfo,
		Timestamp: logTimestamp(flow),
		Attrs:     attrs,
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
	e.LogEvent(telemetry.Event{
		Name:      eventNameFlow,
		Body:      body,
		Severity:  telemetry.SeverityInfo,
		Timestamp: logTimestamp(flow),
		Attrs:     attrs,
	})
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

// resolve maps an "addr:port" to a device hostname via the cache. A nil cache
// yields "unknown". host is the already-split host part of addrPort. When
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
	if p.keepExternal && (name == "external" || name == "unknown") && host != "" {
		return host
	}
	return name
}

// logTimestamp prefers the record's logged time, falling back to its window end.
func logTimestamp(flow FlowLog) time.Time {
	if !flow.Logged.IsZero() {
		return flow.Logged
	}
	return flow.End
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

// transportName maps an IANA protocol number to its transport name. Zero (the
// absent/null case) yields "unknown"; numbers without a known name yield their
// decimal string.
func transportName(proto int) string {
	if proto == 0 {
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
