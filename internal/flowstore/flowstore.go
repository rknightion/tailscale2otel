// Package flowstore retains recent flow activity in aggregate so the admin
// flow view can render a tailnet's traffic without a metrics backend in the
// loop.
//
// It is deliberately NOT a second telemetry pipeline. The OTLP path stays the
// system of record; this holds a bounded, recent, pre-aggregated view for
// interactive use. Three properties follow from that and must be preserved:
//
//   - It never blocks or fails the emit path. Record takes a short lock, does a
//     handful of map writes, and returns. There is no I/O, no backpressure, and
//     no error to propagate — an overflowing store drops into an "__other__"
//     bucket and counts the drop rather than slowing the caller.
//   - It is bounded in every dimension. The stream receiver is a potentially
//     unauthenticated ingress, so a flood of unique flow keys must not grow the
//     store without limit. Every map has an insert-time cap, mirroring the
//     approach already taken in internal/flowlog/rollup.go.
//   - It aggregates on the way in, at one-minute resolution. Individual
//     connections are not retained; a query re-buckets the minutes it needs, so
//     a wide window costs no extra storage and needs no coarser tier.
package flowstore

import (
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/boundedtext"
)

// Resolution is the fixed bucket width. Everything is aggregated to the minute
// on the way in; queries re-bucket upward from there. One minute matches the
// finest interval a flow record can meaningfully describe (observed capture
// windows average ~5s but are reported far less regularly) and keeps a day of
// history to 1440 buckets.
const Resolution = time.Minute

// Insert-time caps, applied per bucket. These bound peak memory between
// evictions; anything beyond a cap folds into Other so totals stay exact. They
// sit far above any legitimate per-minute volume on a real tailnet.
const (
	MaxPairsPerBucket = 4000
	MaxNodesPerBucket = 2000
	MaxPortsPerBucket = 2000
	MaxLabelsPerKind  = 500
	// MaxMatrixCellsPerBucket bounds each identity matrix. A matrix is a CROSS
	// PRODUCT, so it is the dimension most able to blow up — and tags come from
	// the control plane, not from us. The cap sits far above any real tailnet's
	// tag/user/OS pairing count.
	MaxMatrixCellsPerBucket = 2000
	// MaxUnexplainedPerBucket bounds the unexplained-relationship aggregate. It
	// is the one policy dimension an adversary can grow: the stream ingress can
	// mint unique addresses, and an address no rule names is by construction a
	// relationship nothing explains.
	MaxUnexplainedPerBucket = 500
	// MaxRulesPerBucket bounds the per-rule exercise counts. Unlike every other
	// cap this one cannot be reached by traffic: the keys are indexes into the
	// tailnet's OWN compiled policy, so the dimension is bounded by how many
	// rules an operator wrote. It exists so the package's "bounded in every
	// dimension" promise holds without an argument attached.
	MaxRulesPerBucket = 1000
	// MaxPeerPathsPerBucket bounds the per-peer underlay-path split. A real
	// tailnet's peer count is its device count, but the peer here is named from a
	// record the stream ingress can mint, so it gets the same insert-time cap as
	// every other dimension.
	MaxPeerPathsPerBucket = 2000
)

// Unidentified names an endpoint of an unexplained relationship that carries no
// identity at all: the record supplied no tags, no user, no resolved name and no
// address. Unlike the breakdowns, where an absent value simply occupies no label, a
// relationship needs both ends named or the counts would not add up to the
// verdict totals beside them.
const Unidentified = "unidentified"

// The names the flow processor collapses an unresolvable endpoint to. They are
// sentinels, not device names, so a relationship prefers the raw address over
// either — a live tailnet's entire unexplained residue was traffic to two LAN
// addresses behind a subnet router, and "external → external" would have hidden
// exactly the thing worth acting on.
const (
	external = "external"
	unknown  = "unknown"
)

// MaxRecent is the size of the raw-connection ring. It is bounded by COUNT, not
// by time: the ring is the only unaggregated thing the store holds, so it is
// what a flood on the stream ingress would grow, and a time bound would not
// bound it. At roughly 200 bytes per entry this is well under a megabyte.
const MaxRecent = 2000

const (
	MaxRetainedFieldBytes       = 4096
	MaxRetainedObservationBytes = 32 << 10
)

// Profile names a bounded capacity/memory policy for a Memory store: how many
// entries each per-bucket dimension may hold, and how large the raw
// connection ring is. There are exactly three, each a FIXED, hard-coded
// preset — there is no arbitrary/continuous knob a caller can set — so an
// operator trading memory for fidelity can only ever land on one of three
// known-safe points, never something unbounded (#329).
const (
	// ProfileCompact halves every DefaultCaps dimension: roughly half the
	// worst-case per-bucket memory of the default profile, at the cost of
	// folding more into Other sooner on a busy tailnet.
	ProfileCompact = "compact"
	// ProfileDefault reproduces today's hardcoded limits (the Max*PerBucket
	// and MaxRecent constants above) exactly. It is what a Memory built with
	// no capacity option gets, so pre-#329 behavior is unchanged unless an
	// operator opts into a different profile.
	ProfileDefault = "default"
	// ProfileExpanded doubles every DefaultCaps dimension. Still a fixed,
	// hard-coded ceiling — "far above any legitimate volume", per the
	// per-dimension doc comments above, multiplied by two — never an
	// operator-supplied raw number.
	ProfileExpanded = "expanded"
)

// Caps bounds every per-bucket dimension and the raw-connection ring for one
// Memory store. It has no exported constructor other than CapsForProfile:
// the zero value caps every dimension at zero (nothing would ever be
// retained), so the only way to obtain a usable Caps from outside this
// package is by naming one of the three fixed profiles above.
type Caps struct {
	MaxPairsPerBucket       int
	MaxNodesPerBucket       int
	MaxPortsPerBucket       int
	MaxLabelsPerKind        int
	MaxMatrixCellsPerBucket int
	MaxUnexplainedPerBucket int
	MaxRulesPerBucket       int
	MaxPeerPathsPerBucket   int
	// MaxRecent is the raw-connection ring size for this profile — the
	// per-store analog of the package-level MaxRecent constant.
	MaxRecent int
}

// DefaultCaps mirrors today's hardcoded per-bucket/ring limits exactly, so a
// Memory built with no capacity-profile option behaves byte-identically to
// before #329.
var DefaultCaps = Caps{
	MaxPairsPerBucket:       MaxPairsPerBucket,
	MaxNodesPerBucket:       MaxNodesPerBucket,
	MaxPortsPerBucket:       MaxPortsPerBucket,
	MaxLabelsPerKind:        MaxLabelsPerKind,
	MaxMatrixCellsPerBucket: MaxMatrixCellsPerBucket,
	MaxUnexplainedPerBucket: MaxUnexplainedPerBucket,
	MaxRulesPerBucket:       MaxRulesPerBucket,
	MaxPeerPathsPerBucket:   MaxPeerPathsPerBucket,
	MaxRecent:               MaxRecent,
}

// compactCaps halves every DefaultCaps dimension.
var compactCaps = Caps{
	MaxPairsPerBucket:       MaxPairsPerBucket / 2,
	MaxNodesPerBucket:       MaxNodesPerBucket / 2,
	MaxPortsPerBucket:       MaxPortsPerBucket / 2,
	MaxLabelsPerKind:        MaxLabelsPerKind / 2,
	MaxMatrixCellsPerBucket: MaxMatrixCellsPerBucket / 2,
	MaxUnexplainedPerBucket: MaxUnexplainedPerBucket / 2,
	MaxRulesPerBucket:       MaxRulesPerBucket / 2,
	MaxPeerPathsPerBucket:   MaxPeerPathsPerBucket / 2,
	MaxRecent:               MaxRecent / 2,
}

// expandedCaps doubles every DefaultCaps dimension. Still a fixed, hard-coded
// ceiling, not an operator-suppliable number — see Profile's doc comment.
var expandedCaps = Caps{
	MaxPairsPerBucket:       MaxPairsPerBucket * 2,
	MaxNodesPerBucket:       MaxNodesPerBucket * 2,
	MaxPortsPerBucket:       MaxPortsPerBucket * 2,
	MaxLabelsPerKind:        MaxLabelsPerKind * 2,
	MaxMatrixCellsPerBucket: MaxMatrixCellsPerBucket * 2,
	MaxUnexplainedPerBucket: MaxUnexplainedPerBucket * 2,
	MaxRulesPerBucket:       MaxRulesPerBucket * 2,
	MaxPeerPathsPerBucket:   MaxPeerPathsPerBucket * 2,
	MaxRecent:               MaxRecent * 2,
}

// CapsForProfile returns the Caps for a named profile, and false for anything
// else — including the empty string. It is the ONLY way to obtain a Caps
// other than DefaultCaps itself: there is no exported way to set an
// arbitrary/unbounded cap, matching #329's "do not expose unbounded raw
// limits" requirement. config.Validate() calls this to reject a bad
// flows.capacity_profile value before a Memory is ever built.
func CapsForProfile(profile string) (Caps, bool) {
	switch profile {
	case ProfileCompact:
		return compactCaps, true
	case ProfileDefault:
		return DefaultCaps, true
	case ProfileExpanded:
		return expandedCaps, true
	default:
		return Caps{}, false
	}
}

