package acl

import (
	"context"
	"strings"

	"github.com/rknightion/tailscale2otel/v4/internal/apistate"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v4/internal/tsapi"
)

// opValidate is the upstream OpenAPI operationId for the validate endpoint,
// used as the tailscale.api.operation attribute value on the availability
// signal (apistate).
const opValidate = "validateAndTestPolicyFile"

// Validator is the narrow validate-policy dependency the acl collector
// accepts as an optional functional option (WithValidator). It is satisfied
// by *tsapi.Client. It is intentionally NOT part of provider.ControlPlane —
// that interface is shared with the Headscale adapter, which has no validate
// endpoint — so a collector built without WithValidator is a clean no-op for
// every tailscale.acl.validation.* signal.
type Validator interface {
	ValidatePolicyFile(ctx context.Context, policy string) (*tsapi.PolicyValidation, error)
}

// WithValidator wires the optional ACL policy-validation dependency. Omit
// this option (or pass nil) to disable policy-validation signals entirely —
// the correct choice for a provider with no validate endpoint (Headscale).
// WithAPIState wires the shared availability tracker so the validate operation's
// state reaches the admin status page and the capability matrix (#430). A nil
// tracker is a no-op.
func WithAPIState(t *apistate.Tracker) Option {
	return func(c *Collector) { c.tracker = t }
}

func WithValidator(v Validator) Option {
	return func(c *Collector) { c.validator = v }
}

// WithValidate enables (true, the default already set in New) or disables
// (false) policy-validation signals when a validator IS wired via
// WithValidator. Mirrors config.AclCollector.Validate, the operator-facing off
// switch. Has no effect when no validator is wired.
func WithValidate(enabled bool) Option {
	return func(c *Collector) { c.validate = enabled }
}

// collectValidation validates the policy document the collector just fetched,
// when
// a validator is wired and enabled, and emits the resulting signals. It is a
// clean no-op — no validation.* gauge, no availability entry — when no
// validator is wired or validation is disabled via WithValidate(false).
//
// A validate-call failure is classified via apistate and emitted as the
// bounded availability state (so a genuine permission regression reads as
// scope_denied/credential_rejected, never a passing validation or a healthy
// zero) but does NOT fail the enclosing Collect: validation is a
// supplementary probe alongside the primary policy fetch, matching the
// framework's subrequest pattern elsewhere (devices, webhooks).
func (c *Collector) collectValidation(ctx context.Context, e telemetry.Emitter, policy string) {
	if c.validator == nil || !c.validate {
		return
	}
	// No policy document means nothing to validate. Emitting an availability
	// entry here would report a probe that never happened; staying silent keeps
	// the row honest at unknown.
	if strings.TrimSpace(policy) == "" {
		return
	}

	v, err := c.validator.ValidatePolicyFile(ctx, policy)
	state := apistate.Observe(e, c.tracker, c.Name(), opValidate, apistate.Disposition{}, err, c.now())
	if state != apistate.StateSupported || v == nil {
		return
	}

	c.emitValidation(e, v)
}

// emitValidation writes the bounded validation.{ok,errors,warnings,test_failures}
// gauges plus, for each non-zero issue kind, one WARN validation_issue log
// event carrying only the bounded attrValidationKind attribute. The
// validator's free-text messages never reach this call — tsapi.PolicyValidation
// carries only counts.
func (c *Collector) emitValidation(e telemetry.Emitter, v *tsapi.PolicyValidation) {
	e.Gauge(docACLValidationOK.Name, docACLValidationOK.Unit, docACLValidationOK.Description,
		boolValue(v.OK), nil)
	e.Gauge(docACLValidationErrors.Name, docACLValidationErrors.Unit, docACLValidationErrors.Description,
		float64(v.Errors), nil)
	e.Gauge(docACLValidationWarnings.Name, docACLValidationWarnings.Unit, docACLValidationWarnings.Description,
		float64(v.Warnings), nil)
	e.Gauge(docACLValidationTestFailures.Name, docACLValidationTestFailures.Unit, docACLValidationTestFailures.Description,
		float64(v.TestFailures), nil)

	if v.Errors > 0 {
		c.emitValidationIssue(e, validationKindError)
	}
	if v.Warnings > 0 {
		c.emitValidationIssue(e, validationKindWarning)
	}
	if v.TestFailures > 0 {
		c.emitValidationIssue(e, validationKindTestFailure)
	}
}

// emitValidationIssue emits one WARN validation_issue log event for kind. The
// body is a static, content-free string — never the upstream free-text
// message — so it is safe to forward unconditionally.
func (c *Collector) emitValidationIssue(e telemetry.Emitter, kind string) {
	e.LogEvent(telemetry.Event{
		Name:     EventValidationIssue,
		Severity: telemetry.SeverityWarn,
		Body:     "ACL policy validation reported an issue",
		Attrs:    telemetry.Attrs{attrValidationKind: kind},
	})
}

// boolValue maps a bool to a 0/1 gauge value, mirroring the settings
// collector's helper of the same shape (kept package-local to avoid a shared
// dependency for one line).
func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
