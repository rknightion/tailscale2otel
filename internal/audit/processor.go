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

	attrEndUserID      = "enduser.id"
	attrActorLogin     = "tailscale.actor.login"
	attrActorDisplay   = "tailscale.actor.display"
	attrTargetID       = "tailscale.target.id"
	attrTargetName     = "tailscale.target.name"
	attrTargetType     = "tailscale.target.type"
	attrTargetProperty = "tailscale.target.property"

	attrError = "error"
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

// DedupKey derives a stable de-duplication key for a single audit event. It is
// the single home for the key formula shared by the auditlogs collector and the
// processor's cross-component de-dup. When the event carries an eventGroupID it
// identifies a logical change, so the key is "<eventGroupID>|<eventTime>". When
// the eventGroupID is empty the key instead combines the event time with the
// action and target ID, so distinct events sharing a timestamp are not
// collapsed into one.
func DedupKey(ev Event) string {
	t := ev.EventTime.UTC().Format(time.RFC3339Nano)
	if ev.EventGroupID != "" {
		return ev.EventGroupID + "|" + t
	}
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
		attrEndUserID:      ev.Actor.ID,
		attrActorLogin:     ev.Actor.LoginName,
		attrActorDisplay:   ev.Actor.DisplayName,
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
		Name:      auditEventName,
		Body:      summary(ev),
		Severity:  severity,
		Timestamp: ev.EventTime,
		Attrs:     attrs,
	})

	e.Counter(MetricAuditEvents, semconv.UnitEvents, "Tailscale configuration audit events", 1, telemetry.Attrs{
		attrAction: ev.Action,
		attrOrigin: ev.Origin,
	})
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

// summary builds the human-readable log body, e.g.
// "a@example.com CREATE NODE:node.ts.net".
func summary(ev Event) string {
	return fmt.Sprintf("%s %s %s:%s", ev.Actor.LoginName, ev.Action, ev.Target.Type, ev.Target.Name)
}
