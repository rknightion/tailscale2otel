package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v4/internal/certreload"
	"github.com/rknightion/tailscale2otel/v4/internal/httpguard"
	"github.com/rknightion/tailscale2otel/v4/internal/listenaddr"
	"github.com/rknightion/tailscale2otel/v4/internal/safefile"
	"github.com/rknightion/tailscale2otel/v4/internal/semconv"
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
	mux.HandleFunc("/debug/pprof/", wrap(pprofGuard(pprof.Index)))
	mux.HandleFunc("/debug/pprof/cmdline", wrap(pprofGuard(pprof.Cmdline)))
	mux.HandleFunc("/debug/pprof/profile", wrap(pprofGuard(pprof.Profile)))
	mux.HandleFunc("/debug/pprof/symbol", wrap(pprofGuard(pprof.Symbol)))
	mux.HandleFunc("/debug/pprof/trace", wrap(pprofGuard(pprof.Trace)))
}

const (
	maxPprofDurationSeconds = 60
	maxPprofSymbolBodyBytes = 64 << 10
)

// pprofGuard bounds the work accepted by the standard-library handlers. The
// stdlib deliberately extends write deadlines for requested profile durations,
// so the server timeout alone is not a duration bound.
func pprofGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !httpguard.SameOrigin(r) {
			http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
			return
		}
		isSymbol := r.URL.Path == "/debug/pprof/symbol"
		if r.Method != http.MethodGet && r.Method != http.MethodHead && (!isSymbol || r.Method != http.MethodPost) {
			w.Header().Set("Allow", "GET, HEAD")
			if isSymbol {
				w.Header().Set("Allow", "GET, HEAD, POST")
			}
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if values, ok := r.URL.Query()["seconds"]; ok {
			if len(values) != 1 {
				http.Error(w, "invalid pprof duration", http.StatusBadRequest)
				return
			}
			seconds, err := strconv.ParseFloat(values[0], 64)
			if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > maxPprofDurationSeconds {
				http.Error(w, "invalid pprof duration", http.StatusBadRequest)
				return
			}
		}
		if isSymbol && r.Method == http.MethodPost {
			if r.ContentLength > maxPprofSymbolBodyBytes {
				http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxPprofSymbolBodyBytes+1))
			if err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			if len(body) > maxPprofSymbolBodyBytes {
				http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
		}
		next(w, r)
	}
}

// adminAuthorized reports whether r presents the configured admin token, either
// as the HTTP Basic password or as an "Authorization: Bearer <token>" header.
// The comparison is constant-time. This mirrors stream.Server.authorized so the
// admin surface and the log-stream receiver verify shared secrets the same way.
func adminAuthorized(r *http.Request, token string) bool {
	if _, pass, ok := r.BasicAuth(); ok {
		return constantTimeTokenEqual(pass, token)
	}
	if fields := strings.Fields(r.Header.Get("Authorization")); len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return constantTimeTokenEqual(fields[1], token)
	}
	return false
}

// constantTimeTokenEqual compares fixed-size digests so a wrong-length
// credential follows the same ConstantTimeCompare path as an equal-length one.
// The caller already knows the candidate length; hashing removes the configured
// token length from the comparison primitive's early-return behavior.
func constantTimeTokenEqual(candidate, token string) bool {
	candidateHash := sha256.Sum256([]byte(candidate))
	tokenHash := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(candidateHash[:], tokenHash[:]) == 1
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
	// reasonThrottled marks a request refused during the bounded per-source
	// backoff after repeated missing or invalid credentials.
	reasonThrottled = "throttled"
)

const (
	maxAdminAuthSources = 1024
	adminAuthOverflow   = "overflow"
)

type adminAuthSourceState struct {
	windowStart  time.Time
	failures     int
	blockedUntil time.Time
}

// adminAuthLimiter is a fixed-cap, per-source failure tracker. Once its source
// budget is full, previously unseen sources share one overflow bucket. That
// fails closed under a source-address spray without allowing the map to grow or
// evicting an attacker's own active lockout.
type adminAuthLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	backoff time.Duration
	now     func() time.Time
	sources map[string]adminAuthSourceState
}

func newAdminAuthLimiter(limit int, window, backoff time.Duration, now func() time.Time) *adminAuthLimiter {
	if now == nil {
		now = time.Now
	}
	return &adminAuthLimiter{
		limit: limit, window: window, backoff: backoff, now: now,
		sources: make(map[string]adminAuthSourceState),
	}
}

