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
}

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
	s.compiled.Store(p)
	s.failure.Store(nil)
	return nil
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
