package flowstore_test

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/flowstore"
)

// at is a fixed instant inside one bucket, so these tests never depend on the
// wall clock.
var at = time.Date(2026, 7, 24, 9, 30, 0, 0, time.UTC)

// evaluated builds an observation carrying a policy verdict with one byte of traffic, which is enough
// to rank and to sum.
func evaluated(verdict string, rule int, reversed bool) flowstore.Observation {
	return flowstore.Observation{
		Time:        at,
		TrafficType: "virtual",
		Transport:   "tcp",
		SrcNode:     "web",
		DstNode:     "db",
		DstPort:     "5432",
		Verdict:     verdict,
		Rule:        rule,
		Reversed:    reversed,
		Counts:      flowstore.Counts{TxBytes: 1, Flows: 1},
	}
}

func query(m *flowstore.Memory) flowstore.Result {
	return m.Query(flowstore.Query{Start: at.Add(-time.Hour), End: at.Add(time.Hour)})
}

// labelFlows flattens a breakdown into a comparable map.
func labelFlows(in []flowstore.LabelStat) map[string]int64 {
	out := map[string]int64{}
	for _, l := range in {
		out[l.Label] = l.Counts.Flows
	}
	return out
}

// Return traffic is normal — 37% of connections on a live capture — so it is
// split out for display rather than lumped in with the establishing direction,
// which would make the permitted share look like it came from rules it did not.
func TestVerdicts_BreakdownSplitsReturnTraffic(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(evaluated(flowstore.VerdictPermitted, 0, false))
	m.Record(evaluated(flowstore.VerdictPermitted, 0, false))
	m.Record(evaluated(flowstore.VerdictPermitted, 1, true))
	m.Record(evaluated(flowstore.VerdictNoRule, -1, false))
	m.Record(evaluated(flowstore.VerdictUndetermined, -1, false))

	got := labelFlows(query(m).Verdicts)
	want := map[string]int64{
		flowstore.VerdictPermitted:        2,
		flowstore.VerdictPermittedReverse: 1,
		flowstore.VerdictNoRule:           1,
		flowstore.VerdictUndetermined:     1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("verdict %q = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("verdicts = %v, want exactly %d entries", got, len(want))
	}
}

// A connection nothing evaluated is not a connection a policy could not decide.
// Counting the two together would report a tailnet with no ACL collected as one
// the evaluator is confused by.
func TestVerdicts_UnevaluatedConnectionsAreNotCounted(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(evaluated("", -1, false))
	m.Record(evaluated(flowstore.VerdictPermitted, 0, false))

	res := query(m)
	if got := labelFlows(res.Verdicts); len(got) != 1 || got[flowstore.VerdictPermitted] != 1 {
		t.Errorf("verdicts = %v, want only the one evaluated connection", got)
	}
	// The traffic itself still counts everywhere else.
	if res.Totals.Flows != 2 {
		t.Errorf("Totals.Flows = %d, want 2 — an unevaluated connection is still traffic", res.Totals.Flows)
	}
}

// The unexplained aggregate is the finding the page exists to surface: it turns
// thousands of scattered rows into the handful of RELATIONSHIPS behind them.
func TestUnexplained_AggregatesByRelationship(t *testing.T) {
	m := flowstore.NewMemory(0)
	for range 3 {
		o := evaluated(flowstore.VerdictNoRule, -1, false)
		o.SrcTags, o.DstAddr = "tag:aws", "10.0.0.254:53"
		o.DstNode, o.DstPort, o.Transport = "", "53", "udp"
		m.Record(o)
	}
	o := evaluated(flowstore.VerdictNoRule, -1, false)
	o.SrcTags, o.DstAddr = "tag:aws", "10.0.0.5:53"
	o.DstNode, o.DstPort, o.Transport = "", "53", "udp"
	m.Record(o)

	res := query(m)
	if len(res.Unexplained) != 2 {
		t.Fatalf("unexplained = %+v, want 2 relationships", res.Unexplained)
	}
	top := res.Unexplained[0]
	if top.Src != "tag:aws" || top.Dst != "10.0.0.254" {
		t.Errorf("busiest relationship = %q -> %q, want tag:aws -> 10.0.0.254", top.Src, top.Dst)
	}
	if top.Transport != "udp" || top.Port != "53" {
		t.Errorf("transport/port = %q/%q, want udp/53 — without these the finding is not actionable", top.Transport, top.Port)
	}
	if top.Counts.Flows != 3 {
		t.Errorf("flows = %d, want 3", top.Counts.Flows)
	}
}

// Only a definite "no rule covers this" is a finding. An undetermined verdict
// means the evaluator could not apply the policy — reporting it as unexplained
// is exactly the confident-false-alarm failure the evaluator is built to avoid.
func TestUnexplained_ExcludesEveryOtherVerdict(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(evaluated(flowstore.VerdictUndetermined, -1, false))
	m.Record(evaluated(flowstore.VerdictPermitted, 0, false))
	m.Record(evaluated(flowstore.VerdictPermitted, 0, true))
	m.Record(evaluated("", -1, false))

	if res := query(m); len(res.Unexplained) != 0 {
		t.Errorf("unexplained = %+v, want none — nothing here was definitively unexplained", res.Unexplained)
	}
}

// An endpoint is named by the most useful thing known about it: what it IS
// (a tag, an owner) before where it is.
func TestUnexplained_NamesEndpointsByBestKnownIdentity(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*flowstore.Observation)
		wantSrc string
	}{
		{"tags win over everything", func(o *flowstore.Observation) {
			o.SrcTags, o.SrcUser, o.SrcNode, o.SrcAddr = "tag:aws", "rob@example.com", "web", "100.64.0.1:1"
		}, "tag:aws"},
		{"then the owner", func(o *flowstore.Observation) {
			o.SrcUser, o.SrcNode, o.SrcAddr = "rob@example.com", "web", "100.64.0.1:1"
		}, "rob@example.com"},
		{"then the device name", func(o *flowstore.Observation) {
			o.SrcNode, o.SrcAddr = "web", "100.64.0.1:1"
		}, "web"},
		{"then the bare address, without its port", func(o *flowstore.Observation) {
			o.SrcNode, o.SrcAddr = "", "100.64.0.1:1"
		}, "100.64.0.1"},
		{"and an endpoint stripped by the PII filter is named as such", func(o *flowstore.Observation) {
			o.SrcNode, o.SrcAddr = "", ""
		}, flowstore.Unidentified},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := flowstore.NewMemory(0)
			o := evaluated(flowstore.VerdictNoRule, -1, false)
			tc.mutate(&o)
			m.Record(o)

			res := query(m)
			if len(res.Unexplained) != 1 {
				t.Fatalf("unexplained = %+v, want 1", res.Unexplained)
			}
			if got := res.Unexplained[0].Src; got != tc.wantSrc {
				t.Errorf("src = %q, want %q", got, tc.wantSrc)
			}
		})
	}
}

