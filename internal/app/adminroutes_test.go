package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

// TestAdminRoutes_AreActuallyRegistered drives the REAL mux built by
// buildAdminServer and fails on a 404 for every route the admin surface is
// supposed to expose.
//
// It exists because "the handler works" and "the handler is reachable" are
// different claims, and this repository has now shipped the gap between them
// FOUR times: #320's full-config projection could be unassigned from
// /api/config.json with the suite green; #300's event store could be
// disconnected from both its feeds with the suite green; #300's /events page
// had no link from anywhere; and #321's support bundle was tested through a
// mux the test built itself, so deleting the real registration in admin.go
// changed nothing that any test could see.
//
// The pattern behind all four is the same: a handler test that constructs its
// own mux, or calls the handler directly, passes identically whether or not
// admin.go registers the route — and whether or not it wraps it in
// requireAdminAuth. Only driving the server the process actually serves can
// tell the difference. Every new admin route belongs in this table.
//
// A 404 here means the route is missing. Any other status — including 401/403
// — proves registration, which is all this test is about; auth behavior has
// its own tests in admin_auth_test.go.
func TestAdminRoutes_AreActuallyRegistered(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091" // loopback: tokenless mode, so a 403 cannot mask a 404
	cfg.Tailscale.Tailnet = "example.com"
	cfg.Flows.Enabled = true
	cfg.Events.Enabled = true
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())

	if a.runtimes[0].flowStore == nil {
		t.Fatal("flow store not built, so the /flows routes would be legitimately absent and this test would prove nothing")
	}
	if a.eventStore == nil {
		t.Fatal("event store not built, so the /events routes would be legitimately absent and this test would prove nothing")
	}

	srv := a.buildAdminServer()
	for _, path := range []string{
		"/",
		"/healthz",
		"/readyz",
		"/api/status.json",
		"/api/cardinality.json",
		"/api/config.json",
		"/api/support-bundle.zip",
		"/flows",
		"/api/flows.json",
		"/api/flows/export.csv",
		"/api/flows/export.json",
		"/events",
		"/api/events.json",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = adminLoopbackHost // tokenless loopback mode requires a loopback Host
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("GET %s = 404: the route is not registered in buildAdminServer, so it does not exist "+
				"in a running process however well its handler is tested", path)
		}
	}
}
