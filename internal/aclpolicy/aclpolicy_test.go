package aclpolicy_test

import (
	"net/netip"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/aclpolicy"
)

// dir is the identity directory a real tailnet supplies from the users
// collector: who is the owner, who is an admin, who is an active member.
func dir() aclpolicy.Directory {
	return aclpolicy.Directory{Roles: map[string]string{
		"rob@example.com": "owner",
		"ada@example.com": "admin",
		"sam@example.com": "member",
	}}
}

func compile(t *testing.T, doc string) *aclpolicy.Policy {
	t.Helper()
	p, err := aclpolicy.Compile([]byte(doc), dir())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return p
}

func addr(s string) netip.Addr {
	a, _ := netip.ParseAddr(s)
	return a
}

// tagged/owned build the two kinds of endpoint a tailnet has: a machine that
// belongs to a tag, and a device that belongs to a person.
func tagged(ip string, tags ...string) aclpolicy.Endpoint {
	return aclpolicy.Endpoint{Tags: tags, Addr: addr(ip)}
}

func owned(ip, user string) aclpolicy.Endpoint {
	return aclpolicy.Endpoint{User: user, Addr: addr(ip)}
}

func tcp(src, dst aclpolicy.Endpoint, port uint16) aclpolicy.Conn {
	return aclpolicy.Conn{Src: src, Dst: dst, Proto: "tcp", DstPort: port, HasPort: true}
}

func verdictOf(t *testing.T, p *aclpolicy.Policy, c aclpolicy.Conn) aclpolicy.Verdict {
	t.Helper()
	return p.Evaluate(c).Verdict
}

// ---------------------------------------------------------------- selectors

func TestEvaluate_TagSelectors(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["tag:web"], "dst": ["tag:db"], "ip": ["tcp:5432"]}
	]}`)

	tests := []struct {
		name string
		conn aclpolicy.Conn
		want aclpolicy.Verdict
	}{
		{"exact match", tcp(tagged("100.64.0.1", "tag:web"), tagged("100.64.0.2", "tag:db"), 5432), aclpolicy.Permitted},
		{"wrong port", tcp(tagged("100.64.0.1", "tag:web"), tagged("100.64.0.2", "tag:db"), 5433), aclpolicy.NoRule},
		{"wrong source tag", tcp(tagged("100.64.0.1", "tag:app"), tagged("100.64.0.2", "tag:db"), 5432), aclpolicy.NoRule},
		{"one of several tags is enough", tcp(tagged("100.64.0.1", "tag:app", "tag:web"), tagged("100.64.0.2", "tag:db"), 5432), aclpolicy.Permitted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdictOf(t, p, tc.conn); got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluate_UserAndAutogroups(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["autogroup:owner"],  "dst": ["tag:prod"], "ip": ["*"]},
		{"src": ["autogroup:admin"],  "dst": ["tag:ci"],   "ip": ["tcp:22"]},
		{"src": ["autogroup:member"], "dst": ["tag:wiki"], "ip": ["tcp:443"]},
		{"src": ["sam@example.com"],  "dst": ["tag:lab"],  "ip": ["tcp:8080"]}
	]}`)

	tests := []struct {
		name string
		conn aclpolicy.Conn
		want aclpolicy.Verdict
	}{
		{"owner reaches prod", tcp(owned("100.64.0.1", "rob@example.com"), tagged("100.64.0.9", "tag:prod"), 443), aclpolicy.Permitted},
		{"member does not reach prod", tcp(owned("100.64.0.3", "sam@example.com"), tagged("100.64.0.9", "tag:prod"), 443), aclpolicy.NoRule},
		// The Owner is treated as holding the Admin role too; this is a modeling
		// choice the docs state, not a fact read from the API.
		{"owner counts as admin", tcp(owned("100.64.0.1", "rob@example.com"), tagged("100.64.0.8", "tag:ci"), 22), aclpolicy.Permitted},
		{"admin reaches ci", tcp(owned("100.64.0.2", "ada@example.com"), tagged("100.64.0.8", "tag:ci"), 22), aclpolicy.Permitted},
		{"member is not admin", tcp(owned("100.64.0.3", "sam@example.com"), tagged("100.64.0.8", "tag:ci"), 22), aclpolicy.NoRule},
		{"member reaches wiki", tcp(owned("100.64.0.3", "sam@example.com"), tagged("100.64.0.7", "tag:wiki"), 443), aclpolicy.Permitted},
		{"named user reaches lab", tcp(owned("100.64.0.3", "sam@example.com"), tagged("100.64.0.6", "tag:lab"), 8080), aclpolicy.Permitted},
		{"other user does not", tcp(owned("100.64.0.2", "ada@example.com"), tagged("100.64.0.6", "tag:lab"), 8080), aclpolicy.NoRule},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdictOf(t, p, tc.conn); got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

// A tag-owned machine is not a "member" — it belongs to a tag, not a person.
// Getting this backwards would silently permit machine traffic under a rule
// written for people.
func TestEvaluate_TaggedDeviceIsNotAMember(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["autogroup:member"], "dst": ["*"], "ip": ["*"]}
	]}`)

	if got := verdictOf(t, p, tcp(tagged("100.64.0.1", "tag:ci"), tagged("100.64.0.2", "tag:db"), 443)); got != aclpolicy.NoRule {
		t.Errorf("tagged source matched autogroup:member: %v", got)
	}
	if got := verdictOf(t, p, tcp(owned("100.64.0.3", "sam@example.com"), tagged("100.64.0.2", "tag:db"), 443)); got != aclpolicy.Permitted {
		t.Errorf("user-owned source did not match autogroup:member: %v", got)
	}
}

// autogroup:self is the only selector whose meaning depends on the OTHER
// endpoint: it means "devices belonging to the same person as the source".
func TestEvaluate_AutogroupSelf(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["autogroup:member"], "dst": ["autogroup:self"], "ip": ["*"]}
	]}`)

	same := tcp(owned("100.64.0.3", "sam@example.com"), owned("100.64.0.4", "sam@example.com"), 22)
	if got := verdictOf(t, p, same); got != aclpolicy.Permitted {
		t.Errorf("same-user devices = %v, want Permitted", got)
	}
	other := tcp(owned("100.64.0.3", "sam@example.com"), owned("100.64.0.5", "ada@example.com"), 22)
	if got := verdictOf(t, p, other); got != aclpolicy.NoRule {
		t.Errorf("cross-user devices = %v, want NoRule", got)
	}
}

