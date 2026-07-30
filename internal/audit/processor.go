package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/dedup"
	"github.com/rknightion/tailscale2otel/v4/internal/eventstore"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
)

// MetricAuditEvents counts configuration audit events. Low cardinality only:
// it carries the action and origin, never per-actor or per-target attributes.
const MetricAuditEvents = "tailscale.config.audit.events"

// MetricAuditChanges counts a curated, bounded subset of security- and
// lifecycle-relevant audit changes. Unlike MetricAuditEvents (action+origin),
// it carries a derived change category, the action, and the actor TYPE (a
// bounded enum, never actor identity). See classify.go for the curated set.
const MetricAuditChanges = "tailscale.config.audit.changes"

// MetricAuditDeferredDelay records how long Tailscale deferred audit records
// before logging them. It has no attributes, preserving a single fleet-level
// bounded histogram.
const MetricAuditDeferredDelay = "tailscale.config.audit.deferred.delay"

// MetricAuditProcessingDelay records the time from the best upstream audit
// timestamp (DeferredAt when present, otherwise EventTime) to acceptance by
// this processor. It has no attributes, preserving a single fleet-level
// bounded histogram.
const MetricAuditProcessingDelay = "tailscale.config.audit.processing.delay"

// MetricAuditSchemaDrift counts observations of values in the bounded audit
// schema vocabulary. It identifies only the field and whether the observed
// value is known to this version of the collector; raw values never become
// metric attributes.
const MetricAuditSchemaDrift = "tailscale.config.audit.schema_drift"

// auditEventName is the OTEL LogRecord EventName for configuration audit logs.
const auditEventName = "tailscale.config.audit"

// auditDelayBucketsSeconds cover immediate processing through the rate-limited
// delivery delays that configuration audit records can incur.
var auditDelayBucketsSeconds = []float64{1, 5, 30, 60, 300, 900, 3600, 21600, 86400}

