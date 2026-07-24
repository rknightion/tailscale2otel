package aclpolicy_test

import (
	"sync"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/aclpolicy"
)

const twoRuleDoc = `{"grants": [
	{"src": ["autogroup:owner"], "dst": ["tag:prod"], "ip": ["*"]},
	{"src": ["tag:a"], "dst": ["tag:b"], "ip": ["tcp:443"]}
]}`

// Nothing has been collected yet on a fresh start. The emit path reads this on
// every connection, so it must be safe and simply mean "cannot evaluate".
func TestStore_EmptyYieldsNoPolicy(t *testing.T) {
	var s aclpolicy.Store
	if p := s.Policy(); p != nil {
		t.Errorf("Policy() = %v, want nil before anything is collected", p)
	}
	if err := s.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestStore_CompilesOnDocument(t *testing.T) {
	var s aclpolicy.Store
	if err := s.SetDocument([]byte(twoRuleDoc)); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}
	p := s.Policy()
	if p == nil {
		t.Fatal("Policy() = nil after a document was set")
	}
	if got := len(p.Rules()); got != 2 {
		t.Errorf("rules = %d, want 2", got)
	}
}

// The directory arrives from a different collector than the document, on its own
// schedule. Either order must end in the same compiled policy.
func TestStore_RecompilesWhenEitherInputChanges(t *testing.T) {
	dirFirst := &aclpolicy.Store{}
	if err := dirFirst.SetDirectory(dir()); err != nil {
		t.Fatalf("SetDirectory: %v", err)
	}
	if dirFirst.Policy() != nil {
		t.Error("a directory with no document should not produce a policy")
	}
	if err := dirFirst.SetDocument([]byte(twoRuleDoc)); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}

	docFirst := &aclpolicy.Store{}
	if err := docFirst.SetDocument([]byte(twoRuleDoc)); err != nil {
		t.Fatalf("SetDocument: %v", err)
	}
	if err := docFirst.SetDirectory(dir()); err != nil {
		t.Fatalf("SetDirectory: %v", err)
	}

	// Both orders must resolve autogroup:owner, which only works once the
	// directory has landed.
	c := tcp(owned("100.64.0.1", "rob@example.com"), tagged("100.64.0.9", "tag:prod"), 443)
	for name, s := range map[string]*aclpolicy.Store{"directory first": dirFirst, "document first": docFirst} {
		p := s.Policy()
		if p == nil {
			t.Fatalf("%s: Policy() = nil", name)
		}
		if got := p.Evaluate(c).Verdict; got != aclpolicy.Permitted {
			t.Errorf("%s: verdict = %v, want Permitted", name, got)
		}
	}
}

// Recompiling on every collect would churn garbage on the hot read path for no
// reason: the ACL changes rarely.
func TestStore_UnchangedInputsDoNotRecompile(t *testing.T) {
	var s aclpolicy.Store
	if err := s.SetDocument([]byte(twoRuleDoc)); err != nil {
		t.Fatal(err)
	}
	first := s.Policy()
	if err := s.SetDocument([]byte(twoRuleDoc)); err != nil {
		t.Fatal(err)
	}
	if s.Policy() != first {
		t.Error("an identical document produced a new policy")
	}

	if err := s.SetDirectory(dir()); err != nil {
		t.Fatal(err)
	}
	second := s.Policy()
	if second == first {
		t.Fatal("a new directory did not recompile")
	}
	if err := s.SetDirectory(dir()); err != nil {
		t.Fatal(err)
	}
	if s.Policy() != second {
		t.Error("an identical directory produced a new policy")
	}
}

// A malformed document must not replace a good policy with a broken one, or
// with nothing: the previous answer stays correct until a better one arrives.
func TestStore_BadDocumentKeepsThePreviousPolicy(t *testing.T) {
	var s aclpolicy.Store
	if err := s.SetDocument([]byte(twoRuleDoc)); err != nil {
		t.Fatal(err)
	}
	good := s.Policy()

	err := s.SetDocument([]byte(`{"grants": "not a list"}`))
	if err == nil {
		t.Fatal("SetDocument accepted a malformed document")
	}
	if s.Policy() != good {
		t.Error("a malformed document discarded the working policy")
	}
	if s.Err() == nil {
		t.Error("Err() = nil after a failed compile; the status page has nothing to report")
	}

	// Recovering clears the error.
	if err := s.SetDocument([]byte(twoRuleDoc + " ")); err != nil {
		t.Fatalf("SetDocument after recovery: %v", err)
	}
	if s.Err() != nil {
		t.Errorf("Err() = %v, want nil after a successful recompile", s.Err())
	}
}

// The emit path reads this concurrently with collector writes.
func TestStore_ConcurrentReadWrite(t *testing.T) {
	var s aclpolicy.Store
	if err := s.SetDocument([]byte(twoRuleDoc)); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := tcp(tagged("100.64.0.1", "tag:a"), tagged("100.64.0.2", "tag:b"), 443)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if p := s.Policy(); p != nil {
					_ = p.Evaluate(c)
				}
			}
		}()
	}
	for i := range 50 {
		d := aclpolicy.Directory{Roles: map[string]string{"a@example.com": "member"}}
		if i%2 == 0 {
			d.Roles["b@example.com"] = "admin"
		}
		if err := s.SetDirectory(d); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}
