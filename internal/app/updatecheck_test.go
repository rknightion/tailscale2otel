package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

func TestRunUpdateCheckEmits(t *testing.T) {
	// synctest (Go 1.26) gives a fake clock so the immediate emit is observable
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