func (l *adminAuthLimiter) keyLocked(source string, now time.Time) string {
	for key, state := range l.sources {
		if !state.blockedUntil.After(now) && now.Sub(state.windowStart) >= l.window {
			delete(l.sources, key)
		}
	}
	if _, ok := l.sources[source]; ok || len(l.sources) < maxAdminAuthSources-1 {
		return source
	}
	return adminAuthOverflow
}

func (l *adminAuthLimiter) allow(source string) (bool, time.Duration) {
	if l == nil || l.limit <= 0 {
		return true, 0
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	key := l.keyLocked(source, now)
	state, ok := l.sources[key]
	if !ok || !state.blockedUntil.After(now) {
		if ok && !state.blockedUntil.IsZero() {
			delete(l.sources, key)
		}
		return true, 0
	}
	return false, state.blockedUntil.Sub(now)
}

func (l *adminAuthLimiter) failure(source string) (bool, time.Duration) {
	if l == nil || l.limit <= 0 {
		return false, 0
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	key := l.keyLocked(source, now)
	state := l.sources[key]
	if state.blockedUntil.After(now) {
		return true, state.blockedUntil.Sub(now)
	}
	if state.windowStart.IsZero() || now.Sub(state.windowStart) >= l.window {
		state.windowStart = now
		state.failures = 0
	}
	state.failures++
	if state.failures >= l.limit {
		state.blockedUntil = now.Add(l.backoff)
	}
	l.sources[key] = state
	if state.blockedUntil.After(now) {
		return true, state.blockedUntil.Sub(now)
	}
	return false, 0
}

func (l *adminAuthLimiter) success(source string) {
	if l == nil || l.limit <= 0 {
		return
	}
	l.mu.Lock()
	key := l.keyLocked(source, l.now())
	if key != adminAuthOverflow {
		delete(l.sources, key)
	}
	l.mu.Unlock()
}

func (l *adminAuthLimiter) sourceCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sources)
}

func adminAuthSource(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return "unknown"
	}
	return addr.Unmap().String()
}

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
	return httpguard.IsLoopbackHost(host)
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
	return a.newAdminAuthGate()(next)
}

// newAdminAuthGate returns one route-wrapper factory with a shared bounded
// limiter. buildAdminServer creates it once so failures follow a source across
// every protected route instead of resetting at each handler boundary.
func (a *App) newAdminAuthGate() func(http.HandlerFunc) http.HandlerFunc {
	return a.newAdminAuthGateAt(time.Now)
}

func (a *App) newAdminAuthGateAt(now func() time.Time) func(http.HandlerFunc) http.HandlerFunc {
	token := a.cfg.Admin.Auth.Token.Reveal()
	if token == "" {
		if listenaddr.IsLoopback(a.cfg.Admin.Listen) {
			return a.requireLoopbackCaller
		}
		return func(_ http.HandlerFunc) http.HandlerFunc {
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
	}
	auth := a.cfg.Admin.Auth
	limiter := newAdminAuthLimiter(auth.FailureLimit, auth.FailureWindow.D(), auth.FailureBackoff.D(), now)
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			source := adminAuthSource(r.RemoteAddr)
			if allowed, retryAfter := limiter.allow(source); !allowed {
				a.rejectThrottledAdminAuth(w, r, retryAfter)
				return
			}
			if !adminAuthorized(r, token) {
				reason := reasonBadCredentials
				if r.Header.Get("Authorization") == "" {
					reason = reasonMissingCredentials
				}
				if blocked, retryAfter := limiter.failure(source); blocked {
					a.rejectThrottledAdminAuth(w, r, retryAfter)
					return
				}
				a.adminAuthRejected(reason)
				a.logger.Warn("admin request rejected", "reason", reason, "path", r.URL.Path)
				w.Header().Set("WWW-Authenticate", `Basic realm="tailscale2otel admin"`)
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			limiter.success(source)
			next(w, r)
		}
	}
}

