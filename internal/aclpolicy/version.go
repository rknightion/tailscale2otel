package aclpolicy

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
)

// versionLen is how much of the digest a version string carries. Eight bytes is
// far more than enough to make an accidental collision between the handful of
// policy revisions one tailnet produces impossible in practice, and short enough
// to sit in a JSON row and a log line without dominating either.
const versionLen = 16

// computeVersion derives a policy's identity from its compiled rule list.
//
// The hash covers each rule's kind and source text, in order, so it changes on
// any edit AND on any reorder — both of which move what a given index means, and
// both of which would otherwise silently re-attribute retained traffic to a rule
// that never carried it (#302).
//
// It deliberately does NOT cover the raw document. Reformatting an ACL, or
// re-serializing it through the API, leaves the rules an operator wrote exactly
// as they were; churning the version there would discard attributions that are
// still perfectly valid. It also does not cover the identity directory: a role
// change alters what a rule MATCHES, but not which rule text index N is, and the
// index is the only thing a retained observation joins on.
//
// The length prefix on each field is what makes the encoding unambiguous — a
// plain concatenation would let a rule ending in one character and the next
// beginning with another hash identically to a different split.
func computeVersion(rules []Rule) string {
	h := sha256.New()
	var lenBuf [8]byte
	write := func(s string) {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
		_, _ = h.Write(lenBuf[:])
		_, _ = io.WriteString(h, s)
	}
	for _, r := range rules {
		write(r.Kind)
		write(r.Source)
	}
	return hex.EncodeToString(h.Sum(nil))[:versionLen]
}

// Version identifies this policy's rule list. It is stable across recompiles of
// the same rules and changes whenever any rule's text or position changes, which
// is exactly the property that makes (Version, rule index) a safe join key for
// traffic retained across a policy edit.
//
// A policy with no rules still has a version, so an observation evaluated
// against an empty policy stays distinguishable from one that was never
// evaluated at all.
func (p *Policy) Version() string {
	if p == nil {
		return ""
	}
	return p.version
}
