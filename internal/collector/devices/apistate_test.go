package devices_test

import (
	"context"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/apistate"
	"github.com/rknightion/tailscale2otel/v5/internal/collector/devices"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v5/internal/tsapi"
)

// availabilityStates returns, per operation, the single state whose gauge is 1.
// Copied from postureintegrations_test.go (the shared idiom for this
// assertion) rather than shared as a helper package, to keep each collector's
// test suite self-contained.
func availabilityStates(t *testing.T, rec *telemetrytest.Recorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range rec.MetricPoints(apistate.MetricAvailability) {
		op := p.Attrs["tailscale.api.operation"]
		st := p.Attrs["tailscale.api.state"]
		switch p.Value {
		case 1:
			if prev, dup := out[op]; dup {
				t.Fatalf("operation %q has two states at 1: %q and %q", op, prev, st)
			}
			out[op] = st
		case 0:
		default:
			t.Fatalf("availability gauge for %q/%q = %v, want 0 or 1", op, st, p.Value)
		}
	}
	return out
}

// TestCollect_APIState_ListTailnetDevices is the #524 regression for the main
// listing operation: every DevicesRich outcome must be classified through
// apistate under the "listTailnetDevices" operation name (the upstream
// operationId), using the DEFAULT Disposition{} — a 403 must read
// scope_denied, never disabled. Collect's own error propagation (return err
// unconditionally on a listing failure) must be unchanged.
func TestCollect_APIState_ListTailnetDevices(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState apistate.State
		wantErr   bool
	}{
		{"success", nil, apistate.StateSupported, false},
		{"401 credential rejected", &tsapi.StatusError{Code: 401}, apistate.StateCredentialRejected, true},
		{"403 scope denied", &tsapi.StatusError{Code: 403}, apistate.StateScopeDenied, true},
		{"400 request rejected", &tsapi.StatusError{Code: 400}, apistate.StateRequestRejected, true},
		{"transient failure", context.DeadlineExceeded, apistate.StateTransientFailure, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{devices: sampleDevices(), err: tc.err}
			c, rec := epicCollector(t, api, false, false)
			err := c.Collect(context.Background(), rec.Emitter())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Collect() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got := availabilityStates(t, rec)["listTailnetDevices"]; got != string(tc.wantState) {
				t.Errorf("listTailnetDevices state = %q, want %q", got, tc.wantState)
			}
		})
	}
}

