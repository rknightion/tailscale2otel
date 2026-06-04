package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/pprof"
	"time"
)

// registerProbes registers the liveness (/healthz) and readiness (/readyz)
// endpoints. They carry no Tailscale data and are safe to expose to a cluster's
// health checks.
func registerProbes(mux *http.ServeMux) {
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}
	mux.HandleFunc("/healthz", ok)
	mux.HandleFunc("/readyz", ok)
}

// registerPprof mounts the standard net/http/pprof endpoints so Grafana Alloy's
// pyroscope.scrape (or `go tool pprof`) can PULL profiles. Opt-in via
// profiling.pprof.enabled.
func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

// newAdminServer builds a probes-only admin HTTP server. Retained for the
// probe-focused unit test; the running service uses (*App).buildAdminServer,
// which layers the status page and pprof onto the same mux.
func newAdminServer(listen string) *http.Server {
	mux := http.NewServeMux()
	registerProbes(mux)
	return &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// buildAdminServer builds the admin HTTP server: always the /healthz + /readyz
// probes, plus the status landing page (/ and /api/status.json) unless
// admin.landing_page is disabled, plus /debug/pprof when profiling.pprof is
// enabled. The "/" handler is a catch-all, so handleIndex 404s unknown paths.
func (a *App) buildAdminServer() *http.Server {
	mux := http.NewServeMux()
	registerProbes(mux)
	if a.cfg.Admin.LandingPage {
		mux.HandleFunc("/", a.handleIndex)
		mux.HandleFunc("/api/status.json", a.handleStatusJSON)
	}
	if a.cfg.Profiling.Pprof.Enabled {
		registerPprof(mux)
	}
	return &http.Server{
		Addr:              a.cfg.Admin.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// runAdmin serves the admin endpoints until ctx is canceled, then shuts down
// gracefully. Errors other than the expected close are logged.
func (a *App) runAdmin(ctx context.Context) {
	errCh := make(chan error, 1)
	go func() { errCh <- a.adminSrv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.adminSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("admin server stopped", "error", err)
		}
	}
}
