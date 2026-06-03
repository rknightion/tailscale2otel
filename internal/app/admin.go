package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// newAdminServer builds the optional always-on admin HTTP server exposing
// liveness (/healthz) and readiness (/readyz) endpoints. It carries no
// Tailscale data and is safe to expose to a cluster's health checks.
func newAdminServer(listen string) *http.Server {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}
	mux.HandleFunc("/healthz", ok)
	mux.HandleFunc("/readyz", ok)
	return &http.Server{
		Addr:              listen,
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