// "Which rules never fired" is the second half of the reconciliation, and it can
// only be answered by counting the ones that did.
func TestRules_CountsOnlyRulesThatPermittedSomething(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(evaluated(flowstore.VerdictPermitted, 3, false))
	m.Record(evaluated(flowstore.VerdictPermitted, 3, false))
	m.Record(evaluated(flowstore.VerdictPermitted, 7, true)) // return traffic still exercises its rule
	m.Record(evaluated(flowstore.VerdictNoRule, -1, false))
	m.Record(evaluated(flowstore.VerdictUndetermined, -1, false))
	m.Record(evaluated("", 0, false)) // unevaluated: rule 0 is the zero value, not a match
	// A rule index alongside anything but "permitted" is a contradiction. The
	// processor does not produce one, and if it ever did, believing it would
	// report a rule as exercised by traffic it did not permit — which is the one
	// claim the unexercised list rests on.
	m.Record(evaluated(flowstore.VerdictNoRule, 5, false))
	m.Record(evaluated(flowstore.VerdictUndetermined, 5, false))

	got := map[int]int64{}
	for _, r := range query(m).Rules {
		got[r.Rule] = r.Counts.Flows
	}
	want := map[int]int64{3: 2, 7: 1}
	if len(got) != len(want) {
		t.Fatalf("rules = %v, want %v — an unevaluated observation must not exercise rule 0", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("rule %d = %d, want %d", k, got[k], v)
		}
	}
}

