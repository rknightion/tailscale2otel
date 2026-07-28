package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// A listener that fails to bind used to log an ERROR, bump a counter, and stop
// there: /readyz kept answering 200 and the status page kept saying healthy,
// so an admin or Prometheus surface could be dead for as long as nobody looked
// at the logs (#306). Receivers already fed readiness (#57); the two listeners
// did not.

func listenerApp(t *testing.T) *App {
	t.Helper()
	// A real recorder, not a nil emitter: the failure path also increments
	// tailscale2otel.component.errors, and self-observability is on by default.
	return &App{
		cfg:         config.Default(),
		logger:      slog.New(slog.DiscardHandler),
		readyState:  newComponentHealth(),
		procEmitter: telemetrytest.New().Emitter(),
	}
}

// occupy binds a loopback port and returns its address, so a second bind of the
// same address fails for real rather than through a mock. The listener closes
// with the test.
func occupy(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

func TestAdminBindFailureMakesTheProcessUnready(t *testing.T) {
	addr := occupy(t)
	a := listenerApp(t)
	a.cfg.Admin.Listen = addr
	a.adminSrv = &http.Server{Addr: addr, ReadHeaderTimeout: time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.runAdmin(ctx)

	reasons := a.readyState.reasons()
	if len(reasons) == 0 {
		t.Fatalf("the admin listener failed to bind %q and readiness knows nothing about it. "+
			"A required listener must not be able to die while the process reports itself healthy.", addr)
	}
	if !strings.Contains(strings.Join(reasons, "; "), appcatalog.ComponentAdmin) {
		t.Errorf("readiness reasons = %v, want one naming the %q component", reasons, appcatalog.ComponentAdmin)
	}
}

func TestMetricsBindFailureMakesTheProcessUnready(t *testing.T) {
	addr := occupy(t)
	a := listenerApp(t)
	a.cfg.Prometheus.Listen = addr
	a.metricsSrv = &http.Server{Addr: addr, ReadHeaderTimeout: time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.runMetrics(ctx)

	reasons := a.readyState.reasons()
	if len(reasons) == 0 {
		t.Fatalf("the Prometheus listener failed to bind %q and readiness knows nothing about it", addr)
	}
	if !strings.Contains(strings.Join(reasons, "; "), appcatalog.ComponentMetrics) {
		t.Errorf("readiness reasons = %v, want one naming the %q component", reasons, appcatalog.ComponentMetrics)
	}
}

// The failure has to reach /readyz itself, not just the tracker behind it.
func TestReadyzReports503AfterAListenerFails(t *testing.T) {
	a := listenerApp(t)
	a.readyState.fail(appcatalog.ComponentMetrics, errors.New("listen tcp 0.0.0.0:2112: bind: address already in use"))

	rec := httptest.NewRecorder()
	a.readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d after a listener failed to bind, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), appcatalog.ComponentMetrics) {
		t.Errorf("/readyz body = %q, want it to name the failed component", rec.Body.String())
	}
}

// A clean shutdown is not a failure: SIGTERM closes both servers, and a pod that
// reported itself unready on the way out would look like a crash. This guards the
// context-cancellation branch specifically — it returns after Shutdown without
// consulting the error channel at all, so the clean-shutdown filter is never
// reached on this path and cannot be what makes the test pass.
func TestGracefulShutdownIsNotAListenerFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*App, context.Context)
		set  func(*App, string)
	}{
		{"admin", (*App).runAdmin, func(a *App, addr string) { a.adminSrv = &http.Server{Addr: addr, ReadHeaderTimeout: time.Second} }},
		{"metrics", (*App).runMetrics, func(a *App, addr string) { a.metricsSrv = &http.Server{Addr: addr, ReadHeaderTimeout: time.Second} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := listenerApp(t)
			tc.set(a, "127.0.0.1:0")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { tc.run(a, ctx); close(done) }()
			// Let the server reach ListenAndServe before asking it to stop.
			time.Sleep(50 * time.Millisecond)
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("run did not return after context cancellation")
			}
			if reasons := a.readyState.reasons(); len(reasons) != 0 {
				t.Errorf("a graceful shutdown recorded readiness failures %v; a pod reporting "+
					"unready on its way out looks like a crash", reasons)
			}
		})
	}
}