// Exit traffic has no destination endpoint at all, so autogroup:internet is the
// only thing that can describe it.
func TestEvaluate_AutogroupInternet(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["*"], "dst": ["autogroup:internet"], "ip": ["*"]}
	]}`)

	exit := aclpolicy.Conn{Src: tagged("100.64.0.1", "tag:web"), Proto: "tcp", DstPort: 443, HasPort: true, IsExit: true}
	if got := verdictOf(t, p, exit); got != aclpolicy.Permitted {
		t.Errorf("exit traffic = %v, want Permitted", got)
	}
	// The same rule must not permit ordinary tailnet-internal traffic.
	inside := tcp(tagged("100.64.0.1", "tag:web"), tagged("100.64.0.2", "tag:db"), 443)
	if got := verdictOf(t, p, inside); got != aclpolicy.NoRule {
		t.Errorf("internal traffic matched autogroup:internet: %v", got)
	}
}

func TestEvaluate_AddressSelectors(t *testing.T) {
	p := compile(t, `{
		"hosts":  {"nas": "10.0.0.9"},
		"ipsets": {"ipset:lan": ["add 10.0.0.0/24"]},
		"grants": [
			{"src": ["tag:web"], "dst": ["10.0.50.4"],   "ip": ["tcp:5432"]},
			{"src": ["tag:web"], "dst": ["ipset:lan"],   "ip": ["tcp:80"]},
			{"src": ["tag:web"], "dst": ["nas"],         "ip": ["tcp:445"]},
			{"src": ["tag:web"], "dst": ["10.1.0.0/16"], "ip": ["tcp:8080"]}
		]}`)

	web := tagged("100.64.0.1", "tag:web")
	tests := []struct {
		name string
		conn aclpolicy.Conn
		want aclpolicy.Verdict
	}{
		{"bare ip", tcp(web, aclpolicy.Endpoint{Addr: addr("10.0.50.4")}, 5432), aclpolicy.Permitted},
		{"ipset member", tcp(web, aclpolicy.Endpoint{Addr: addr("10.0.0.77")}, 80), aclpolicy.Permitted},
		{"ipset non-member", tcp(web, aclpolicy.Endpoint{Addr: addr("10.9.0.77")}, 80), aclpolicy.NoRule},
		{"host alias", tcp(web, aclpolicy.Endpoint{Addr: addr("10.0.0.9")}, 445), aclpolicy.Permitted},
		{"cidr", tcp(web, aclpolicy.Endpoint{Addr: addr("10.1.2.3")}, 8080), aclpolicy.Permitted},
		{"outside cidr", tcp(web, aclpolicy.Endpoint{Addr: addr("10.2.2.3")}, 8080), aclpolicy.NoRule},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdictOf(t, p, tc.conn); got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

// ------------------------------------------------------------------- ports

func TestEvaluate_PortSpecs(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["tag:a"], "dst": ["tag:wild"],  "ip": ["*"]},
		{"src": ["tag:a"], "dst": ["tag:bare"],  "ip": ["53"]},
		{"src": ["tag:a"], "dst": ["tag:proto"], "ip": ["udp:123"]},
		{"src": ["tag:a"], "dst": ["tag:range"], "ip": ["tcp:1-21", "tcp:23-100"]},
		{"src": ["tag:a"], "dst": ["tag:icmp"],  "ip": ["icmp:*"]}
	]}`)

	src := tagged("100.64.0.1", "tag:a")
	conn := func(dstTag, proto string, port uint16, hasPort bool) aclpolicy.Conn {
		return aclpolicy.Conn{
			Src: src, Dst: tagged("100.64.0.2", dstTag),
			Proto: proto, DstPort: port, HasPort: hasPort,
		}
	}

	tests := []struct {
		name string
		conn aclpolicy.Conn
		want aclpolicy.Verdict
	}{
		// "*" is a wildcard over PROTOCOL as well as port. Treating it as tcp/udp
		// only made every ICMP flow look unexplained in a prototype run.
		{"star matches tcp", conn("tag:wild", "tcp", 443, true), aclpolicy.Permitted},
		{"star matches icmp", conn("tag:wild", "icmp", 0, false), aclpolicy.Permitted},
		{"star matches unknown proto", conn("tag:wild", "unknown", 0, false), aclpolicy.Permitted},

		// A BARE port number implies tcp+udp, and cannot describe a portless protocol.
		{"bare port over tcp", conn("tag:bare", "tcp", 53, true), aclpolicy.Permitted},
		{"bare port over udp", conn("tag:bare", "udp", 53, true), aclpolicy.Permitted},
		{"bare port does not match icmp", conn("tag:bare", "icmp", 0, false), aclpolicy.NoRule},
		{"bare port wrong number", conn("tag:bare", "tcp", 54, true), aclpolicy.NoRule},

		{"proto qualified", conn("tag:proto", "udp", 123, true), aclpolicy.Permitted},
		{"proto qualified wrong proto", conn("tag:proto", "tcp", 123, true), aclpolicy.NoRule},

		{"range low", conn("tag:range", "tcp", 1, true), aclpolicy.Permitted},
		{"range high", conn("tag:range", "tcp", 21, true), aclpolicy.Permitted},
		{"between ranges", conn("tag:range", "tcp", 22, true), aclpolicy.NoRule},
		{"second range", conn("tag:range", "tcp", 99, true), aclpolicy.Permitted},
		{"beyond ranges", conn("tag:range", "tcp", 101, true), aclpolicy.NoRule},

		{"icmp wildcard", conn("tag:icmp", "icmp", 0, false), aclpolicy.Permitted},
		{"icmp rule does not match tcp", conn("tag:icmp", "tcp", 80, true), aclpolicy.NoRule},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verdictOf(t, p, tc.conn); got != tc.want {
				t.Errorf("verdict = %v, want %v", got, tc.want)
			}
		})
	}
}