func (a *App) rejectThrottledAdminAuth(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	seconds := max(1, int64(math.Ceil(retryAfter.Seconds())))
	a.adminAuthRejected(reasonThrottled)
	a.logger.Warn("admin request throttled after repeated authentication failures",
		"reason", reasonThrottled, "path", r.URL.Path, "retry_after_seconds", seconds)
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
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
	auth := a.newAdminAuthGate()
	registerProbes(mux, a.readyz)
	if a.cfg.Admin.LandingPage {
		mux.HandleFunc("/", auth(a.handleIndex))
		mux.HandleFunc("/api/status.json", auth(a.handleStatusJSON))
		mux.HandleFunc("/api/cardinality.json", auth(a.handleCardinalityJSON))
		mux.HandleFunc("/api/config.json", auth(a.handleConfigJSON))
		mux.HandleFunc("/api/rdns/purge", auth(a.handleRDNSPurge))
		// Always available with the landing page, like /api/config.json: the
		// bundle is the thing an operator reaches for when something is wrong,
		// so gating it behind another switch would hide it exactly then.
		mux.HandleFunc("/api/support-bundle.zip", auth(a.handleSupportBundle))
		// The flow view is registered only when a store is actually being fed, so
		// a disabled view 404s rather than serving an empty result that reads as
		// "no traffic".
		if a.flowsEnabled() {
			mux.HandleFunc("/flows", auth(a.handleFlowsPage))
			mux.HandleFunc("/api/flows.json", auth(a.handleFlowsJSON))
			mux.HandleFunc("/api/flows/export.csv", auth(a.handleFlowsExportCSV))
			mux.HandleFunc("/api/flows/export.json", auth(a.handleFlowsExportJSON))
		}
		// Same rule as the flow view above: registered only when a store is being
		// fed, so a disabled explorer 404s rather than serving an empty timeline
		// that reads as "nothing has happened".
		if a.eventsEnabled() {
			mux.HandleFunc("/events", auth(a.handleEventsPage))
			mux.HandleFunc("/api/events.json", auth(a.handleEventsJSON))
		}
	}
	if a.cfg.Profiling.Pprof.Enabled {
		registerPprof(mux, auth)
	}
	srv := &http.Server{
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
	// GetCertificate (not Certificates/certFile+keyFile) so a rotated file is
	// picked up without restarting the listener (#316) — see CertReloader in
	// tlsreload.go. runAdmin then calls ListenAndServeTLS("", ""): passing the
	// paths there too would make the stdlib do its OWN one-shot load on top of
	// this, defeating the whole point.
	if a.cfg.Admin.TLS.CertFile != "" && a.cfg.Admin.TLS.KeyFile != "" {
		reloader := certreload.New(a.cfg.Admin.TLS.CertFile, a.cfg.Admin.TLS.KeyFile,
			appcatalog.ComponentAdmin, a.logger, a.procEmitter)
		srv.TLSConfig = &tls.Config{GetCertificate: reloader.GetCertificate}
		a.applyAdminClientAuth(srv.TLSConfig)
		a.adminCerts = reloader
	}
	return srv
}

// applyAdminClientAuth gives the admin listener the same client-CA semantics as
// the Prometheus listener. The CA is loaded once at listener construction; the
// presented server certificate continues to use the independent hot reloader.
func (a *App) applyAdminClientAuth(tlsCfg *tls.Config) {
	caFile := a.cfg.Admin.TLS.ClientCAFile
	if caFile == "" {
		return
	}
	pool := x509.NewCertPool()
	pemBytes, err := safefile.ReadRegular(caFile, safefile.MaxPEMBytes, safefile.AllowSymlink)
	switch {
	case err != nil:
		a.logger.With(semconv.AttrComponent, appcatalog.ComponentAdmin).
			Error("admin client CA bundle could not be read; the listener will reject every client",
				"client_ca_file", caFile, "error", err)
		a.componentError(appcatalog.ComponentAdmin)
		tlsCfg.ClientCAs, tlsCfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
		return
	case !pool.AppendCertsFromPEM(pemBytes):
		a.logger.With(semconv.AttrComponent, appcatalog.ComponentAdmin).
			Error("admin client CA bundle contains no parseable certificate; the listener will reject every client",
				"client_ca_file", caFile)
		a.componentError(appcatalog.ComponentAdmin)
		tlsCfg.ClientCAs, tlsCfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
		return
	}
	tlsCfg.ClientCAs = pool
	tlsCfg.ClientAuth = adminClientAuthType(a.cfg.Admin.TLS.ClientAuth, caFile)
}

func adminClientAuthType(mode, clientCAFile string) tls.ClientAuthType {
	return metricsClientAuthType(mode, clientCAFile)
}

// runAdmin serves the admin endpoints until ctx is canceled, then shuts down
// gracefully. Errors other than the expected close are logged. Serves HTTPS
// when both admin.tls files are configured (Validate has already confirmed
// they exist and are readable); otherwise serves plain HTTP, byte-identical to
// before TLS support existed.
func (a *App) runAdmin(ctx context.Context) {
	tlsEnabled := a.cfg.Admin.TLS.CertFile != "" && a.cfg.Admin.TLS.KeyFile != ""
	errCh := make(chan error, 1)
	go func() {
		if tlsEnabled {
			// Empty paths: the certificate comes from TLSConfig.GetCertificate
			// (a CertReloader, wired in buildAdminServer), not a one-shot file
			// load by ListenAndServeTLS itself (#316).
			errCh <- a.adminSrv.ListenAndServeTLS("", "")
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