// TestCollect_APIState_HeadlineRegression is the #524 headline: on the real
// deployment, devices ran every 60s at 100% success and every one of its three
// capability-matrix rows still showed "unknown" because Observe was never
// called. A fully successful tick over N>0 devices with both subrequests
// enabled must emit "supported" for all three operations.
func TestCollect_APIState_HeadlineRegression(t *testing.T) {
	devs := sampleDevices()
	api := &fakeAPI{
		devices: devs,
		posture: map[string]map[string]any{},
		invites: map[string][]tsapi.DeviceInvite{},
	}
	c, rec := epicCollector(t, api, false, true, devices.WithDeviceInvites(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	states := availabilityStates(t, rec)
	for _, op := range []string{"listTailnetDevices", "device_posture", "device_invites"} {
		if got := states[op]; got != string(apistate.StateSupported) {
			t.Errorf("%s state = %q, want supported (#524: a clean scrape must not leave the capability matrix unknown)", op, got)
		}
	}
}

// TestCollect_APIState_DeviceInvitesScopeDeniedEveryDevice covers a
// device_invites:read scope 403ing on every device while the main listing
// still succeeds: the two operations must disagree (listTailnetDevices
// supported, device_invites scope_denied) in the SAME tick, and the tick must
// still return nil (per-device subrequest failures stay non-fatal).
func TestCollect_APIState_DeviceInvitesScopeDeniedEveryDevice(t *testing.T) {
	api := &fakeAPI{
		devices:       sampleDevices(),
		inviteFailAll: true,
		inviteErr:     &tsapi.StatusError{Code: 403},
	}
	c, rec := epicCollector(t, api, false, false, devices.WithDeviceInvites(true))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect() must stay non-fatal on a subrequest failure: %v", err)
	}
	states := availabilityStates(t, rec)
	if got := states["listTailnetDevices"]; got != string(apistate.StateSupported) {
		t.Errorf("listTailnetDevices = %q, want supported", got)
	}
	if got := states["device_invites"]; got != string(apistate.StateScopeDenied) {
		t.Errorf("device_invites = %q, want scope_denied (a missing scope must never read as disabled)", got)
	}
}

// TestCollect_APIState_OneDeviceFailingStillReportsFailure asserts that one
// failing device out of several is not hidden by the others' successes: the
// aggregated subrequest state must be the failure, not supported.
func TestCollect_APIState_OneDeviceFailingStillReportsFailure(t *testing.T) {
	devs := sampleDevices() // laptop=3690401478992208, n-desktop, n-phone
	api := &fakeAPI{
		devices:     devs,
		postureFail: "n-desktop",
		postureErr:  context.DeadlineExceeded,
		posture: map[string]map[string]any{
			"3690401478992208": {"node:os": "linux"},
			"n-phone":          {},
		},
	}
	c, rec := epicCollector(t, api, false, true)
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := availabilityStates(t, rec)["device_posture"]; got != string(apistate.StateTransientFailure) {
		t.Errorf("device_posture = %q, want transient_failure (one failing device out of three must not be masked by the other two succeeding)", got)
	}
}

// TestCollect_APIState_ZeroAttemptsStaysUnrecorded covers both ways a
// subrequest can have zero attempts in a tick: disabled by config (with
// devices present), and no devices at all (with the subrequest enabled). In
// both cases NO availability entry may exist for that operation — a probe
// that never happened must stay unknown, not fabricate a state.
func TestCollect_APIState_ZeroAttemptsStaysUnrecorded(t *testing.T) {
	t.Run("subrequests disabled", func(t *testing.T) {
		api := &fakeAPI{devices: sampleDevices()}
		c, rec := epicCollector(t, api, false, false)
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
		states := availabilityStates(t, rec)
		for _, op := range []string{"device_posture", "device_invites"} {
			if got, ok := states[op]; ok {
				t.Errorf("%s recorded availability %q despite zero attempts this tick", op, got)
			}
		}
		if got := states["listTailnetDevices"]; got != string(apistate.StateSupported) {
			t.Errorf("listTailnetDevices = %q, want supported", got)
		}
	})

	t.Run("no devices", func(t *testing.T) {
		api := &fakeAPI{devices: nil}
		c, rec := epicCollector(t, api, false, true, devices.WithDeviceInvites(true))
		if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
			t.Fatalf("Collect: %v", err)
		}
		states := availabilityStates(t, rec)
		for _, op := range []string{"device_posture", "device_invites"} {
			if got, ok := states[op]; ok {
				t.Errorf("%s recorded availability %q despite an empty tailnet this tick", op, got)
			}
		}
	})
}

// TestCollect_APIState_CoverageTalliesUnaffected is the #524 non-regression
// check against the #421 Coverage tally (TestCollect_SubrequestCoverage in
// epic479_test.go asserts this in full; this test re-confirms it alongside
// the NEW availability signal, proving apistate.Observe is a pure addition
// that coexists with Coverage rather than replacing or altering it).
func TestCollect_APIState_CoverageTalliesUnaffected(t *testing.T) {
	cov := apistate.NewCoverage()
	api := &fakeAPI{
		devices:     coverageDevices(), // k1, k2, k3 (epic479_test.go fixture)
		inviteFail:  "k1",
		inviteErr:   &tsapi.StatusError{Code: 403},
		postureFail: "k2",
		postureErr:  context.DeadlineExceeded,
		posture:     map[string]map[string]any{"k1": {"node:os": "linux"}},
	}
	c, rec := epicCollector(t, api, false, true,
		devices.WithDeviceInvites(true), devices.WithCoverage(cov))
	if err := c.Collect(context.Background(), rec.Emitter()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	snap := cov.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Coverage.Snapshot() = %d entries, want 2 (unchanged by #524)", len(snap))
	}
	for _, e := range snap {
		if e.Attempted != 3 || e.Succeeded != 2 || e.Failed != 1 {
			t.Errorf("coverage entry %+v changed shape; #524 must be a pure addition alongside Coverage", e)
		}
	}

	states := availabilityStates(t, rec)
	if got := states["device_invites"]; got != string(apistate.StateScopeDenied) {
		t.Errorf("device_invites availability = %q, want scope_denied", got)
	}
	if got := states["device_posture"]; got != string(apistate.StateTransientFailure) {
		t.Errorf("device_posture availability = %q, want transient_failure", got)
	}
}
