package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
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
// profiling.pprof.enabled. Each handler is passed through wrap so it inherits
// the admin auth gate — pprof can expose in-memory secrets, so config.Validate
// requires admin.auth.token whenever pprof is enabled.
func registerPprof(mux *http.ServeMux, wrap func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("/debug/pprof/", wrap(pprof.Index))
	mux.HandleFunc("/debug/pprof/cmdline", wrap(pprof.Cmdline))
	mux.HandleFunc("/debug/pprof/profile", wrap(pprof.Profile))
	mux.HandleFunc("/debug/pprof/symbol", wrap(pprof.Symbol))
	mux.HandleFunc("/debug/pprof/trace", wrap(pprof.Trace))
}

// adminAuthorized reports whether r presents the configured admin token, either
// as the HTTP Basic password or as an "Authorization: Bearer <token>" header.
// The comparison is constant-time. This mirrors stream.Server.authorized so the
// admin surface and the log-stream receiver verify shared secrets the same way.
func adminAuthorized(r *http.Request, token string) bool {
	if _, pass, ok := r.BasicAuth(); ok {
		return subtle.ConstantTimeCompare([]byte(pass), []byte(token)) == 1
	}
	if fields := strings.Fields(r.Header.Get("Authorization")); len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return subtle.ConstantTimeCompare([]byte(fields[1]), []byte(token)) == 1
	}
	return false
}

// requireAdminAuth wraps next so it is reachable only with the configured admin
// token. When no token is configured it returns next unchanged: the status page
// is opt-in (Warnings advises configuring a token on an exposed bind), and pprof
// cannot reach an untokened wrapper because Validate requires a token whenever
// pprof is enabled. On failure it sends a Basic-auth challenge so browsers
// prompt, records the rejection, and returns 401. The /healthz and /readyz
// probes are registered separately and never wrapped.
func (a *App) requireAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	token := a.cfg.Admin.Auth.Token.Reveal()
	if token == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !adminAuthorized(r, token) {
			reason := reasonBadCredentials
			if r.Header.Get("Authorization") == "" {
				reason = reasonMissingCredentials
			}
			a.adminAuthRejected(reason)
			a.logger.Warn("admin request rejected", "reason", reason, "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Basic realm="tailscale2otel admin"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
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
		mux.HandleFunc("/", a.requireAdminAuth(a.handleIndex))
		mux.HandleFunc("/api/status.json", a.requireAdminAuth(a.handleStatusJSON))
		mux.HandleFunc("/api/rdns/purge", a.requireAdminAuth(a.handleRDNSPurge))
	}
	if a.cfg.Profiling.Pprof.Enabled {
		registerPprof(mux, a.requireAdminAuth)
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
			a.componentError(appcatalog.ComponentAdmin)
		}
	}
}
