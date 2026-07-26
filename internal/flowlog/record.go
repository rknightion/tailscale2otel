// Package flowlog defines the Tailscale network flow log record types and (in
// processor.go) the conversion to OTEL metrics and logs. Both the polling
// collector and the streaming receiver decode into these types and feed the
// same processor.
package flowlog

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/enrich"
)

// NetworkResponse is the GET /tailnet/{tailnet}/logging/network response body.
type NetworkResponse struct {
	Logs []FlowLog `json:"logs"`
}

// FlowLog is one node's flow record for a capture window.
type FlowLog struct {
	Logged          time.Time          `json:"logged"`
	NodeID          string             `json:"nodeId"`
	Start           time.Time          `json:"start"`
	End             time.Time          `json:"end"`
	VirtualTraffic  []ConnectionCounts `json:"virtualTraffic"`
	SubnetTraffic   []ConnectionCounts `json:"subnetTraffic"`
	ExitTraffic     []ConnectionCounts `json:"exitTraffic"`
	PhysicalTraffic []ConnectionCounts `json:"physicalTraffic"`
	// SrcNode and DstNodes are the identities of the record's endpoints, embedded
	// by the control plane in the record itself. A live capture found srcNode on
	// 100% of records and dstNodes on 99%, so this is the primary enrichment
	// source: it makes name resolution independent of the devices collector, and
	// carries per-flow user/tags/os that no other API surface provides per flow.
	SrcNode  *NodeRef  `json:"srcNode,omitempty"`
	DstNodes []NodeRef `json:"dstNodes,omitempty"`
}

// NodeRef is a node identity embedded in a flow log record. Every field is
// optional and varies per node: srcNode entries carry no os, and user is absent
// on tag-owned nodes. Decode defensively — never assume a field is present.
type NodeRef struct {
	NodeID    string   `json:"nodeId"`
	Name      string   `json:"name"`      // MagicDNS FQDN, e.g. "laptop.tail1a2b.ts.net"
	Addresses []string `json:"addresses"` // tailnet addresses, IPv4 and IPv6
	Tags      []string `json:"tags"`
	User      string   `json:"user"`
	OS        string   `json:"os"`
}

// Hostname is the short display name: the first label of the MagicDNS FQDN.
// Empty when the ref carries no name.
func (n NodeRef) Hostname() string {
	if i := strings.IndexByte(n.Name, '.'); i > 0 {
		return n.Name[:i]
	}
	return n.Name
}

// Addrs parses the ref's addresses, silently dropping any that do not parse —
// an unparseable address is not worth failing an otherwise usable identity for.
func (n NodeRef) Addrs() []netip.Addr {
	if len(n.Addresses) == 0 {
		return nil
	}
	out := make([]netip.Addr, 0, len(n.Addresses))
	for _, s := range n.Addresses {
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// deviceMeta converts the ref to the cache's normalized shape. Returns false
// when the ref has no node ID, which makes it unusable as a cache entry.
func (n NodeRef) deviceMeta() (enrich.DeviceMeta, bool) {
	if n.NodeID == "" {
		return enrich.DeviceMeta{}, false
	}
	return enrich.DeviceMeta{
		NodeID:   n.NodeID,
		Name:     n.Name,
		Hostname: n.Hostname(),
		OS:       n.OS,
		User:     n.User,
		Tags:     n.Tags,
		Addrs:    n.Addrs(),
	}, true
}

// nodeRefs returns every non-empty identity embedded in the record, source
// first. Used to seed the device cache before the record's connections are
// processed, so a record enriches itself.
func (f FlowLog) nodeRefs() []enrich.DeviceMeta {
	out := make([]enrich.DeviceMeta, 0, len(f.DstNodes)+1)
	if f.SrcNode != nil {
		if m, ok := f.SrcNode.deviceMeta(); ok {
			out = append(out, m)
		}
	}
	for i := range f.DstNodes {
		if m, ok := f.DstNodes[i].deviceMeta(); ok {
			out = append(out, m)
		}
	}
	return out
}

// refByAddr returns the embedded identity owning addr, if the record carries
// one. Used to attach per-flow identity attributes to the connection's two
// endpoints without a cache round-trip.
func (f FlowLog) refByAddr(addr string) *NodeRef {
	if addr == "" {
		return nil
	}
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return nil
	}
	if f.SrcNode != nil && slices.Contains(f.SrcNode.Addrs(), a) {
		return f.SrcNode
	}
	for i := range f.DstNodes {
		if slices.Contains(f.DstNodes[i].Addrs(), a) {
			return &f.DstNodes[i]
		}
	}
	return nil
}

// ConnectionCounts is bidirectional traffic for a single 5-tuple in a window.
// Src and Dst are "addr:port" strings. Proto is the IANA protocol number (e.g.
// 6=tcp, 17=udp); the API omits it for some physical-traffic entries. Proto
// remains an int for existing processing, while protoPresent preserves the
// wire-level distinction between an omitted field and an explicit protocol 0.
type ConnectionCounts struct {
	Proto   int    `json:"proto"`
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	TxPkts  int64  `json:"txPkts"`
	TxBytes int64  `json:"txBytes"`
	RxPkts  int64  `json:"rxPkts"`
	RxBytes int64  `json:"rxBytes"`

	protoPresent bool
}

// UnmarshalJSON preserves protocol-field presence without changing the public
// numeric surface. Protocol 0 is a valid IANA value, so treating Proto == 0 as
// synonymous with omission would make observed-completeness telemetry lie.
func (c *ConnectionCounts) UnmarshalJSON(data []byte) error {
	var wire struct {
		Proto   *int   `json:"proto"`
		Src     string `json:"src"`
		Dst     string `json:"dst"`
		TxPkts  int64  `json:"txPkts"`
		TxBytes int64  `json:"txBytes"`
		RxPkts  int64  `json:"rxPkts"`
		RxBytes int64  `json:"rxBytes"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*c = ConnectionCounts{
		Src:     wire.Src,
		Dst:     wire.Dst,
		TxPkts:  wire.TxPkts,
		TxBytes: wire.TxBytes,
		RxPkts:  wire.RxPkts,
		RxBytes: wire.RxBytes,
	}
	if wire.Proto != nil {
		c.Proto = *wire.Proto
		c.protoPresent = true
	}
	return nil
}

func (c ConnectionCounts) protocolPresent() bool {
	return c.protoPresent || c.Proto != 0
}
