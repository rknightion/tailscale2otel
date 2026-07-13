package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rknightion/tailscale2otel/v2/internal/appcatalog"
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
	return &http.Server{
		Addr:              a.cfg.Prometheus.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// requireMetricsAuth gates next behind prometheus.auth.token when set, reusing the
// constant-time Bearer/Basic check shared with the admin surface (adminAuthorized).
// Empty token returns next unchanged (open; Warnings advises a token on a wildcard
// bind).
func (a *App) requireMetricsAuth(next http.Handler) http.Handler {
	token := a.cfg.Prometheus.Auth.Token.Reveal()
	if token == "" {
		return next
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
	certFile := a.cfg.Prometheus.TLS.CertFile
	keyFile := a.cfg.Prometheus.TLS.KeyFile
	errCh := make(chan error, 1)
	go func() {
		if certFile != "" && keyFile != "" {
			errCh <- a.metricsSrv.ListenAndServeTLS(certFile, keyFile)
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
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("metrics server stopped", "error", err)
			a.componentError(appcatalog.ComponentMetrics)
		}
	}
}
