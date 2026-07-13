package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
)

// TestAdminAndMetricsServersSetFullTimeouts verifies that slow-body/slow-read
// protection is applied to every listener, not just the header.  The webhook
// and stream servers already set ReadTimeout/WriteTimeout/IdleTimeout; admin
// and metrics must match.
func TestAdminAndMetricsServersSetFullTimeouts(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	for name, tc := range map[string]struct {
		srv          *http.Server
		writeTimeout time.Duration
	}{
		// Admin WriteTimeout is 120s, not 30s: /debug/pprof/profile?seconds=N
		// streams for N seconds and must fit inside it.
		"admin":   {a.buildAdminServer(), 120 * time.Second},
		"metrics": {a.buildMetricsServer(prometheus.NewRegistry()), 30 * time.Second},
	} {
		srv := tc.srv
		if srv.ReadHeaderTimeout != 10*time.Second {
			t.Errorf("%s: ReadHeaderTimeout = %v, want 10s", name, srv.ReadHeaderTimeout)
		}
		if srv.ReadTimeout != 30*time.Second {
			t.Errorf("%s: ReadTimeout = %v, want 30s", name, srv.ReadTimeout)
		}
		if srv.WriteTimeout != tc.writeTimeout {
			t.Errorf("%s: WriteTimeout = %v, want %v", name, srv.WriteTimeout, tc.writeTimeout)
		}
		if srv.IdleTimeout != 120*time.Second {
			t.Errorf("%s: IdleTimeout = %v, want 120s", name, srv.IdleTimeout)
		}
	}
}