// bytesPerCountsMapEntry is a conservative flat per-entry cost for a Go map
// keyed by a small comparable type and holding a *Counts (40 bytes): it
// covers the map's own bucket/pointer overhead plus the Counts allocation.
// This is a PLANNING estimate, not a measurement — real map overhead varies
// with key shape and load factor, and the true figure is smaller in practice
// because most dimensions never fill to their cap. It exists so the admin
// status page can show a worst-case footnote next to the configured profile,
// not to size an allocator.
const bytesPerCountsMapEntry = 96

// bytesPerRecentEntry matches the "roughly 200 bytes per entry" estimate in
// MaxRecent's doc comment above.
const bytesPerRecentEntry = 200

// bytesPerBucket estimates one bucket's WORST-CASE memory footprint: every
// capped map at its cap. See bytesPerCountsMapEntry for what "estimate"
// means here.
func (c Caps) bytesPerBucket() int64 {
	// Eight single-key label maps, each capped at MaxLabelsPerKind:
	// transports, trafficTypes, users, tags, oses, paths, derpRegions,
	// verdicts.
	labelMaps := int64(8) * int64(c.MaxLabelsPerKind) * bytesPerCountsMapEntry
	pairs := int64(c.MaxPairsPerBucket) * bytesPerCountsMapEntry
	ports := int64(c.MaxPortsPerBucket) * bytesPerCountsMapEntry
	unexplained := int64(c.MaxUnexplainedPerBucket) * bytesPerCountsMapEntry
	rules := int64(c.MaxRulesPerBucket) * bytesPerCountsMapEntry
	peerPaths := int64(c.MaxPeerPathsPerBucket) * bytesPerCountsMapEntry
	// nodes holds *nodeCounts (two Counts), roughly double the per-entry cost
	// of the single-Counts maps above.
	nodes := int64(c.MaxNodesPerBucket) * (bytesPerCountsMapEntry + 40)
	// Three identity matrices (tag, user, OS), each capped at
	// MaxMatrixCellsPerBucket.
	matrices := int64(3) * int64(c.MaxMatrixCellsPerBucket) * bytesPerCountsMapEntry
	return labelMaps + pairs + ports + unexplained + rules + peerPaths + nodes + matrices
}

// EstimatedBytes estimates the store's WORST-CASE steady-state footprint:
// every retained bucket full in every dimension, plus the raw-connection
// ring. bucketCapacity is the store's retained bucket count (flows.retention
// / Resolution); a non-positive value contributes nothing.
func (c Caps) EstimatedBytes(bucketCapacity int) int64 {
	if bucketCapacity < 0 {
		bucketCapacity = 0
	}
	return c.bytesPerBucket()*int64(bucketCapacity) + int64(c.MaxRecent)*bytesPerRecentEntry
}

// Limits reports the effective (post-validation, post-clamp) capacity policy
// a Memory store was built with, and its estimated in-memory footprint. It is
// the seam the admin status page reads to show configured/effective limits
// and estimated usage (#329) — see Memory.Limits.
type Limits struct {
	// Profile is one of ProfileCompact, ProfileDefault or ProfileExpanded —
	// whichever WithCapacityProfile selected, or ProfileDefault when unset or
	// unrecognized.
	Profile                 string
	BucketCapacity          int
	MaxPairsPerBucket       int
	MaxNodesPerBucket       int
	MaxPortsPerBucket       int
	MaxLabelsPerKind        int
	MaxMatrixCellsPerBucket int
	MaxUnexplainedPerBucket int
	MaxRulesPerBucket       int
	MaxPeerPathsPerBucket   int
	MaxRecent               int
	// EstimatedBytes is Caps.EstimatedBytes(BucketCapacity): the store's
	// worst-case steady-state footprint at these limits. A planning estimate,
	// not a measurement — see bytesPerCountsMapEntry.
	EstimatedBytes int64
}

// Other is the key that overflow folds into, in every dimension. It is
// deliberately the same sentinel the flow-metric rollup uses, so an operator
// sees one consistent "everything else" label across the metric and the UI.
const Other = "__other__"

// Counts is the measured quantity in every dimension of the store. Transmit and
// receive are kept separate throughout: both are real, independently measured
// values on this data, and collapsing them loses information.
type Counts struct {
	TxBytes int64 `json:"tx_bytes"`
	RxBytes int64 `json:"rx_bytes"`
	TxPkts  int64 `json:"tx_packets"`
	RxPkts  int64 `json:"rx_packets"`
	Flows   int64 `json:"flows"`
}

// Bytes is the total volume in both directions.
func (c Counts) Bytes() int64 { return c.TxBytes + c.RxBytes }

func (c *Counts) add(o Counts) {
	c.TxBytes += o.TxBytes
	c.RxBytes += o.RxBytes
	c.TxPkts += o.TxPkts
	c.RxPkts += o.RxPkts
	c.Flows += o.Flows
}

// Observation is one connection's contribution to the store, as the flow
// processor already has it: identity resolved, transport named, counters
// summed. Empty string means "the record did not carry this" — the store
// preserves that distinction rather than substituting a placeholder, so an
// absent flow endpoint stays absent in the UI too.
type Observation struct {
	// Time is when the traffic occurred (the flow window start), not when the
	// record was captured.
	Time        time.Time
	TrafficType string
	Transport   string
	// SrcAddr/DstAddr are the raw "addr:port" endpoints the record carried. They
	// are kept only in the bounded recent-connection ring, never in the minute
	// buckets — an address is far too high-cardinality to aggregate on, but the
	// exact tuple is what makes a flow list useful.
	SrcAddr string
	DstAddr string
	SrcNode string
	DstNode string
	// DstPort is empty for a protocol that has no ports, not "0". Tailscale
	// writes ":0" on the wire wherever the addr:port shape demands a port and
	// there is none, and the processor reads that as absent when it decodes the
	// record (flowlog.splitEndpoint), so the empty-string convention above covers
	// this case like any other. The raw endpoint is still in DstAddr verbatim.
	DstPort    string
	DstService string
	SrcUser    string
	DstUser    string
	SrcTags    string
	DstTags    string
	SrcOS      string
	DstOS      string
	// ReporterNodeID is the verified record reporter (FlowLog.NodeID), distinct
	// from the flow-embedded endpoint references. ReporterTrust and
	// ReporterConsistency explain how the configured trust policy and the
	// unverified embedded source reference relate to that reporter.
	ReporterNodeID      string
	ReporterTrust       string
	ReporterConsistency string
	// Verdict is the network policy's reading of this connection:
	// VerdictPermitted, VerdictNoRule or VerdictUndetermined. Empty means the
	// connection was NOT evaluated — no policy has been collected yet, or the
	// traffic is not policy-governed. That is deliberately distinct from
	// VerdictUndetermined, where a policy was applied and could not decide; a
	// store that conflated them would report "we cannot tell" for traffic
	// nothing ever looked at.
	Verdict string
	// Reversed reports that the rule matched the connection's establishing
	// direction rather than the direction observed. Return traffic is normal and
	// expected — it accounted for 37% of connections on a live capture — so the
	// distinction is presentational, not a finding.
	Reversed bool
	// Rule indexes the policy's rule list. Read only when Verdict is
	// VerdictPermitted; -1 whenever nothing matched.
	//
	// The index alone does NOT identify a rule: it is only meaningful against the
	// exact rule list that produced it, and an operator reordering their ACL moves
	// every index without changing anything the store can see. PolicyVersion is
	// the other half of the identity, so a window spanning a policy edit reports
	// each observation against the rules it was actually evaluated against (#302).
	Rule          int
	PolicyVersion string
	// Path is how the two nodes actually reached each other on the wire:
	// PathDirectIPv4, PathDirectIPv6 or PathDERP. Only physical traffic — the
	// WireGuard underlay — has one, so it is empty on every overlay connection.
	// It is also empty for a physical entry carrying no underlay endpoint, which
	// says nothing about the path rather than saying it was direct.
	Path string
	// DERPRegion is the relay's region ID, set only when Path is PathDERP.
	DERPRegion string
	Counts     Counts
}

// NormalizeObservationForRetention returns the representation safe for any
// retained flow store. Both the memory and SQLite backends call this shared
// boundary so a sibling backend cannot retain larger attacker-controlled text.
func NormalizeObservationForRetention(o Observation) Observation {
	values := []string{o.TrafficType, o.Transport, o.SrcAddr, o.DstAddr, o.SrcNode, o.DstNode,
		o.DstPort, o.DstService, o.SrcUser, o.DstUser, o.SrcTags, o.DstTags, o.SrcOS, o.DstOS,
		o.ReporterNodeID, o.ReporterTrust, o.ReporterConsistency, o.Verdict, o.PolicyVersion, o.Path, o.DERPRegion}
	boundedtext.StringsBudget(values, MaxRetainedFieldBytes, MaxRetainedObservationBytes)
	o.TrafficType, o.Transport, o.SrcAddr, o.DstAddr = values[0], values[1], values[2], values[3]
	o.SrcNode, o.DstNode, o.DstPort, o.DstService = values[4], values[5], values[6], values[7]
	o.SrcUser, o.DstUser, o.SrcTags, o.DstTags = values[8], values[9], values[10], values[11]
	o.SrcOS, o.DstOS = values[12], values[13]
	o.ReporterNodeID, o.ReporterTrust, o.ReporterConsistency = values[14], values[15], values[16]
	o.Verdict, o.PolicyVersion, o.Path, o.DERPRegion = values[17], values[18], values[19], values[20]
	return o
}

