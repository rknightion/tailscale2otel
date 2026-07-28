package app

import (
	"context"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v3/internal/listenaddr"
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

// Additional admin auth-rejection reasons, in the same closed-set shape as the
// reason* constants in selfobs.go (they label the same
// tailscale2otel.admin.auth.rejected counter, so the set stays bounded).
const (
	// reasonUntrustedHost marks a tokenless-loopback request whose Host header
	// is not a loopback literal or "localhost" — the DNS-rebinding case in
	// GHSA-gvm7-8848-7hcq.
	reasonUntrustedHost = "untrusted_host"
	// reasonCrossSiteRequest marks a tokenless-loopback request a browser
	// labeled cross-site (or whose Origin does not match the request Host) —
	// a remote page reading the local admin surface.
	reasonCrossSiteRequest = "cross_site_request"
)

// loopbackHostHeader reports whether an HTTP Host header addresses this machine
// over loopback: a loopback IP literal (127.0.0.0/8, ::1, with or without a
// port or brackets) or the "localhost" name.
//
// It exists because listenaddr.IsLoopback classifies the *bind* address, which
// says nothing about who a request was addressed to. A loopback bind is not a
// caller-authorization proof on its own: a remote origin can point its own name
// at 127.0.0.1 (DNS rebinding) and reach the listener through a victim's
// browser, carrying its own name in Host.
//
// It fails CLOSED — an empty, unparseable or non-literal host is not loopback.
// A name other than "localhost" is never resolved: a DNS answer is not a
// security boundary, and resolving one is exactly the attack.
func loopbackHostHeader(host string) bool {
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	// A rooted FQDN ("localhost.") is the same name.
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// requireLoopbackCaller gates the tokenless-loopback escape hatch on the
// request actually being addressed to loopback and not originating from another
// web origin. Both checks are browser-facing: Host is what DNS rebinding
// forges, and Sec-Fetch-Site is what a browser stamps on a cross-site fetch and
// script cannot override. Non-browser clients (curl) send neither Sec-Fetch-*
// nor Origin and are unaffected; they still must address loopback.
func (a *App) requireLoopbackCaller(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHostHeader(r.Host) {
			a.adminAuthRejected(reasonUntrustedHost)
			a.logger.Warn("admin request rejected: tokenless loopback mode requires a loopback Host header",
				"reason", reasonUntrustedHost, "path", r.URL.Path, "host", r.Host,
				"remedy", "reach the admin server as 127.0.0.1/localhost, or set admin.auth.token")
			http.Error(w,
				"admin access refused: without admin.auth.token the request must address loopback (Host: 127.0.0.1 or localhost)",
				http.StatusForbidden)
			return
		}
		if !sameOrigin(r) {
			a.adminAuthRejected(reasonCrossSiteRequest)
			a.logger.Warn("admin request rejected: cross-site request to the tokenless loopback admin server",
				"reason", reasonCrossSiteRequest, "path", r.URL.Path,
				"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
			http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// requireAdminAuth wraps next so it is reachable only with the configured admin
// token. When no token is configured it fails CLOSED unless the admin listener
// is bound to loopback (#227): a wildcard/tailnet bind with no token would
// otherwise disclose the full device inventory (and config shape) to any host
// that can reach the port. A loopback bind stays usable without a credential —
// only the local host can reach it, so that is the deliberate escape hatch.
// pprof cannot reach the untokened branch at all: Validate requires a token
// whenever pprof is enabled, regardless of bind.
//
// The escape hatch is additionally gated by requireLoopbackCaller
// (GHSA-gvm7-8848-7hcq): a loopback *bind* is not proof the *caller* is local,
// because a remote origin can rebind its own DNS name to 127.0.0.1 and drive
// the listener through a victim's browser. So in that branch the request must
// also address loopback in its Host header and must not be a cross-site browser
// fetch. Neither check applies once a token is configured — a tokened admin
// server is legitimately reached under any hostname behind a reverse proxy, and
// the token itself is the caller proof.
//
// On an auth failure (wrong bind, wrong/missing credential, rebound Host,
// cross-site fetch) it records the rejection reason and responds:
//   - no token configured, non-loopback bind: 403 plain text naming both
//     remedies (set admin.auth.token, or bind admin.listen to loopback). No
//     WWW-Authenticate challenge — this is misconfiguration, not a missing
//     credential, and a 401 would make a browser prompt for a password that
//     does not exist.
//   - no token configured, loopback bind, non-loopback Host or cross-site
//     fetch: 403 plain text, likewise with no challenge.
//   - token configured but the caller's credential is wrong/absent: 401 with a
//     Basic-auth challenge, as before.
//
// The /healthz and /readyz probes are registered separately and never wrapped:
// cluster health checks legitimately send an arbitrary Host.
func (a *App) requireAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	token := a.cfg.Admin.Auth.Token.Reveal()
	if token == "" {
		if listenaddr.IsLoopback(a.cfg.Admin.Listen) {
			return a.requireLoopbackCaller(next)
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
		// The flow view is registered only when a store is actually being fed, so
		// a disabled view 404s rather than serving an empty result that reads as
		// "no traffic".
		if a.flowsEnabled() {
			mux.HandleFunc("/flows", a.requireAdminAuth(a.handleFlowsPage))
			mux.HandleFunc("/api/flows.json", a.requireAdminAuth(a.handleFlowsJSON))
		}
	}
	if a.cfg.Profiling.Pprof.Enabled {
		registerPprof(mux, a.requireAdminAuth)
	}
	return &http.Server{
		Addr: a.cfg.Admin.Listen,
		// Wrapped at the mux, not per route: a handler registered later cannot
		// then be born without the defensive headers (#322).
		Handler:           a.securityHeaders(mux),
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
		// Through the same path as a receiver stop, so a listener that fails to
		// bind reaches readiness rather than stopping at a log line and a
		// counter. /readyz answering 200 while the admin surface is dead is the
		// apparently-healthy process #306 is about.
		a.recordComponentStop(appcatalog.ComponentAdmin, err)
	}
}
