package aclpolicy_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/aclpolicy"
)

// realShapedPolicy mirrors the shape of the live tailnet's policy the evaluator
// was validated against: a spread of tag-to-tag grants, a role-based one, an
// exit-node grant, and a CIDR destination.
const realShapedPolicy = `{
	"groups": {"group:eng": ["ada@example.com"]},
	"hosts":  {"router": "10.0.0.1"},
	"ipsets": {"ipset:lan": ["add 10.0.0.0/24"]},
	"grants": [
		{"src": ["tag:web"],           "dst": ["tag:db"],                "ip": ["tcp:5432"]},
		{"src": ["tag:web"],           "dst": ["tag:cache"],             "ip": ["tcp:6379"]},
		{"src": ["tag:ci"],            "dst": ["tag:registry"],          "ip": ["tcp:443"]},
		{"src": ["group:eng"],         "dst": ["tag:web", "tag:db"],     "ip": ["tcp:22"]},
		{"src": ["autogroup:owner"],   "dst": ["*"],                     "ip": ["*"]},
		{"src": ["autogroup:member"],  "dst": ["autogroup:internet"],    "ip": ["*"]},
		{"src": ["tag:monitor"],       "dst": ["ipset:lan"],             "ip": ["tcp:9100", "udp:161"]},
		{"src": ["tag:app"],           "dst": ["router"],                "ip": ["udp:53", "tcp:53"]},
		{"src": ["tag:app"],           "dst": ["10.1.0.0/16"],           "ip": ["tcp:1-1024"]},
		{"src": ["*"],                 "dst": ["tag:ntp"],               "ip": ["udp:123"]}
	]
}`

// returnHalf builds the connection as the RETURN direction of an established
// session appears in a flow record: the server answering from its listening
// port to the client's ephemeral one. Both ports are present, which is what
// lets the evaluator recover the establishing direction — a record carrying
// only the destination port reverses into one carrying none, and degrades to
// undetermined.
func returnHalf(src aclpolicy.Endpoint, srcPort uint16, dst aclpolicy.Endpoint, dstPort uint16) aclpolicy.Conn {
	return aclpolicy.Conn{
		Src: src, Dst: dst, Proto: "tcp",
		DstPort: dstPort, HasPort: true,
		SrcPort: srcPort, HasSrcPort: true,
	}
}

// Evaluation runs once per connection on the flow emit path, which is the same
// path the OTLP exporters are on. Allocating there would turn every connection a
// busy tailnet reports into garbage-collector pressure on the hot path, so the
// budget is zero and this test is what holds it there.
//
// The worst case is a connection nothing permits: it walks every rule in the
// forward direction, then every rule again in reverse, before concluding.
func TestEvaluate_DoesNotAllocate(t *testing.T) {
	p, err := aclpolicy.Compile([]byte(realShapedPolicy), dir())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	cases := []struct {
		name string
		conn aclpolicy.Conn
		want aclpolicy.Verdict
	}{
		{
			"permitted by an early rule",
			tcp(tagged("100.64.0.1", "tag:web"), tagged("100.64.0.2", "tag:db"), 5432),
			aclpolicy.Permitted,
		},
		{
			"permitted only in reverse — the return half of a connection",
			returnHalf(tagged("100.64.0.2", "tag:db"), 5432, tagged("100.64.0.1", "tag:web"), 54321),
			aclpolicy.Permitted,
		},
		{
			"explained by no rule — walks every rule in both directions",
			tcp(tagged("100.64.0.9", "tag:app"), tagged("100.64.0.8", "tag:db"), 9999),
			aclpolicy.NoRule,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Evaluate(tc.conn).Verdict; got != tc.want {
				t.Fatalf("verdict = %v, want %v — the benchmark is measuring the wrong path", got, tc.want)
			}
			got := testing.AllocsPerRun(200, func() {
				_ = p.Evaluate(tc.conn)
			})
			if got != 0 {
				t.Errorf("Evaluate allocated %.0f times per call, want 0", got)
			}
		})
	}
}

func BenchmarkEvaluate(b *testing.B) {
	p, err := aclpolicy.Compile([]byte(realShapedPolicy), aclpolicy.Directory{
		Roles: map[string]string{"rob@example.com": "owner", "ada@example.com": "admin"},
	})
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}

	benches := []struct {
		name string
		conn aclpolicy.Conn
	}{
		{"permitted", tcp(tagged("100.64.0.1", "tag:web"), tagged("100.64.0.2", "tag:db"), 5432)},
		{"reversed", returnHalf(tagged("100.64.0.2", "tag:db"), 5432, tagged("100.64.0.1", "tag:web"), 54321)},
		{"no_rule", tcp(tagged("100.64.0.9", "tag:app"), tagged("100.64.0.8", "tag:db"), 9999)},
	}
	for _, bc := range benches {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = p.Evaluate(bc.conn)
			}
		})
	}
}
