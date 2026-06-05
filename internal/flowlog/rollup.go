package flowlog

import (
	"sort"
	"sync"

	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// Flow-metric emission modes (Options.FlowMetricsMode). "all" is the safe library
// default (per-connection raw io/packets, the original behavior); the config
// layer supplies the product default "rollup".
const (
	flowModeAll    = "all"
	flowModeRollup = "rollup"
	flowModeBoth   = "both"
)

// defaultRollupTopN bounds the number of busiest src/dst node pairs kept per
// flush when RollupTopN is unset (<= 0). Comfortably under the per-instrument
// cardinality cap (cardinality.metric_limit, default 10000).
const defaultRollupTopN = 500

// Insert-time safety caps on the live accumulator maps. Unlike topN (which is
// applied only at Flush), these bound peak memory *between* flushes so a flood of
// unique flow keys cannot grow the accumulator without limit. This is the
// attacker-amplified case: raw src/dst addresses become live map keys when
// cardinality.flow.collapse_external is off or reverse_dns is on, and reach record/observeUnique
// via the (possibly unauthenticated) stream receiver. Overflow folds into the
// __other__ remainder (counters) or saturates the set (unique gauges); see
// record and addToSet. The caps sit far above any legitimate per-interval volume.
const (
	maxRollupKeys     = 8000    // live rollupKeys before new ones fold to __other__
	maxUniqueSrcNodes = 2000    // distinct source nodes tracked for unique gauges
	maxUniquePerSrc   = 1 << 16 // distinct peers/ports per source (L4 port space)
)

// rollupKey is the bounded dimension set of a *.rollup series — the low-card
// stand-ins for a flow, deliberately WITHOUT L4 ports. srcNode/dstNode are empty
// when flow node dimensions are off; dstService is empty when the destination
// port maps to no known IANA service.
type rollupKey struct {
	transport   string
	trafficType string
	srcNode     string
	dstNode     string
	dstService  string
}

// rollupEntry accumulates a flow's bytes and packets (both directions) for one
// rollupKey within a flush interval.
type rollupEntry struct {
	txBytes, rxBytes float64
	txPkts, rxPkts   float64
}

func (e *rollupEntry) add(o *rollupEntry) {
	e.txBytes += o.txBytes
	e.rxBytes += o.rxBytes
	e.txPkts += o.txPkts
	e.rxPkts += o.rxPkts
}

// rollupAccumulator accumulates per-connection contributions and, on Flush, emits
// the busiest top-N rollupKeys plus a folded __other__ remainder (one per
// transport/traffic_type/dst_service group) and the per-source-node unique
// gauges, then resets. All methods are safe for concurrent use and are no-ops on
// a nil receiver.
type rollupAccumulator struct {
	topN  int
	nodes bool

	mu       sync.Mutex
	entries  map[rollupKey]*rollupEntry
	dstPeers map[string]map[string]struct{} // srcNode -> distinct dstNode set
	dstPorts map[string]map[string]struct{} // srcNode -> distinct dstPort set
}

// newRollupAccumulator returns an accumulator keeping the busiest topN node pairs
// per flush. nodes mirrors cardinality.flow.node_dims: when false the rollup carries no node
// dimensions and the per-source-node unique gauges are suppressed.
func newRollupAccumulator(topN int, nodes bool) *rollupAccumulator {
	if topN <= 0 {
		topN = defaultRollupTopN
	}
	return &rollupAccumulator{
		topN:     topN,
		nodes:    nodes,
		entries:  map[rollupKey]*rollupEntry{},
		dstPeers: map[string]map[string]struct{}{},
		dstPorts: map[string]map[string]struct{}{},
	}
}

// record adds one connection's tx/rx bytes and packets under the bounded rollup
// key. srcNode/dstNode are dropped from the key when node dimensions are off.
func (a *rollupAccumulator) record(transport, trafficType, srcNode, dstNode, dstService string, txBytes, rxBytes, txPkts, rxPkts float64) {
	k := rollupKey{transport: transport, trafficType: trafficType, dstService: dstService}
	if a.nodes {
		k.srcNode = srcNode
		k.dstNode = dstNode
	}
	a.mu.Lock()
	e := a.entries[k]
	if e == nil && len(a.entries) >= maxRollupKeys {
		// Live map full: fold this new key into the per-group __other__ remainder
		// instead of growing without bound between flushes. Mirrors flushCounters'
		// node collapse, so group totals stay exact; the __other__ key space is
		// itself bounded (transport/traffic_type/dst_service come from fixed tables).
		k = rollupKey{transport: transport, trafficType: trafficType, dstService: dstService}
		if a.nodes {
			k.srcNode = semconv.RollupOther
			k.dstNode = semconv.RollupOther
		}
		e = a.entries[k]
	}
	if e == nil {
		e = &rollupEntry{}
		a.entries[k] = e
	}
	e.txBytes += txBytes
	e.rxBytes += rxBytes
	e.txPkts += txPkts
	e.rxPkts += rxPkts
	a.mu.Unlock()
}

// observeUnique records one distinct destination peer and port for srcNode, for
// the per-source-node unique gauges. It is a no-op when node dimensions are off
// (the gauges are keyed by source node, so emitting them would reintroduce the
// node cardinality the operator turned off) or for empty values.
func (a *rollupAccumulator) observeUnique(srcNode, dstNode, dstPort string) {
	if !a.nodes || srcNode == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if dstNode != "" {
		addToSet(a.dstPeers, srcNode, dstNode)
	}
	if dstPort != "" {
		addToSet(a.dstPorts, srcNode, dstPort)
	}
}

// addToSet inserts val into the string set at m[key], creating it on first use.
// It enforces insert-time caps in both dimensions — the number of source-node
// keys (maxUniqueSrcNodes) and each set's size (maxUniquePerSrc) — so a flood of
// distinct sources/ports saturates the unique gauges rather than growing the
// accumulator without bound between flushes.
func addToSet(m map[string]map[string]struct{}, key, val string) {
	s := m[key]
	if s == nil {
		if len(m) >= maxUniqueSrcNodes {
			return
		}
		s = map[string]struct{}{}
		m[key] = s
	}
	if len(s) >= maxUniquePerSrc {
		return
	}
	s[val] = struct{}{}
}

// Flush emits the bounded rollup counters and unique gauges for the current
// interval, then resets the accumulator. It is a no-op on a nil accumulator.
func (a *rollupAccumulator) Flush(e telemetry.Emitter) {
	if a == nil {
		return
	}
	a.mu.Lock()
	entries := a.entries
	dstPeers := a.dstPeers
	dstPorts := a.dstPorts
	a.entries = map[rollupKey]*rollupEntry{}
	a.dstPeers = map[string]map[string]struct{}{}
	a.dstPorts = map[string]map[string]struct{}{}
	a.mu.Unlock()

	a.flushCounters(e, entries)
	a.flushUnique(e, dstPeers, dstPorts)
}

// keyedEntry pairs a rollupKey with its accumulated entry for sorting.
type keyedEntry struct {
	key   rollupKey
	entry *rollupEntry
}

// flushCounters emits the busiest topN keys directly and folds the remainder into
// one __other__ series per (transport, traffic_type, dst_service) group so totals
// stay exact within each group.
func (a *rollupAccumulator) flushCounters(e telemetry.Emitter, entries map[rollupKey]*rollupEntry) {
	if len(entries) == 0 {
		return
	}
	ks := make([]keyedEntry, 0, len(entries))
	for k, en := range entries {
		ks = append(ks, keyedEntry{k, en})
	}
	// Busiest first by total bytes; lexicographic key tie-break keeps membership
	// stable across flushes when totals are equal.
	sort.Slice(ks, func(i, j int) bool {
		ti := ks[i].entry.txBytes + ks[i].entry.rxBytes
		tj := ks[j].entry.txBytes + ks[j].entry.rxBytes
		if ti != tj {
			return ti > tj
		}
		return lessRollupKey(ks[i].key, ks[j].key)
	})

	cut := min(a.topN, len(ks))
	for _, ke := range ks[:cut] {
		a.emitEntry(e, ke.key, ke.entry)
	}
	if cut >= len(ks) {
		return
	}

	// Fold the remainder, collapsing only the node dimensions.
	type otherGroup struct{ transport, trafficType, dstService string }
	other := map[otherGroup]*rollupEntry{}
	for _, ke := range ks[cut:] {
		g := otherGroup{ke.key.transport, ke.key.trafficType, ke.key.dstService}
		o := other[g]
		if o == nil {
			o = &rollupEntry{}
			other[g] = o
		}
		o.add(ke.entry)
	}
	for g, o := range other {
		k := rollupKey{transport: g.transport, trafficType: g.trafficType, dstService: g.dstService}
		if a.nodes {
			k.srcNode = semconv.RollupOther
			k.dstNode = semconv.RollupOther
		}
		a.emitEntry(e, k, o)
	}
}

// emitEntry emits the io.rollup and packets.rollup transmit/receive points for one
// key, skipping zero-value directions so one-directional flows produce no phantom
// zero series.
func (a *rollupAccumulator) emitEntry(e telemetry.Emitter, k rollupKey, en *rollupEntry) {
	base := k.attrs()
	if en.txBytes > 0 {
		e.Counter(docIORollup.Name, docIORollup.Unit, docIORollup.Description, en.txBytes, dirAttrs(base, semconv.DirectionTransmit))
	}
	if en.rxBytes > 0 {
		e.Counter(docIORollup.Name, docIORollup.Unit, docIORollup.Description, en.rxBytes, dirAttrs(base, semconv.DirectionReceive))
	}
	if en.txPkts > 0 {
		e.Counter(docPacketsRollup.Name, docPacketsRollup.Unit, docPacketsRollup.Description, en.txPkts, dirAttrs(base, semconv.DirectionTransmit))
	}
	if en.rxPkts > 0 {
		e.Counter(docPacketsRollup.Name, docPacketsRollup.Unit, docPacketsRollup.Description, en.rxPkts, dirAttrs(base, semconv.DirectionReceive))
	}
}

// flushUnique emits one gauge per source node for the distinct destination-peer
// and destination-port counts observed this interval.
func (a *rollupAccumulator) flushUnique(e telemetry.Emitter, dstPeers, dstPorts map[string]map[string]struct{}) {
	for src, set := range dstPeers {
		e.Gauge(docUniqueDstPeers.Name, docUniqueDstPeers.Unit, docUniqueDstPeers.Description,
			float64(len(set)), telemetry.Attrs{semconv.AttrSrcNode: src})
	}
	for src, set := range dstPorts {
		e.Gauge(docUniqueDstPorts.Name, docUniqueDstPorts.Unit, docUniqueDstPorts.Description,
			float64(len(set)), telemetry.Attrs{semconv.AttrSrcNode: src})
	}
}

// attrs builds the base metric attribute set for a key, omitting empty dimensions.
func (k rollupKey) attrs() telemetry.Attrs {
	a := telemetry.Attrs{
		semconv.NetworkTransport: k.transport,
		semconv.AttrTrafficType:  k.trafficType,
	}
	if k.srcNode != "" {
		a[semconv.AttrSrcNode] = k.srcNode
	}
	if k.dstNode != "" {
		a[semconv.AttrDstNode] = k.dstNode
	}
	if k.dstService != "" {
		a[semconv.AttrDstService] = k.dstService
	}
	return a
}

// lessRollupKey is a total order over rollup keys for deterministic tie-breaking.
func lessRollupKey(a, b rollupKey) bool {
	switch {
	case a.transport != b.transport:
		return a.transport < b.transport
	case a.trafficType != b.trafficType:
		return a.trafficType < b.trafficType
	case a.srcNode != b.srcNode:
		return a.srcNode < b.srcNode
	case a.dstNode != b.dstNode:
		return a.dstNode < b.dstNode
	default:
		return a.dstService < b.dstService
	}
}
