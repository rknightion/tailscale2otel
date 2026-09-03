package acl_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// fakeValidator implements acl.Validator for tests.
type fakeValidator struct {
	result *tsapi.PolicyValidation
	err    error
	calls  int
	// gotPolicy records the document the collector passed through, so a test can
	// prove the live policy reaches the validator rather than an empty body
	// (#523).
	gotPolicy string
}

func (f *fakeValidator) ValidatePolicyFile(_ context.Context, policy string) (*tsapi.PolicyValidation, error) {
	f.calls++
	f.gotPolicy = policy
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// newACLAPI is a minimal PolicyFileRaw fake so validation tests don't need to
// care about the rest of Collect's behavior.
func newACLAPI() *fakeAPI {
	return &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[]}`, ETag: "etag-1"}}
}

func validationGauge(t *testing.T, rec *telemetrytest.Recorder, name string) (float64, bool) {
	t.Helper()
	pts := rec.MetricPoints(name)
	if len(pts) == 0 {
		return 0, false
	}
	if len(pts) != 1 {
		t.Fatalf("%s points = %d, want at most 1", name, len(pts))
	}
	if pts[0].Kind != "gauge" {
		t.Fatalf("%s kind = %q, want gauge", name, pts[0].Kind)
	}
	return pts[0].Value, true
}

func availabilityState(t *testing.T, rec *telemetrytest.Recorder, operation string) (string, bool) {
	t.Helper()
	for _, p := range rec.MetricPoints("tailscale2otel.api.availability") {
		if p.Attrs["tailscale.api.operation"] != operation {
			continue
		}
		if p.Value == 1 {
			return p.Attrs["tailscale.api.state"], true
		}
	}
	return "", false
}

// TestValidation_NoValidatorWiredIsANoOp covers the Headscale-adapter case:
// when no validator dependency is wired at all, Collect must emit NONE of the
// validation.* signals and NONE of the availability signal for this
// operation — a nil validator is a clean no-op, not a degraded state.
func TestValidation_NoValidatorWiredIsANoOp(t *testing.T) {
	rec := telemetrytest.New()
	c := acl.New(newACLAPI(), 0, time.Now)

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, name := range []string{
		"tailscale.acl.validation.ok",
		"tailscale.acl.validation.errors",
		"tailscale.acl.validation.warnings",
		"tailscale.acl.validation.test_failures",
	} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("%s emitted with no validator wired: %+v", name, pts)
		}
	}
	if _, ok := availabilityState(t, rec, "validateAndTestPolicyFile"); ok {
		t.Error("availability signal emitted for validateAndTestPolicyFile with no validator wired")
	}
}

// TestValidation_DefaultOnWhenValidatorWired covers the frozen decision: ACL
// validation is ON BY DEFAULT once a validator is wired via WithValidator,
// even without an explicit WithValidate call.
func TestValidation_DefaultOnWhenValidatorWired(t *testing.T) {
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: true}}
	rec := telemetrytest.New()
	c := acl.New(newACLAPI(), 0, time.Now, acl.WithValidator(v))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v.calls != 1 {
		t.Fatalf("validator calls = %d, want 1 (validation defaults to on)", v.calls)
	}
	if got, ok := validationGauge(t, rec, "tailscale.acl.validation.ok"); !ok || got != 1 {
		t.Fatalf("validation.ok = %v (present=%v), want 1", got, ok)
	}
}

// TestValidation_DisabledViaConfigFlag covers the off switch: even with a
// validator wired, WithValidate(false) must suppress both the validator call
// and every validation.*/availability signal.
func TestValidation_DisabledViaConfigFlag(t *testing.T) {
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: true}}
	rec := telemetrytest.New()
	c := acl.New(newACLAPI(), 0, time.Now, acl.WithValidator(v), acl.WithValidate(false))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v.calls != 0 {
		t.Fatalf("validator calls = %d, want 0 (validate disabled)", v.calls)
	}
	if pts := rec.MetricPoints("tailscale.acl.validation.ok"); len(pts) != 0 {
		t.Errorf("validation.ok emitted while disabled: %+v", pts)
	}
}

// TestValidation_SuccessEmitsAllZeroCounts covers the bare {} success case.
func TestValidation_SuccessEmitsAllZeroCounts(t *testing.T) {
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: true}}
	rec := telemetrytest.New()
	c := acl.New(newACLAPI(), 0, time.Now, acl.WithValidator(v), acl.WithValidate(true))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for name, want := range map[string]float64{
		"tailscale.acl.validation.ok":            1,
		"tailscale.acl.validation.errors":        0,
		"tailscale.acl.validation.warnings":      0,
		"tailscale.acl.validation.test_failures": 0,
	} {
		got, ok := validationGauge(t, rec, name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	// A clean validation must not emit a validation_issue log.
	for _, lr := range rec.LogRecords() {
		if lr.EventName == acl.EventValidationIssue {
			t.Fatalf("unexpected validation_issue log on a clean validation: %+v", lr)
		}
	}

	if state, ok := availabilityState(t, rec, "validateAndTestPolicyFile"); !ok || state != "supported" {
		t.Errorf("availability state = %q (present=%v), want supported", state, ok)
	}
}

// TestValidation_TestFailuresEmitCountAndLog covers the "test(s) failed" shape
// end to end through the collector.
func TestValidation_TestFailuresEmitCountAndLog(t *testing.T) {
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: false, TestFailures: 2}}
	rec := telemetrytest.New()
	c := acl.New(newACLAPI(), 0, time.Now, acl.WithValidator(v))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got, _ := validationGauge(t, rec, "tailscale.acl.validation.ok"); got != 0 {
		t.Errorf("validation.ok = %v, want 0", got)
	}
	if got, _ := validationGauge(t, rec, "tailscale.acl.validation.test_failures"); got != 2 {
		t.Errorf("validation.test_failures = %v, want 2", got)
	}
	if got, _ := validationGauge(t, rec, "tailscale.acl.validation.errors"); got != 0 {
		t.Errorf("validation.errors = %v, want 0", got)
	}

	var issueLogs []telemetrytest.LogRecord
	for _, lr := range rec.LogRecords() {
		if lr.EventName == acl.EventValidationIssue {
			issueLogs = append(issueLogs, lr)
		}
	}
	if len(issueLogs) != 1 {
		t.Fatalf("validation_issue logs = %d, want 1", len(issueLogs))
	}
	if got := issueLogs[0].Attrs["tailscale.acl.validation.kind"]; got != "test_failure" {
		t.Errorf("validation_issue kind = %q, want test_failure", got)
	}
	if issueLogs[0].SeverityText != "WARN" {
		t.Errorf("validation_issue severity = %q, want WARN", issueLogs[0].SeverityText)
	}
}

// TestValidation_WarningsEmitCountAndLog covers the "warning(s) found" shape.
func TestValidation_WarningsEmitCountAndLog(t *testing.T) {
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: false, Warnings: 1}}
	rec := telemetrytest.New()
	c := acl.New(newACLAPI(), 0, time.Now, acl.WithValidator(v))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got, _ := validationGauge(t, rec, "tailscale.acl.validation.warnings"); got != 1 {
		t.Errorf("validation.warnings = %v, want 1", got)
	}

	var kinds []string
	for _, lr := range rec.LogRecords() {
		if lr.EventName == acl.EventValidationIssue {
			kinds = append(kinds, lr.Attrs["tailscale.acl.validation.kind"])
		}
	}
	if len(kinds) != 1 || kinds[0] != "warning" {
		t.Fatalf("validation_issue kinds = %v, want [warning]", kinds)
	}
}

// TestValidation_ScopeDeniedNeverEmitsHealthyZero is the crux of #428:
// permission denial on the validate call must surface as scope_denied via the
// availability signal, and NEVER as a passing validation or an all-zero
// "healthy" reading. Collect itself must not hard-fail on this (it is a
// supplementary probe, not the primary policy fetch).
func TestValidation_ScopeDeniedNeverEmitsHealthyZero(t *testing.T) {
	v := &fakeValidator{err: &tsapi.StatusError{Method: "POST", URL: "https://example.invalid/acl/validate", Code: http.StatusForbidden, Body: "nope"}}
	rec := telemetrytest.New()
	c := acl.New(newACLAPI(), 0, time.Now, acl.WithValidator(v))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect must not hard-fail on a validate-only permission error: %v", err)
	}

	for _, name := range []string{
		"tailscale.acl.validation.ok",
		"tailscale.acl.validation.errors",
		"tailscale.acl.validation.warnings",
		"tailscale.acl.validation.test_failures",
	} {
		if pts := rec.MetricPoints(name); len(pts) != 0 {
			t.Errorf("%s emitted on scope-denied validate call (must be absence, not a healthy zero): %+v", name, pts)
		}
	}

	state, ok := availabilityState(t, rec, "validateAndTestPolicyFile")
	if !ok || state != "scope_denied" {
		t.Fatalf("availability state = %q (present=%v), want scope_denied", state, ok)
	}
}

// SnapshotCollector compile-time check already exists in acl_test.go; this
// file only adds validation-specific coverage.

// TestCollectValidationPassesTheFetchedPolicy proves the collector hands the
// document it just fetched to the validator. Before #523 it passed nothing, the
// API 400ed every tick, and the failure read as transient flakiness.
func TestCollectValidationPassesTheFetchedPolicy(t *testing.T) {
	api := newACLAPI()
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: true}}
	rec := telemetrytest.New()
	c := acl.New(api, 0, time.Now, acl.WithValidator(v))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v.calls != 1 {
		t.Fatalf("validator calls = %d, want 1", v.calls)
	}
	if v.gotPolicy != api.acl.HuJSON {
		t.Errorf("validator got policy %q, want the fetched document %q", v.gotPolicy, api.acl.HuJSON)
	}
}

// TestCollectValidationSkippedWhenPolicyEmpty proves an empty document produces
// no probe at all. Validating nothing passes unconditionally, so a green
// availability row here would be a lie.
func TestCollectValidationSkippedWhenPolicyEmpty(t *testing.T) {
	api := newACLAPI()
	api.acl.HuJSON = ""
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: true}}
	rec := telemetrytest.New()
	c := acl.New(api, 0, time.Now, acl.WithValidator(v))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v.calls != 0 {
		t.Errorf("validator calls = %d, want 0 for an empty policy", v.calls)
	}
	if st, ok := availabilityState(t, rec, "validateAndTestPolicyFile"); ok {
		t.Errorf("availability state = %q, want no entry at all", st)
	}
}
