package audit

import (
	"fmt"

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
// and an events counter. It holds no state and is safe for concurrent use;
// the same instance is shared by the polling collector and streaming receiver.
type Processor struct{}

// NewProcessor returns an audit Processor.
func NewProcessor() *Processor { return &Processor{} }

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
	if ev.Old != "" {
		attrs[attrOld] = ev.Old
	}
	if ev.New != "" {
		attrs[attrNew] = ev.New
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

// summary builds the human-readable log body, e.g.
// "a@example.com CREATE NODE:node.ts.net".
func summary(ev Event) string {
	return fmt.Sprintf("%s %s %s:%s", ev.Actor.LoginName, ev.Action, ev.Target.Type, ev.Target.Name)
}