// The verdict vocabulary, which is also the JSON API's. It mirrors the
// evaluator's naming, but the store deliberately does not import the evaluator:
// this package stays a leaf that knows nothing about policy syntax, and holds
// only the label.
const (
	VerdictPermitted = "permitted"
	VerdictNoRule    = "no_rule"
	// VerdictUndetermined is "a policy was applied and could not decide", which
	// is never a finding.
	VerdictUndetermined = "undetermined"
	// VerdictPermittedReverse is a presentation-only split of VerdictPermitted
	// for connections matched in their establishing direction. The processor
	// never writes it; the breakdown folds Verdict and Reversed into it.
	VerdictPermittedReverse = "permitted_reverse"
)

// The underlay-path vocabulary, held here for the same reason as the verdict
// vocabulary: this is where the labels are aggregated, and the package stays a
// leaf that knows nothing about how they were derived.
//
// The values match the raw `path` label tailscaled exports on its own throughput
// counters, so an operator running the node-metrics collector sees one vocabulary
// across both surfaces. Tailscale's newer peer-relay paths are NOT distinguished
// here: a flow record reports the endpoint a peer was reached at, and a peer
// relay is indistinguishable from a direct endpoint in that field. Claiming
// otherwise would be a guess.
const (
	PathDirectIPv4 = "direct_ipv4"
	PathDirectIPv6 = "direct_ipv6"
	PathDERP       = "derp"
)

// TrafficPhysical is the one traffic type this package has to know by name:
// it is the WireGuard underlay, and several dimensions are scoped to it or away
// from it. Held here rather than imported for the same leaf reason as the two
// vocabularies above; the value matches semconv.TrafficPhysical, which is where
// the emitted telemetry gets it from.
const TrafficPhysical = "physical"

// PairKey identifies one directed node-to-node relationship within a traffic
// type. Direction is preserved: src and dst are not normalised into an
// unordered pair, because on this data a flow is reported once, by one node,
// and the direction it reports is real information.
type PairKey struct {
	Src         string `json:"src"`
	Dst         string `json:"dst"`
	TrafficType string `json:"traffic_type"`
}

// MatrixKey is one directed identity-to-identity relationship: which tag, user
// or operating system talked to which. Direction is preserved for the same
// reason it is on PairKey — a flow is reported once, by one node.
type MatrixKey struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// UnexplainedKey is one directed relationship the policy did not explain, at the
// granularity a rule would be written at: who talked to whom, over what.
// Aggregating to this shape is what turns thousands of individually unexplained
// connections into the handful of relationships actually behind them — on a live
// capture, 9,786 connections collapsed to three.
type UnexplainedKey struct {
	// Src and Dst name each endpoint by the most useful thing known about it —
	// its tags, then its owner, then its device name, then its bare address —
	// falling back to Unidentified. That is the order an operator would write a
	// rule in.
	Src string `json:"src"`
	Dst string `json:"dst"`
	// Transport and Port describe what was carried, so the relationship reads as
	// the grant that would cover it rather than as a bare pair of names.
	Transport string `json:"transport"`
	Port      string `json:"port,omitempty"`
}

// peerPathKey is one peer and one way of reaching it. Splitting per path rather
// than storing a direct/relayed pair keeps the bucket a plain Counts map like
// every other dimension, and lets the query fold the two direct families
// together without the store deciding what "direct" means.
type peerPathKey struct {
	peer string
	path string
}

// PortKey identifies a destination service endpoint.
type PortKey struct {
	Port      string `json:"port"`
	Transport string `json:"transport"`
	Service   string `json:"service"`
}

// bucket is one minute of aggregated activity.
type bucket struct {
	start time.Time
	total Counts

	// caps is the owning Memory's capacity profile, copied in at creation
	// time. A bucket enforces its own caps rather than reaching back through
	// a pointer to Memory, so its methods stay lock-free and independent of
	// the store around them (they already run under Memory's mutex via
	// RecordResult, but nothing here should ever need to know that).
	caps Caps

	pairs map[PairKey]*Counts
	nodes map[string]*nodeCounts
	ports map[PortKey]*Counts

	transports   map[string]*Counts
	trafficTypes map[string]*Counts
	users        map[string]*Counts
	tags         map[string]*Counts
	oses         map[string]*Counts

	// Identity matrices: who talked to whom, by tag, by user, by operating
	// system. Separate from the breakdowns above, which answer "how much of
	// each" rather than "what to what".
	tagMatrix  map[MatrixKey]*Counts
	userMatrix map[MatrixKey]*Counts
	osMatrix   map[MatrixKey]*Counts

	// Policy reconciliation. verdicts is how the policy read the window's
	// traffic; unexplained is the relationships behind the no_rule share; rules
	// counts what each rule actually permitted, which is the only way to answer
	// which rules were never exercised.
	verdicts    map[string]*Counts
	unexplained map[UnexplainedKey]*Counts
	rules       map[RuleKey]*Counts

	// Underlay path quality, from physical traffic only. paths is the overall
	// direct-vs-relayed split; derpRegions is which relays carried the relayed
	// share; peerPaths is the same split per peer, which is the form that names
	// the device to go and look at.
	paths       map[string]*Counts
	derpRegions map[string]*Counts
	peerPaths   map[peerPathKey]*Counts

	// dropped counts observations whose keys overflowed a cap and were folded
	// into Other. Surfaced so the UI can say so rather than silently implying
	// complete coverage.
	dropped int64
}

// nodeCounts splits a node's activity by role, so the UI can distinguish what a
// device sent from what it received without inferring it from pair direction.
type nodeCounts struct {
	Sent     Counts
	Received Counts
}

func newBucket(start time.Time, caps Caps) *bucket {
	return &bucket{
		start:        start,
		caps:         caps,
		pairs:        map[PairKey]*Counts{},
		nodes:        map[string]*nodeCounts{},
		ports:        map[PortKey]*Counts{},
		transports:   map[string]*Counts{},
		trafficTypes: map[string]*Counts{},
		users:        map[string]*Counts{},
		tags:         map[string]*Counts{},
		oses:         map[string]*Counts{},
		tagMatrix:    map[MatrixKey]*Counts{},
		userMatrix:   map[MatrixKey]*Counts{},
		osMatrix:     map[MatrixKey]*Counts{},
		verdicts:     map[string]*Counts{},
		unexplained:  map[UnexplainedKey]*Counts{},
		rules:        map[RuleKey]*Counts{},
		paths:        map[string]*Counts{},
		derpRegions:  map[string]*Counts{},
		peerPaths:    map[peerPathKey]*Counts{},
	}
}

// addLabel accumulates into a bounded label map, folding overflow into Other.
// An empty key is skipped entirely: "not carried" is not a label value.
func addLabel(m map[string]*Counts, key string, c Counts, cap int) bool {
	if key == "" {
		return false
	}
	e, ok := m[key]
	if !ok {
		if len(m) >= cap {
			key = Other
			e, ok = m[key]
		}
		if !ok {
			e = &Counts{}
			m[key] = e
		}
	}
	e.add(c)
	return true
}

func (b *bucket) record(o Observation) {
	b.total.add(o.Counts)

	// Pairs. An observation missing either endpoint still contributes to totals
	// and to every other dimension, but cannot form an edge in the topology.
	if o.SrcNode != "" && o.DstNode != "" {
		k := PairKey{Src: o.SrcNode, Dst: o.DstNode, TrafficType: o.TrafficType}
		e, ok := b.pairs[k]
		if !ok {
			if len(b.pairs) >= b.caps.MaxPairsPerBucket {
				k = PairKey{Src: Other, Dst: Other, TrafficType: o.TrafficType}
				b.dropped++
				e, ok = b.pairs[k]
			}
			if !ok {
				e = &Counts{}
				b.pairs[k] = e
			}
		}
		e.add(o.Counts)
	}

	b.addNode(o.SrcNode, o.Counts, true)
	b.addNode(o.DstNode, o.Counts, false)

	b.recordPorts(o)

	addLabel(b.transports, o.Transport, o.Counts, b.caps.MaxLabelsPerKind)
	addLabel(b.trafficTypes, o.TrafficType, o.Counts, b.caps.MaxLabelsPerKind)
	addLabel(b.users, o.SrcUser, o.Counts, b.caps.MaxLabelsPerKind)
	if o.DstUser != o.SrcUser {
		addLabel(b.users, o.DstUser, o.Counts, b.caps.MaxLabelsPerKind)
	}
	addLabel(b.oses, o.SrcOS, o.Counts, b.caps.MaxLabelsPerKind)
	if o.DstOS != o.SrcOS {
		addLabel(b.oses, o.DstOS, o.Counts, b.caps.MaxLabelsPerKind)
	}

	// Tags are a SET per endpoint, so the breakdown is per individual tag: a
	// device tagged "tag:servers,tag:prod" must be visible under both, not under
	// a joined label that matches nothing an operator would search for. A tag on
	// both endpoints describes one flow, so it counts once.
	srcTags, dstTags := splitTags(o.SrcTags), splitTags(o.DstTags)
	for _, tag := range srcTags {
		addLabel(b.tags, tag, o.Counts, b.caps.MaxLabelsPerKind)
	}
	for _, tag := range dstTags {
		if !slices.Contains(srcTags, tag) {
			addLabel(b.tags, tag, o.Counts, b.caps.MaxLabelsPerKind)
		}
	}

	// Matrices. Each is the cross product of the two endpoints' values for that
	// kind, so it answers "what talks to what" rather than "how much of each".
	// A flow whose endpoints do not BOTH carry the kind occupies no cell — it is
	// not a relationship we observed, and inventing one would make the page's
	// coverage statement wrong.
	b.addMatrix(b.tagMatrix, srcTags, dstTags, o.Counts)
	b.addMatrix(b.userMatrix, oneOrNone(o.SrcUser), oneOrNone(o.DstUser), o.Counts)
	b.addMatrix(b.osMatrix, oneOrNone(o.SrcOS), oneOrNone(o.DstOS), o.Counts)

	b.recordPolicy(o)
	b.recordPath(o)
}

