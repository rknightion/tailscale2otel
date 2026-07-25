// Package tsscope classifies Tailscale API credential scopes by privilege
// semantics rather than by count.
//
// Why this exists (#415): a scope *count* is a actively misleading blast-radius
// signal. A credential holding the single scope `all` has unrestricted read and
// write over the whole tailnet — including endpoints that do not exist yet — and
// scores 1. A credential holding eleven narrow `*:read` scopes scores 11 and
// trips a "broad scope" threshold, despite being categorically less dangerous.
//
// The classification rule is deliberately MECHANICAL, not a lookup table.
// Neither spec/tailscale-api.json nor the published Tailscale documentation
// enumerates the scope vocabulary as a closed set — the OpenAPI `scopes` field
// carries no `enum`, only illustrative examples — and the docs explicitly state
// that `all`/`all:read` cover "new APIs created in the future". A hardcoded
// scope table would therefore misclassify every scope Tailscale adds after this
// file was written. Instead:
//
//	<resource>[:<subresource>][:read]
//
// If the LAST colon-separated segment is "read", the scope is read-only;
// otherwise it is write-capable. Verified against
// tailscale.com/docs/reference/trust-credentials (e.g. `devices:core` grants
// read+write device lifecycle/tags/authorize-remove, `devices:core:read` is the
// read-only subset; `logs:network:read` shows the three-segment form).
package tsscope

import "strings"

// Class is the bounded privilege classification of a credential's scope set.
// The values are a CLOSED set, safe as a metric label.
type Class string

const (
	// ClassNone means the credential carries no scopes at all (auth keys, which
	// have capabilities rather than scopes).
	ClassNone Class = "none"
	// ClassRead means every scope is a narrow, read-only resource scope.
	ClassRead Class = "read"
	// ClassWrite means at least one narrow scope is write-capable.
	ClassWrite Class = "write"
	// ClassAllRead means the credential holds `all:read` — read access to the
	// entire tailnet, including future APIs.
	ClassAllRead Class = "all_read"
	// ClassAll means the credential holds `all` — unrestricted read AND write
	// over the entire tailnet, including future APIs. The maximum blast radius.
	ClassAll Class = "all"
)

// ScopeAll and ScopeAllRead are the two maximal-breadth scope strings.
const (
	ScopeAll     = "all"
	ScopeAllRead = "all:read"
)

// readSuffix is the trailing segment marking a scope read-only.
const readSuffix = "read"

// rank orders classes by blast radius so Classify can take the maximum.
//
// The ordering is a deliberate judgement call, documented so it is not silently
// "fixed" later: any write capability outranks any read-only grant, because a
// write can mutate the tailnet while a read cannot; and within each half,
// tailnet-wide outranks narrow. Hence all > write > all_read > read > none.
// `all` sits top because it is both tailnet-wide AND write-capable.
var rank = map[Class]int{
	ClassNone:    0,
	ClassRead:    1,
	ClassAllRead: 2,
	ClassWrite:   3,
	ClassAll:     4,
}

// Classes returns every Class, weakest first. Useful for zero-seeding a bounded
// label set.
func Classes() []Class {
	return []Class{ClassNone, ClassRead, ClassAllRead, ClassWrite, ClassAll}
}

// normalize trims and lowercases a scope. Scope strings arrive from the API and
// from operator-supplied config, so casing and padding are both plausible.
func normalize(scope string) string {
	return strings.ToLower(strings.TrimSpace(scope))
}

// IsReadOnly reports whether scope grants read-only access, i.e. its last
// colon-separated segment is "read". An empty scope is not read-only.
//
// Note the last-segment requirement is load-bearing: a hypothetical scope named
// "read:devices" must NOT be treated as read-only just because "read" appears
// in it.
func IsReadOnly(scope string) bool {
	s := normalize(scope)
	if s == "" {
		return false
	}
	_, last, found := cutLast(s, ":")
	if !found {
		return false
	}
	return last == readSuffix
}

// cutLast splits s around the final instance of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// classifyOne classifies a single scope string.
func classifyOne(scope string) Class {
	s := normalize(scope)
	switch {
	case s == "":
		return ClassNone
	case s == ScopeAll:
		return ClassAll
	case s == ScopeAllRead:
		return ClassAllRead
	case IsReadOnly(s):
		return ClassRead
	default:
		// Anything else — including a scope Tailscale has not invented yet —
		// is assumed write-capable. Erring toward the more dangerous reading is
		// the correct default for a security posture signal.
		return ClassWrite
	}
}

// Classify returns the blast-radius class of a whole scope set: the highest-
// ranked class among its members. An empty or all-blank set is ClassNone.
func Classify(scopes []string) Class {
	out := ClassNone
	for _, s := range scopes {
		if c := classifyOne(s); rank[c] > rank[out] {
			out = c
		}
	}
	return out
}

// Satisfies reports whether the granted scope set covers the required scope.
//
// This is used for the startup preflight (#425), which is advisory only: the
// Tailscale server remains authoritative, and a runtime 403 is classified
// separately (see internal/apistate). Because a false "insufficient" warning is
// cheap and a false "sufficient" one is silent, the rules below are deliberately
// conservative — they never infer coverage from a shared prefix.
//
// An empty requirement is satisfied by anything (the operation needs no scope).
func Satisfies(granted []string, required string) bool {
	req := normalize(required)
	if req == "" {
		return true
	}
	reqReadOnly := IsReadOnly(req)
	for _, g := range granted {
		gs := normalize(g)
		switch {
		case gs == "":
			continue
		case gs == ScopeAll:
			// Unrestricted read+write covers every requirement.
			return true
		case gs == ScopeAllRead && reqReadOnly:
			// Tailnet-wide read covers any read-only requirement, but NOT a
			// write one.
			return true
		case gs == req:
			return true
		case reqReadOnly && gs == strings.TrimSuffix(req, ":"+readSuffix):
			// Holding the write-capable parent (`devices:core`) implies its
			// read-only child (`devices:core:read`). The converse is false.
			return true
		}
	}
	return false
}
