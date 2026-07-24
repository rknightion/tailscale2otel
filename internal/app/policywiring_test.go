package app

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/aclpolicy"
	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

const ownerGrant = `{"grants":[{"src":["autogroup:owner"],"dst":["tag:prod"],"ip":["*"]}]}`

// The policy store rides with the flow view: it exists to feed the
// reconciliation the view renders, so building one without the other would be
// memory nobody reads.
func TestPolicyStore_BuiltWithTheFlowView(t *testing.T) {
	on := flowsTestApp(t, nil)
	if on.runtimes[0].policy == nil {
		t.Error("no policy store alongside an enabled flow view")
	}

	off := flowsTestApp(t, func(c *config.Config) { c.Flows.Enabled = false })
	if off.runtimes[0].policy != nil {
		t.Error("policy store built with the flow view disabled")
	}
}

// Nothing is known until the collectors have run. Reading this on the emit path
// must mean "cannot evaluate", never "nothing is permitted".
func TestPolicyStore_EmptyBeforeCollection(t *testing.T) {
	a := flowsTestApp(t, nil)
	if p := a.runtimes[0].policy.Policy(); p != nil {
		t.Errorf("Policy() = %v before any collector ran", p)
	}
}

// The document and the directory come from two different collectors on
// independent schedules, so either may land first and both orders must end up
// able to resolve a role-based selector.
func TestPolicyStore_ResolvesRolesFromEitherOrder(t *testing.T) {
	roles := aclpolicy.Directory{Roles: map[string]string{"rob@example.com": "owner"}}
	conn := aclpolicy.Conn{
		Src:     aclpolicy.Endpoint{User: "rob@example.com"},
		Dst:     aclpolicy.Endpoint{Tags: []string{"tag:prod"}},
		Proto:   "tcp",
		DstPort: 443, HasPort: true,
	}

	tests := []struct {
		name string
		load func(*aclpolicy.Store) error
	}{
		{"acl collector first", func(s *aclpolicy.Store) error {
			if err := s.SetDocument([]byte(ownerGrant)); err != nil {
				return err
			}
			return s.SetDirectory(roles)
		}},
		{"users collector first", func(s *aclpolicy.Store) error {
			if err := s.SetDirectory(roles); err != nil {
				return err
			}
			return s.SetDocument([]byte(ownerGrant))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := flowsTestApp(t, nil).runtimes[0].policy
			if err := tc.load(s); err != nil {
				t.Fatalf("load: %v", err)
			}
			p := s.Policy()
			if p == nil {
				t.Fatal("Policy() = nil after both inputs landed")
			}
			if got := p.Evaluate(conn).Verdict; got != aclpolicy.Permitted {
				t.Errorf("verdict = %v, want Permitted (was the role directory applied?)", got)
			}
		})
	}
}

// Each tailnet has its own policy, so a multi-tailnet process cannot evaluate
// one tailnet's traffic against another's rules.
func TestPolicyStore_IsPerTailnet(t *testing.T) {
	a := twoTailnetFlowApp(t)
	if a.runtimes[0].policy == nil || a.runtimes[1].policy == nil {
		t.Fatal("every tailnet needs its own policy store")
	}
	if a.runtimes[0].policy == a.runtimes[1].policy {
		t.Fatal("both tailnets share one policy store")
	}

	if err := a.runtimes[0].policy.SetDocument([]byte(ownerGrant)); err != nil {
		t.Fatal(err)
	}
	if a.runtimes[1].policy.Policy() != nil {
		t.Error("loading one tailnet's policy populated the other's")
	}
}
