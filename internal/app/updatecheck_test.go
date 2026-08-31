package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/release"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// TestRunUpdateCheck_ZeroIntervalClamped verifies that passing interval=0 to
// runUpdateCheck does not panic (time.NewTicker(0) panics; the clamp prevents
// it) and still produces the initial emit.
func TestRunUpdateCheck_ZeroIntervalClamped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		latest := func() (string, bool) { return "v9.9.9", true }

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Must not panic even with a zero interval.
		go runUpdateCheck(ctx, rec.Emitter(), latest, "v0.1.0", 0)

		synctest.Wait()
		pts := rec.MetricPoints(appcatalog.MetricUpdateAvailable)
		if len(pts) != 1 || pts[0].Value != 1 {
			t.Fatalf("update_available with zero interval = %+v, want one point value 1", pts)
		}
	})
}

func TestRunUpdateCheckEmits(t *testing.T) {
	// synctest (Go 1.27) gives a fake clock so the immediate emit is observable
	// deterministically without polling real wall-clock time.
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		latest := func() (string, bool) { return "v9.9.9", true }

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Long interval: only the immediate emit fires within the test.
		go runUpdateCheck(ctx, rec.Emitter(), latest, "v0.1.0", time.Hour)

		// Wait until the goroutine has done its initial emit and is durably
		// blocked in its select.
		synctest.Wait()
		pts := rec.MetricPoints(appcatalog.MetricUpdateAvailable)
		if len(pts) != 1 || pts[0].Value != 1 {
			t.Fatalf("update_available points = %+v want one point value 1", pts)
		}
	})
}

func TestRunUpdateCheckUpToDate(t *testing.T) {
	rec := telemetrytest.New()
	latest := func() (string, bool) { return "v0.1.0", true }
	emitUpdateCheck(rec.Emitter(), latest, "v0.1.0") // direct one-shot helper
	pts := rec.MetricPoints(appcatalog.MetricUpdateAvailable)
	if len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("update_available = %+v want value 0", pts)
	}
}

func TestRunUpdateCheckDowngrade(t *testing.T) {
	rec := telemetrytest.New()
	// Running build is newer than the latest published release -> not an update.
	emitUpdateCheck(rec.Emitter(), func() (string, bool) { return "v0.1.0", true }, "v9.9.9")
	pts := rec.MetricPoints(appcatalog.MetricUpdateAvailable)
	if len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("update_available = %+v want value 0", pts)
	}
}

func TestRunUpdateCheckNoValueOrDevBuild(t *testing.T) {
	rec := telemetrytest.New()
	emitUpdateCheck(rec.Emitter(), func() (string, bool) { return "", false }, "v0.1.0")   // no upstream value
	emitUpdateCheck(rec.Emitter(), func() (string, bool) { return "v9.9.9", true }, "dev") // dev build
	if pts := rec.MetricPoints(appcatalog.MetricUpdateAvailable); len(pts) != 0 {
		t.Fatalf("expected no emission, got %+v", pts)
	}
}

// TestUpdateInfo_Disabled asserts a disabled check renders as State="disabled"
// with no other field populated (distinguishable from "current" — #330).
func TestUpdateInfo_Disabled(t *testing.T) {
	got := updateInfo(false, release.Snapshot{}, "v1.0.0")
	if got.Enabled {
		t.Error("Enabled should be false")
	}
	if got.State != "disabled" {
		t.Errorf("State = %q, want %q", got.State, "disabled")
	}
	if got.CurrentVersion != "" || got.LatestVersion != "" || got.LastCheckedAt != "" {
		t.Errorf("disabled UpdateInfo should carry no other data: %+v", got)
	}
}

// TestUpdateInfo_CheckingNeverSucceeded asserts an enabled check that has
// never completed a successful fetch, and has no recorded failure either
// (still warming up), renders as "checking" — not "current".
func TestUpdateInfo_CheckingNeverSucceeded(t *testing.T) {
	got := updateInfo(true, release.Snapshot{}, "v1.0.0")
	if got.State != "checking" {
		t.Errorf("State = %q, want %q", got.State, "checking")
	}
}

// TestUpdateInfo_ErrorNeverSucceeded asserts an enabled check whose only
// attempt failed renders as "error", distinct from "checking" and "current".
func TestUpdateInfo_ErrorNeverSucceeded(t *testing.T) {
	checkedAt := time.Now()
	got := updateInfo(true, release.Snapshot{CheckedAt: checkedAt, ErrClass: "network"}, "v1.0.0")
	if got.State != "error" {
		t.Errorf("State = %q, want %q", got.State, "error")
	}
	if got.LastErrorClass != "network" {
		t.Errorf("LastErrorClass = %q, want %q", got.LastErrorClass, "network")
	}
	if got.LastCheckedAt == "" {
		t.Error("LastCheckedAt should be populated")
	}
}

