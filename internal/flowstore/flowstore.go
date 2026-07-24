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
	SrcAddr    string
	DstAddr    string
	SrcNode    string
	DstNode    string
	DstPort    string
	DstService string
	SrcUser    string
	DstUser    string
	SrcTags    string
	DstTags    string
	SrcOS      string
	DstOS      string
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
	Rule int
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
	rules       map[int]*Counts

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

func newBucket(start time.Time) *bucket {
	return &bucket{
		start:        start,
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
		rules:        map[int]*Counts{},
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
			if len(b.pairs) >= MaxPairsPerBucket {
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

	addLabel(b.transports, o.Transport, o.Counts, MaxLabelsPerKind)
	addLabel(b.trafficTypes, o.TrafficType, o.Counts, MaxLabelsPerKind)
	addLabel(b.users, o.SrcUser, o.Counts, MaxLabelsPerKind)
	if o.DstUser != o.SrcUser {
		addLabel(b.users, o.DstUser, o.Counts, MaxLabelsPerKind)
	}
	addLabel(b.oses, o.SrcOS, o.Counts, MaxLabelsPerKind)
	if o.DstOS != o.SrcOS {
		addLabel(b.oses, o.DstOS, o.Counts, MaxLabelsPerKind)
	}

	// Tags are a SET per endpoint, so the breakdown is per individual tag: a
	// device tagged "tag:servers,tag:prod" must be visible under both, not under
	// a joined label that matches nothing an operator would search for. A tag on
	// both endpoints describes one flow, so it counts once.
	srcTags, dstTags := splitTags(o.SrcTags), splitTags(o.DstTags)
	for _, tag := range srcTags {
		addLabel(b.tags, tag, o.Counts, MaxLabelsPerKind)
	}
	for _, tag := range dstTags {
		if !slices.Contains(srcTags, tag) {
			addLabel(b.tags, tag, o.Counts, MaxLabelsPerKind)
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
		if len(b.ports) >= MaxPortsPerBucket {
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
	addLabel(b.paths, o.Path, o.Counts, MaxLabelsPerKind)
	// A relayed connection whose region was unreadable still counts as relayed
	// above; it simply names no relay here.
	if o.Path == PathDERP {
		addLabel(b.derpRegions, o.DERPRegion, o.Counts, MaxLabelsPerKind)
	}

	k := peerPathKey{peer: peerIdentity(o.SrcNode, o.SrcAddr), path: o.Path}
	e, ok := b.peerPaths[k]
	if !ok {
		if len(b.peerPaths) >= MaxPeerPathsPerBucket {
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
	addLabel(b.verdicts, verdict, o.Counts, MaxLabelsPerKind)

	switch {
	case o.Verdict == VerdictPermitted && o.Rule >= 0:
		// Only a rule that PERMITTED something has been exercised, and only a
		// permitted verdict carries a meaningful rule index — Rule is otherwise
		// -1, or the zero value of an observation nothing evaluated.
		e, ok := b.rules[o.Rule]
		if !ok {
			if len(b.rules) >= MaxRulesPerBucket {
				b.dropped++
				return
			}
			e = &Counts{}
			b.rules[o.Rule] = e
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
		// Tailscale writes ":0" for the destination of a protocol that has no
		// ports — every one of 3,427 ICMP entries in a live capture. Carrying it
		// through would render "icmp/0", which is not a port and not a rule anyone
		// could write.
		Port: portOrNone(o.DstPort),
	}
	e, ok := b.unexplained[k]
	if !ok {
		if len(b.unexplained) >= MaxUnexplainedPerBucket {
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

// portOrNone reads "0" as "this protocol has no ports". See addUnexplained.
func portOrNone(port string) string {
	if port == "0" {
		return ""
	}
	return port
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
				if len(m) >= MaxMatrixCellsPerBucket {
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
		if len(b.nodes) >= MaxNodesPerBucket {
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
	mu       sync.Mutex
	buckets  map[int64]*bucket
	capacity int
	now      func() time.Time

	// recent is a fixed-size circular buffer of the newest raw connections.
	// next is where the following write lands; filled counts how much of the
	// buffer is live before it first wraps.
	recent []Recent
	next   int
	filled int

	recorded int64
	dropped  int64
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
	// The policy's reading of this one connection, so the list an operator drills
	// into after seeing a relationship in the aggregate can show which
	// connections were unexplained and which rule covered the rest.
	Verdict  string `json:"verdict,omitempty"`
	Reversed bool   `json:"reversed,omitempty"`
	Rule     int    `json:"rule"`
	// How this one connection was carried, on physical traffic only, so the list
	// behind a relayed peer shows which of its connections were the relayed ones.
	Path       string `json:"path,omitempty"`
	DERPRegion string `json:"derp_region,omitempty"`
	Counts     Counts `json:"counts"`
}

// NewMemory returns a store retaining at most capacity one-minute buckets.
// A non-positive capacity selects six hours.
func NewMemory(capacity int) *Memory {
	if capacity <= 0 {
		capacity = int((6 * time.Hour) / Resolution)
	}
	return &Memory{
		buckets:  map[int64]*bucket{},
		capacity: capacity,
		recent:   make([]Recent, MaxRecent),
		now:      time.Now,
	}
}

// bucketKey is the minute-aligned Unix timestamp a time falls in.
func bucketKey(t time.Time) int64 {
	return t.UTC().Truncate(Resolution).Unix()
}

// Record accumulates one observation. Observations with no timestamp are
// stamped with the current time: a record we cannot place in time is still real
// traffic, and dropping it would understate totals.
func (m *Memory) Record(o Observation) {
	if o.Time.IsZero() {
		o.Time = m.now()
	}
	key := bucketKey(o.Time)

	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.buckets[key]
	if !ok {
		b = newBucket(time.Unix(key, 0).UTC())
		m.buckets[key] = b
		m.evictLocked()
	}
	before := b.dropped
	b.record(o)
	m.recordRecentLocked(o)
	m.recorded++
	m.dropped += b.dropped - before
}

// recordRecentLocked appends o to the raw-connection ring, overwriting the
// oldest entry once it is full. Overwriting is not a drop — the ring is a
// deliberate window on the newest activity, not a sample of everything — so it
// does not count toward Truncated.
func (m *Memory) recordRecentLocked(o Observation) {
	m.recent[m.next] = Recent{
		Time:        o.Time,
		TrafficType: o.TrafficType,
		Transport:   o.Transport,
		SrcAddr:     o.SrcAddr,
		DstAddr:     o.DstAddr,
		SrcNode:     o.SrcNode,
		DstNode:     o.DstNode,
		DstService:  o.DstService,
		SrcUser:     o.SrcUser,
		DstUser:     o.DstUser,
		SrcTags:     o.SrcTags,
		DstTags:     o.DstTags,
		SrcOS:       o.SrcOS,
		DstOS:       o.DstOS,
		Verdict:     o.Verdict,
		Reversed:    o.Reversed,
		Rule:        o.Rule,
		Path:        o.Path,
		DERPRegion:  o.DERPRegion,
		Counts:      o.Counts,
	}
	m.next = (m.next + 1) % len(m.recent)
	m.filled = min(m.filled+1, len(m.recent))
}

// Recent returns up to limit of the most recently recorded connections, newest
// first. A non-positive limit returns nothing rather than everything: the caller
// is always a UI page size, and defaulting an unset one to "all" is the wrong
// direction to fail in.
func (m *Memory) Recent(limit int) []Recent {
	if limit <= 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	limit = min(limit, m.filled)
	out := make([]Recent, 0, limit)
	// Walk backwards from the most recent write, wrapping at the start.
	for i := range limit {
		idx := (m.next - 1 - i + len(m.recent)) % len(m.recent)
		out = append(out, m.recent[idx])
	}
	return out
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

// RuleStat is how much traffic one policy rule permitted. Rule indexes the
// compiled policy's rule list; a rule absent from the result permitted nothing
// in the window, which is the whole point of reporting it.
type RuleStat struct {
	Rule   int    `json:"rule"`
	Counts Counts `json:"counts"`
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
	rules := map[int]*Counts{}
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
func allRules(m map[int]*Counts) []RuleStat {
	out := make([]RuleStat, 0, len(m))
	for k, c := range m {
		out = append(out, RuleStat{Rule: k, Counts: *c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rule < out[j].Rule })
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
type Stats struct {
	Buckets      int       `json:"buckets"`
	Capacity     int       `json:"capacity"`
	Observations int64     `json:"observations"`
	Truncated    int64     `json:"truncated"`
	Earliest     time.Time `json:"earliest"`
	Latest       time.Time `json:"latest"`
}

// Stats returns the store's current state.
func (m *Memory) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := Stats{Buckets: len(m.buckets), Capacity: m.capacity, Observations: m.recorded, Truncated: m.dropped}
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