// recordPorts accumulates the destination-service dimension, from OVERLAY
// traffic only. It is the mirror of recordPath: the underlay says HOW two nodes
// reached each other, the overlay says WHAT was contacted, and the two are
// complementary views rather than the same view counted twice.
//
// A physical entry's destination is a WireGuard endpoint, so its port is the
// ephemeral one the peer happened to be listening on — and on a relayed path it
// is not a port at all, but the DERP region ID Tailscale writes where a port
// would go. Neither names a service, so neither belongs in a table read as "what
// was contacted". On a live tailnet the underlay supplied 50.8% of the section's
// bytes and both of its top two rows (two ephemeral ports, 6.7 GB each) before
// this gate.
//
// It also leaves the section's cap to the traffic the section is for. Ephemeral
// ports are unbounded by nature — a fresh one per connection — so underlay churn
// alone could exhaust MaxPortsPerBucket and fold the real services into Other,
// leaving the page reporting partial coverage of a table it could have covered
// completely.
//
// Nothing is lost by this. The underlay endpoint is still on the connection list
// verbatim, its path is in the path section, and its bytes count toward the
// totals and every other breakdown.
func (b *bucket) recordPorts(o Observation) {
	if o.DstPort == "" || o.TrafficType == TrafficPhysical {
		return
	}
	k := PortKey{Port: o.DstPort, Transport: o.Transport, Service: o.DstService}
	e, ok := b.ports[k]
	if !ok {
		if len(b.ports) >= b.caps.MaxPortsPerBucket {
			k = PortKey{Port: Other, Transport: o.Transport}
			b.dropped++
			e, ok = b.ports[k]
		}
		if !ok {
			e = &Counts{}
			b.ports[k] = e
		}
	}
	e.add(o.Counts)
}

// recordPath accumulates the underlay path dimensions. Only physical traffic
// carries a path, so everything else returns immediately — which is most of the
// emit path's traffic, and keeps three map lookups off it.
func (b *bucket) recordPath(o Observation) {
	if o.Path == "" {
		return
	}
	addLabel(b.paths, o.Path, o.Counts, b.caps.MaxLabelsPerKind)
	// A relayed connection whose region was unreadable still counts as relayed
	// above; it simply names no relay here.
	if o.Path == PathDERP {
		addLabel(b.derpRegions, o.DERPRegion, o.Counts, b.caps.MaxLabelsPerKind)
	}

	k := peerPathKey{peer: peerIdentity(o.SrcNode, o.SrcAddr), path: o.Path}
	e, ok := b.peerPaths[k]
	if !ok {
		if len(b.peerPaths) >= b.caps.MaxPeerPathsPerBucket {
			k = peerPathKey{peer: Other, path: o.Path}
			b.dropped++
			e, ok = b.peerPaths[k]
		}
		if !ok {
			e = &Counts{}
			b.peerPaths[k] = e
		}
	}
	e.add(o.Counts)
}

// peerIdentity names the far end of an underlay connection.
//
// Unlike endpointIdentity, this deliberately does NOT prefer tags or owner: the
// per-peer table exists to name the ONE device to go and look at, and keying it
// on a tag would merge every tagged server into a single row answering a
// different question. Device name, then bare address, then the sentinel — which
// at least says the peer was not resolvable — then Unidentified.
func peerIdentity(node, addrPort string) string {
	if node != "" && node != external && node != unknown {
		return node
	}
	if host := addrHost(addrPort); host != "" {
		return host
	}
	if node != "" {
		return node
	}
	return Unidentified
}

// recordPolicy accumulates the policy reconciliation dimensions. An observation
// with no verdict was never evaluated — no policy has been collected, or the
// traffic is not policy-governed — and takes no part in any of them, so a
// tailnet whose ACL has not arrived shows an empty section rather than one
// claiming its entire traffic is undecidable. The early return also keeps three
// map lookups per connection off the emit path in that case, which is the
// default one until an ACL is first collected.
func (b *bucket) recordPolicy(o Observation) {
	if o.Verdict == "" {
		return
	}

	verdict := o.Verdict
	if verdict == VerdictPermitted && o.Reversed {
		verdict = VerdictPermittedReverse
	}
	addLabel(b.verdicts, verdict, o.Counts, b.caps.MaxLabelsPerKind)

	switch {
	case o.Verdict == VerdictPermitted && o.Rule >= 0:
		// Only a rule that PERMITTED something has been exercised, and only a
		// permitted verdict carries a meaningful rule index — Rule is otherwise
		// -1, or the zero value of an observation nothing evaluated.
		k := RuleKey{PolicyVersion: o.PolicyVersion, Rule: o.Rule}
		e, ok := b.rules[k]
		if !ok {
			if len(b.rules) >= b.caps.MaxRulesPerBucket {
				b.dropped++
				return
			}
			e = &Counts{}
			b.rules[k] = e
		}
		e.add(o.Counts)
	case o.Verdict == VerdictNoRule:
		// Only a DEFINITE "no rule covers this" is a finding. An undetermined
		// verdict means the policy could not be applied, and reporting that as
		// unexplained is precisely the confident-false-alarm the evaluator's
		// three-valued matching exists to prevent.
		b.addUnexplained(o)
	}
}

// addUnexplained accumulates one unexplained connection into its relationship.
func (b *bucket) addUnexplained(o Observation) {
	k := UnexplainedKey{
		Src:       endpointIdentity(o.SrcTags, o.SrcUser, o.SrcNode, o.SrcAddr),
		Dst:       endpointIdentity(o.DstTags, o.DstUser, o.DstNode, o.DstAddr),
		Transport: o.Transport,
		// A protocol with no ports arrives here with no port, so the relationship
		// reads "icmp" rather than "icmp/0" — which is not a port and not a rule
		// anyone could write. That normalisation used to happen on this line;
		// it now happens once where the record is decoded (flowlog.splitEndpoint),
		// which is why an Observation can no longer carry a zero port at all.
		Port: o.DstPort,
	}
	e, ok := b.unexplained[k]
	if !ok {
		if len(b.unexplained) >= b.caps.MaxUnexplainedPerBucket {
			k = UnexplainedKey{Src: Other, Dst: Other, Transport: o.Transport}
			b.dropped++
			e, ok = b.unexplained[k]
		}
		if !ok {
			e = &Counts{}
			b.unexplained[k] = e
		}
	}
	e.add(o.Counts)
}

// endpointIdentity names one end of an unexplained relationship, preferring what
// the endpoint IS over where it is: its tags, then its owner, then its device
// name, then its bare address. That is the order a rule would be written in, and
// it is what made a live capture's 9,786 unexplained connections legible as
// three relationships.
//
// An endpoint that carries none of them is named Unidentified rather than
// dropped — dropping it would leave the relationship counts short of the verdict
// totals displayed beside them.
func endpointIdentity(tags, user, node, addrPort string) string {
	switch {
	case tags != "":
		return tags
	case user != "":
		return user
	case node != "" && node != external && node != unknown:
		return node
	}
	if host := addrHost(addrPort); host != "" {
		return host
	}
	// No address either. A collapse sentinel is still the most that is known —
	// it says the endpoint was outside the tailnet, which "unidentified" does not.
	if node != "" {
		return node
	}
	return Unidentified
}

// addrHost strips the port from an "addr:port" endpoint, yielding "" when there
// is nothing to strip it from.
func addrHost(addrPort string) string {
	if addrPort == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addrPort); err == nil {
		return host
	}
	return addrPort
}

// addMatrix accumulates the cross product of src and dst into m, folding
// overflow into a single Other cell. Unlike the one-dimensional breakdowns the
// diagonal is kept: a device talking to another device with the same owner or
// tag is a real, common, and interesting relationship.
func (b *bucket) addMatrix(m map[MatrixKey]*Counts, src, dst []string, c Counts) {
	for _, s := range src {
		for _, d := range dst {
			k := MatrixKey{Src: s, Dst: d}
			e, ok := m[k]
			if !ok {
				if len(m) >= b.caps.MaxMatrixCellsPerBucket {
					k = MatrixKey{Src: Other, Dst: Other}
					b.dropped++
					e, ok = m[k]
				}
				if !ok {
					e = &Counts{}
					m[k] = e
				}
			}
			e.add(c)
		}
	}
}

