package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

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

// TestAPIObserverDuration verifies that apiObserver records one
// tailscale2otel.api.duration histogram observation per request, in seconds,
// bucketed by the explicit bounds and labeled with endpoint + status code. The
// duration is the full logical-request wall-clock (incl. retry backoff).
func TestAPIObserverDuration(t *testing.T) {
	rec := telemetrytest.New()
	// 150ms request to /devices that returned 200 after 1 attempt.
	apiObserver(rec.Emitter())(context.Background(), "/devices", 200, 1, 150*time.Millisecond)

	pts := rec.MetricPoints("tailscale2otel.api.duration")
	if len(pts) != 1 {
		t.Fatalf("got %d api.duration points, want 1", len(pts))
	}
	p := pts[0]
	if p.Kind != "histogram" {
		t.Errorf("kind = %q, want histogram", p.Kind)
	}
	if p.Unit != "s" {
		t.Errorf("unit = %q, want s", p.Unit)
	}
	if p.Count != 1 {
		t.Errorf("count = %d, want 1", p.Count)
	}
	if p.Attrs["endpoint"] != "/devices" {
		t.Errorf("endpoint = %q, want /devices", p.Attrs["endpoint"])
	}
	if p.Attrs["http.response.status_code"] != "200" {
		t.Errorf("status_code = %q, want 200", p.Attrs["http.response.status_code"])
	}
	// 0.15s falls in the (0.1, 0.25] bucket. Bounds: 0.025,0.05,0.1,0.25,0.5,1,2.5,5,10,30
	// → BucketCounts index 3 = (0.1, 0.25].
	if got := p.BucketCounts[3]; got != 1 {
		t.Errorf("BucketCounts = %v, want index 3 == 1", p.BucketCounts)
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
