package flowlog_test

import (
	"encoding/json"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v4/internal/enrich"
	"github.com/rknightion/tailscale2otel/v4/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v4/internal/flowstore"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry/pii"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// testPolicy is written against the live record fixture: camden is tag-owned
// (tag:servers, tag:sshrecorder) at 100.64.0.1, mbp16 belongs to rob@example.com
// at 100.64.0.2.
const testPolicy = `{"grants": [
	{"src": ["rob@example.com"],  "dst": ["tag:servers"],       "ip": ["tcp:443"]},
	{"src": ["tag:servers"],      "dst": ["tag:ntp"],           "ip": ["udp:123"]},
	{"src": ["tag:servers"],      "dst": ["autogroup:internet"],"ip": ["*"]}
]}`

// policyStore compiles doc into a store the processor can read, exactly as the
// acl and users collectors populate it at runtime.
func policyStore(t *testing.T, doc string) *aclpolicy.Store {
	t.Helper()
	s := &aclpolicy.Store{}
	if err := s.SetDocument([]byte(doc)); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}
	if err := s.SetDirectory(aclpolicy.Directory{Roles: map[string]string{"rob@example.com": "owner"}}); err != nil {
		t.Fatalf("SetDirectory: %v", err)
	}
	return s
}

// policyProcessor builds a processor that both evaluates against src and feeds
// a fake store, which is where the verdict lands.
func policyProcessor(t *testing.T, src flowlog.PolicySource) (*flowlog.Processor, *fakeStore) {
	t.Helper()
	fs := &fakeStore{}
	return flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{Store: fs, Policy: src}), fs
}

// only returns the single observation the store received, failing otherwise.
func only(t *testing.T, fs *fakeStore) flowstore.Observation {
	t.Helper()
	if len(fs.got) != 1 {
		t.Fatalf("store received %d observations, want 1: %+v", len(fs.got), fs.got)
	}
	return fs.got[0]
}

// The live fixture is the RETURN half of a session: the server answers from
// :443 to the client's ephemeral port. A policy governs only the direction a
// connection was established in, so this must be recognized as permitted by the
// rule covering the forward direction — on a live 3h capture 37% of connections
// were explainable only this way.
func TestProcess_PolicyMatchesReturnTraffic(t *testing.T) {
	p, fs := policyProcessor(t, policyStore(t, testPolicy))
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	o := only(t, fs)
	if o.Verdict != "permitted" {
		t.Errorf("Verdict = %q, want permitted (was the source port supplied so the tuple could be reversed?)", o.Verdict)
	}
	if !o.Reversed {
		t.Error("Reversed = false; this connection is only explained by the establishing direction")
	}
	if o.Rule != 0 {
		t.Errorf("Rule = %d, want 0", o.Rule)
	}
}

// A connection matching in the direction it was observed carries the rule that
// covered it and is not marked as return traffic.
func TestProcess_PolicyMatchesForwardTraffic(t *testing.T) {
	p, fs := policyProcessor(t, policyStore(t, testPolicy))
	rec := telemetrytest.New()

	// mbp16 (rob@example.com) reaching camden (tag:servers) on 443.
	flow := decodeLiveRecord(t)
	flow.VirtualTraffic = []flowlog.ConnectionCounts{
		{Proto: 6, Src: "100.64.0.2:52000", Dst: "100.64.0.1:443", TxBytes: 100, TxPkts: 1},
	}
	p.Process(flow, rec.Emitter())

	o := only(t, fs)
	if o.Verdict != "permitted" || o.Reversed {
		t.Errorf("Verdict/Reversed = %q/%v, want permitted/false", o.Verdict, o.Reversed)
	}
	if o.Rule != 0 {
		t.Errorf("Rule = %d, want 0", o.Rule)
	}
}

// Traffic no rule covers in either direction is the finding the page exists to
// surface. Rule must not point at a rule that did not match.
func TestProcess_PolicyReportsUnexplainedTraffic(t *testing.T) {
	p, fs := policyProcessor(t, policyStore(t, testPolicy))
	rec := telemetrytest.New()

	flow := decodeLiveRecord(t)
	flow.VirtualTraffic = []flowlog.ConnectionCounts{
		{Proto: 6, Src: "100.64.0.1:40000", Dst: "100.64.0.2:9999", TxBytes: 100, TxPkts: 1},
	}
	p.Process(flow, rec.Emitter())

	o := only(t, fs)
	if o.Verdict != "no_rule" {
		t.Errorf("Verdict = %q, want no_rule", o.Verdict)
	}
	if o.Rule != -1 {
		t.Errorf("Rule = %d, want -1 — nothing matched", o.Rule)
	}
}