// A record with no port cannot be judged against a specific port. Guessing
// would either invent a permission or invent a finding.
func TestEvaluate_MissingPortIsUndetermined(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["tag:a"], "dst": ["tag:b"], "ip": ["tcp:443"]}
	]}`)

	c := aclpolicy.Conn{Src: tagged("100.64.0.1", "tag:a"), Dst: tagged("100.64.0.2", "tag:b"), Proto: "tcp"}
	if got := verdictOf(t, p, c); got != aclpolicy.Undetermined {
		t.Errorf("verdict = %v, want Undetermined when the record carries no port", got)
	}
}

// ------------------------------------------------------- three-valued logic

// An unresolvable selector must not be read as "no". Otherwise a policy we only
// partly understand produces confident, wrong findings.
func TestEvaluate_UnknownSelectorIsUndetermined(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"group not declared", `{"grants": [{"src": ["group:eng"], "dst": ["tag:b"], "ip": ["*"]}]}`},
		{"vip service", `{"grants": [{"src": ["tag:a"], "dst": ["svc:argocd"], "ip": ["*"]}]}`},
		{"unknown autogroup", `{"grants": [{"src": ["autogroup:shared"], "dst": ["tag:b"], "ip": ["*"]}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := compile(t, tc.doc)
			c := tcp(tagged("100.64.0.1", "tag:a"), tagged("100.64.0.2", "tag:b"), 443)
			res := p.Evaluate(c)
			if res.Verdict != aclpolicy.Undetermined {
				t.Errorf("verdict = %v, want Undetermined", res.Verdict)
			}
			if res.Reason == "" {
				t.Error("Undetermined with no reason; the page has nothing to show the operator")
			}
		})
	}
}

