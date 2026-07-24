// Package aclpolicy compiles a Tailscale network policy and decides whether an
// observed connection is explained by it.
//
// It exists so the admin flow view can reconcile what a tailnet's policy ALLOWS
// against what it actually CARRIED. That is a security-adjacent readout, so the
// design is built around one rule: never state more than the data supports.
//
// Three properties follow, and must be preserved:
//
//   - Matching is THREE-VALUED. A selector matches, definitely does not match,
//     or cannot be decided (an undeclared group, a VIP service, an endpoint
//     carrying no identity). A connection is reported as unexplained only when
//     EVERY rule definitively fails. Collapsing "unknown" into "no" is what
//     would turn this from a diagnostic into a source of confident false alarms.
//   - Return traffic is matched in REVERSE. Flow logs report both halves of a
//     connection, but a policy governs only the direction it was established in.
//     On a live 3h capture, 37% of connections were explained only by the
//     reversed tuple; treating them as unexplained would have buried the real
//     findings under thousands of false ones.
//   - It never fails open. An empty or unparseable policy permits nothing.
//
// Only `accept` rules are evaluated: `grants` (the current syntax) and the
// legacy `acls` list. The `ssh`, `nodeAttrs`, `postures` and `autoApprovers`
// sections govern other things entirely and are out of scope.
package aclpolicy

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"github.com/tailscale/hujson"
)

// Verdict is the outcome of evaluating one connection against the policy.
type Verdict uint8

const (
	// Undetermined means the policy could not be fully applied — not that the
	// connection is allowed, and not that it is unexplained.
	Undetermined Verdict = iota
	// Permitted means at least one rule definitively covers the connection.
	Permitted
	// NoRule means every rule definitively fails to cover it.
	NoRule
)

func (v Verdict) String() string {
	switch v {
	case Permitted:
		return "permitted"
	case NoRule:
		return "no_rule"
	default:
		return "undetermined"
	}
}

// Directory carries the identity facts a policy REFERENCES but does not
// CONTAIN: which login holds which tailnet role. Without it every autogroup
// over people is undecidable, which the evaluator reports honestly rather than
// guessing.
type Directory struct {
	// Roles maps a login name to its tailnet role ("owner", "admin", "member",
	// …), as the users collector reports it.
	Roles map[string]string
}

// Endpoint is one side of an observed connection.
type Endpoint struct {
	// User is the login the device belongs to; empty on a tag-owned machine.
	User string
	// Tags are the device's ACL tags; empty on a user-owned device.
	Tags []string
	// Addr is the address seen on the wire. Invalid when the record did not
	// carry it, or when the PII filter removed it.
	Addr netip.Addr
}

// identified reports whether anything is known about this endpoint. Nothing
// known means no identity-keyed selector can be decided for it.
func (e Endpoint) identified() bool {
	return e.User != "" || len(e.Tags) > 0 || e.Addr.IsValid()
}

// Conn is one observed connection, as the flow processor has it.
type Conn struct {
	Src, Dst Endpoint
	// Proto is the lowercase transport name ("tcp", "udp", "icmp", …).
	Proto string
	// DstPort/SrcPort are valid only when the corresponding Has flag is set;
	// ICMP and truncated records carry none.
	DstPort    uint16
	HasPort    bool
	SrcPort    uint16
	HasSrcPort bool
	// IsExit marks traffic leaving the tailnet through an exit node, which is
	// the only thing autogroup:internet describes.
	IsExit bool
}

// reversed returns the connection as its return half would appear, so a rule
// written for the establishing direction can be matched against it.
func (c Conn) reversed() Conn {
	return Conn{
		Src: c.Dst, Dst: c.Src,
		Proto:   c.Proto,
		DstPort: c.SrcPort, HasPort: c.HasSrcPort,
		SrcPort: c.DstPort, HasSrcPort: c.HasPort,
		IsExit: c.IsExit,
	}
}

// Result is the outcome of one evaluation.
type Result struct {
	Verdict Verdict
	// Rule indexes into Rules(); -1 when nothing matched.
	Rule int
	// Reversed reports that the match was against the return direction.
	Reversed bool
	// Reason explains an Undetermined verdict, for display.
	Reason string
}

// Rule is one evaluable accept rule.
type Rule struct {
	// Kind is "grant" or "acl".
	Kind string
	// Source is the compact original text, for display next to a finding.
	Source string

	src   []selector
	dst   []selector
	ports []portSpec
}

