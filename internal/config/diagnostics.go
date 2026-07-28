package config

import "strings"

// Severity classifies a Diagnostic: SeverityError mirrors what Validate()
// would reject outright (the config cannot start); SeverityWarning mirrors
// Warnings() — advisory, never fatal on its own unless the caller opts into
// -warnings-as-errors (see cmd/tailscale2otel).
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Diagnostic is one configuration finding, structured for both a human
// reading -validate output and a tool consuming -json. Path names the dotted
// config key the finding concerns (empty only for the handful of rules that
// genuinely span no single key — a userinfo-credential check that names its
// own field in Message, or a bad *_file/value pairing whose field name is only
// known at check time); Message explains what is wrong; Remediation says what
// to actually change. Remediation is the field that turns a fix-run-fix loop
// into a single pass over a printed list (#307), so it is populated on every
// check where there is a concrete action to take.
//
// NEVER put a secret VALUE in any of these fields — a diagnostic about a
// malformed credential names the KEY, never the value (see
// TestDiagnostics_NoSecretValues). This mirrors the existing convention in
// Warnings()/validateNoURLCredentials, which already redact via
// internal/redact rather than echo raw values.
type Diagnostic struct {
	Severity    Severity `json:"severity"`
	Path        string   `json:"path,omitempty"`
	Message     string   `json:"message"`
	Remediation string   `json:"remediation,omitempty"`
}

// configCheck is one independently-evaluable rule inside Validate(). Every
// rule reads Config fields but never mutates them, so whether a later rule's
// own precondition holds (e.g. "provider == headscale") is exactly as true or
// false regardless of whether an earlier, unrelated rule already failed —
// that is what lets runChecks evaluate every rule and collect every
// independent failure without a separate dependency graph. Where a rule is
// genuinely DERIVED from another (a config too broken to evaluate a later
// rule meaningfully), the later rule's own closure re-tests the same gating
// condition the original sequential code used, which is naturally false when
// the upstream value never resolved to the state the derived rule needs —
// see the "skip" comments in validationChecks for the two cases where this is
// spelled out explicitly rather than merely inherited from an if/else.
type configCheck struct {
	// path is the dotted config key this rule concerns. A rule that covers a
	// small, dynamically-sized loop (e.g. one entry per configured tailnet)
	// uses the shared prefix ("tailnets") rather than a per-index path, since
	// the check reports only the first violation within that loop — see the
	// loop-collapsing note in validationChecks.
	path string
	// remediation is what to actually do about a failure of this check. Left
	// empty only where the message already states the fix in full sentence
	// form (this repo's existing convention for many of these messages).
	remediation string
	fn          func() error
}

// runChecks evaluates every check in order. In fail-fast mode it stops at
// and returns the first error, reproducing Validate()'s historical contract
// exactly: same rule order, same first error text. In collect mode it keeps
// going and returns every failure as a Diagnostic.
func runChecks(checks []configCheck, failFast bool) ([]Diagnostic, error) {
	var diags []Diagnostic
	for _, chk := range checks {
		err := chk.fn()
		if err == nil {
			continue
		}
		if failFast {
			return nil, err
		}
		diags = append(diags, Diagnostic{
			Severity:    SeverityError,
			Path:        chk.path,
			Message:     err.Error(),
			Remediation: chk.remediation,
		})
	}
	return diags, nil
}

// Diagnostics reports every configuration problem in one pass instead of
// Validate()'s first-error contract (#307): every independently-evaluable
// Validate() rule that fails (severity=error), plus every Warnings() advisory
// (severity=warning). Order is Validate()'s check order, then Warnings()'s
// order. Validate() itself is unchanged — same signature, same "first error"
// semantics — because config.Load and other existing callers depend on it;
// Diagnostics is an addition, not a replacement.
func (c *Config) Diagnostics() []Diagnostic {
	diags, _ := runChecks(c.validationChecks(), false)
	for _, w := range c.Warnings() {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Path:     advisoryKey(w),
			Message:  w,
		})
	}
	return diags
}

// HasErrors reports whether diags contains any severity=error entry.
func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// HasWarnings reports whether diags contains any severity=warning entry.
func HasWarnings(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityWarning {
			return true
		}
	}
	return false
}

// oneOfRemediation renders a stock remediation string for an enum-style
// check ("field: must be one of a, b, c"), so the common case doesn't need
// its own hand-written remediation text.
func oneOfRemediation(field string, allowed ...string) string {
	return "Set " + field + " to one of: " + strings.Join(allowed, ", ") + "."
}