// oneOrNone adapts a single-valued identity field to the slice the matrix takes.
// An absent value yields no entries, which is what keeps the flow out of the
// matrix entirely.
func oneOrNone(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}

// splitTags splits the comma-joined tag set a flow record carries into
// individual tags, tolerating the whitespace and empty entries that a
// hand-edited ACL produces. Returns nil for an absent set, so callers get "no
// tags" rather than one empty tag.
func splitTags(joined string) []string {
	if joined == "" {
		return nil
	}
	parts := strings.Split(joined, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// addNode accumulates a node's role-split activity. The sender's transmit is
// the receiver's receive, so the counters are mirrored per role rather than
// copied — a node's "received" total is what its peers sent to it.
func (b *bucket) addNode(name string, c Counts, sending bool) {
	if name == "" {
		return
	}
	e, ok := b.nodes[name]
	if !ok {
		if len(b.nodes) >= b.caps.MaxNodesPerBucket {
			name = Other
			b.dropped++
			e, ok = b.nodes[name]
		}
		if !ok {
			e = &nodeCounts{}
			b.nodes[name] = e
		}
	}
	if sending {
		e.Sent.add(c)
	} else {
		e.Received.add(Counts{
			TxBytes: c.RxBytes, RxBytes: c.TxBytes,
			TxPkts: c.RxPkts, RxPkts: c.TxPkts,
			Flows: c.Flows,
		})
	}
}

// Memory is an in-memory ring of per-minute buckets. It is the default store:
// no disk, no configuration, bounded by construction, and lost on restart —
// which is acceptable because the OTLP backend, not this, is the system of
// record. Safe for concurrent use.
type Memory struct {
	mu            sync.Mutex
	buckets       map[int64]*bucket
	capacity      int
	now           func() time.Time
	maxFutureSkew time.Duration

	// caps is this store's capacity profile (#329): every per-bucket cap and
	// the raw-connection ring size. Defaults to DefaultCaps/ProfileDefault,
	// reproducing the pre-#329 hardcoded behavior exactly when no
	// WithCapacityProfile option is given.
	caps    Caps
	profile string

	// recent holds up to MaxRecent raw connections, kept sorted ascending by
	// event time (ties broken by recentSeq, the ingestion order) so "newest"
	// always means "latest event time seen", not "latest Record() call" (#301).
	// A late poll/object-store replay can be ingested after genuinely newer
	// traffic; sorting on the observation's own Time — instead of appending in
	// call order — keeps it from displacing real current activity.
	//
	// The live rows are recent[recentStart:]. Evicting the oldest advances
	// recentStart instead of copying the whole slice down, and the slice is
	// compacted only once the dead prefix reaches MaxRecent — so eviction is
	// amortized O(1) rather than an O(MaxRecent) memmove on every observation.
	// Record runs on the emit path under the store lock, so a per-connection
	// memmove of the whole ring is exactly the kind of work this package's
	// contract says it will not do: measured at 12.6us/op on a full ring, which
	// at ~45k connections in one observed capture is over half a second of
	// memcpy per poll cycle.
	recent      []Recent
	recentStart int
	recentSeq   uint64

	recorded int64
	dropped  int64
}

// Admission reports whether an observation entered the store.
type Admission uint8

const (
	// AdmissionAccepted means the observation was recorded.
	AdmissionAccepted Admission = iota
	// AdmissionExpired means the observation predates the store's retention window.
	AdmissionExpired
	// AdmissionFuture means the observation is too far ahead of the current time.
	AdmissionFuture
)

// Option configures a Memory store.
type Option func(*Memory)

// WithClock supplies the store clock. A nil clock is ignored.
func WithClock(now func() time.Time) Option {
	return func(m *Memory) {
		if now != nil {
			m.now = now
		}
	}
}

// WithMaxFutureSkew allows observations up to skew ahead of the current time.
// Negative values normalize to zero, so they never widen admission.
func WithMaxFutureSkew(skew time.Duration) Option {
	return func(m *Memory) {
		m.maxFutureSkew = max(skew, 0)
	}
}

// WithCapacityProfile selects the bounded capacity/memory profile the store
// enforces on every dimension: ProfileCompact, ProfileDefault (also the
// zero-option behavior) or ProfileExpanded (#329). An unrecognized or empty
// profile is ignored and the store keeps ProfileDefault — config.Validate()
// is what rejects a bad flows.capacity_profile value before this is ever
// called; this option fails safe rather than silently going unbounded.
func WithCapacityProfile(profile string) Option {
	return func(m *Memory) {
		if caps, ok := CapsForProfile(profile); ok {
			m.caps = caps
			m.profile = profile
		}
	}
}

// Recent is one raw connection, retained verbatim. The minute buckets answer
// "how much"; these answer "what exactly" — which tuple, between which devices,
// when. They cannot be reconstructed from the aggregates, which is why a bounded
// number are kept alongside them.
type Recent struct {
	Time        time.Time `json:"time"`
	TrafficType string    `json:"traffic_type"`
	Transport   string    `json:"transport"`
	SrcAddr     string    `json:"src_addr,omitempty"`
	DstAddr     string    `json:"dst_addr,omitempty"`
	SrcNode     string    `json:"src_node,omitempty"`
	DstNode     string    `json:"dst_node,omitempty"`
	DstService  string    `json:"dst_service,omitempty"`
	// Endpoint identity, so the connection list can answer "what did this user's
	// devices actually do" — a question no aggregate can. Omitted when the record
	// did not carry it.
	SrcUser string `json:"src_user,omitempty"`
	DstUser string `json:"dst_user,omitempty"`
	SrcTags string `json:"src_tags,omitempty"`
	DstTags string `json:"dst_tags,omitempty"`
	SrcOS   string `json:"src_os,omitempty"`
	DstOS   string `json:"dst_os,omitempty"`
	// Reporter diagnosis is bounded to closed trust/consistency values. The node
	// ID is retained only in this bounded, admin-authenticated recent ring.
	ReporterNodeID      string `json:"reporter_node_id,omitempty"`
	ReporterTrust       string `json:"reporter_trust,omitempty"`
	ReporterConsistency string `json:"reporter_consistency,omitempty"`
	// The policy's reading of this one connection, so the list an operator drills
	// into after seeing a relationship in the aggregate can show which
	// connections were unexplained and which rule covered the rest.
	Verdict  string `json:"verdict,omitempty"`
	Reversed bool   `json:"reversed,omitempty"`
	Rule     int    `json:"rule"`
	// PolicyVersion identifies the rule list Rule indexes into; see Observation.
	PolicyVersion string `json:"policy_version,omitempty"`
	// How this one connection was carried, on physical traffic only, so the list
	// behind a relayed peer shows which of its connections were the relayed ones.
	Path       string `json:"path,omitempty"`
	DERPRegion string `json:"derp_region,omitempty"`
	Counts     Counts `json:"counts"`
	// seq is the ingestion sequence, used only to break ties between equal
	// event times deterministically (later ingested sorts newer). Unexported:
	// it is ordering metadata, not part of the connection the API describes.
	seq uint64
}

// NewMemory returns a store retaining at most capacity one-minute buckets.
// A non-positive capacity selects six hours. Observations up to five minutes
// ahead of the clock are admitted by default.
func NewMemory(capacity int, opts ...Option) *Memory {
	if capacity <= 0 {
		capacity = int((6 * time.Hour) / Resolution)
	}
	m := &Memory{
		buckets:       map[int64]*bucket{},
		capacity:      capacity,
		caps:          DefaultCaps,
		profile:       ProfileDefault,
		now:           time.Now,
		maxFutureSkew: 5 * time.Minute,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	// Sized from m.caps, which options above may have replaced — this must
	// happen after the options loop, not in the literal above.
	m.recent = make([]Recent, 0, m.caps.MaxRecent)
	return m
}

// Limits reports the effective (post-validation, post-clamp) capacity policy
// this store was built with, and its estimated in-memory footprint (#329).
// It is the seam the admin status page reads; see the Limits type.
func (m *Memory) Limits() Limits {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Limits{
		Profile:                 m.profile,
		BucketCapacity:          m.capacity,
		MaxPairsPerBucket:       m.caps.MaxPairsPerBucket,
		MaxNodesPerBucket:       m.caps.MaxNodesPerBucket,
		MaxPortsPerBucket:       m.caps.MaxPortsPerBucket,
		MaxLabelsPerKind:        m.caps.MaxLabelsPerKind,
		MaxMatrixCellsPerBucket: m.caps.MaxMatrixCellsPerBucket,
		MaxUnexplainedPerBucket: m.caps.MaxUnexplainedPerBucket,
		MaxRulesPerBucket:       m.caps.MaxRulesPerBucket,
		MaxPeerPathsPerBucket:   m.caps.MaxPeerPathsPerBucket,
		MaxRecent:               m.caps.MaxRecent,
		EstimatedBytes:          m.caps.EstimatedBytes(m.capacity),
	}
}

// bucketKey is the minute-aligned Unix timestamp a time falls in.
func bucketKey(t time.Time) int64 {
	return t.UTC().Truncate(Resolution).Unix()
}

// Record attempts to accumulate one observation and discards its admission
// result. Observations with no timestamp are stamped with the current time: a
// record we cannot place in time is still real traffic, and dropping it would
// understate totals.
func (m *Memory) Record(o Observation) {
	_ = m.RecordResult(o)
}

// RecordResult admits and accumulates one observation. An observation outside
// the retained time window is rejected before it can change the store.
func (m *Memory) RecordResult(o Observation) Admission {
	now := m.now()
	if o.Time.IsZero() {
		o.Time = now
	} else {
		oldest := now.Add(-time.Duration(m.capacity) * Resolution)
		if o.Time.Before(oldest) {
			return AdmissionExpired
		}
		if o.Time.After(now.Add(m.maxFutureSkew)) {
			return AdmissionFuture
		}
	}
	o = NormalizeObservationForRetention(o)
	key := bucketKey(o.Time)

	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.buckets[key]
	if !ok {
		b = newBucket(time.Unix(key, 0).UTC(), m.caps)
		m.buckets[key] = b
		m.evictLocked()
	}
	before := b.dropped
	b.record(o)
	m.recordRecentLocked(o)
	m.recorded++
	m.dropped += b.dropped - before
	return AdmissionAccepted
}

// recordRecentLocked inserts o into the raw-connection ring in event-time
// order (#301), evicting the entry with the OLDEST event time once the ring
// is full — not whichever entry happens to have been ingested longest ago.
// A late arrival (poll backfill, object-store replay, restart replay) whose
// Time is older than every entry currently retained is rejected outright
// rather than evicted-in-place: the ring already holds only-as-new-or-newer
// rows, and displacing one of them for something older would be exactly the
// bug this guards against. Rejecting it this way is not a drop and does not
// count toward Truncated, matching the existing overwrite-is-not-a-drop
// contract for this ring.
//
// Entries are kept sorted ascending by (Time, seq); seq is a monotonic
// ingestion counter used only to break exact-timestamp ties deterministically
// (later ingested sorts newer, matching the pre-#301 behavior for the common
// case of strictly increasing event times).
func (m *Memory) recordRecentLocked(o Observation) {
	entry := Recent{
		Time:                o.Time,
		TrafficType:         o.TrafficType,
		Transport:           o.Transport,
		SrcAddr:             o.SrcAddr,
		DstAddr:             o.DstAddr,
		SrcNode:             o.SrcNode,
		DstNode:             o.DstNode,
		DstService:          o.DstService,
		SrcUser:             o.SrcUser,
		DstUser:             o.DstUser,
		SrcTags:             o.SrcTags,
		DstTags:             o.DstTags,
		SrcOS:               o.SrcOS,
		DstOS:               o.DstOS,
		ReporterNodeID:      o.ReporterNodeID,
		ReporterTrust:       o.ReporterTrust,
		ReporterConsistency: o.ReporterConsistency,
		Verdict:             o.Verdict,
		Reversed:            o.Reversed,
		Rule:                o.Rule,
		PolicyVersion:       o.PolicyVersion,
		Path:                o.Path,
		DERPRegion:          o.DERPRegion,
		Counts:              o.Counts,
		seq:                 m.recentSeq,
	}
	m.recentSeq++

	live := m.recent[m.recentStart:]
	if len(live) >= m.caps.MaxRecent {
		if !entry.Time.After(live[0].Time) {
			// Older than (or exactly as old as) the oldest row currently
			// retained: it would not make the newest-MaxRecent cut, so it is
			// rejected rather than evicting something at least as new.
			return
		}
		// Evict the oldest by advancing the window, not by shifting the slice.
		m.recentStart++
		live = m.recent[m.recentStart:]
	}

	n := len(live)
	idx := sort.Search(n, func(i int) bool {
		return live[i].Time.After(entry.Time)
	})
	// The common case by far is strictly increasing event time, where idx == n
	// and this is a bare append with nothing to move.
	m.recent = append(m.recent, Recent{})
	live = m.recent[m.recentStart:]
	copy(live[idx+1:], live[idx:n])
	live[idx] = entry

	// Reclaim the dead prefix once it has grown to a full window. Doing it here
	// rather than per-eviction is what makes eviction amortized O(1): the copy
	// costs O(MaxRecent) but happens at most once per MaxRecent inserts.
	if m.recentStart >= m.caps.MaxRecent {
		n := copy(m.recent, m.recent[m.recentStart:])
		m.recent = m.recent[:n]
		m.recentStart = 0
	}
}

// Recent returns up to limit of the most recently recorded connections,
// newest event time first (ties broken by ingestion order). A non-positive
// limit returns nothing rather than everything: the caller is always a UI
// page size, and defaulting an unset one to "all" is the wrong direction to
// fail in.
func (m *Memory) Recent(limit int) []Recent {
	if limit <= 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	live := m.liveRecentLocked()
	n := len(live)
	limit = min(limit, n)
	out := make([]Recent, 0, limit)
	// live is sorted ascending, so newest-first is a walk from the end.
	for i := n - 1; i >= n-limit; i-- {
		out = append(out, live[i])
	}
	return out
}

// liveRecentLocked is the retained window of the recent ring. Everything before
// recentStart has been evicted and is waiting to be reclaimed; readers must never
// see it. Callers must hold m.mu.
func (m *Memory) liveRecentLocked() []Recent { return m.recent[m.recentStart:] }

// RecentRange returns up to limit of the most recent connections whose event
// time falls within [start, end], newest first. A zero start means "the
// beginning of what's retained"; a zero end means "now". This lets a caller
// pin the connection list to the same window it pinned an aggregate Query to
// (#295), instead of Recent's always-global-newest view.
func (m *Memory) RecentRange(start, end time.Time, limit int) []Recent {
	if limit <= 0 {
		return nil
	}
	if end.IsZero() {
		end = m.now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	live := m.liveRecentLocked()
	out := make([]Recent, 0, min(limit, len(live)))
	for i := len(live) - 1; i >= 0 && len(out) < limit; i-- {
		r := live[i]
		if r.Time.After(end) {
			continue
		}
		if !start.IsZero() && r.Time.Before(start) {
			// m.recent is sorted ascending by Time, so everything further
			// back is also before start.
			break
		}
		out = append(out, r)
	}
	return out
}

// RecentFilter narrows RecentPage to rows matching every non-empty field
// (an AND across dimensions). Device/Addr/Service/Identity are
// case-insensitive substring matches — the operator is searching, not
// querying a key/value store. Type/Verdict/Path are exact, case-insensitive
// matches: each is a closed enumeration (e.g. Verdict is one of "permitted",
// "no_rule", "undetermined"), and a substring match on those would make "no"
// match both "no_rule" and anything else containing it.
type RecentFilter struct {
	Device   string // substring, case-insensitive, matches SrcNode OR DstNode
	Addr     string // substring, case-insensitive, matches SrcAddr OR DstAddr
	Service  string // substring, case-insensitive, matches DstService
	Identity string // substring, case-insensitive, matches SrcUser, DstUser, SrcTags OR DstTags
	Type     string // exact, case-insensitive, matches TrafficType
	Verdict  string // exact, case-insensitive, matches Verdict
	Path     string // exact, case-insensitive, matches Path
}

// isZero reports whether every field is unset, so RecentPage can skip
// per-row filter work entirely for the common unfiltered case.
func (f RecentFilter) isZero() bool {
	return f == RecentFilter{}
}

// matches reports whether r satisfies every non-empty field of f.
func (f RecentFilter) matches(r Recent) bool {
	if f.Device != "" && !containsFold(r.SrcNode, f.Device) && !containsFold(r.DstNode, f.Device) {
		return false
	}
	if f.Addr != "" && !containsFold(r.SrcAddr, f.Addr) && !containsFold(r.DstAddr, f.Addr) {
		return false
	}
	if f.Service != "" && !containsFold(r.DstService, f.Service) {
		return false
	}
	if f.Identity != "" &&
		!containsFold(r.SrcUser, f.Identity) && !containsFold(r.DstUser, f.Identity) &&
		!containsFold(r.SrcTags, f.Identity) && !containsFold(r.DstTags, f.Identity) {
		return false
	}
	if f.Type != "" && !strings.EqualFold(r.TrafficType, f.Type) {
		return false
	}
	if f.Verdict != "" && !strings.EqualFold(r.Verdict, f.Verdict) {
		return false
	}
	if f.Path != "" && !strings.EqualFold(r.Path, f.Path) {
		return false
	}
	return true
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// RecentQuery is one request against the raw-connection ring: a time window
// (matching Query's [Start, End) convention), a page size, a resume point,
// and an optional filter.
type RecentQuery struct {
	Start, End time.Time
	Limit      int
	// Cursor resumes after a previous page: 0 means "start from the newest",
	// otherwise only rows with seq < Cursor are considered. It is the seq of
	// the last row a previous RecentPage call returned.
	Cursor uint64
	Filter RecentFilter
}

// RecentPage is one page of the raw-connection ring, plus the bookkeeping an
// operator needs to tell "nothing else matches" apart from "the ring no
// longer holds it".
type RecentPage struct {
	// Rows is newest-first, same convention as Recent and RecentRange.
	Rows []Recent
	// NextCursor is the seq of the last row in Rows when further matching rows
	// remain beyond Limit, and 0 when there are none.
	NextCursor uint64
	// Matched is how many rows in [Start, End] satisfy Filter, ignoring both
	// Limit and Cursor — it answers "how many are there", not "how many did
	// this page return".
	Matched int
	// Retained is how many rows the ring currently holds, ignoring both the
	// window and the filter.
	Retained int
	// Truncated reports that the ring is at MaxRecent capacity, so older
	// matching rows may already have been evicted rather than simply not
	// existing.
	Truncated bool
}

// RecentPage returns a filtered, cursor-paginated page of the raw-connection
// ring. An empty Filter and a zero Cursor make it equivalent to RecentRange —
// RecentPage is a superset, not a replacement, so every existing caller of
// RecentRange sees identical behavior (#296).
//
// Filtering happens entirely here, at query time. Record must stay a short
// lock and a handful of map writes (a live capture carried 44,825 calls in a
// single poll — see recentbench_test.go); this walks the already-retained
// ring under the same lock instead of adding any per-observation cost.
func (m *Memory) RecentPage(q RecentQuery) RecentPage {
	end := q.End
	m.mu.Lock()
	defer m.mu.Unlock()
	if end.IsZero() {
		end = m.now()
	}

	live := m.liveRecentLocked()
	page := RecentPage{Retained: len(live), Truncated: len(live) >= m.caps.MaxRecent}

	var rows []Recent
	if q.Limit > 0 {
		rows = make([]Recent, 0, min(q.Limit, len(live)))
	}
	var haveMore bool
	for i := len(live) - 1; i >= 0; i-- {
		r := live[i]
		if r.Time.After(end) {
			continue
		}
		if !q.Start.IsZero() && r.Time.Before(q.Start) {
			// live is sorted ascending by Time, so everything further back is
			// also before Start.
			break
		}
		if !q.Filter.isZero() && !q.Filter.matches(r) {
			continue
		}
		// Matched counts every in-window match regardless of Cursor/Limit, so
		// it keeps counting after this page has filled up below.
		page.Matched++
		if q.Cursor != 0 && r.seq >= q.Cursor {
			// Already served by an earlier page.
			continue
		}
		if q.Limit <= 0 || len(rows) >= q.Limit {
			haveMore = true
			continue
		}
		rows = append(rows, r)
	}
	page.Rows = rows
	if haveMore && len(rows) > 0 {
		page.NextCursor = rows[len(rows)-1].seq
	}
	return page
}

// evictLocked drops the oldest buckets until the ring is within capacity.
func (m *Memory) evictLocked() {
	for len(m.buckets) > m.capacity {
		var oldest int64
		first := true
		for k := range m.buckets {
			if first || k < oldest {
				oldest, first = k, false
			}
		}
		delete(m.buckets, oldest)
	}
}

// Range is the time span a query covers or a result describes.
type Range struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Query selects a window and the shape of the result.
type Query struct {
	// Start/End bound the window. A zero End means "now"; a zero Start means
	// "everything retained".
	Start, End time.Time
	// Step is the timeline resolution. Values below Resolution, or that would
	// produce more than MaxPoints, are raised to fit.
	Step time.Duration
	// TopN bounds each ranked list. Non-positive selects 20.
	TopN int
}

// MaxPoints caps timeline length so a wide window cannot produce an unbounded
// series. The step is raised until the window fits.
const MaxPoints = 720

// Point is one timeline sample.
type Point struct {
	Time   time.Time `json:"time"`
	Counts Counts    `json:"counts"`
}

// PairStat, NodeStat, PortStat and LabelStat are the ranked outputs. Each is
// sorted by total bytes descending, then by key for a stable order when volumes
// tie — without the tiebreak, equal-volume rows would reshuffle between polls.
type PairStat struct {
	PairKey
	Counts Counts `json:"counts"`
}

// NodeStat is one device's activity, split by role.
type NodeStat struct {
	Node     string `json:"node"`
	Sent     Counts `json:"sent"`
	Received Counts `json:"received"`
}

// Bytes is the node's total volume in both roles.
func (n NodeStat) Bytes() int64 { return n.Sent.Bytes() + n.Received.Bytes() }

// PortStat is one destination endpoint's activity.
type PortStat struct {
	PortKey
	Counts Counts `json:"counts"`
}

// MatrixCell is one cell of an identity matrix.
type MatrixCell struct {
	MatrixKey
	Counts Counts `json:"counts"`
}

// MaxMatrixCellsReturned bounds a matrix in the response. A grid an operator can
// read is far smaller than this; the cap exists so a pathological tailnet cannot
// turn one request into a multi-megabyte body.
const MaxMatrixCellsReturned = 400

// LabelStat is one value of a single-dimension breakdown.
type LabelStat struct {
	Label  string `json:"label"`
	Counts Counts `json:"counts"`
}

// UnexplainedStat is one relationship the policy did not explain.
type UnexplainedStat struct {
	UnexplainedKey
	Counts Counts `json:"counts"`
}

// RuleKey identifies one rule of one policy revision.
//
// The index alone is not an identity. It is a position in a list an operator
// edits, so the same index before and after a reorder names two different rules,
// and a store keyed on the index alone would sum their traffic together and
// attribute the total to whichever rule happens to sit there now. Pairing it
// with the policy version makes the key mean what it says (#302).
type RuleKey struct {
	PolicyVersion string
	Rule          int
}

// RuleStat is how much traffic one policy rule permitted. Rule indexes the
// compiled policy's rule list; a rule absent from the result permitted nothing
// in the window, which is the whole point of reporting it.
type RuleStat struct {
	Rule int `json:"rule"`
	// PolicyVersion identifies the rule list Rule indexes into. Two stats sharing
	// an index but not a version are DIFFERENT rules, and must never be summed.
	PolicyVersion string `json:"policy_version,omitempty"`
	Counts        Counts `json:"counts"`
}

// PeerPathStat is how one peer was reached over the window: how much of the
// underlay traffic to it went directly, and how much had to be relayed through
// DERP. The two IPv4 and IPv6 direct families are folded together — an operator
// asking this question wants to know whether a peer is being relayed, and the
// per-family split is in Paths for anyone who wants it.
type PeerPathStat struct {
	Peer    string `json:"peer"`
	Direct  Counts `json:"direct"`
	Relayed Counts `json:"relayed"`
}

// Result is a complete answer to a Query.
type Result struct {
	Window       Range       `json:"window"`
	Step         string      `json:"step"`
	Totals       Counts      `json:"totals"`
	Series       []Point     `json:"series"`
	Pairs        []PairStat  `json:"pairs"`
	Nodes        []NodeStat  `json:"nodes"`
	Ports        []PortStat  `json:"ports"`
	Transports   []LabelStat `json:"transports"`
	TrafficTypes []LabelStat `json:"traffic_types"`
	Users        []LabelStat `json:"users"`
	Tags         []LabelStat `json:"tags"`
	OSes         []LabelStat `json:"oses"`
	// The identity matrices: which tag/user/OS talked to which. Ranked by bytes
	// and capped at MaxMatrixCellsReturned, so a wide matrix degrades to its
	// busiest corner rather than to an unbounded response.
	TagMatrix  []MatrixCell `json:"tag_matrix"`
	UserMatrix []MatrixCell `json:"user_matrix"`
	OSMatrix   []MatrixCell `json:"os_matrix"`
	// Policy reconciliation over the window. Verdicts is how the policy read the
	// traffic; Unexplained is the relationships behind the no_rule share, ranked
	// by volume; Rules is what each rule permitted, from which the caller derives
	// the rules that permitted nothing.
	//
	// All three are empty when no policy was in force, which a caller must
	// distinguish from a policy that explained everything.
	Verdicts    []LabelStat       `json:"verdicts"`
	Unexplained []UnexplainedStat `json:"unexplained"`
	Rules       []RuleStat        `json:"rules"`
	// Underlay path quality over the window, from physical traffic only. Paths is
	// the overall split, DERPRegions is which relays carried the relayed share,
	// and PeerPaths is the same split per peer, ranked so the relayed ones come
	// first. All three are empty when the tailnet reports no physical traffic,
	// which is the case whenever flow logs are collected without it.
	Paths       []LabelStat    `json:"paths"`
	DERPRegions []LabelStat    `json:"derp_regions"`
	PeerPaths   []PeerPathStat `json:"peer_paths"`
	// Truncated reports observations folded into Other because a cap was hit, so
	// the UI can say coverage is partial instead of implying it is complete.
	Truncated int64 `json:"truncated"`
}

// Query aggregates the retained buckets overlapping the window.
func (m *Memory) Query(q Query) Result {
	end := q.End
	if end.IsZero() {
		end = m.now()
	}
	topN := q.TopN
	if topN <= 0 {
		topN = 20
	}
	step := q.Step
	if step < Resolution {
		step = Resolution
	}
	if !q.Start.IsZero() {
		for span := end.Sub(q.Start); span/step > MaxPoints; span = end.Sub(q.Start) {
			step *= 2
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	res := Result{
		Window: Range{Start: q.Start, End: end},
		Step:   step.String(),
	}

	pairs := map[PairKey]*Counts{}
	nodes := map[string]*nodeCounts{}
	ports := map[PortKey]*Counts{}
	transports, trafficTypes := map[string]*Counts{}, map[string]*Counts{}
	users, tags, oses := map[string]*Counts{}, map[string]*Counts{}, map[string]*Counts{}
	tagMatrix, userMatrix, osMatrix := map[MatrixKey]*Counts{}, map[MatrixKey]*Counts{}, map[MatrixKey]*Counts{}
	verdicts := map[string]*Counts{}
	unexplained := map[UnexplainedKey]*Counts{}
	rules := map[RuleKey]*Counts{}
	paths, derpRegions := map[string]*Counts{}, map[string]*Counts{}
	peerPaths := map[peerPathKey]*Counts{}
	series := map[int64]*Counts{}

	var earliest, latest time.Time
	for _, b := range m.buckets {
		if !q.Start.IsZero() && b.start.Before(q.Start) {
			continue
		}
		if b.start.After(end) {
			continue
		}
		if earliest.IsZero() || b.start.Before(earliest) {
			earliest = b.start
		}
		if b.start.After(latest) {
			latest = b.start
		}

		res.Totals.add(b.total)
		res.Truncated += b.dropped

		slot := b.start.Truncate(step).Unix()
		e, ok := series[slot]
		if !ok {
			e = &Counts{}
			series[slot] = e
		}
		e.add(b.total)

		mergeInto(pairs, b.pairs)
		mergeInto(ports, b.ports)
		mergeInto(transports, b.transports)
		mergeInto(trafficTypes, b.trafficTypes)
		mergeInto(users, b.users)
		mergeInto(tags, b.tags)
		mergeInto(oses, b.oses)
		mergeInto(tagMatrix, b.tagMatrix)
		mergeInto(userMatrix, b.userMatrix)
		mergeInto(osMatrix, b.osMatrix)
		mergeInto(verdicts, b.verdicts)
		mergeInto(unexplained, b.unexplained)
		mergeInto(rules, b.rules)
		mergeInto(paths, b.paths)
		mergeInto(derpRegions, b.derpRegions)
		mergeInto(peerPaths, b.peerPaths)
		for k, v := range b.nodes {
			n, ok := nodes[k]
			if !ok {
				n = &nodeCounts{}
				nodes[k] = n
			}
			n.Sent.add(v.Sent)
			n.Received.add(v.Received)
		}
	}

	// Report the window actually covered, so the UI shows the retained span
	// rather than a requested one the store cannot satisfy.
	if res.Window.Start.IsZero() {
		res.Window.Start = earliest
	}
	if !latest.IsZero() && latest.Add(Resolution).Before(res.Window.End) {
		res.Window.End = latest.Add(Resolution)
	}

	res.Series = buildSeries(series)
	res.Pairs = topPairs(pairs, topN)
	res.Nodes = topNodes(nodes, topN)
	res.Ports = topPorts(ports, topN)
	res.Transports = topLabels(transports, topN)
	res.TrafficTypes = topLabels(trafficTypes, topN)
	res.Users = topLabels(users, topN)
	res.Tags = topLabels(tags, topN)
	res.OSes = topLabels(oses, topN)
	res.TagMatrix = topMatrix(tagMatrix)
	res.UserMatrix = topMatrix(userMatrix)
	res.OSMatrix = topMatrix(osMatrix)
	res.Verdicts = topLabels(verdicts, topN)
	res.Unexplained = topUnexplained(unexplained, topN)
	res.Rules = allRules(rules)
	res.Paths = topLabels(paths, topN)
	res.DERPRegions = topLabels(derpRegions, topN)
	res.PeerPaths = topPeerPaths(peerPaths, topN)
	return res
}

// topPeerPaths folds the per-peer, per-path counts into one row per peer.
//
// Ranking is by RELAYED volume first, deliberately. A peer moving gigabytes
// directly is working correctly; a peer being relayed is the thing an operator
// would act on, and ranking by total volume alone would bury it under the peers
// that are fine.
func topPeerPaths(m map[peerPathKey]*Counts, n int) []PeerPathStat {
	byPeer := make(map[string]*PeerPathStat, len(m))
	for k, c := range m {
		e, ok := byPeer[k.peer]
		if !ok {
			e = &PeerPathStat{Peer: k.peer}
			byPeer[k.peer] = e
		}
		if k.path == PathDERP {
			e.Relayed.add(*c)
		} else {
			e.Direct.add(*c)
		}
	}
	out := make([]PeerPathStat, 0, len(byPeer))
	for _, e := range byPeer {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Relayed.Bytes(), out[j].Relayed.Bytes(); a != b {
			return a > b
		}
		if a, b := out[i].Direct.Bytes(), out[j].Direct.Bytes(); a != b {
			return a > b
		}
		return out[i].Peer < out[j].Peer
	})
	return truncate(out, n)
}

func topUnexplained(m map[UnexplainedKey]*Counts, n int) []UnexplainedStat {
	out := make([]UnexplainedStat, 0, len(m))
	for k, c := range m {
		out = append(out, UnexplainedStat{UnexplainedKey: k, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		if out[i].Transport != out[j].Transport {
			return out[i].Transport < out[j].Transport
		}
		return out[i].Port < out[j].Port
	})
	return truncate(out, n)
}

// allRules returns every exercised rule, unranked by volume and unbounded by
// TopN: the caller subtracts this from the policy's rule list to find what was
// never exercised, and a truncated list would report live rules as dead.
func allRules(m map[RuleKey]*Counts) []RuleStat {
	out := make([]RuleStat, 0, len(m))
	for k, c := range m {
		out = append(out, RuleStat{Rule: k.Rule, PolicyVersion: k.PolicyVersion, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PolicyVersion != out[j].PolicyVersion {
			return out[i].PolicyVersion < out[j].PolicyVersion
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// mergeInto accumulates src into dst, allocating destination entries as needed.
func mergeInto[K comparable](dst, src map[K]*Counts) {
	for k, v := range src {
		e, ok := dst[k]
		if !ok {
			e = &Counts{}
			dst[k] = e
		}
		e.add(*v)
	}
}

func buildSeries(m map[int64]*Counts) []Point {
	out := make([]Point, 0, len(m))
	for ts, c := range m {
		out = append(out, Point{Time: time.Unix(ts, 0).UTC(), Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

func topPairs(m map[PairKey]*Counts, n int) []PairStat {
	out := make([]PairStat, 0, len(m))
	for k, c := range m {
		out = append(out, PairStat{PairKey: k, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		return out[i].TrafficType < out[j].TrafficType
	})
	return truncate(out, n)
}

func topNodes(m map[string]*nodeCounts, n int) []NodeStat {
	out := make([]NodeStat, 0, len(m))
	for k, c := range m {
		out = append(out, NodeStat{Node: k, Sent: c.Sent, Received: c.Received})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Bytes(), out[j].Bytes(); a != b {
			return a > b
		}
		return out[i].Node < out[j].Node
	})
	return truncate(out, n)
}

func topPorts(m map[PortKey]*Counts, n int) []PortStat {
	out := make([]PortStat, 0, len(m))
	for k, c := range m {
		out = append(out, PortStat{PortKey: k, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Transport < out[j].Transport
	})
	return truncate(out, n)
}

func topMatrix(m map[MatrixKey]*Counts) []MatrixCell {
	out := make([]MatrixCell, 0, len(m))
	for k, c := range m {
		out = append(out, MatrixCell{MatrixKey: k, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		return out[i].Dst < out[j].Dst
	})
	return truncate(out, MaxMatrixCellsReturned)
}

func topLabels(m map[string]*Counts, n int) []LabelStat {
	out := make([]LabelStat, 0, len(m))
	for k, c := range m {
		out = append(out, LabelStat{Label: k, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Counts.Bytes(), out[j].Counts.Bytes(); a != b {
			return a > b
		}
		return out[i].Label < out[j].Label
	})
	return truncate(out, n)
}

func truncate[T any](s []T, n int) []T {
	if n > 0 && len(s) > n {
		return s[:n]
	}
	return s
}

// Stats describes the store's own state, for the admin status surface.
//
// Backend names which implementation answered, so the page can say "this is a
// bounded in-memory ring that dies with the process" or "this is on disk and
// survives a restart" rather than leaving the operator to infer it. It is
// additive to the versioned /api contract (#323): every field is optional
// except Kind, which memory fills in as BackendMemory.
type Stats struct {
	Buckets      int       `json:"buckets"`
	Capacity     int       `json:"capacity"`
	Observations int64     `json:"observations"`
	Truncated    int64     `json:"truncated"`
	Earliest     time.Time `json:"earliest"`
	Latest       time.Time `json:"latest"`
	Backend      Backend   `json:"backend"`
}

// Stats returns the store's current state.
func (m *Memory) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := Stats{
		Buckets:      len(m.buckets),
		Capacity:     m.capacity,
		Observations: m.recorded,
		Truncated:    m.dropped,
		Backend:      Backend{Kind: BackendMemory, Healthy: true},
	}
	for _, b := range m.buckets {
		if s.Earliest.IsZero() || b.start.Before(s.Earliest) {
			s.Earliest = b.start
		}
		if b.start.After(s.Latest) {
			s.Latest = b.start
		}
	}
	return s
}
