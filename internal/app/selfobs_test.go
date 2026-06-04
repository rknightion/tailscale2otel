package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/rknightion/tailscale2otel/internal/telemetrytest"
)

// TestIsCleanShutdownErr guards the false-positive trap: a receiver/admin server
// returning a normal stop signal (context cancel/deadline or a closed HTTP
// server, including wrapped) must NOT be counted as a component failure, or
// every SIGTERM would look like an outage.
func TestIsCleanShutdownErr(t *testing.T) {
	clean := []error{
		nil,
		context.Canceled,
		context.DeadlineExceeded,
		http.ErrServerClosed,
		fmt.Errorf("shutdown: %w", context.DeadlineExceeded),
	}
	for _, e := range clean {
		if !isCleanShutdownErr(e) {
			t.Errorf("isCleanShutdownErr(%v) = false, want true", e)
		}
	}
	dirty := []error{
		errors.New("listen tcp :9090: bind: address already in use"),
		errors.New("connection reset"),
	}
	for _, e := range dirty {
		if isCleanShutdownErr(e) {
			t.Errorf("isCleanShutdownErr(%v) = true, want false", e)
		}
	}
}

// TestEmitComponentError verifies the lifecycle error counter records one
// increment per call, classified by the component attribute, so a downed
// receiver / admin server / auto-configure failure is alertable.
func TestEmitComponentError(t *testing.T) {
	rec := telemetrytest.New()
	e := rec.Emitter()

	for _, c := range []string{"stream", "webhook", "admin", "auto_configure"} {
		emitComponentError(e, c)
	}

	pts := rec.MetricPoints("tailscale2otel.component.errors")
	byComponent := map[string]telemetrytest.MetricPoint{}
	for _, p := range pts {
		byComponent[p.Attrs["component"]] = p
	}
	for _, c := range []string{"stream", "webhook", "admin", "auto_configure"} {
		p, ok := byComponent[c]
		if !ok {
			t.Errorf("no component.errors point for component=%q (have %v)", c, byComponent)
			continue
		}
		if p.Value != 1 {
			t.Errorf("component=%q value = %v, want 1", c, p.Value)
		}
		if p.Kind != "sum" || !p.Monotonic {
			t.Errorf("component=%q kind=%q monotonic=%v, want a monotonic sum", c, p.Kind, p.Monotonic)
		}
	}
}
