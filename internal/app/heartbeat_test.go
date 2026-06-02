package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

func TestRunHeartbeat_EmitsUp(t *testing.T) {
	// synctest (Go 1.26) gives a fake clock so the ticker is deterministic and
	// the test runs instantly instead of waiting a real interval.
	synctest.Test(t, func(t *testing.T) {
		rec := telemetrytest.New()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go runHeartbeat(ctx, rec.Emitter(), time.Hour)

		// Wait until the heartbeat goroutine has done its initial emit and is
		// durably blocked in its select.
		synctest.Wait()
		pts := rec.MetricPoints("tailscale2otel.up")
		if len(pts) != 1 || pts[0].Value != 1 {
			t.Fatalf("after start: up = %+v, want a single point value 1", pts)
		}

		// Advance the fake clock one interval; the ticker fires and re-emits.
		time.Sleep(time.Hour)
		synctest.Wait()
		pts = rec.MetricPoints("tailscale2otel.up")
		if len(pts) != 1 || pts[0].Value != 1 {
			t.Fatalf("after one tick: up = %+v, want value 1", pts)
		}
	})
}
