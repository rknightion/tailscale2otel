package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rknightion/tailscale2otel/internal/dedup"
	"github.com/rknightion/tailscale2otel/internal/semconv"
	"github.com/rknightion/tailscale2otel/internal/telemetry"
)

// MetricAuditEvents counts configuration audit events. Low cardinality only:
// it carries the action and origin, never per-actor or per-target attributes.
const MetricAuditEvents = "tailscale.config.audit.events"

// MetricAuditChanges counts a curated, bounded subset of security- and
// lifecycle-relevant audit changes. Unlike MetricAuditEvents (action+origin),
// it carries a derived change category, the action, and the actor TYPE (a
// bounded enum, never actor identity). See classify.go for the curated set.
const MetricAuditChanges = "tailscale.config.audit.changes"

// auditEventName is the OTEL LogRecord EventName for configuration audit logs.
const auditEventName = "tailscale.config.audit"

// Audit log attribute keys (namespaced under "tailscale.").
const (
	attrAction       = "tailscale.audit.action"
	attrOrigin       = "tailscale.audit.origin"
	attrEventGroupID = "tailscale.audit.event_group_id"
	attrOld          = "tailscale.audit.old"
	attrNew          = "tailscale.audit.new"
	attrDetails      = "tailscale.audit.details"
	attrChange       = "tailscale.audit.change"

	// Actor identity now uses the stable OTel user.* registry (the deprecated
	// enduser.* namespace is gone as of v2.0.0); actor TYPE stays tailscale.*
	// (bounded enum, no convention). Reference the semconv constants, never
	// literals, so the docs/Prometheus names can't drift.
	attrUserID       = semconv.AttrUserID
	attrUserName     = semconv.AttrUserName
	attrUserFullName = semconv.AttrUserFullName
	attrActorType    = "tailscale.actor.type"

	attrTargetID       = "tailscale.target.id"
	attrTargetName     = "tailscale.target.name"
	attrTargetType     = "tailscale.target.type"
	attrTargetProperty = "tailscale.target.property"

	attrError = semconv.AttrErrorMessage
)

// Processor converts Tailscale configuration audit Events into OTEL log records
// and an events counter. It is safe for concurrent use; the same instance is
// shared by the polling collector and streaming receiver.
//
// When a de-duplication set is configured via WithDedup, an event whose key has
// already been seen is dropped silently: no log record and no counter
// increment. The dedup.Set is itself thread-safe and may be shared with other
// components so that an event arriving from more than one source is emitted
// exactly once.
type Processor struct {
	dedup      *dedup.Set
	crossDedup *dedup.Set
}

// Option configures a Processor at construction time.
type Option func(*Processor)

// WithDedup attaches a cross-component de-duplication set. When set is non-nil,
// Process drops any event whose key (see DedupKey) has already been recorded in
// set. Passing a nil set is a no-op, preserving the default (no de-dup)
// behavior.
func WithDedup(set *dedup.Set) Option {
	return func(p *Processor) { p.dedup = set }
}

// WithCrossDedup attaches a cross-SOURCE de-duplication set shared with the
// webhook receiver. When set is non-nil, Process drops any event whose
// CrossSourceKey has already been recorded in set (e.g. the same change already
// emitted via a webhook). This is BEST-EFFORT and SEPARATE from WithDedup's
// eventGroupID-keyed poll<->stream dedup: it reconciles audit and webhook events
// on a normalized (verb, subject, time-bucket) key (see NormalizedCrossKey).
// First-seen-wins, so the surviving copy depends on arrival order. Passing a nil
// set is a no-op.
func WithCrossDedup(set *dedup.Set) Option {
	return func(p *Processor) { p.crossDedup = set }
}

