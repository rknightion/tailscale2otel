package aclpolicy

import (
	"bytes"
	"maps"
	"sync"
	"sync/atomic"
)

// Store keeps the newest policy document and identity directory, recompiling
// when either changes and publishing the result for lock-free reads.
//
// The two inputs arrive from different collectors on independent schedules —
// the ACL document from the acl collector, the roles from the users collector —
// and either may land first. The compiled policy is read on the flow emit path
// for every connection, so reads take no lock and never block a writer.
//
// The zero Store is ready to use and reports "no policy yet", which callers
// must treat as "cannot evaluate" rather than as "nothing is permitted".
type Store struct {
	mu  sync.Mutex // serializes recompiles; never held by a reader
	doc []byte
	dir Directory

	compiled atomic.Pointer[Policy]
	failure  atomic.Pointer[compileFailure]

	// history is every recently compiled rule list, keyed by policy version, so
	// traffic retained across a policy edit can be shown against the rules it was
	// actually evaluated against (#302). Published as an immutable map so readers
	// stay lock-free like Policy(); order is tracked separately for eviction.
	history atomic.Pointer[map[string][]Rule]
	order   []string // oldest first; guarded by mu
}

// MaxRetainedPolicies bounds the snapshot history.
//
// It is a count of DISTINCT rule lists, not of collections: re-reporting an
// unchanged ACL is already a no-op, so this only advances when an operator
// actually edits their policy. Sixteen edits is far more than a tailnet produces
// inside the flow store's retention window, which is what the history has to
// outlive — and when it is not, the caller reports the version as unavailable
// rather than guessing, so overflowing here degrades honestly instead of
// misattributing. An unbounded map keyed by a value an external actor can change
// would be a leak with no ceiling.
const MaxRetainedPolicies = 16

// compileFailure boxes an error so it can live in an atomic.Pointer.
type compileFailure struct{ err error }

// SetDocument publishes a new policy document. An identical document is a
// no-op, so the common case — a collector re-reporting an unchanged ACL — does
// not churn the compiled policy that readers are holding.
//
// A document that fails to compile returns the error and leaves the previous
// policy in place: a stale-but-valid answer beats no answer, and beats a
// half-parsed one.
func (s *Store) SetDocument(doc []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.doc != nil && bytes.Equal(s.doc, doc) {
		return nil
	}
	prev := s.doc
	s.doc = bytes.Clone(doc)
	if err := s.recompileLocked(); err != nil {
		s.doc = prev // keep the input that last compiled
		return err
	}
	return nil
}

// SetDirectory publishes a new identity directory. An equal directory is a
// no-op, for the same reason as SetDocument.
func (s *Store) SetDirectory(d Directory) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dir.Roles != nil && maps.Equal(s.dir.Roles, d.Roles) {
		return nil
	}
	prev := s.dir
	s.dir = Directory{Roles: maps.Clone(d.Roles)}
	if err := s.recompileLocked(); err != nil {
		s.dir = prev
		return err
	}
	return nil
}

// recompileLocked rebuilds the policy from the current inputs. With no document
// there is nothing to compile and the store stays empty — a directory alone
// describes no rules.
func (s *Store) recompileLocked() error {
	if s.doc == nil {
		return nil
	}
	p, err := Compile(s.doc, s.dir)
	if err != nil {
		s.failure.Store(&compileFailure{err: err})
		return err
	}
	s.retainLocked(p)
	s.compiled.Store(p)
	s.failure.Store(nil)
	return nil
}

// retainLocked files a freshly compiled policy in the snapshot history.
//
// A version already held is left alone rather than re-filed: recompiling the
// same rules — which every directory update does — must not consume a history
// slot, or a tailnet with a busy user list would evict the policy versions its
// retained traffic still refers to.
func (s *Store) retainLocked(p *Policy) {
	v := p.Version()
	cur := s.history.Load()
	if cur != nil {
		if _, ok := (*cur)[v]; ok {
			return
		}
	}

	next := make(map[string][]Rule, MaxRetainedPolicies)
	if cur != nil {
		maps.Copy(next, *cur)
	}
	next[v] = p.Rules()
	s.order = append(s.order, v)

	for len(s.order) > MaxRetainedPolicies {
		delete(next, s.order[0])
		s.order = s.order[1:]
	}
	s.history.Store(&next)
}

// Snapshot returns the rule list a given policy version compiled to, and whether
// it is still retained.
//
// A caller that gets false must say so — "policy version unavailable" — and must
// NOT fall back to the current rules. Naming today's rule for yesterday's traffic
// is the misattribution this whole mechanism exists to prevent, and it is worse
// than admitting the gap because it looks like an answer.
func (s *Store) Snapshot(version string) ([]Rule, bool) {
	h := s.history.Load()
	if h == nil {
		return nil, false
	}
	r, ok := (*h)[version]
	return r, ok
}

// retained is the number of policy versions currently held, for tests.
func (s *Store) retained() int {
	h := s.history.Load()
	if h == nil {
		return 0
	}
	return len(*h)
}

// Policy returns the current compiled policy, or nil when none has compiled
// yet. Safe to call from any goroutine, including the emit path.
func (s *Store) Policy() *Policy { return s.compiled.Load() }

// Err returns the most recent compile failure, or nil. It is cleared by a
// subsequent successful compile, so it reflects the current state rather than
// the history.
func (s *Store) Err() error {
	if f := s.failure.Load(); f != nil {
		return f.err
	}
	return nil
}