// Policy is a compiled, evaluable policy. It is immutable after Compile and
// safe for concurrent use, so it can be swapped in atomically when the ACL
// changes and read from the emit path without locking.
type Policy struct {
	rules []Rule
}

// Rules returns the compiled rules in document order. The index is stable and
// is what an "exercised" report joins on.
func (p *Policy) Rules() []Rule { return p.rules }

// tri is a three-valued match outcome.
type tri uint8

const (
	triNo tri = iota
	triYes
	triUnknown
)

// ---------------------------------------------------------------- compiling

// document is the subset of a policy this package evaluates.
type document struct {
	Groups map[string][]string `json:"groups"`
	Hosts  map[string]string   `json:"hosts"`
	IPSets map[string][]string `json:"ipsets"`
	Grants []grant             `json:"grants"`
	ACLs   []legacyACL         `json:"acls"`
}

type grant struct {
	Src []string `json:"src"`
	Dst []string `json:"dst"`
	IP  []string `json:"ip"`
}

type legacyACL struct {
	Action string   `json:"action"`
	Src    []string `json:"src"`
	Dst    []string `json:"dst"`
}

// compileCtx carries what selectors need beyond their own text.
type compileCtx struct {
	groups map[string][]string
	hosts  map[string]string
	ipsets map[string][]netip.Prefix
	dir    Directory
}

// Compile parses a HuJSON policy document into an evaluable Policy. It fails
// rather than degrading on a malformed document: half a policy would produce
// confident, wrong findings.
func Compile(doc []byte, dir Directory) (*Policy, error) {
	std, err := hujson.Standardize(doc)
	if err != nil {
		return nil, fmt.Errorf("standardize policy: %w", err)
	}
	var d document
	if err := json.Unmarshal(std, &d); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}

	ctx := compileCtx{
		groups: d.Groups,
		hosts:  d.Hosts,
		ipsets: compileIPSets(d.IPSets),
		dir:    dir,
	}

	p := &Policy{}
	for _, g := range d.Grants {
		ports := g.IP
		if len(ports) == 0 {
			// A grant with no ip: covers no traffic; keep it so it can be
			// reported as unexercised rather than silently dropped.
			ports = nil
		}
		p.rules = append(p.rules, Rule{
			Kind:   "grant",
			Source: compactJSON(g),
			src:    compileSelectors(g.Src, ctx),
			dst:    compileSelectors(g.Dst, ctx),
			ports:  compilePorts(ports),
		})
	}
	for _, a := range d.ACLs {
		// Tailscale's acls list has no deny form; anything else is not an
		// accept rule and must not be treated as one.
		if a.Action != "accept" {
			continue
		}
		// Legacy destinations carry the port on the string itself.
		dsts := make([]string, 0, len(a.Dst))
		ports := make([]string, 0, len(a.Dst))
		for _, d := range a.Dst {
			host, port := splitLegacyDst(d)
			dsts = append(dsts, host)
			ports = append(ports, port)
		}
		p.rules = append(p.rules, Rule{
			Kind:   "acl",
			Source: compactJSON(a),
			src:    compileSelectors(a.Src, ctx),
			dst:    compileSelectors(dsts, ctx),
			ports:  compilePorts(ports),
		})
	}
	return p, nil
}

func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// splitLegacyDst splits "host:port" from a legacy acls destination, handling the
// bracketed form an IPv6 literal takes.
func splitLegacyDst(s string) (host, port string) {
	if strings.HasPrefix(s, "[") {
		if end := strings.LastIndex(s, "]"); end >= 0 {
			host = s[1:end]
			rest := s[end+1:]
			return host, strings.TrimPrefix(rest, ":")
		}
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, "*"
}

func compileIPSets(in map[string][]string) map[string][]netip.Prefix {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]netip.Prefix, len(in))
	for name, entries := range in {
		var pfx []netip.Prefix
		for _, e := range entries {
			// Entries are written as directives, e.g. "add 10.0.0.0/24".
			e = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(e), "add "))
			if p, ok := parsePrefix(e); ok {
				pfx = append(pfx, p)
			}
		}
		out[name] = pfx
	}
	return out
}

// parsePrefix accepts a CIDR or a bare address, which stands for a host route.
func parsePrefix(s string) (netip.Prefix, bool) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p, err == nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(a, a.BitLen()), true
}
