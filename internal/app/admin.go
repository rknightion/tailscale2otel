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

	"github.com/rknightion/tailscale2otel/v2/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v2/internal/listenaddr"
)

// registerProbes registers the liveness (/healthz) and readiness (/readyz)
// endpoints. They carry no Tailscale data and are safe to expose to a
// cluster's health checks. /healthz is always the unconditional "process is
// up" handler. /readyz uses ready when given (the real (*App).readyz, wired
// by buildAdminServer) so it reflects actual startup/receiver state (#57); a
// nil ready falls back to the same unconditional handler, which is all
// newAdminServer's App-less probe scaffold can offer.
func registerProbes(mux *http.ServeMux, ready http.HandlerFunc) {
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}
	mux.HandleFunc("/healthz", ok)
	if ready == nil {
		ready = ok
	}
	mux.HandleFunc("/readyz", ready)
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
// token. When no token is configured it fails CLOSED unless the admin listener
// is bound to loopback (#227): a wildcard/tailnet bind with no token would
// otherwise disclose the full device inventory (and config shape) to any host
// that can reach the port. A loopback bind stays usable without a credential —
// only the local host can reach it, so that is the deliberate escape hatch.
// pprof cannot reach the untokened branch at all: Validate requires a token
// whenever pprof is enabled, regardless of bind. On an auth failure (wrong bind,
// wrong/missing credential) it records the rejection reason and responds:
//   - no token configured, non-loopback bind: 403 plain text naming both
//     remedies (set admin.auth.token, or bind admin.listen to loopback). No
//     WWW-Authenticate challenge — this is misconfiguration, not a missing
//     credential, and a 401 would make a browser prompt for a password that
//     does not exist.
//   - token configured but the caller's credential is wrong/absent: 401 with a
//     Basic-auth challenge, as before.
//
// The /healthz and /readyz probes are registered separately and never wrapped.
func (a *App) requireAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	token := a.cfg.Admin.Auth.Token.Reveal()
	if token == "" {
		if listenaddr.IsLoopback(a.cfg.Admin.Listen) {
			return next
		}
		return func(w http.ResponseWriter, r *http.Request) {
			a.adminAuthRejected(reasonAuthRequired)
			a.logger.Warn("admin request rejected: no admin.auth.token configured on a network-reachable bind",
				"reason", reasonAuthRequired, "path", r.URL.Path, "listen", a.cfg.Admin.Listen,
				"remedy", "set admin.auth.token, or bind admin.listen to loopback (127.0.0.1 or localhost)")
			http.Error(w,
				"admin access refused: set admin.auth.token, or bind admin.listen to loopback (127.0.0.1 or localhost)",
				http.StatusForbidden)
		}
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
	registerProbes(mux, nil)
	return &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// buildAdminServer builds the admin HTTP server: always the /healthz + /readyz
// probes (/readyz backed by (*App).readyz, so it reflects real startup/
// receiver state — #57), plus the status landing page (/ and
// /api/status.json) unless admin.landing_page is disabled, plus /debug/pprof
// when profiling.pprof is enabled. The "/" handler is a catch-all, so
// handleIndex 404s unknown paths.
func (a *App) buildAdminServer() *http.Server {
	mux := http.NewServeMux()
	registerProbes(mux, a.readyz)
	if a.cfg.Admin.LandingPage {
		mux.HandleFunc("/", a.requireAdminAuth(a.handleIndex))
		mux.HandleFunc("/api/status.json", a.requireAdminAuth(a.handleStatusJSON))
		mux.HandleFunc("/api/cardinality.json", a.requireAdminAuth(a.handleCardinalityJSON))
		mux.HandleFunc("/api/config.json", a.requireAdminAuth(a.handleConfigJSON))
		mux.HandleFunc("/api/rdns/purge", a.requireAdminAuth(a.handleRDNSPurge))
	}
	if a.cfg.Profiling.Pprof.Enabled {
		registerPprof(mux, a.requireAdminAuth)
	}
	return &http.Server{
		Addr:              a.cfg.Admin.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 120s, not the 30s the other listeners use: /debug/pprof/profile?seconds=N
		// (and /trace) stream their response for N seconds and must complete inside
		// the write window. Still bounds a slow-read client at two minutes.
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
}

// runAdmin serves the admin endpoints until ctx is canceled, then shuts down
// gracefully. Errors other than the expected close are logged. Serves HTTPS
// when both admin.tls files are configured (Validate has already confirmed
// they exist and are readable); otherwise serves plain HTTP, byte-identical to
// before TLS support existed.
func (a *App) runAdmin(ctx context.Context) {
	certFile := a.cfg.Admin.TLS.CertFile
	keyFile := a.cfg.Admin.TLS.KeyFile
	errCh := make(chan error, 1)
	go func() {
		if certFile != "" && keyFile != "" {
			errCh <- a.adminSrv.ListenAndServeTLS(certFile, keyFile)
		} else {
			errCh <- a.adminSrv.ListenAndServe()
		}
	}()
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
