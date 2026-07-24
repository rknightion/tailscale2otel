package flowlog

import (
	"sort"
	"sync"

	"github.com/rknightion/tailscale2otel/v3/internal/semconv"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetry"
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
// port maps to no known IANA service; path/derpRegion are empty for every traffic
// type except physical (and derpRegion only for a relayed physical path).
//
// The identity fields are empty unless identity dimensions are on. They cost
// almost nothing on top of the node dimensions because identity is a property of
// the NODE, not of the flow: user/tags/os are functionally dependent on
// srcNode/dstNode, so with node dimensions on this widens the label set without
// multiplying the series count. With node dimensions OFF it would be the
// opposite — identity would become the only distinguishing dimension and would
// reintroduce most of the cardinality that turning nodes off removes — so
// newRollupAccumulator refuses to carry it in that case.
type rollupKey struct {
	transport   string
	trafficType string
	srcNode     string
	dstNode     string
	dstService  string
	path        string
	derpRegion  string
	identity    identityKey
}

// identityKey is the node-derived identity half of a rollupKey, split out so the
// __other__ fold can zero it in one assignment.
type identityKey struct {
	srcUser, srcTags, srcOS string
	dstUser, dstTags, dstOS string
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
	topN     int
	nodes    bool
	identity bool

	mu       sync.Mutex
	entries  map[rollupKey]*rollupEntry
	dstPeers map[string]map[string]struct{} // srcNode -> distinct dstNode set
	dstPorts map[string]map[string]struct{} // srcNode -> distinct dstPort set
}

// newRollupAccumulator returns an accumulator keeping the busiest topN node pairs
// per flush. nodes mirrors cardinality.flow.node_dims: when false the rollup carries no node
// dimensions and the per-source-node unique gauges are suppressed.
//
// identity mirrors cardinality.flow.identity_dims, but is forced off when nodes
// is off. Identity is node-derived, so without the node dimensions it stops being
// a free widening of an existing series and becomes the sole dimension splitting
// the rollup — exactly the cardinality an operator turning node_dims off is
// trying to shed. Silently honoring it there would make node_dims a lever that
// does not lower cardinality.
func newRollupAccumulator(topN int, nodes, identity bool) *rollupAccumulator {
	if topN <= 0 {
		topN = defaultRollupTopN
	}
	return &rollupAccumulator{
		topN:     topN,
		nodes:    nodes,
		identity: identity && nodes,
		entries:  map[rollupKey]*rollupEntry{},
		dstPeers: map[string]map[string]struct{}{},
		dstPorts: map[string]map[string]struct{}{},
	}
}

// wantsIdentity reports whether the accumulator will key on identity, so the
// caller can skip resolving node blocks it would then discard. Safe on a nil
// receiver, like every other method here.
func (a *rollupAccumulator) wantsIdentity() bool {
	return a != nil && a.identity
}

// rollupDims is the full dimension set one connection offers the accumulator,
// before the node/identity gates decide what actually enters the key. It is a
// struct rather than a parameter list because the gates need to read fields
// selectively in three places (key, otherKey, observeUnique).
type rollupDims struct {
	transport   string
	trafficType string
	srcNode     string
	dstNode     string
	dstService  string
	path        string
	derpRegion  string
	identity    identityKey
}

// key builds the accumulator key for one connection, applying the node and
// identity gates.
func (a *rollupAccumulator) key(d rollupDims) rollupKey {
	k := rollupKey{
		transport:   d.transport,
		trafficType: d.trafficType,
		dstService:  d.dstService,
		path:        d.path,
		derpRegion:  d.derpRegion,
	}
	if a.nodes {
		k.srcNode = d.srcNode
		k.dstNode = d.dstNode
	}
	if a.identity {
		k.identity = d.identity
	}
	return k
}

// otherKey builds the folded remainder key for one connection: the node
// dimensions collapse to __other__ and identity drops out entirely.
//
// Identity is dropped rather than set to __other__ because the remainder has no
// single identity to report — the fold is by definition many nodes, and many
// users. An __other__ user would read as a real principal. Dropping it is also
// what keeps the fold's key space bounded, which is the invariant the insert-time
// cap exists to protect.
func (a *rollupAccumulator) otherKey(d rollupDims) rollupKey {
	k := rollupKey{
		transport:   d.transport,
		trafficType: d.trafficType,
		dstService:  d.dstService,
		path:        d.path,
		derpRegion:  d.derpRegion,
	}
	if a.nodes {
		k.srcNode = semconv.RollupOther
		k.dstNode = semconv.RollupOther
	}
	return k
}

// record adds one connection's tx/rx bytes and packets under the bounded rollup
// key. srcNode/dstNode are dropped from the key when node dimensions are off, and
// identity when identity dimensions are off.
func (a *rollupAccumulator) record(d rollupDims, txBytes, rxBytes, txPkts, rxPkts float64) {
	k := a.key(d)
	a.mu.Lock()
	e := a.entries[k]
	if e == nil && len(a.entries) >= maxRollupKeys {
		// Live map full: fold this new key into the per-group __other__ remainder
		// instead of growing without bound between flushes. Mirrors flushCounters'
		// node collapse, so group totals stay exact; the __other__ key space is
		// itself bounded — transport/traffic_type/dst_service/path come from fixed
		// tables, and identity is dropped with the node dimensions it derives from.
		k = a.otherKey(d)
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

	// Fold the remainder, collapsing the node dimensions and the identity that
	// derives from them. Everything left in the group key comes from a fixed
	// table, so the folded key space stays bounded however many distinct nodes,
	// users or tags the tailnet has.
	type otherGroup struct{ transport, trafficType, dstService, path, derpRegion string }
	other := map[otherGroup]*rollupEntry{}
	for _, ke := range ks[cut:] {
		g := otherGroup{ke.key.transport, ke.key.trafficType, ke.key.dstService, ke.key.path, ke.key.derpRegion}
		o := other[g]
		if o == nil {
			o = &rollupEntry{}
			other[g] = o
		}
		o.add(ke.entry)
	}
	for g, o := range other {
		k := rollupKey{
			transport:   g.transport,
			trafficType: g.trafficType,
			dstService:  g.dstService,
			path:        g.path,
			derpRegion:  g.derpRegion,
		}
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
	addPathAttrs(a, k.path, k.derpRegion)
	setIfNotEmpty(a, semconv.AttrSrcUser, k.identity.srcUser)
	setIfNotEmpty(a, semconv.AttrSrcTags, k.identity.srcTags)
	setIfNotEmpty(a, semconv.AttrSrcOS, k.identity.srcOS)
	setIfNotEmpty(a, semconv.AttrDstUser, k.identity.dstUser)
	setIfNotEmpty(a, semconv.AttrDstTags, k.identity.dstTags)
	setIfNotEmpty(a, semconv.AttrDstOS, k.identity.dstOS)
	return a
}

// setIfNotEmpty assigns v under key only when v is non-empty, so an identity
// field the record did not carry stays absent rather than becoming an empty
// label. Absent and empty read differently on a metric: absent says the record
// carried nothing, empty says it carried a blank value.
func setIfNotEmpty(a telemetry.Attrs, key, v string) {
	if v != "" {
		a[key] = v
	}
}

// lessRollupKey is a total order over rollup keys for deterministic tie-breaking.
//
// It must cover EVERY field of rollupKey. A field left out makes two distinct
// keys compare equal, and sort.Slice is not stable, so top-N membership would
// shift between flushes purely on map-iteration order — a series would blink in
// and out while its traffic was unchanged.
func lessRollupKey(a, b rollupKey) bool {
	for _, p := range [...][2]string{
		{a.transport, b.transport},
		{a.trafficType, b.trafficType},
		{a.srcNode, b.srcNode},
		{a.dstNode, b.dstNode},
		{a.dstService, b.dstService},
		{a.path, b.path},
		{a.derpRegion, b.derpRegion},
		{a.identity.srcUser, b.identity.srcUser},
		{a.identity.srcTags, b.identity.srcTags},
		{a.identity.srcOS, b.identity.srcOS},
		{a.identity.dstUser, b.identity.dstUser},
		{a.identity.dstTags, b.identity.dstTags},
		{a.identity.dstOS, b.identity.dstOS},
	} {
		if p[0] != p[1] {
			return p[0] < p[1]
		}
	}
	return false
}