// The connection list is where an operator goes after seeing a relationship in
// the aggregate, so each connection carries its own verdict.
func TestRecent_CarriesTheVerdict(t *testing.T) {
	m := flowstore.NewMemory(0)
	m.Record(evaluated(flowstore.VerdictPermitted, 4, true))

	got := m.Recent(10)
	if len(got) != 1 {
		t.Fatalf("Recent = %+v, want 1", got)
	}
	if got[0].Verdict != flowstore.VerdictPermitted || !got[0].Reversed || got[0].Rule != 4 {
		t.Errorf("verdict/reversed/rule = %q/%v/%d, want permitted/true/4", got[0].Verdict, got[0].Reversed, got[0].Rule)
	}
}

// The relationship aggregate is the one policy dimension an adversary can grow:
// the stream ingress can mint unique addresses, and every one of them is a
// relationship nothing explains.
func TestUnexplained_IsBounded(t *testing.T) {
	m := flowstore.NewMemory(0)
	for i := range flowstore.MaxUnexplainedPerBucket * 2 {
		o := evaluated(flowstore.VerdictNoRule, -1, false)
		o.SrcNode, o.DstNode = "attacker", ""
		o.DstAddr = netAddr(i)
		m.Record(o)
	}

	// Asked for everything, so the ranked TopN cannot hide the fold.
	res := m.Query(flowstore.Query{Start: at.Add(-time.Hour), End: at.Add(time.Hour), TopN: 1 << 20})
	// The distinct keys are capped; the single Other row overflow folds into sits
	// on top of them.
	if len(res.Unexplained) > flowstore.MaxUnexplainedPerBucket+1 {
		t.Errorf("unexplained holds %d relationships, above the %d cap", len(res.Unexplained), flowstore.MaxUnexplainedPerBucket)
	}
	if res.Truncated == 0 {
		t.Error("Truncated = 0; the page would imply complete coverage of a truncated aggregate")
	}
	var total int64
	for _, u := range res.Unexplained {
		total += u.Counts.Flows
	}
	if want := int64(flowstore.MaxUnexplainedPerBucket * 2); total != want {
		t.Errorf("flows across relationships = %d, want %d — overflow must fold, not vanish", total, want)
	}
}

// netAddr builds a distinct "addr:port" for the cap test.
func netAddr(i int) string {
	return "10." + itoa(i/65536%256) + "." + itoa(i/256%256) + "." + itoa(i%256) + ":443"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// "external" and "unknown" are what the processor collapses an address it cannot
// resolve to. They are sentinels, not names, and naming a relationship with one
// throws away the only actionable thing about it — a live tailnet's entire
// unexplained residue was traffic to two LAN addresses behind a subnet router,
// which no device name covers.
func TestUnexplained_PrefersAnAddressToACollapseSentinel(t *testing.T) {
	for _, sentinel := range []string{"external", "unknown"} {
		t.Run(sentinel, func(t *testing.T) {
			m := flowstore.NewMemory(0)
			o := evaluated(flowstore.VerdictNoRule, -1, false)
			o.SrcNode, o.SrcAddr = "web", "100.64.0.1:41000"
			o.DstNode, o.DstAddr = sentinel, "10.0.0.254:53"
			m.Record(o)

			res := query(m)
			if len(res.Unexplained) != 1 {
				t.Fatalf("unexplained = %+v, want 1", res.Unexplained)
			}
			if got := res.Unexplained[0].Dst; got != "10.0.0.254" {
				t.Errorf("dst = %q, want the address — %q says nothing an operator can act on", got, sentinel)
			}
		})
	}
}

// The sentinel is only unhelpful when something better exists. With no address
// it is still the most that is known, and replacing it with "unidentified" would
// lose the fact that the endpoint was outside the tailnet.
func TestUnexplained_KeepsTheSentinelWhenNothingElseIsKnown(t *testing.T) {
	m := flowstore.NewMemory(0)
	o := evaluated(flowstore.VerdictNoRule, -1, false)
	o.DstNode, o.DstAddr = "external", ""
	m.Record(o)

	res := query(m)
	if len(res.Unexplained) != 1 || res.Unexplained[0].Dst != "external" {
		t.Errorf("unexplained = %+v, want the sentinel retained", res.Unexplained)
	}
}
