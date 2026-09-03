// Package eventstore retains a bounded, recent window of audit and webhook
// events in memory so the admin event explorer (#300) can show what happened
// on a tailnet without requiring a metrics/logs backend in the loop.
//
// Unlike internal/flowstore, this package does NOT aggregate. A flow record is
// one connection among potentially tens of thousands per poll, so flowstore
// buckets and ranks them; an audit or webhook event is already the meaningful
// unit — one action, by one actor, on one target — so there is nothing to
// average over a time bucket. Memory is therefore a plain bounded ring:
// events are appended in the order Record is called, and once the ring is
// full the oldest retained event is evicted to make room for the newest.
//
// It shares flowstore's non-negotiable properties:
//   - Never blocks or fails the emit path. Record takes a short lock and a
//     single append; there is no I/O and no error to propagate. Callers
//     (internal/audit, internal/webhook) call it AFTER emitting the
//     corresponding OTLP log record and counters, so a full ring never
//     affects what is exported.
//   - Bounded by both event count and attacker-controlled text bytes. Overflow
//     evicts the oldest event and is counted (Stats.Evicted), never silently
//     dropped.
//   - Not a second telemetry pipeline. OTLP remains the system of record;
//     this is a bounded, recent, admin-authenticated view for interactive use,
//     and it is lost on restart.
package eventstore

import (
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/boundedtext"
)

// DefaultCapacity is used when NewMemory is given a non-positive capacity.
const DefaultCapacity = 5000

// MaxDetailBytes bounds the Details and Summary fields retained per event.
// Audit old/new payloads and a webhook policyUpdate body can each carry an
// entire ACL document; retaining that verbatim for up to the ring's capacity
// would make this one field unbounded in practice even though the event COUNT
// is bounded. Truncated content is marked (Event.Truncated) so the page can
// say the diff was cut rather than silently showing a partial one as if it
// were complete.
const (
	MaxFieldBytes  = 4096
	MaxEventBytes  = 32 << 10
	MaxDetailBytes = MaxFieldBytes
)

// truncationSuffix marks a Details/Summary value cut to MaxDetailBytes.
const truncationSuffix = "…[truncated]"

// Truncate bounds s to MaxDetailBytes, appending truncationSuffix when cut.
func Truncate(s string) (string, bool) {
	if len(s) <= MaxDetailBytes {
		return boundedtext.String(s, MaxDetailBytes), !utf8Equivalent(s)
	}
	return boundedtext.String(s, MaxDetailBytes-len(truncationSuffix)) + truncationSuffix, true
}

func utf8Equivalent(s string) bool { return boundedtext.String(s, len(s)) == s }

// Source distinguishes which processor fed an event into the store.
type Source string

const (
	SourceAudit   Source = "audit"
	SourceWebhook Source = "webhook"
)

// The severity vocabulary, held here (rather than importing internal/telemetry)
// for the same "stay a leaf" reason as flowstore's verdict/path vocabularies.
const (
	SeverityInfo = "info"
	SeverityWarn = "warn"
)

// Event is one audit or webhook occurrence, as much as this package retains.
// Empty string means "not carried" throughout, mirroring flowstore.Observation.
type Event struct {
	// Time is when the underlying occurrence happened (audit EventTime, webhook
	// event timestamp), not when Record was called. A zero Time is stamped with
	// the store's clock at Record time.
	Time    time.Time `json:"time"`
	Source  Source    `json:"source"`
	Tailnet string    `json:"tailnet,omitempty"`

	// Action is the audit verb (e.g. "CREATE") or, for a webhook event, its
	// bounded type dimension (e.g. "nodeCreated") — the closest each source
	// has to "what happened".
	Action string `json:"action,omitempty"`
	// Type further classifies the event: for audit this is the target's
	// upstream Type (e.g. "CONFIG"); for webhook it duplicates Action, since a
	// webhook event has no separate action/type split. Kept as its own field
	// so the explorer's "type" filter (acceptance criteria) has one place to
	// look regardless of source.
	Type string `json:"type,omitempty"`
	// Origin is audit-only: how the change was made (e.g. "ADMIN_CONSOLE").
	Origin string `json:"origin,omitempty"`

	ActorID   string `json:"actor_id,omitempty"`
	ActorName string `json:"actor_name,omitempty"`
	ActorType string `json:"actor_type,omitempty"`

	TargetID       string `json:"target_id,omitempty"`
	TargetName     string `json:"target_name,omitempty"`
	TargetType     string `json:"target_type,omitempty"`
	TargetProperty string `json:"target_property,omitempty"`

	// Severity is SeverityInfo or SeverityWarn.
	Severity string `json:"severity"`
	// Error is the upstream error string, when the occurrence carried one.
	Error string `json:"error,omitempty"`

	// Summary is a short, human-readable description built only from
	// non-identifying enum fields (mirrors internal/audit's summary()) — safe
	// to show unconditionally.
	Summary string `json:"summary,omitempty"`
	// Details is the free-form payload: an audit old/new diff, or a webhook
	// message body. Bounded to MaxDetailBytes by Record; Truncated reports
	// whether that bound was hit.
	Details   string `json:"details,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`

	// seq is the ingestion sequence: monotonically increasing per Record call,
	// used only to identify a row for cursor pagination (see Query.Cursor).
	// Unexported: ordering metadata, not part of the event the API describes.
	seq uint64
}

