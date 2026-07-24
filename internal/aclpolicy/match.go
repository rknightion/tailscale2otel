package aclpolicy

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// selector matches one side of a connection. Every implementation returns a
// three-valued answer so an undecidable selector can never be mistaken for a
// definite "no".
type selector interface {
	// match reports whether ep satisfies the selector. other is the connection's
	// opposite endpoint, which autogroup:self needs; c supplies connection-level
	// facts such as whether the traffic left the tailnet.
	match(ep, other Endpoint, c Conn) tri
	// why explains an unknown outcome, for display.
	why() string
}

func compileSelectors(in []string, ctx compileCtx) []selector {
	out := make([]selector, 0, len(in))
	for _, s := range in {
		out = append(out, compileSelector(strings.TrimSpace(s), ctx))
	}
	return out
}

func compileSelector(s string, ctx compileCtx) selector {
	switch {
	case s == "*":
		return anySelector{}
	case strings.HasPrefix(s, "tag:"):
		return tagSelector{tag: s}
	case strings.HasPrefix(s, "group:"):
		members, ok := ctx.groups[s]
		if !ok {
			return unknownSelector{reason: "group " + s + " is referenced but not declared in the policy"}
		}
		return userSetSelector{users: members, label: s}
	case strings.HasPrefix(s, "ipset:"):
		pfx, ok := ctx.ipsets[s]
		if !ok {
			return unknownSelector{reason: "ipset " + s + " is referenced but not declared in the policy"}
		}
		return prefixSelector{prefixes: pfx}
	case strings.HasPrefix(s, "svc:"):
		// VIP services resolve through the services collector, which this
		// package deliberately does not depend on.
		return unknownSelector{reason: "VIP service " + s + " cannot be resolved to addresses here"}
	case strings.HasPrefix(s, "autogroup:"):
		return compileAutogroup(strings.TrimPrefix(s, "autogroup:"), ctx)
	case strings.Contains(s, "@"):
		return userSetSelector{users: []string{s}, label: s}
	}
	// A host alias resolves through the hosts map before being read as an
	// address.
	if v, ok := ctx.hosts[s]; ok {
		s = v
	}
	if p, ok := parsePrefix(s); ok {
		return prefixSelector{prefixes: []netip.Prefix{p}}
	}
	return unknownSelector{reason: "selector " + s + " is not a form this evaluator understands"}
}

func compileAutogroup(name string, ctx compileCtx) selector {
	switch name {
	case "internet":
		return internetSelector{}
	case "self":
		return selfSelector{}
	case "owner":
		return roleSelector{roles: []string{"owner"}, label: "autogroup:owner", dir: ctx.dir}
	case "admin":
		// The Owner also holds administrative rights, so the Owner satisfies
		// autogroup:admin. This is a modeling choice the docs state, not a fact
		// the API reports.
		return roleSelector{roles: []string{"owner", "admin"}, label: "autogroup:admin", dir: ctx.dir}
	case "member":
		return memberSelector{dir: ctx.dir}
	default:
		// autogroup:shared, :tagged, :nonroot and anything Tailscale adds later
		// describe things this evaluator cannot observe.
		return unknownSelector{reason: "autogroup:" + name + " cannot be evaluated from flow data"}
	}
}

// ------------------------------------------------------------- selectors

type anySelector struct{}

func (anySelector) match(Endpoint, Endpoint, Conn) tri { return triYes }
func (anySelector) why() string                        { return "" }

type unknownSelector struct{ reason string }

func (unknownSelector) match(Endpoint, Endpoint, Conn) tri { return triUnknown }
func (u unknownSelector) why() string                      { return u.reason }

type tagSelector struct{ tag string }

func (t tagSelector) match(ep, _ Endpoint, _ Conn) tri {
	if slices.Contains(ep.Tags, t.tag) {
		return triYes
	}
	// An endpoint we know is user-owned definitively lacks every tag; one we
	// know nothing about might have any.
	if ep.User != "" || len(ep.Tags) > 0 {
		return triNo
	}
	if ep.Addr.IsValid() {
		// An address with no node behind it is not a tailnet device, so it
		// carries no tags.
		return triNo
	}
	return triUnknown
}
func (tagSelector) why() string { return "" }

// userSetSelector matches a device owned by one of a set of logins — a named
// user, or the members of a declared group.
type userSetSelector struct {
	users []string
	label string
}