// A declared group resolves normally. This path has no live tailnet to validate
// against (groups is empty on ours), so the unit test is the only coverage.
func TestEvaluate_DeclaredGroup(t *testing.T) {
	p := compile(t, `{
		"groups": {"group:eng": ["sam@example.com"]},
		"grants": [{"src": ["group:eng"], "dst": ["tag:ci"], "ip": ["tcp:22"]}]
	}`)

	if got := verdictOf(t, p, tcp(owned("100.64.0.3", "sam@example.com"), tagged("100.64.0.8", "tag:ci"), 22)); got != aclpolicy.Permitted {
		t.Errorf("group member = %v, want Permitted", got)
	}
	if got := verdictOf(t, p, tcp(owned("100.64.0.2", "ada@example.com"), tagged("100.64.0.8", "tag:ci"), 22)); got != aclpolicy.NoRule {
		t.Errorf("non-member = %v, want NoRule", got)
	}
}

// A definite match must win over an undecidable rule: knowing something is
// permitted is a complete answer regardless of what else we could not read.
func TestEvaluate_DefiniteMatchBeatsUnknown(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["group:mystery"], "dst": ["tag:b"], "ip": ["*"]},
		{"src": ["tag:a"],         "dst": ["tag:b"], "ip": ["tcp:443"]}
	]}`)

	if got := verdictOf(t, p, tcp(tagged("100.64.0.1", "tag:a"), tagged("100.64.0.2", "tag:b"), 443)); got != aclpolicy.Permitted {
		t.Errorf("verdict = %v, want Permitted despite an undecidable sibling rule", got)
	}
}

// ------------------------------------------------------- return traffic

// Flow logs report both halves of a connection, but ACLs govern only the
// direction it was established in. Without this the return half of every
// permitted connection looks unexplained — 37% of a real capture.
func TestEvaluate_ReturnTrafficMatchesReversed(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["tag:client"], "dst": ["tag:server"], "ip": ["tcp:443"]}
	]}`)

	client := tagged("100.64.0.1", "tag:client")
	server := tagged("100.64.0.2", "tag:server")

	forward := p.Evaluate(tcp(client, server, 443))
	if forward.Verdict != aclpolicy.Permitted || forward.Reversed {
		t.Errorf("forward = %+v, want Permitted and not reversed", forward)
	}

	// The return half: server's port 443 talking back to the client's ephemeral port.
	back := p.Evaluate(aclpolicy.Conn{
		Src: server, Dst: client, Proto: "tcp", DstPort: 40742, HasPort: true, SrcPort: 443, HasSrcPort: true,
	})
	if back.Verdict != aclpolicy.Permitted {
		t.Fatalf("return traffic = %v, want Permitted", back.Verdict)
	}
	if !back.Reversed {
		t.Error("return traffic was not flagged as matched in reverse")
	}
}

