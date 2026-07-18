package app

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/rknightion/tailscale2otel/v2/internal/app/statushtml"
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

// handleCardinalityJSON serves just the cardinality section of the status
// snapshot as machine-readable JSON — the "export" affordance on the cardinality
// tab. Read-only, GET-only.
func (a *App) handleCardinalityJSON(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	writeIndentedJSON(w, a.buildStatus().Cardinality, a.logger, "encode cardinality json")
}

// handleConfigJSON serves the redacted configuration summary as JSON — the
// "download" affordance on the config tab. Secret VALUES never appear (see
// redactedConfigSummary). Read-only, GET-only.
func (a *App) handleConfigJSON(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="tailscale2otel-config.json"`)
	writeIndentedJSON(w, a.redactedConfigSummary(), a.logger, "encode config json")
}

// getOnly enforces GET (and HEAD) on a read-only endpoint, writing 405 otherwise.
func getOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// writeIndentedJSON encodes v as indented JSON, logging (never panicking) on a
// late encode error.
func writeIndentedJSON(w http.ResponseWriter, v any, logger interface {
	Error(string, ...any)
}, logMsg string) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		logger.Error(logMsg, "error", err)
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