// Option configures a Memory store.
type Option func(*Memory)

// WithClock overrides the store's clock, for deterministic tests. A nil clock
// is ignored.
func WithClock(now func() time.Time) Option {
	return func(m *Memory) {
		if now != nil {
			m.now = now
		}
	}
}

// Memory is a bounded ring of recent audit/webhook events. Safe for
// concurrent use.
type Memory struct {
	mu       sync.Mutex
	now      func() time.Time
	capacity int

	// events holds up to capacity live rows at events[start:], in the order
	// Record was called. Evicting the oldest advances start instead of
	// shifting the slice, exactly as flowstore's recent ring does, so Record
	// stays an O(1) append rather than an O(capacity) memmove per call.
	events []Event
	start  int
	seq    uint64

	recorded      int64
	evicted       int64
	retainedBytes int
}

// NewMemory returns a store retaining at most capacity events. A non-positive
// capacity selects DefaultCapacity.
func NewMemory(capacity int, opts ...Option) *Memory {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	m := &Memory{
		capacity: capacity,
		events:   make([]Event, 0, capacity),
		now:      time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// liveLocked is the retained window of the ring. Everything before start has
// been evicted and is waiting to be reclaimed; callers must never see it.
// Callers must hold m.mu.
func (m *Memory) liveLocked() []Event { return m.events[m.start:] }

// Record appends ev to the ring, evicting the oldest retained event first if
// the ring is already at capacity. It never blocks on I/O and never returns an
// error: there is nothing for a caller on the emit path to check.
func (m *Memory) Record(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = m.now()
	}
	if s, cut := Truncate(ev.Details); cut {
		ev.Details = s
		ev.Truncated = true
	}
	if s, cut := Truncate(ev.Summary); cut {
		ev.Summary = s
		ev.Truncated = true
	}
	if normalizeEvent(&ev) {
		ev.Truncated = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.liveLocked()) >= m.capacity {
		m.retainedBytes -= eventStringBytes(m.events[m.start])
		m.start++
		m.evicted++
	}
	ev.seq = m.seq
	m.seq++
	m.recorded++
	m.events = append(m.events, ev)
	m.retainedBytes += eventStringBytes(ev)

	// Reclaim the dead prefix once it has grown to a full window, exactly as
	// flowstore's recent ring does: the copy costs O(capacity) but happens at
	// most once per capacity inserts, so eviction stays amortized O(1).
	if m.start >= m.capacity {
		n := copy(m.events, m.events[m.start:])
		m.events = m.events[:n]
		m.start = 0
	}
}

func normalizeEvent(ev *Event) bool {
	values := []string{string(ev.Source), ev.Tailnet, ev.Action, ev.Type, ev.Origin,
		ev.ActorID, ev.ActorName, ev.ActorType, ev.TargetID, ev.TargetName, ev.TargetType,
		ev.TargetProperty, ev.Severity, ev.Error, ev.Summary, ev.Details}
	original := append([]string(nil), values...)
	boundedtext.StringsBudget(values, MaxFieldBytes, MaxEventBytes)
	ev.Source = Source(values[0])
	ev.Tailnet, ev.Action, ev.Type, ev.Origin = values[1], values[2], values[3], values[4]
	ev.ActorID, ev.ActorName, ev.ActorType = values[5], values[6], values[7]
	ev.TargetID, ev.TargetName, ev.TargetType, ev.TargetProperty = values[8], values[9], values[10], values[11]
	ev.Severity, ev.Error, ev.Summary, ev.Details = values[12], values[13], values[14], values[15]
	for i := range values {
		if values[i] != original[i] {
			return true
		}
	}
	return false
}

func eventStringBytes(ev Event) int {
	return len(ev.Source) + len(ev.Tailnet) + len(ev.Action) + len(ev.Type) + len(ev.Origin) +
		len(ev.ActorID) + len(ev.ActorName) + len(ev.ActorType) + len(ev.TargetID) + len(ev.TargetName) +
		len(ev.TargetType) + len(ev.TargetProperty) + len(ev.Severity) + len(ev.Error) + len(ev.Summary) + len(ev.Details)
}

// Filter narrows Page to rows matching every non-empty field (an AND across
// dimensions), covering the acceptance criteria's "actor, action, target,
// severity, error, and type" (time is Query.Start/End). Actor/Action/Target
// are substring, case-insensitive matches — the operator is searching, not
// querying a key/value store. Source/Severity/Type are exact, case-insensitive
// matches: each is a closed-ish enumeration, and a substring match would let
// "no" match unrelated values containing it.
type Filter struct {
	Source     string // exact: "audit" or "webhook"
	Actor      string // substring, matches ActorID OR ActorName
	Action     string // substring, matches Action
	Target     string // substring, matches TargetID OR TargetName
	Severity   string // exact
	Type       string // exact, matches Type
	ErrorsOnly bool   // when true, match only events carrying a non-empty Error
}

func (f Filter) isZero() bool { return f == Filter{} }

func (f Filter) matches(e Event) bool {
	if f.Source != "" && !strings.EqualFold(string(e.Source), f.Source) {
		return false
	}
	if f.Actor != "" && !containsFold(e.ActorID, f.Actor) && !containsFold(e.ActorName, f.Actor) {
		return false
	}
	if f.Action != "" && !containsFold(e.Action, f.Action) {
		return false
	}
	if f.Target != "" && !containsFold(e.TargetID, f.Target) && !containsFold(e.TargetName, f.Target) {
		return false
	}
	if f.Severity != "" && !strings.EqualFold(e.Severity, f.Severity) {
		return false
	}
	if f.Type != "" && !strings.EqualFold(e.Type, f.Type) {
		return false
	}
	if f.ErrorsOnly && e.Error == "" {
		return false
	}
	return true
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// Query is one request against the ring: a time window, a page size, a resume
// point, and an optional filter. Mirrors flowstore.RecentQuery.
type Query struct {
	Start, End time.Time
	Limit      int
	// Cursor resumes after a previous page: 0 means "start from the newest",
	// otherwise only rows with seq < Cursor are considered. It is the seq of
	// the last row a previous Page call returned.
	Cursor uint64
	Filter Filter
}

// Page is one page of the ring, plus the bookkeeping an operator needs to
// tell "nothing else matches" apart from "the ring no longer holds it".
// Mirrors flowstore.RecentPage.
type Page struct {
	// Rows is newest-first.
	Rows []Event
	// NextCursor is the seq of the last row in Rows when further matching rows
	// remain beyond Limit, and 0 when there are none.
	NextCursor uint64
	// Matched is how many rows in [Start, End] satisfy Filter, ignoring both
	// Limit and Cursor.
	Matched int
	// Retained is how many rows the ring currently holds, ignoring both the
	// window and the filter.
	Retained int
	// Truncated reports the ring is at capacity, so an older matching row may
	// already have been evicted rather than simply not existing.
	Truncated bool
}

// Page returns a filtered, cursor-paginated page of the ring, newest first.
//
// The ring is NOT assumed sorted by Event.Time — Record appends in call
// order, and an out-of-order poll backfill or replay can carry an older event
// time than what is already retained. So, unlike flowstore.RecentRange, this
// walks every live row rather than stopping at the first one before Start:
// stopping early would be correct only under a sortedness guarantee this
// package deliberately does not make.
func (m *Memory) Page(q Query) Page {
	end := q.End
	m.mu.Lock()
	defer m.mu.Unlock()
	if end.IsZero() {
		end = m.now()
	}

	live := m.liveLocked()
	page := Page{Retained: len(live), Truncated: len(live) >= m.capacity}

	var rows []Event
	if q.Limit > 0 {
		rows = make([]Event, 0, min(q.Limit, len(live)))
	}
	var haveMore bool
	for i := len(live) - 1; i >= 0; i-- {
		e := live[i]
		if e.Time.After(end) {
			continue
		}
		if !q.Start.IsZero() && e.Time.Before(q.Start) {
			continue
		}
		if !q.Filter.isZero() && !q.Filter.matches(e) {
			continue
		}
		page.Matched++
		if q.Cursor != 0 && e.seq >= q.Cursor {
			// Already served by an earlier page.
			continue
		}
		if q.Limit <= 0 || len(rows) >= q.Limit {
			haveMore = true
			continue
		}
		rows = append(rows, e)
	}
	page.Rows = rows
	if haveMore && len(rows) > 0 {
		page.NextCursor = rows[len(rows)-1].seq
	}
	return page
}

// Stats describes the store's own state, for the admin status/events surface.
type Stats struct {
	Capacity      int       `json:"capacity"`
	Retained      int       `json:"retained"`
	Recorded      int64     `json:"recorded"`
	Evicted       int64     `json:"evicted"`
	RetainedBytes int       `json:"retained_bytes"`
	MaxBytes      int       `json:"max_bytes"`
	Earliest      time.Time `json:"earliest"`
	Latest        time.Time `json:"latest"`
}

// Stats returns the store's current state.
func (m *Memory) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	live := m.liveLocked()
	s := Stats{Capacity: m.capacity, Retained: len(live), Recorded: m.recorded, Evicted: m.evicted,
		RetainedBytes: m.retainedBytes, MaxBytes: m.capacity * MaxEventBytes}
	for _, e := range live {
		if s.Earliest.IsZero() || e.Time.Before(s.Earliest) {
			s.Earliest = e.Time
		}
		if e.Time.After(s.Latest) {
			s.Latest = e.Time
		}
	}
	return s
}