// Exit traffic carries no destination at all, so autogroup:internet is the only
// thing that can describe it. The processor must tell the evaluator that this
// left the tailnet; nothing in the tuple says so.
func TestProcess_PolicyEvaluatesExitTraffic(t *testing.T) {
	p, fs := policyProcessor(t, policyStore(t, testPolicy))
	rec := telemetrytest.New()

	flow := decodeLiveRecord(t)
	flow.VirtualTraffic = nil
	flow.ExitTraffic = []flowlog.ConnectionCounts{
		{Proto: 6, Src: "100.64.0.1:0", TxBytes: 320, TxPkts: 4},
	}
	p.Process(flow, rec.Emitter())

	o := only(t, fs)
	if o.Verdict != "permitted" {
		t.Errorf("Verdict = %q, want permitted by the autogroup:internet grant", o.Verdict)
	}
	if o.Rule != 2 {
		t.Errorf("Rule = %d, want 2", o.Rule)
	}
}

// physicalTraffic is the WireGuard underlay — the encrypted transport between
// endpoints, not the tailnet traffic a policy governs. Evaluating it would
// report every peer-to-peer path as unexplained.
func TestProcess_PolicySkipsPhysicalTraffic(t *testing.T) {
	p, fs := policyProcessor(t, policyStore(t, testPolicy))
	rec := telemetrytest.New()

	flow := decodeLiveRecord(t)
	flow.VirtualTraffic = nil
	flow.PhysicalTraffic = []flowlog.ConnectionCounts{
		{Proto: 17, Src: "100.64.0.1:41641", Dst: "100.64.0.2:41641", TxBytes: 500, TxPkts: 5},
	}
	p.Process(flow, rec.Emitter())

	if o := only(t, fs); o.Verdict != "" {
		t.Errorf("Verdict = %q, want empty — the underlay is not policy-governed", o.Verdict)
	}
}

// Nothing has been collected on a fresh start. An unevaluated connection must be
// distinguishable from one a policy WAS applied to and could not decide, so this
// is empty rather than "undetermined".
func TestProcess_PolicyNotYetCollected(t *testing.T) {
	p, fs := policyProcessor(t, &aclpolicy.Store{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	o := only(t, fs)
	if o.Verdict != "" {
		t.Errorf("Verdict = %q, want empty before any policy is collected", o.Verdict)
	}
	if o.Rule != -1 {
		t.Errorf("Rule = %d, want -1", o.Rule)
	}
}

// Reconciliation is opt-in and independent of the store.
func TestProcess_NoPolicyConfigured(t *testing.T) {
	p, fs := storeProcessor(t, flowlog.Options{})
	rec := telemetrytest.New()

	p.Process(decodeLiveRecord(t), rec.Emitter())

	if o := only(t, fs); o.Verdict != "" || o.Rule != -1 {
		t.Errorf("Verdict/Rule = %q/%d, want empty/-1 with no policy configured", o.Verdict, o.Rule)
	}
}

// The PII filter governs what the process DISCLOSES, not what it may reason
// about. Since #241 nothing on this path is redacted at all, but the emitter's
// filter still runs alongside it — and a verdict derived from what the EMITTER
// was allowed to say would turn a privacy setting into wrong security findings.
func TestProcess_PolicyUnaffectedByPIIRedaction(t *testing.T) {
	fs := &fakeStore{}
	p := flowlog.NewProcessor(enrich.NewDeviceCache(), flowlog.Options{
		Store:  fs,
		Policy: policyStore(t, testPolicy),
	})
	rec := telemetrytest.NewWithPII(pii.Categories{
		pii.CatTailscaleIPs: false, pii.CatEmails: false, pii.CatHostnames: false,
	})

	p.Process(decodeLiveRecord(t), rec.Emitter())

	o := only(t, fs)
	if o.Verdict != "permitted" || !o.Reversed {
		t.Errorf("Verdict/Reversed = %q/%v, want permitted/true — redaction changed the verdict", o.Verdict, o.Reversed)
	}
}

// Reconciliation runs per connection on the same path that emits the OTLP
// metrics. The number that matters is the DELTA against the same processing
// without a policy — everything else here is work the processor already did.
func BenchmarkProcessWithPolicy(b *testing.B) {
	var fl flowlog.FlowLog
	if err := json.Unmarshal([]byte(liveRecordJSON), &fl); err != nil {
		b.Fatalf("decode: %v", err)
	}
	store := &aclpolicy.Store{}
	if err := store.SetDocument([]byte(testPolicy)); err != nil {
		b.Fatalf("SetDocument: %v", err)
	}

	benches := []struct {
		name string
		opts flowlog.Options
	}{
		{"without policy", flowlog.Options{}},
		{"with policy", flowlog.Options{Policy: store}},
	}
	for _, bc := range benches {
		b.Run(bc.name, func(b *testing.B) {
			bc.opts.Store = discardStore{}
			bc.opts.LogMode = "off"
			p := flowlog.NewProcessor(enrich.NewDeviceCache(), bc.opts)
			e := telemetrytest.New().Emitter()
			b.ReportAllocs()
			for b.Loop() {
				p.Process(fl, e)
			}
		})
	}
}

// discardStore is the cheapest possible Recorder, so the benchmark measures the
// processor rather than the store.
type discardStore struct{}

func (discardStore) Record(flowstore.Observation) {}
