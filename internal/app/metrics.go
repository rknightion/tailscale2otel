package app

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rknightion/tailscale2otel/v3/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v3/internal/certreload"
	"github.com/rknightion/tailscale2otel/v3/internal/listenaddr"
)

// buildMetricsServer builds the dedicated Prometheus pull-endpoint server. Only
// /metrics is served; it is bearer/Basic-gated when prometheus.auth.token is set.
// Separate from the admin server so pull works without the status page/pprof.
func (a *App) buildMetricsServer(g prometheus.Gatherer) *http.Server {
	mux := http.NewServeMux()
	// ContinueOnError (not the promhttp default HTTPErrorOnError): when
	// pii_filter.tailnet_name=false drops the tailscale.tailnet distinguisher, the
	// per-provider registries can produce byte-identical series (per-tailnet series
	// in multi mode; process+tailnet self-obs in single mode). The default turns
	// that Gather collision into a permanent HTTP 500 on every scrape; first-wins
	// keeps /metrics returning 200 instead of taking the whole pull path down (#103).
	mux.Handle("/metrics", a.requireMetricsAuth(promhttp.HandlerFor(g, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})))
	srv := &http.Server{
		Addr:              a.cfg.Prometheus.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Same GetCertificate-backed reload as the admin listener (#316); see
	// buildAdminServer's comment and CertReloader in tlsreload.go.
	if a.cfg.Prometheus.TLS.CertFile != "" && a.cfg.Prometheus.TLS.KeyFile != "" {
		reloader := certreload.New(a.cfg.Prometheus.TLS.CertFile, a.cfg.Prometheus.TLS.KeyFile,
			appcatalog.ComponentMetrics, a.logger, a.procEmitter)
		srv.TLSConfig = &tls.Config{GetCertificate: reloader.GetCertificate}
		a.metricsCerts = reloader
	}
	return srv
}

// requireMetricsAuth gates next behind prometheus.auth.token when set, reusing the
// constant-time Bearer/Basic check shared with the admin surface (adminAuthorized).
//
// With no token it fails CLOSED on a network-reachable bind, matching
// requireAdminAuth (#315). /metrics carries every series this exporter produces —
// device names, flow endpoints, audit identities — so a default wildcard listener
// with no credential disclosed the whole inventory to anything that could reach
// the port, guarded by nothing but a startup WARN.
//
// Two deliberate escape hatches, because a flat refusal would break real
// deployments:
//   - a loopback bind stays open, as on the admin surface: only this host can
//     reach it, which is what makes local development workable.
//   - prometheus.auth.allow_unauthenticated re-opens a network bind explicitly.
//     An in-cluster scrape behind a NetworkPolicy is legitimate and has
//     network-level control this process cannot observe; the point is that the
//     operator states it rather than inheriting it from a default.
//
// The acknowledgement covers only the no-token case. A configured token is
// enforced on every bind regardless.
//
// The refusal is 403, not 401, and carries no WWW-Authenticate: this is
// misconfiguration rather than a missing credential, and a challenge would make a
// browser prompt for a password that does not exist.
func (a *App) requireMetricsAuth(next http.Handler) http.Handler {
	token := a.cfg.Prometheus.Auth.Token.Reveal()
	if token == "" {
		if a.cfg.Prometheus.Auth.AllowUnauthenticated || listenaddr.IsLoopback(a.cfg.Prometheus.Listen) {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.logger.Warn("metrics request rejected: no prometheus.auth.token configured on a network-reachable bind",
				"path", r.URL.Path, "listen", a.cfg.Prometheus.Listen,
				"remedy", "set prometheus.auth.token, bind prometheus.listen to loopback, or set prometheus.auth.allow_unauthenticated")
			http.Error(w,
				"metrics access refused: /metrics exposes every series (device names, flow endpoints, "+
					"audit identities). Set prometheus.auth.token, bind prometheus.listen to loopback "+
					"(127.0.0.1), or acknowledge the exposure with prometheus.auth.allow_unauthenticated=true",
				http.StatusForbidden)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !adminAuthorized(r, token) {
			w.Header().Set("WWW-Authenticate", `Basic realm="tailscale2otel metrics"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// runMetrics serves the Prometheus endpoint until ctx is canceled, then shuts
// down gracefully. Mirrors runAdmin, including HTTPS when both prometheus.tls
// files are configured (Validate has already confirmed they exist and are
// readable); otherwise serves plain HTTP, byte-identical to before TLS support
// existed.
func (a *App) runMetrics(ctx context.Context) {
	tlsEnabled := a.cfg.Prometheus.TLS.CertFile != "" && a.cfg.Prometheus.TLS.KeyFile != ""
	errCh := make(chan error, 1)
	go func() {
		if tlsEnabled {
			// Empty paths: see runAdmin's identical comment (#316).
			errCh <- a.metricsSrv.ListenAndServeTLS("", "")
		} else {
			errCh <- a.metricsSrv.ListenAndServe()
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.metricsSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		// Same one readiness source as the admin server and the receivers (#306):
		// a Prometheus listener that never bound must not leave the process
		// reporting itself ready.
		a.recordComponentStop(appcatalog.ComponentMetrics, err)
	}
}