func (u userSetSelector) match(ep, _ Endpoint, _ Conn) tri {
	if ep.User == "" {
		// Tag-owned and unidentified endpoints: a tagged device definitively has
		// no user; an endpoint we know nothing about is undecidable.
		if len(ep.Tags) > 0 || ep.Addr.IsValid() {
			return triNo
		}
		return triUnknown
	}
	if slices.Contains(u.users, ep.User) {
		return triYes
	}
	return triNo
}
func (userSetSelector) why() string { return "" }

// roleSelector matches a device owned by a login holding one of the given
// tailnet roles.
type roleSelector struct {
	roles []string
	label string
	dir   Directory
}

func (r roleSelector) match(ep, _ Endpoint, _ Conn) tri {
	if ep.User == "" {
		if len(ep.Tags) > 0 || ep.Addr.IsValid() {
			return triNo
		}
		return triUnknown
	}
	role, ok := r.dir.Roles[ep.User]
	if !ok {
		// The policy names a role we have no directory entry for. Saying "no"
		// would manufacture a finding out of missing data.
		return triUnknown
	}
	if slices.Contains(r.roles, role) {
		return triYes
	}
	return triNo
}
func (roleSelector) why() string {
	return "the tailnet role of an endpoint's owner is not known (is the users collector enabled?)"
}

// memberSelector matches a device owned by any person, as opposed to a machine
// that belongs to a tag.
type memberSelector struct{ dir Directory }

func (m memberSelector) match(ep, _ Endpoint, _ Conn) tri {
	if len(ep.Tags) > 0 {
		return triNo // a tag-owned machine is not a member
	}
	if ep.User == "" {
		if ep.Addr.IsValid() {
			return triNo
		}
		return triUnknown
	}
	return triYes
}
func (memberSelector) why() string { return "" }

// selfSelector matches the devices of the SAME person as the other endpoint. It
// is the one selector whose meaning depends on both sides of the connection.
type selfSelector struct{}

func (selfSelector) match(ep, other Endpoint, _ Conn) tri {
	if ep.User == "" || other.User == "" {
		// One side is a machine, or we do not know: either way this is not a
		// person reaching their own devices.
		if (ep.User == "" && len(ep.Tags) > 0) || (other.User == "" && len(other.Tags) > 0) {
			return triNo
		}
		if ep.identified() && other.identified() {
			return triNo
		}
		return triUnknown
	}
	if ep.User == other.User {
		return triYes
	}
	return triNo
}
func (selfSelector) why() string { return "" }

// internetSelector matches traffic leaving the tailnet through an exit node,
// which is the only thing autogroup:internet covers.
type internetSelector struct{}

func (internetSelector) match(_, _ Endpoint, c Conn) tri {
	if c.IsExit {
		return triYes
	}
	return triNo
}
func (internetSelector) why() string { return "" }

// prefixSelector matches an endpoint by address: a literal, a CIDR, a host
// alias, or a named ipset.
type prefixSelector struct{ prefixes []netip.Prefix }

func (p prefixSelector) match(ep, _ Endpoint, _ Conn) tri {
	if !ep.Addr.IsValid() {
		// Without an address there is nothing to compare. This is the case the
		// PII filter creates when tailscale_ips is disabled.
		return triUnknown
	}
	for _, pfx := range p.prefixes {
		if pfx.Contains(ep.Addr) {
			return triYes
		}
	}
	return triNo
}
func (prefixSelector) why() string { return "" }

// matchSide reduces a rule's list of selectors for one side: any definite match
// wins, otherwise an undecidable one makes the whole side undecidable.
func matchSide(sels []selector, ep, other Endpoint, c Conn) (tri, string) {
	if len(sels) == 0 {
		return triNo, ""
	}
	var reason string
	unknown := false
	for _, s := range sels {
		switch s.match(ep, other, c) {
		case triYes:
			return triYes, ""
		case triUnknown:
			unknown = true
			if reason == "" {
				reason = s.why()
			}
		}
	}
	if unknown {
		if reason == "" {
			reason = "an endpoint carries no identity to match on"
		}
		return triUnknown, reason
	}
	return triNo, ""
}

// ----------------------------------------------------------------- ports