// TestUpdateInfo_UnknownDevBuild asserts a "dev" running version never
// renders as "current" even with a perfectly good upstream value — the
// comparison is not trustworthy (#330 acceptance criterion).
func TestUpdateInfo_UnknownDevBuild(t *testing.T) {
	got := updateInfo(true, release.Snapshot{OK: true, Latest: "v9.9.9"}, "dev")
	if got.State != "unknown" {
		t.Errorf("State = %q, want %q", got.State, "unknown")
	}
	if got.CurrentVersion != "" || got.LatestVersion != "" {
		t.Errorf("unknown state should not carry version fields: %+v", got)
	}
}

// TestUpdateInfo_UnknownUnparseableUpstream mirrors the dev-build case for a
// garbage/unparseable value fetched from upstream.
func TestUpdateInfo_UnknownUnparseableUpstream(t *testing.T) {
	got := updateInfo(true, release.Snapshot{OK: true, Latest: "not-a-version"}, "v1.0.0")
	if got.State != "unknown" {
		t.Errorf("State = %q, want %q", got.State, "unknown")
	}
}

// TestUpdateInfo_Current asserts a trustworthy comparison where the running
// build is already the latest renders "current" with normalized versions.
func TestUpdateInfo_Current(t *testing.T) {
	got := updateInfo(true, release.Snapshot{OK: true, Latest: "v1.2.3"}, "v1.2.3")
	if got.State != "current" {
		t.Errorf("State = %q, want %q", got.State, "current")
	}
	if got.CurrentVersion != "1.2.3" || got.LatestVersion != "1.2.3" {
		t.Errorf("versions = %q/%q, want 1.2.3/1.2.3", got.CurrentVersion, got.LatestVersion)
	}
	if got.MinorsBehind != 0 {
		t.Errorf("MinorsBehind = %d, want 0", got.MinorsBehind)
	}
	if got.ReleaseURL != release.SelfReleaseURL {
		t.Errorf("ReleaseURL = %q, want %q", got.ReleaseURL, release.SelfReleaseURL)
	}
}

// TestUpdateInfo_Available asserts a newer upstream release renders
// "available" with MinorsBehind computed.
func TestUpdateInfo_Available(t *testing.T) {
	got := updateInfo(true, release.Snapshot{OK: true, Latest: "v1.5.0"}, "v1.2.3")
	if got.State != "available" {
		t.Errorf("State = %q, want %q", got.State, "available")
	}
	if got.LatestVersion != "1.5.0" {
		t.Errorf("LatestVersion = %q, want 1.5.0", got.LatestVersion)
	}
	if got.MinorsBehind != 3 {
		t.Errorf("MinorsBehind = %d, want 3", got.MinorsBehind)
	}
}

// TestUpdateInfo_FailOpenKeepsVerdictButShowsError asserts the fail-open
// contract survives into the page: a previously successful fetch's verdict
// (current/available) is preserved even while the most recent check attempt
// is failing, and that failure is still surfaced via LastErrorClass so an
// operator isn't shown stale data with no warning.
func TestUpdateInfo_FailOpenKeepsVerdictButShowsError(t *testing.T) {
	got := updateInfo(true, release.Snapshot{OK: true, Latest: "v1.5.0", ErrClass: "network"}, "v1.2.3")
	if got.State != "available" {
		t.Errorf("State = %q, want %q (fail-open should preserve the last good verdict)", got.State, "available")
	}
	if got.LastErrorClass != "network" {
		t.Errorf("LastErrorClass = %q, want %q", got.LastErrorClass, "network")
	}
}

// TestDeviceVersionCheckInfo_FailOpenVisible guards the device-side version
// fetch too: unlike the emitted skew metrics, status must make a blocked
// refresh visible even while the last fetched stable version remains usable.
func TestDeviceVersionCheckInfo_FailOpenVisible(t *testing.T) {
	got := deviceVersionCheckInfo(true, release.Snapshot{
		OK:       true,
		Latest:   "1.2.3",
		ErrClass: "network",
	})
	if got.State != "ready" {
		t.Errorf("State = %q, want ready from cached successful version", got.State)
	}
	if got.LatestVersion != "1.2.3" {
		t.Errorf("LatestVersion = %q, want 1.2.3", got.LatestVersion)
	}
	if got.LastErrorClass != "network" {
		t.Errorf("LastErrorClass = %q, want network (blocked refresh must be visible)", got.LastErrorClass)
	}
}

// TestDeviceVersionCheckInfo_NeverSucceededIsNotReady negative-tests the
// status guard: a fetcher with no successful data must not look usable.
func TestDeviceVersionCheckInfo_NeverSucceededIsNotReady(t *testing.T) {
	got := deviceVersionCheckInfo(true, release.Snapshot{ErrClass: "network"})
	if got.State == "ready" {
		t.Errorf("State = ready with no successful fetch: got %+v", got)
	}
	if got.State != "error" {
		t.Errorf("State = %q, want error", got.State)
	}
}
