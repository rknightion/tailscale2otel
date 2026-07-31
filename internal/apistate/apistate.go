// Package apistate models the availability of an individual Tailscale API
// operation, and the coverage of per-entity subrequests.
//
// Why this exists (#420): before this package, every collector hand-rolled its
// own status-code predicate off tsapi.StatusError.Code, and they disagreed.
// flowlogs read 403 as "premium feature off"; oauthapps read 403 OR 404 as
// "feature off"; logstream read ANY 4xx (including a genuine 401) as "not
// configured"; the devices collector's per-device posture and invite
// subrequests discarded every error silently, with no log and no metric; and
// the postureintegrations and webhooks collectors propagated everything as a
// hard scrape failure. Meanwhile the one unpaused alert in the security group
// fired critical on any 401 OR 403 and declared "all polling fails".
//
// The consequence was that a tailnet simply lacking a premium feature looked
// identical to one with a revoked credential. This package gives every call
// site one bounded vocabulary so the two can never be conflated again.
//
// The rule that matters most: an ambiguous 403 defaults to StateScopeDenied,
// NOT StateDisabled. Only an operation that explicitly declares
// Disposition{DisabledOn: []int{403}} — because upstream documents 403 as its
// feature gate — reads it as disabled. Defaulting the other way is how a real
// permission regression turns into a healthy-looking zero.
package apistate

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// State is the bounded availability state of one API operation. The values are
// a CLOSED set, safe as a metric label.
type State string

const (
	// StateUnknown means the operation has not been probed yet, or the probe was
	// abandoned during shutdown. Never a failure signal.
	StateUnknown State = "unknown"
	// StateSupported means the last call succeeded.
	StateSupported State = "supported"
	// StateDisabled means the feature is not enabled or not configured on this
	// tailnet. Expected, benign, and NOT an alertable condition.
	StateDisabled State = "disabled"
	// StateScopeDenied means the credential is valid but lacks the scope for
	// this operation (HTTP 403). Actionable: the operator must widen the scope
	// or disable the collector.
	StateScopeDenied State = "scope_denied"
	// StateCredentialRejected means the credential itself was rejected (HTTP
	// 401) — expired, revoked or wrong. Actionable and usually tailnet-wide.
	//
	//nolint:gosec // G101 false positive: this is a metric label value naming an
	// outcome, not a credential. It holds no secret material.
	StateCredentialRejected State = "credential_rejected"
	// StateTransientFailure means a retryable failure: rate limiting, a server
	// error, a network problem or a timeout.
	StateTransientFailure State = "transient_failure"
	// StateRequestRejected means the API refused the request itself (a 4xx that
	// is not 401/403/404): malformed body, wrong method, unsupported media type.
	// TERMINAL and OUR fault — retrying the same request can never succeed, so
	// this is deliberately NOT transient_failure.
	//
	// This state exists because the absence of it hid a real bug: the acl
	// validate call POSTed an empty body, the API answered 400 on every single
	// tick, and it surfaced as transient_failure — indistinguishable from
	// upstream flakiness — so a feature that had never once worked looked merely
	// unlucky (#523).
	StateRequestRejected State = "request_rejected"
)

// States returns every State, in a stable order. Emitters zero-seed this whole
// set so a bounded, non-churning series set can be emitted with a plain gauge.
func States() []State {
	return []State{
		StateUnknown,
		StateSupported,
		StateDisabled,
		StateScopeDenied,
		StateCredentialRejected,
		StateTransientFailure,
		StateRequestRejected,
	}
}

// Disposition declares, per operation, how the genuinely ambiguous HTTP
// statuses should be read for that operation.
type Disposition struct {
	// DisabledOn lists status codes that mean "this optional feature is not
	// enabled on the tailnet" for THIS operation specifically. Upstream reuses
	// 403 for both "you lack the scope" and "your plan does not include this",
	// and nothing in the response distinguishes them, so each operation must
	// state its own reading. Leave empty unless upstream documents the code as
	// a feature gate.
	DisabledOn []int
}

// Classify maps an error returned by a Tailscale API call to a State.
func Classify(err error, d Disposition) State {
	if err == nil {
		return StateSupported
	}

	// Shutdown must not flap a collector into a failure state. The scheduler
	// already suppresses scrape metrics for shutdown cancellation; mirror that
	// here so the availability signal agrees with it.
	if errors.Is(err, context.Canceled) {
		return StateUnknown
	}

	if se, ok := errors.AsType[*tsapi.StatusError](err); ok {
		if slices.Contains(d.DisabledOn, se.Code) {
			return StateDisabled
		}
		switch se.Code {
		case 401:
			return StateCredentialRejected
		case 403:
			return StateScopeDenied
		case 404:
			// The endpoint is not present for this tailnet — an alpha or
			// plan-gated API. Absence, not a credential problem.
			return StateDisabled
		case 429:
			// Rate limiting: genuinely retryable.
			return StateTransientFailure
		default:
			if se.Code >= 400 && se.Code < 500 {
				// A 4xx we did not special-case above means the API rejected the
				// request we built. Retrying it unchanged cannot help, so this
				// must never read as transient.
				return StateRequestRejected
			}
			// 5xx and any other status: retryable or unclassified. Never
			// silently benign.
			return StateTransientFailure
		}
	}

	// Timeouts, DNS failures, connection resets, decode errors.
	return StateTransientFailure
}

