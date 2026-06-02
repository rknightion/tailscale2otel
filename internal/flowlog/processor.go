package flowlog

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

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
			p.processConn(flow, set.typ, cc, e)

			totalConns++
			totalTxBytes += cc.TxBytes
			totalRxBytes += cc.RxBytes
			totalTxPkts += cc.TxPkts
			totalRxPkts += cc.RxPkts
		}
	}

	if p.logMode == logPerRecord {
		p.emitRecordLog(flow, totalConns, totalTxBytes, totalRxBytes, totalTxPkts, totalRxPkts, e)
	}
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
	for k, v := range base {
		out[k] = v
	}
	out[semconv.NetworkIODirection] = direction
	return out
}

// emitConnLog emits one per-connection flow log event.
func (p *Processor) emitConnLog(flow FlowLog, trafficType string, cc ConnectionCounts, transport, netType, srcAddr, srcPort, dstAddr, dstPort, srcNode, dstNode string, e telemetry.Emitter) {
	body := fmt.Sprintf("%s %s %s -> %s tx=%dB rx=%dB", transport, trafficType, cc.Src, cc.Dst, cc.TxBytes, cc.RxBytes)
	e.LogEvent(telemetry.Event{
		Name:      eventNameFlow,
		Body:      body,
		Severity:  telemetry.SeverityInfo,
		Timestamp: logTimestamp(flow),
		Attrs: telemetry.Attrs{
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
		},
	})
}

// emitRecordLog emits one summary log event for an entire FlowLog.
func (p *Processor) emitRecordLog(flow FlowLog, conns int, txBytes, rxBytes, txPkts, rxPkts int64, e telemetry.Emitter) {
	body := fmt.Sprintf("node %s: %d connections tx=%dB rx=%dB", flow.NodeID, conns, txBytes, rxBytes)
	e.LogEvent(telemetry.Event{
		Name:      eventNameFlow,
		Body:      body,
		Severity:  telemetry.SeverityInfo,
		Timestamp: logTimestamp(flow),
		Attrs: telemetry.Attrs{
			semconv.AttrNodeID:      flow.NodeID,
			"tailscale.connections": int64(conns),
			"tailscale.tx.bytes":    txBytes,
			"tailscale.rx.bytes":    rxBytes,
			"tailscale.tx.packets":  txPkts,
			"tailscale.rx.packets":  rxPkts,
		},
	})
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