// NewProcessor returns an audit Processor. With no options it behaves exactly
// as before: every event produces one log record and one counter increment.
func NewProcessor(opts ...Option) *Processor {
	p := &Processor{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// DedupKey derives the key for the PROCESSOR's cross-source (poll<->stream)
// de-dup set (see Process, wired via WithDedup). It is SEPARATE from the
// auditlogs collector's window-boundary de-dup, which uses its own time-based
// eventKey() (internal/collector/auditlogs): at a poll-window boundary the SAME
// event reappears with the SAME eventTime, so a time-based key is correct there
// and avoids the tradeoff below. The two formulas are intentionally different.
//
// When the event carries an eventGroupID (every real audit event does) the key
// is the SOURCE-INDEPENDENT identity "<eventGroupID>|<action>|<target.id>|
// <target.property>" — deliberately TIME-FREE. A streamed audit record has no
// inner eventTime (it is timed from the millisecond HEC envelope) while a polled
// one carries the API's nanosecond eventTime, so any timestamp component would
// never match across the poll and stream paths (verified live; S4-9(2)). Within
// one eventGroupID each (action, target, property) is a distinct sub-change
// (verified unique across a real 70-event capture), so dropping the timestamp
// does NOT over-suppress distinct events in practice (D11). TRADEOFF (accepted):
// because this set processes every event (poll-only included), two events sharing
// (eventGroupID, action, target.id, property) at DIFFERENT times would collapse
// to one — they shouldn't occur within a single logical operation, but it is not
// API-guaranteed (pinned by TestProcessSharedDedupSamePropertyTwiceCollapses).
// This cross-source de-dup is a best-effort FAILSAFE: the SUPPORTED setup is to
// pick ONE ingestion method per log type (collectors.auditlogs.source /
// streaming.enabled); the app warns at startup when both are active.
//
// When the eventGroupID is empty (a defensive fallback — not seen in practice)
// the key combines the event time with the action and target ID so distinct
// events sharing a timestamp are not collapsed; such events do not de-dup across
// sources (no group to scope a time-free identity).
func DedupKey(ev Event) string {
	if ev.EventGroupID != "" {
		return ev.EventGroupID + "|" + ev.Action + "|" + ev.Target.ID + "|" + ev.Target.Property
	}
	t := ev.EventTime.UTC().Format(time.RFC3339Nano)
	return t + "|" + ev.Action + "|" + ev.Target.ID
}

// ProcessAll converts every Event in resp, emitting one log record and one
// counter increment per event.
func (p *Processor) ProcessAll(resp ConfigurationResponse, e telemetry.Emitter) {
	for _, ev := range resp.Logs {
		p.Process(ev, e)
	}
}

// Process converts a single Event into an OTEL log record plus a counter
// increment. The log carries the full actor/target context; the counter carries
// only low-cardinality action and origin attributes.
func (p *Processor) Process(ev Event, e telemetry.Emitter) {
	if p.dedup != nil && !p.dedup.Add(DedupKey(ev)) {
		// Already seen via another source: emit nothing.
		return
	}
	if p.crossDedup != nil {
		if key, ok := CrossSourceKey(ev); ok && !p.crossDedup.Add(key) {
			// The same change was already emitted via the webhook receiver (or a
			// prior audit event mapping to it): emit nothing.
			return
		}
	}

	severity := telemetry.SeverityInfo
	if ev.Error != "" {
		severity = telemetry.SeverityWarn
	}

	attrs := telemetry.Attrs{
		attrAction:         ev.Action,
		attrOrigin:         ev.Origin,
		attrEventGroupID:   ev.EventGroupID,
		attrUserID:         ev.Actor.ID,
		attrUserName:       ev.Actor.LoginName,
		attrUserFullName:   ev.Actor.DisplayName,
		attrActorType:      ev.Actor.Type,
		attrTargetID:       ev.Target.ID,
		attrTargetName:     ev.Target.Name,
		attrTargetType:     ev.Target.Type,
		attrTargetProperty: ev.Target.Property,
	}
	if ev.Error != "" {
		attrs[attrError] = ev.Error
	}
	if s, ok := renderRaw(ev.Old); ok {
		attrs[attrOld] = s
	}
	if s, ok := renderRaw(ev.New); ok {
		attrs[attrNew] = s
	}
	if ev.ActionDetails != "" {
		attrs[attrDetails] = ev.ActionDetails
	}

	e.LogEvent(telemetry.Event{
		Name:      docAuditLog.Name,
		Body:      summary(ev),
		Severity:  severity,
		Timestamp: ev.EventTime,
		Attrs:     attrs,
	})

	// Action/origin are attacker-controlled wire values on the streaming
	// ingestion path (this Process method is shared with the poller — see
	// package doc). Normalize both to a fixed, bounded admit-set before using
	// them as metric attributes so a flood of crafted values cannot mint
	// unbounded cardinality on this counter (#77); known API values pass
	// through unchanged.
	e.Counter(docAuditEvents.Name, docAuditEvents.Unit, docAuditEvents.Description, 1, telemetry.Attrs{
		attrAction: normalizeAction(ev.Action),
		attrOrigin: normalizeOrigin(ev.Origin),
	})

	if cat, ok := classifyChange(ev); ok {
		e.Counter(docAuditChanges.Name, docAuditChanges.Unit, docAuditChanges.Description, 1, telemetry.Attrs{
			attrChange:    cat,
			attrAction:    normalizeAction(ev.Action),
			attrActorType: normalizeActorType(ev.Actor.Type),
		})
	}
}

// renderRaw turns a polymorphic audit old/new value into a string attribute
// value. It reports ok=false (attribute omitted) when the raw value is nil,
// empty, or the JSON literal null. A JSON string is returned unquoted; any
// other JSON (object, array, number, bool) is returned as compact raw JSON.
func renderRaw(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			if s == "" {
				return "", false
			}
			return s, true
		}
		// Malformed string literal: fall through to raw rendering.
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		// Not valid JSON; emit the trimmed bytes verbatim.
		return string(trimmed), true
	}
	return buf.String(), true
}

// summary builds the human-readable log body using only non-PII enum fields, e.g.
// "CREATE on NODE.ALLOWED_IPS via ADMIN_CONSOLE".
// PII identifiers (actor login/display, target name/id) are emitted as attributes
// (user.name, tailscale.target.name, etc.) where they remain queryable
// and subject to pii_filter redaction — they are intentionally absent from the body.
func summary(ev Event) string {
	if ev.Target.Property != "" {
		return fmt.Sprintf("%s on %s.%s via %s", ev.Action, ev.Target.Type, ev.Target.Property, ev.Origin)
	}
	return fmt.Sprintf("%s on %s via %s", ev.Action, ev.Target.Type, ev.Origin)
}