// portSpec is one entry of a rule's port list.
type portSpec struct {
	// any covers every protocol AND every port ("*"). This is deliberately
	// distinct from a port range over tcp/udp: treating "*" as tcp/udp-only made
	// every ICMP flow look unexplained in an early prototype.
	any bool
	// proto is the required transport, or "" for a BARE port number, which
	// Tailscale reads as tcp+udp.
	proto  string
	lo, hi uint16
	// allPorts marks "proto:*", which covers a protocol regardless of port and
	// so also covers portless protocols like ICMP.
	allPorts bool
}

func compilePorts(specs []string) []portSpec {
	out := make([]portSpec, 0, len(specs))
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if s == "*" {
			out = append(out, portSpec{any: true})
			continue
		}
		proto, rest := "", s
		if i := strings.Index(s, ":"); i >= 0 {
			proto, rest = strings.ToLower(s[:i]), s[i+1:]
		}
		if rest == "*" {
			out = append(out, portSpec{proto: proto, allPorts: true})
			continue
		}
		lo, hi, ok := parsePortRange(rest)
		if !ok {
			continue
		}
		out = append(out, portSpec{proto: proto, lo: lo, hi: hi})
	}
	return out
}

func parsePortRange(s string) (lo, hi uint16, ok bool) {
	if i := strings.Index(s, "-"); i >= 0 {
		a, err1 := strconv.ParseUint(s[:i], 10, 16)
		b, err2 := strconv.ParseUint(s[i+1:], 10, 16)
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return uint16(a), uint16(b), true
	}
	v, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, 0, false
	}
	return uint16(v), uint16(v), true
}

// matchPorts reduces a rule's port list against the connection's protocol and
// destination port.
func matchPorts(specs []portSpec, c Conn) tri {
	unknown := false
	for _, p := range specs {
		switch {
		case p.any:
			return triYes
		case p.proto != "" && p.proto != c.Proto:
			continue
		case p.proto == "" && c.Proto != "tcp" && c.Proto != "udp":
			// A bare port number cannot describe a protocol that has no ports.
			continue
		case p.allPorts:
			return triYes
		case !c.HasPort:
			// The rule names specific ports and the record carries none, so this
			// rule can be neither applied nor ruled out.
			unknown = true
		case c.DstPort >= p.lo && c.DstPort <= p.hi:
			return triYes
		}
	}
	if unknown {
		return triUnknown
	}
	return triNo
}

// ------------------------------------------------------------- evaluation

// Evaluate decides whether the policy explains c.
//
// It tries the connection as observed, then — only if that did not definitively
// match — as its return half, because a policy governs the direction a
// connection was established in while flow logs report both.
func (p *Policy) Evaluate(c Conn) Result {
	res := p.evaluateOneWay(c)
	if res.Verdict == Permitted {
		return res
	}
	back := p.evaluateOneWay(c.reversed())
	if back.Verdict == Permitted {
		back.Reversed = true
		return back
	}
	// Neither direction is permitted. An undecidable result in either direction
	// means we cannot claim the connection is unexplained.
	if res.Verdict == Undetermined {
		return res
	}
	if back.Verdict == Undetermined {
		back.Reversed = true
		return back
	}
	return res
}

// evaluateOneWay applies every rule to c in the direction given.
func (p *Policy) evaluateOneWay(c Conn) Result {
	unknown := false
	reason := ""
	note := func(r string) {
		unknown = true
		if reason == "" {
			reason = r
		}
	}

	for i := range p.rules {
		r := &p.rules[i]

		srcT, srcWhy := matchSide(r.src, c.Src, c.Dst, c)
		if srcT == triNo {
			continue
		}
		dstT, dstWhy := matchSide(r.dst, c.Dst, c.Src, c)
		if dstT == triNo {
			continue
		}
		portT := matchPorts(r.ports, c)
		if portT == triNo {
			continue
		}
		if srcT == triYes && dstT == triYes && portT == triYes {
			return Result{Verdict: Permitted, Rule: i}
		}
		// Something about this rule could not be decided, so the policy as a
		// whole cannot be said to lack a covering rule.
		switch {
		case srcWhy != "":
			note(srcWhy)
		case dstWhy != "":
			note(dstWhy)
		default:
			note("a rule names specific ports but the record carries none")
		}
	}

	if unknown {
		return Result{Verdict: Undetermined, Rule: -1, Reason: reason}
	}
	return Result{Verdict: NoRule, Rule: -1}
}
