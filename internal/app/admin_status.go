package app

import (
	"encoding/json"
	"net/http"

	"github.com/rknightion/tailscale2otel/internal/app/statushtml"
)

// handleIndex renders the HTML admin status/landing page. Because "/" is the
// ServeMux catch-all, any unknown path that falls through to here returns 404
// rather than the page.
func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statushtml.Render(w, a.buildStatus()); err != nil {
		// The status code/headers may already be committed; just log.
		a.logger.Error("render status page", "error", err)
	}
}

// handleStatusJSON serves the same status snapshot as machine-readable JSON.
func (a *App) handleStatusJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a.buildStatus()); err != nil {
		a.logger.Error("encode status json", "error", err)
	}
}