// Actionable reports whether a state represents a problem an operator should
// act on. StateDisabled and StateUnknown are expected conditions, not faults —
// this is the predicate that stops "feature not enabled" from paging someone.
func (s State) Actionable() bool {
	switch s {
	case StateScopeDenied, StateCredentialRejected, StateTransientFailure, StateRequestRejected:
		return true
	default:
		return false
	}
}

// Tracker records the latest availability state per (collector, operation) for
// in-process introspection: the admin status page and the configuration-to-
// capability matrix (#430) read it.
//
// It emits NO OTLP. Metrics come from EmitAvailability at the call site, so the
// two concerns stay independent and the status page keeps working even when
// self-observability emission is switched off (same posture as the collector
// framework's StatusTracker).
//
// Safe for concurrent use; a nil *Tracker is a no-op, so call sites never need
// a nil check.
type Tracker struct {
	mu sync.Mutex
	m  map[trackerKey]Entry
}

type trackerKey struct {
	collector string
	operation string
}

// Entry is one operation's latest observed availability.
type Entry struct {
	Collector string
	Operation string
	State     State
	LastProbe time.Time
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker { return &Tracker{m: make(map[trackerKey]Entry)} }

// Record stores the outcome of one probe of an operation.
func (t *Tracker) Record(collector, operation string, s State, at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = make(map[trackerKey]Entry)
	}
	k := trackerKey{collector: collector, operation: operation}
	t.m[k] = Entry{Collector: collector, Operation: operation, State: s, LastProbe: at}
}

// Snapshot returns every recorded operation, sorted by collector then
// operation, so the status page and tests get a stable order.
func (t *Tracker) Snapshot() []Entry {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.m) == 0 {
		return nil
	}
	out := make([]Entry, 0, len(t.m))
	for _, e := range t.m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Collector != out[j].Collector {
			return out[i].Collector < out[j].Collector
		}
		return out[i].Operation < out[j].Operation
	})
	return out
}

// Coverage records how many per-entity subrequests were attempted and how many
// succeeded, per (collector, subrequest type), with failures broken down by
// State (#421).
//
// The devices collector issues one posture-attributes and one device-invites
// call PER DEVICE. Any of those can fail on its own — a missing
// device_invites:read scope 403s every one of them — while the collector's own
// tick still returns nil and reports a clean scrape. Coverage makes that
// partial blindness visible instead of letting it read as "no invites exist".
//
// Safe for concurrent use; a nil *Coverage is a no-op.
type Coverage struct {
	mu sync.Mutex
	m  map[coverageKey]*CoverageEntry
}

type coverageKey struct {
	collector  string
	subrequest string
}

// CoverageEntry is one subrequest type's tally for the current cycle.
type CoverageEntry struct {
	Collector  string
	Subrequest string
	Attempted  int
	Succeeded  int
	Failed     int
	// Failures counts failed attempts by the state that caused them, so an
	// operator can tell "403 on every device" from "one flaky timeout".
	Failures map[State]int
}

// Ratio returns the fraction of attempts that succeeded, in [0,1].
//
// With nothing attempted the answer is 1, not 0: a tailnet with no devices has
// complete coverage of the devices it has. Returning 0 there would leave every
// empty tailnet permanently reading as fully degraded.
func (e CoverageEntry) Ratio() float64 {
	if e.Attempted == 0 {
		return 1
	}
	return float64(e.Succeeded) / float64(e.Attempted)
}

// NewCoverage returns an empty Coverage tally.
func NewCoverage() *Coverage { return &Coverage{m: make(map[coverageKey]*CoverageEntry)} }

// Record tallies one subrequest attempt with its outcome state.
func (c *Coverage) Record(collector, subrequest string, s State) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[coverageKey]*CoverageEntry)
	}
	k := coverageKey{collector: collector, subrequest: subrequest}
	e := c.m[k]
	if e == nil {
		e = &CoverageEntry{Collector: collector, Subrequest: subrequest, Failures: make(map[State]int)}
		c.m[k] = e
	}
	e.Attempted++
	if s == StateSupported {
		e.Succeeded++
		return
	}
	e.Failed++
	e.Failures[s]++
}

// ResetCollector clears the tallies belonging to one collector, leaving every
// other collector's untouched. Collectors call this at the start of a tick so
// their snapshot describes the most recent complete pass rather than an
// ever-growing lifetime total.
//
// This is the method collectors must use, NOT Reset. One *Coverage is shared per
// tailnet runtime so the status page can read a single object, which means a
// global reset would let any collector wipe another's tallies mid-tick and then
// emit the other's series as its own. Devices is the only N+1 collector today,
// so that is latent rather than live — but it is exactly the kind of sharing bug
// that only shows up once a second collector grows a subrequest.
func (c *Coverage) ResetCollector(collector string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if k.collector == collector {
			delete(c.m, k)
		}
	}
}

// Reset clears every tally, for tests and for a full teardown. Production
// collectors want ResetCollector.
func (c *Coverage) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = make(map[coverageKey]*CoverageEntry)
}

// Snapshot returns the current tallies, sorted by collector then subrequest.
func (c *Coverage) Snapshot() []CoverageEntry {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) == 0 {
		return nil
	}
	out := make([]CoverageEntry, 0, len(c.m))
	for _, e := range c.m {
		cp := *e
		cp.Failures = maps.Clone(e.Failures)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Collector != out[j].Collector {
			return out[i].Collector < out[j].Collector
		}
		return out[i].Subrequest < out[j].Subrequest
	})
	return out
}
