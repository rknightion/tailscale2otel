package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rknightion/tailscale2otel/internal/appcatalog"
)

// buildMetricsServer builds the dedicated Prometheus pull-endpoint server. Only
// /metrics is served; it is bearer/Basic-gated when prometheus.auth.token is set.
// Separate from the admin server so pull works without the status page/pprof.
func (a *App) buildMetricsServer(g prometheus.Gatherer) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", a.requireMetricsAuth(promhttp.HandlerFor(g, promhttp.HandlerOpts{})))
	return &http.Server{
		Addr:              a.cfg.Prometheus.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
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

// runMetrics serves the Prometheus endpoint until ctx is canceled, then shuts down
// gracefully. Mirrors runAdmin.
func (a *App) runMetrics(ctx context.Context) {
	errCh := make(chan error, 1)
	go func() { errCh <- a.metricsSrv.ListenAndServe() }()
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
