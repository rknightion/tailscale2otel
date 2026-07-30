package flowlog

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
)

// The processor must stamp every verdict with the version of the policy that
// PRODUCED it. Anything else — re-reading the store afterwards, or leaving it to
// the consumer — races an ACL update and files a verdict under the wrong rule
// list, which is exactly the misattribution the version exists to prevent (#302).

func policyStore(t *testing.T, doc string) *aclpolicy.Store {
	t.Helper()
	var s aclpolicy.Store
	if err := s.SetDocument([]byte(doc)); err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	return &s
}

func TestReconcileStampsTheEvaluatingPolicyVersion(t *testing.T) {
	store := policyStore(t, `{"grants":[{"src":["*"],"dst":["*"],"ip":["*"]}]}`)
	p := &Processor{policy: store}

	verdict, _, _, version := p.reconcile(semconv.TrafficVirtual, "tcp", nil, nil,
		"100.64.0.1:1234", "1234", "100.64.0.2:443", "443")

	if verdict == "" {
		t.Fatal("nothing was evaluated; the rest of this test proves nothing")
	}
	if want := store.Policy().Version(); version != want {
		t.Errorf("verdict stamped with policy version %q, want %q", version, want)
	}
}

func TestReconcileStampsNoVersionWhenNothingWasEvaluated(t *testing.T) {
	store := policyStore(t, `{"grants":[{"src":["*"],"dst":["*"],"ip":["*"]}]}`)
	p := &Processor{policy: store}

	// Physical traffic is the WireGuard underlay: deliberately not reconciled.
	verdict, _, _, version := p.reconcile(semconv.TrafficPhysical, "tcp", nil, nil,
		"100.64.0.1:1234", "1234", "100.64.0.2:443", "443")

	if verdict != "" {
		t.Fatalf("physical traffic produced verdict %q; it is not policy-governed", verdict)
	}
	if version != "" {
		t.Errorf("an unevaluated connection carries policy version %q. A version on traffic no "+
			"policy read would make it look attributable to a rule", version)
	}
}

func TestReconcileStampsNoVersionWithoutAPolicy(t *testing.T) {
	p := &Processor{policy: &aclpolicy.Store{}} // compiled nothing yet

	verdict, _, _, version := p.reconcile(semconv.TrafficVirtual, "tcp", nil, nil,
		"100.64.0.1:1234", "1234", "100.64.0.2:443", "443")

	if verdict != "" || version != "" {
		t.Errorf("with no compiled policy got verdict %q version %q, want both empty", verdict, version)
	}
}
