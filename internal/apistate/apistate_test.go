package apistate_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/apistate"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
	"github.com/rknightion/tailscale2otel/v3/internal/tsapi"
)

func statusErr(code int) error {
	return &tsapi.StatusError{Method: "GET", URL: "https://example.invalid/x", Code: code, Body: "nope"}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		disp apistate.Disposition
		want apistate.State
	}{
		{"nil error is supported", nil, apistate.Disposition{}, apistate.StateSupported},

		// The whole point of #420: 401 and 403 must not collapse together, and a
		// real 403 regression must never read as a healthy zero.
		{"401 is a rejected credential", statusErr(401), apistate.Disposition{}, apistate.StateCredentialRejected},
		{"403 defaults to scope denied", statusErr(403), apistate.Disposition{}, apistate.StateScopeDenied},
		{"404 is an absent endpoint", statusErr(404), apistate.Disposition{}, apistate.StateDisabled},

		// Only an operation that explicitly opts in reads 403 as "feature off"
		// (flowlogs' documented premium gate).
		{"403 is disabled when declared", statusErr(403), apistate.Disposition{DisabledOn: []int{403}}, apistate.StateDisabled},
		{"401 stays rejected even when 403 is declared", statusErr(401), apistate.Disposition{DisabledOn: []int{403}}, apistate.StateCredentialRejected},

		{"429 is transient", statusErr(429), apistate.Disposition{}, apistate.StateTransientFailure},
		{"500 is transient", statusErr(500), apistate.Disposition{}, apistate.StateTransientFailure},
		{"503 is transient", statusErr(503), apistate.Disposition{}, apistate.StateTransientFailure},
		{"400 is transient", statusErr(400), apistate.Disposition{}, apistate.StateTransientFailure},

		{"wrapped status error is unwrapped", fmt.Errorf("collector: %w", statusErr(403)), apistate.Disposition{}, apistate.StateScopeDenied},

		{"timeout is transient", context.DeadlineExceeded, apistate.Disposition{}, apistate.StateTransientFailure},
		{"network error is transient", &net.OpError{Op: "dial"}, apistate.Disposition{}, apistate.StateTransientFailure},
		{"plain error is transient", errors.New("boom"), apistate.Disposition{}, apistate.StateTransientFailure},

		// Shutdown must not flap a collector into a failure state.
		{"cancellation is unknown", context.Canceled, apistate.Disposition{}, apistate.StateUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := apistate.Classify(tc.err, tc.disp); got != tc.want {
				t.Errorf("Classify(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestStatesIsTheClosedSet(t *testing.T) {
	got := apistate.States()
	want := []apistate.State{
		apistate.StateUnknown,
		apistate.StateSupported,
		apistate.StateDisabled,
		apistate.StateScopeDenied,
		apistate.StateCredentialRejected,
		apistate.StateTransientFailure,
	}
	if len(got) != len(want) {
		t.Fatalf("States() has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("States()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// EmitAvailability must write a full zero-seeded state set so a plain
// synchronous gauge can never ghost a stale state, and so two collectors
// emitting the same metric name cannot clobber each other (which is exactly
// what GaugeSnapshot would have done).
func TestEmitAvailabilityIsZeroSeeded(t *testing.T) {
	r := telemetrytest.New()
	apistate.EmitAvailability(r.Emitter(), "flowlogs", "listNetworkFlowLogs", apistate.StateDisabled)

	pts := r.MetricPoints(apistate.MetricAvailability)
	if len(pts) != len(apistate.States()) {
		t.Fatalf("got %d availability points, want one per state (%d)", len(pts), len(apistate.States()))
	}
	seen := map[string]float64{}
	for _, p := range pts {
		if p.Attrs["tailscale.collector"] != "flowlogs" {
			t.Errorf("point has collector %q, want flowlogs", p.Attrs["tailscale.collector"])
		}
		if p.Attrs["tailscale.api.operation"] != "listNetworkFlowLogs" {
			t.Errorf("point has operation %q, want listNetworkFlowLogs", p.Attrs["tailscale.api.operation"])
		}
		seen[p.Attrs["tailscale.api.state"]] = p.Value
	}
	for _, s := range apistate.States() {
		v, ok := seen[string(s)]
		if !ok {
			t.Errorf("state %q has no series; the set is not zero-seeded", s)
			continue
		}
		want := 0.0
		if s == apistate.StateDisabled {
			want = 1
		}
		if v != want {
			t.Errorf("state %q = %v, want %v", s, v, want)
		}
	}
}

func TestTrackerRecordsLatestStatePerOperation(t *testing.T) {
	tr := apistate.NewTracker()
	t0 := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

	tr.Record("acl", "getPolicyFile", apistate.StateTransientFailure, t0)
	tr.Record("acl", "getPolicyFile", apistate.StateSupported, t0.Add(time.Minute))
	tr.Record("oauth_apps", "listOAuthApps", apistate.StateDisabled, t0)

	snap := tr.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("got %d entries, want 2", len(snap))
	}
	// Snapshot is sorted for a stable status page / test assertions.
	if snap[0].Collector != "acl" || snap[0].Operation != "getPolicyFile" {
		t.Fatalf("first entry is %+v, want acl/getPolicyFile", snap[0])
	}
	if snap[0].State != apistate.StateSupported {
		t.Errorf("acl state = %q, want the LATEST state (supported)", snap[0].State)
	}
	if !snap[0].LastProbe.Equal(t0.Add(time.Minute)) {
		t.Errorf("acl last probe = %v, want %v", snap[0].LastProbe, t0.Add(time.Minute))
	}
	if snap[1].State != apistate.StateDisabled {
		t.Errorf("oauth_apps state = %q, want disabled", snap[1].State)
	}
}

func TestTrackerIsNilSafe(t *testing.T) {
	var tr *apistate.Tracker
	tr.Record("acl", "getPolicyFile", apistate.StateSupported, time.Now())
	if got := tr.Snapshot(); got != nil {
		t.Errorf("nil tracker Snapshot() = %v, want nil", got)
	}
}

func TestCoverageTracksPartialFailure(t *testing.T) {
	c := apistate.NewCoverage()
	c.Record("devices", "device_invites", apistate.StateSupported)
	c.Record("devices", "device_invites", apistate.StateSupported)
	c.Record("devices", "device_invites", apistate.StateScopeDenied)
	c.Record("devices", "posture_attributes", apistate.StateSupported)

	snap := c.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("got %d coverage entries, want 2", len(snap))
	}

	var inv apistate.CoverageEntry
	for _, e := range snap {
		if e.Subrequest == "device_invites" {
			inv = e
		}
	}
	if inv.Attempted != 3 {
		t.Errorf("attempted = %d, want 3", inv.Attempted)
	}
	if inv.Succeeded != 2 {
		t.Errorf("succeeded = %d, want 2", inv.Succeeded)
	}
	if inv.Failed != 1 {
		t.Errorf("failed = %d, want 1", inv.Failed)
	}
	if got := inv.Ratio(); got != 2.0/3.0 {
		t.Errorf("ratio = %v, want %v", got, 2.0/3.0)
	}
	if inv.Failures[apistate.StateScopeDenied] != 1 {
		t.Errorf("scope_denied failures = %d, want 1", inv.Failures[apistate.StateScopeDenied])
	}
}

func TestCoverageRatioOfNothingAttemptedIsOne(t *testing.T) {
	// No devices to iterate is full coverage, not zero coverage — otherwise an
	// empty tailnet permanently reads as degraded.
	e := apistate.CoverageEntry{}
	if got := e.Ratio(); got != 1 {
		t.Errorf("Ratio() with nothing attempted = %v, want 1", got)
	}
}

func TestCoverageResetClearsCounts(t *testing.T) {
	c := apistate.NewCoverage()
	c.Record("devices", "device_invites", apistate.StateScopeDenied)
	c.Reset()
	if got := c.Snapshot(); len(got) != 0 {
		t.Errorf("Snapshot() after Reset() = %v, want empty", got)
	}
}

// One *Coverage is shared per tailnet runtime, so a collector resetting its own
// tallies must not touch anyone else's — otherwise two collectors with
// subrequests would wipe each other mid-tick and emit the other's series.
func TestCoverageResetCollectorIsScoped(t *testing.T) {
	c := apistate.NewCoverage()
	c.Record("devices", "device_invites", apistate.StateSupported)
	c.Record("devices", "posture_attributes", apistate.StateScopeDenied)
	c.Record("users", "user_invites", apistate.StateSupported)

	c.ResetCollector("devices")

	snap := c.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("got %d entries after resetting devices, want 1 (users only): %+v", len(snap), snap)
	}
	if snap[0].Collector != "users" || snap[0].Subrequest != "user_invites" {
		t.Errorf("surviving entry is %+v, want the users one", snap[0])
	}
	if snap[0].Attempted != 1 {
		t.Errorf("users attempted = %d, want 1 (untouched by the devices reset)", snap[0].Attempted)
	}
}

func TestCoverageResetCollectorIsNilSafe(t *testing.T) {
	var c *apistate.Coverage
	c.ResetCollector("devices")
}

func TestCoverageIsNilSafe(t *testing.T) {
	var c *apistate.Coverage
	c.Record("devices", "device_invites", apistate.StateSupported)
	c.Reset()
	if got := c.Snapshot(); got != nil {
		t.Errorf("nil coverage Snapshot() = %v, want nil", got)
	}
}
