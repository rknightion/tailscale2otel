package app

import (
	"encoding/json"
	"net/http"
	"net/url"

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

// handleRDNSPurge clears the reverse-DNS cache. It is the admin server's only
// mutating endpoint, so it is POST-only and same-origin-guarded on top of the
// shared admin auth gate (see requireAdminAuth). The response reports how many
// entries were cleared and whether the cache is enabled at all.
func (a *App) handleRDNSPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		a.logger.Warn("rdns purge rejected: cross-origin request", "origin", r.Header.Get("Origin"))
		http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
		return
	}
	resp := struct {
		Purged  int  `json:"purged"`
		Enabled bool `json:"enabled"`
	}{}
	if a.rdnsCache != nil {
		resp.Enabled = true
		resp.Purged = a.rdnsCache.Purge()
		a.logger.Info("reverse-dns cache purged", "entries", resp.Purged)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		a.logger.Error("encode rdns purge response", "error", err)
	}
}

// sameOrigin reports whether a state-changing request originates from the admin
// page itself, mitigating cross-site request forgery. Modern browsers send
// Sec-Fetch-Site; when absent it falls back to comparing the Origin host with
// the request Host. A missing Origin (non-browser clients such as curl) is
// allowed — the admin auth gate is the primary control for those.
func sameOrigin(r *http.Request) bool {
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" {
		return s == "same-origin" || s == "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}