// Reverse matching must not become a way to permit anything: it only applies
// when the reversed tuple genuinely matches a rule.
func TestEvaluate_ReverseDoesNotPermitEverything(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["tag:client"], "dst": ["tag:server"], "ip": ["tcp:443"]}
	]}`)

	stranger := tagged("100.64.0.9", "tag:stranger")
	server := tagged("100.64.0.2", "tag:server")
	c := aclpolicy.Conn{Src: server, Dst: stranger, Proto: "tcp", DstPort: 40742, HasPort: true, SrcPort: 443, HasSrcPort: true}
	if got := p.Evaluate(c).Verdict; got != aclpolicy.NoRule {
		t.Errorf("verdict = %v, want NoRule", got)
	}
}

// ------------------------------------------------------------ rule surface

// Legacy acls carry the port on the destination string rather than in a
// separate field, and only accept rules are evaluated.
func TestCompile_LegacyACLs(t *testing.T) {
	p := compile(t, `{"acls": [
		{"action": "accept", "src": ["tag:a"], "dst": ["tag:b:443"]},
		{"action": "accept", "src": ["tag:a"], "dst": ["[fd7a:115c:a1e0::1]:8088"]}
	]}`)

	if got := len(p.Rules()); got != 2 {
		t.Fatalf("rules = %d, want 2", got)
	}
	if got := verdictOf(t, p, tcp(tagged("100.64.0.1", "tag:a"), tagged("100.64.0.2", "tag:b"), 443)); got != aclpolicy.Permitted {
		t.Errorf("legacy tag:port = %v, want Permitted", got)
	}
	if got := verdictOf(t, p, tcp(tagged("100.64.0.1", "tag:a"), tagged("100.64.0.2", "tag:b"), 444)); got != aclpolicy.NoRule {
		t.Errorf("wrong port on legacy rule = %v, want NoRule", got)
	}
	v6 := aclpolicy.Endpoint{Addr: addr("fd7a:115c:a1e0::1")}
	if got := verdictOf(t, p, tcp(tagged("100.64.0.1", "tag:a"), v6, 8088)); got != aclpolicy.Permitted {
		t.Errorf("bracketed IPv6 destination = %v, want Permitted", got)
	}
}

func TestCompile_IgnoresNonAcceptRules(t *testing.T) {
	p := compile(t, `{"acls": [
		{"action": "deny",   "src": ["tag:a"], "dst": ["tag:b:443"]},
		{"action": "accept", "src": ["tag:a"], "dst": ["tag:c:443"]}
	]}`)

	if got := len(p.Rules()); got != 1 {
		t.Fatalf("rules = %d, want only the accept rule", got)
	}
}

// The document is HuJSON: comments and trailing commas are normal, and every
// section other than the rules is ignored rather than rejected.
func TestCompile_HuJSONAndUnknownSections(t *testing.T) {
	p := compile(t, `{
		// a comment
		"randomizeClientPort": true,
		"ssh": [{"action": "check", "src": ["autogroup:admin"], "dst": ["tag:a"], "users": ["root"]}],
		"nodeAttrs": [{"target": ["*"], "attr": ["funnel"]}],
		"grants": [
			{"src": ["tag:a"], "dst": ["tag:b"], "ip": ["tcp:443"]},
		],
	}`)

	if got := len(p.Rules()); got != 1 {
		t.Fatalf("rules = %d, want 1 (ssh and nodeAttrs are out of scope)", got)
	}
}

func TestCompile_Rejects(t *testing.T) {
	for _, doc := range []string{`not json at all`, `{"grants": "not a list"}`} {
		if _, err := aclpolicy.Compile([]byte(doc), dir()); err == nil {
			t.Errorf("Compile(%q) = nil error, want a parse failure", doc)
		}
	}
}

// An empty policy permits nothing; it must not fail open.
func TestEvaluate_EmptyPolicyPermitsNothing(t *testing.T) {
	p := compile(t, `{}`)
	if got := len(p.Rules()); got != 0 {
		t.Errorf("rules = %d, want 0", got)
	}
	if got := verdictOf(t, p, tcp(tagged("100.64.0.1", "tag:a"), tagged("100.64.0.2", "tag:b"), 443)); got != aclpolicy.NoRule {
		t.Errorf("empty policy = %v, want NoRule (must not fail open)", got)
	}
}

// The rule index is what the "not exercised" report joins on, so it must be
// stable and point at the rule that actually matched.
func TestEvaluate_ReportsMatchedRule(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["tag:x"], "dst": ["tag:y"], "ip": ["*"]},
		{"src": ["tag:a"], "dst": ["tag:b"], "ip": ["tcp:443"]}
	]}`)

	res := p.Evaluate(tcp(tagged("100.64.0.1", "tag:a"), tagged("100.64.0.2", "tag:b"), 443))
	if res.Rule != 1 {
		t.Errorf("Rule = %d, want 1", res.Rule)
	}
	rules := p.Rules()
	if rules[res.Rule].Source == "" {
		t.Error("the matched rule has no displayable source text")
	}
}

// Identity we do not have cannot be judged. An endpoint with neither user nor
// tags could be anything, so a rule keyed on identity is undecidable for it.
func TestEvaluate_EndpointWithoutIdentity(t *testing.T) {
	p := compile(t, `{"grants": [
		{"src": ["tag:a"], "dst": ["tag:b"], "ip": ["tcp:443"]}
	]}`)

	// A source with no tags, no user and no address: nothing to match on.
	c := aclpolicy.Conn{Dst: tagged("100.64.0.2", "tag:b"), Proto: "tcp", DstPort: 443, HasPort: true}
	if got := verdictOf(t, p, c); got != aclpolicy.Undetermined {
		t.Errorf("verdict = %v, want Undetermined for an endpoint with no identity", got)
	}
}