// Audit log attribute keys (namespaced under "tailscale.").
const (
	attrAction          = "tailscale.audit.action"
	attrOrigin          = "tailscale.audit.origin"
	attrEventGroupID    = "tailscale.audit.event_group_id"
	attrOld             = "tailscale.audit.old"
	attrNew             = "tailscale.audit.new"
	attrDetails         = "tailscale.audit.details"
	attrChange          = "tailscale.audit.change"
	attrType            = "tailscale.audit.type"
	attrActorTags       = "tailscale.actor.tags"
	attrTargetEphemeral = "tailscale.target.ephemeral"
	attrDeferredAt      = "tailscale.audit.deferred_at"

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
	now        func() time.Time
	logger     *slog.Logger
	store      *eventstore.Memory

	mu                  sync.Mutex
	unknownSchemaValues map[string]struct{}
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

// WithClock overrides the acceptance-time source used for audit-delay
// histograms. It is intended for deterministic tests; nil retains time.Now.
func WithClock(now func() time.Time) Option {
	return func(p *Processor) {
		if now != nil {
			p.now = now
		}
	}
}

// WithLogger attaches the logger used for bounded audit-schema drift warnings.
// A nil logger suppresses those warnings while retaining the schema-drift
// metric. Main wires the application logger so operators can identify a schema
// addition without exposing its raw value.
func WithLogger(logger *slog.Logger) Option {
	return func(p *Processor) { p.logger = logger }
}

// WithStore feeds a bounded, admin-facing event view (internal/eventstore,
// #300) after the log record and counters have already been emitted. Passing
// a nil store is a no-op, preserving default behavior. Feeding the store never
// blocks or fails the emit path: eventstore.Memory.Record takes a short lock
// and a single append, with no I/O and no error to report.
func WithStore(store *eventstore.Memory) Option {
	return func(p *Processor) { p.store = store }
}

// NewProcessor returns an audit Processor. With no options it behaves exactly
// as before: every event produces one log record and one counter increment.
func NewProcessor(opts ...Option) *Processor {
	p := &Processor{
		now:                 time.Now,
		unknownSchemaValues: make(map[string]struct{}),
	}
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
// counter increment per event. Equivalent to ProcessAllCtx with
// context.Background() — the logs carry no trace context.
func (p *Processor) ProcessAll(resp ConfigurationResponse, e telemetry.Emitter) {
	p.ProcessAllCtx(context.Background(), resp, e)
}

// ProcessAllCtx is ProcessAll, but ctx is threaded to every event's log
// record so a sampled span context (the poll window's or the streaming
// request's) produces a native TraceID/SpanID on each log (#367).
func (p *Processor) ProcessAllCtx(ctx context.Context, resp ConfigurationResponse, e telemetry.Emitter) {
	for _, ev := range resp.Logs {
		p.ProcessCtx(ctx, ev, e)
	}
}

// Process converts a single Event into an OTEL log record plus a counter
// increment. The log carries the full actor/target context; the counter carries
// only low-cardinality action and origin attributes. Equivalent to ProcessCtx
// with context.Background().
func (p *Processor) Process(ev Event, e telemetry.Emitter) {
	p.ProcessCtx(context.Background(), ev, e)
}

// ProcessCtx is Process, but ctx is used to emit the log record so a sampled
// span context produces a native TraceID/SpanID on it (#367). The dedup/
// severity/attribute logic is unchanged from Process.
func (p *Processor) ProcessCtx(ctx context.Context, ev Event, e telemetry.Emitter) {
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
	acceptedAt := p.now()

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
	if ev.Type != "" {
		attrs[attrType] = ev.Type
	}
	if tags := canonicalTags(ev.Actor.Tags); tags != "" {
		attrs[attrActorTags] = tags
	}
	if ev.Target.IsEphemeral != nil {
		attrs[attrTargetEphemeral] = strconv.FormatBool(*ev.Target.IsEphemeral)
	}
	if !ev.DeferredAt.IsZero() {
		attrs[attrDeferredAt] = ev.DeferredAt.UTC().Format(time.RFC3339Nano)
	}

	e.LogEventCtx(ctx, telemetry.Event{
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

	p.observeSchemaDrift(ev, e)

	if cat, ok := classifyChange(ev); ok {
		e.Counter(docAuditChanges.Name, docAuditChanges.Unit, docAuditChanges.Description, 1, telemetry.Attrs{
			attrChange:    cat,
			attrAction:    normalizeAction(ev.Action),
			attrActorType: normalizeActorType(ev.Actor.Type),
		})
	}

	emitDelayHistograms(ev, acceptedAt, e)

	// Feed the bounded event explorer view AFTER every OTLP emission above, so
	// a full ring (or any bug in this path) can never affect what was already
	// exported (#300). A nil store makes this a no-op.
	if p.store != nil {
		p.store.Record(storeEvent(ev, severity))
	}
}

// storeEvent converts an audit Event into the eventstore's leaf Event shape.
//
// Details carries the raw old/new diff, UNFILTERED by pii_filter — the same
// deliberate choice /flows makes for flow endpoint identity (#241): this view
// is local, bounded, and admin-authenticated, and pii_filter governs what
// this process EXPORTS over OTLP, not what an already-authenticated operator
// can see about their own tailnet locally. eventstore.Memory.Record still
// truncates Details to eventstore.MaxDetailBytes (#300's "policy-diff
// truncation" requirement) — a policyUpdate old/new pair can carry an entire
// ACL document, which would make this one field unbounded even though the
// ring's event COUNT is bounded.
func storeEvent(ev Event, severity telemetry.Severity) eventstore.Event {
	sev := eventstore.SeverityInfo
	if severity == telemetry.SeverityWarn || severity == telemetry.SeverityError {
		sev = eventstore.SeverityWarn
	}
	return eventstore.Event{
		Time:           ev.EventTime,
		Source:         eventstore.SourceAudit,
		Action:         ev.Action,
		Type:           ev.Type,
		Origin:         ev.Origin,
		ActorID:        ev.Actor.ID,
		ActorName:      ev.Actor.LoginName,
		ActorType:      ev.Actor.Type,
		TargetID:       ev.Target.ID,
		TargetName:     ev.Target.Name,
		TargetType:     ev.Target.Type,
		TargetProperty: ev.Target.Property,
		Severity:       sev,
		Error:          ev.Error,
		Summary:        summary(ev),
		Details:        auditDiff(ev),
	}
}

// auditDiff renders the old/new pair for the event explorer's Details column.
// Unlike the log attributes (attrOld/attrNew), which omit an unset side
// entirely, this labels whichever sides ARE present so the two are never
// ambiguous once concatenated.
func auditDiff(ev Event) string {
	oldStr, oldOK := renderRaw(ev.Old)
	newStr, newOK := renderRaw(ev.New)
	switch {
	case oldOK && newOK:
		return "old: " + oldStr + "\nnew: " + newStr
	case newOK:
		return "new: " + newStr
	case oldOK:
		return "old: " + oldStr
	default:
		return ""
	}
}

const schemaDriftWarningLimit = 128

func (p *Processor) observeSchemaDrift(ev Event, e telemetry.Emitter) {
	p.observeSchemaValue("action", ev.Action, knownActions, e)
	p.observeSchemaValue("origin", ev.Origin, knownOrigins, e)
	p.observeSchemaValue("actor_type", ev.Actor.Type, knownActorTypes, e)
	if ev.Target.Property != "" {
		p.observeSchemaValue("target_property", ev.Target.Property, knownProperty, e)
	}
}

func (p *Processor) observeSchemaValue(field, value string, known map[string]bool, e telemetry.Emitter) {
	status := "known"
	if !known[value] {
		status = "unknown"
		p.warnUnknownSchemaValue(field, value)
	}
	e.Counter(docAuditSchemaDrift.Name, docAuditSchemaDrift.Unit, docAuditSchemaDrift.Description, 1, telemetry.Attrs{
		"field":  field,
		"status": status,
	})
}

func (p *Processor) warnUnknownSchemaValue(field, value string) {
	if p.logger == nil {
		return
	}
	key := field + "\x00" + value
	p.mu.Lock()
	if len(p.unknownSchemaValues) >= schemaDriftWarningLimit {
		p.mu.Unlock()
		return
	}
	if _, seen := p.unknownSchemaValues[key]; seen {
		p.mu.Unlock()
		return
	}
	p.unknownSchemaValues[key] = struct{}{}
	p.mu.Unlock()

	p.logger.Warn("unrecognized audit schema enum value", "field", field, "digest", schemaValueDigest(value))
}

func schemaValueDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:6])
}

// canonicalTags returns a stable, comma-joined representation of an audit
// actor's tags. It never mutates the event's input slice: tags are wire data
// that can also be observed by callers after Process returns.
func canonicalTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(tags))
	canonical := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		canonical = append(canonical, tag)
	}
	slices.Sort(canonical)
	return strings.Join(canonical, ",")
}

func emitDelayHistograms(ev Event, acceptedAt time.Time, e telemetry.Emitter) {
	if !ev.DeferredAt.IsZero() && !ev.EventTime.IsZero() {
		if delay := ev.DeferredAt.Sub(ev.EventTime); delay >= 0 {
			e.Histogram(docAuditDeferredDelay.Name, docAuditDeferredDelay.Unit,
				docAuditDeferredDelay.Description, delay.Seconds(), auditDelayBucketsSeconds, nil)
		}
	}

	sourceTime := ev.EventTime
	// A deferred timestamp before the underlying event cannot describe a
	// deferral. Fall back to EventTime for processing delay while deliberately
	// leaving drift accounting to #270's bounded diagnostic signal.
	if !ev.DeferredAt.IsZero() && (ev.EventTime.IsZero() || !ev.DeferredAt.Before(ev.EventTime)) {
		sourceTime = ev.DeferredAt
	}
	if !sourceTime.IsZero() {
		if delay := acceptedAt.Sub(sourceTime); delay >= 0 {
			e.Histogram(docAuditProcessingDelay.Name, docAuditProcessingDelay.Unit,
				docAuditProcessingDelay.Description, delay.Seconds(), auditDelayBucketsSeconds, nil)
		}
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
