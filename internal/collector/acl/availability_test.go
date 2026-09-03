package acl_test

import (
	"context"
	"testing"
	"time"

	tsclient "github.com/tailscale/tailscale-client-go/v2"

	"github.com/rknightion/tailscale2otel/v5/internal/collector/acl"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// TestPrimaryFetchAvailability_Success covers the healthy case: a successful
// PolicyFileRaw call must emit getPolicyFile=supported so the row never reads
// unknown while the fetch is working (#524).
func TestPrimaryFetchAvailability_Success(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[]}`, ETag: "etag-1"}}
	rec := telemetrytest.New()
	c := acl.New(api, 0, time.Now)

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	state, ok := availabilityState(t, rec, "getPolicyFile")
	if !ok || state != "supported" {
		t.Fatalf("availability state = %q (present=%v), want supported", state, ok)
	}
}

// TestPrimaryFetchAvailability_ErrorClassification covers #524's core claim:
// the primary PolicyFileRaw fetch must classify through apistate with a bare
// Disposition{} — no DisabledOn — so a 403 always reads as scope_denied, never
// a hidden "disabled" that would mask a real credential problem.
func TestPrimaryFetchAvailability_ErrorClassification(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState string
	}{
		{"401 credential rejected", &tsapi.StatusError{Code: 401}, "credential_rejected"},
		{"403 scope denied, not disabled", &tsapi.StatusError{Code: 403}, "scope_denied"},
		{"400 request rejected", &tsapi.StatusError{Code: 400}, "request_rejected"},
		{"transport error transient", context.DeadlineExceeded, "transient_failure"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{err: tc.err}
			rec := telemetrytest.New()
			c := acl.New(api, 0, time.Now)

			if err := c.Collect(context.Background(), rec.Emitter()); err == nil {
				t.Fatal("Collect: expected error propagated, got nil")
			}

			state, ok := availabilityState(t, rec, "getPolicyFile")
			if !ok || state != tc.wantState {
				t.Fatalf("availability state = %q (present=%v), want %v", state, ok, tc.wantState)
			}
		})
	}
}

// TestPrimaryFetchAvailability_IndependentFromValidate proves the two
// operations recorded under the "acl" collector are independent entries: a
// tick where both the primary fetch and the validate subrequest run must emit
// BOTH getPolicyFile and validateAndTestPolicyFile, each pinned at exactly one
// state.
func TestPrimaryFetchAvailability_IndependentFromValidate(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[]}`, ETag: "etag-1"}}
	v := &fakeValidator{result: &tsapi.PolicyValidation{OK: true}}
	rec := telemetrytest.New()
	c := acl.New(api, 0, time.Now, acl.WithValidator(v))

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	fetchState, ok := availabilityState(t, rec, "getPolicyFile")
	if !ok || fetchState != "supported" {
		t.Fatalf("getPolicyFile state = %q (present=%v), want supported", fetchState, ok)
	}
	validateState, ok := availabilityState(t, rec, "validateAndTestPolicyFile")
	if !ok || validateState != "supported" {
		t.Fatalf("validateAndTestPolicyFile state = %q (present=%v), want supported", validateState, ok)
	}

	// Exactly one state (value 1) per operation - no double-counting.
	onesByOp := map[string]int{}
	for _, p := range rec.MetricPoints("tailscale2otel.api.availability") {
		if p.Value == 1 {
			onesByOp[p.Attrs["tailscale.api.operation"]]++
		}
	}
	for op, n := range onesByOp {
		if n != 1 {
			t.Fatalf("operation %q has %d states at value 1, want 1", op, n)
		}
	}
}

// TestPrimaryFetchAvailability_NoValidatorWired is the #524 regression this
// whole lane exists for: with no validator wired at all (the Headscale-safe
// default), the acl row must still NOT read unknown - getPolicyFile must be
// emitted from the primary fetch alone.
func TestPrimaryFetchAvailability_NoValidatorWired(t *testing.T) {
	api := &fakeAPI{acl: &tsclient.RawACL{HuJSON: `{"acls":[]}`, ETag: "etag-1"}}
	rec := telemetrytest.New()
	c := acl.New(api, 0, time.Now)

	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	state, ok := availabilityState(t, rec, "getPolicyFile")
	if !ok || state != "supported" {
		t.Fatalf("availability state = %q (present=%v), want supported", state, ok)
	}
	if _, ok := availabilityState(t, rec, "validateAndTestPolicyFile"); ok {
		t.Error("validateAndTestPolicyFile emitted with no validator wired")
	}
}
