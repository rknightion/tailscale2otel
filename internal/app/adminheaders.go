package app

import "net/http"

// adminCSP is the Content-Security-Policy for every admin response.
//
// `default-src 'none'` with no origin allowed anywhere is the load-bearing part.
// Both embedded UIs are documented as entirely self-contained so they render on
// an air-gapped tailnet, and nothing enforced that — a CSP does, in both
// directions: no remote asset can be loaded, and no remote endpoint can be
// reached, so the page cannot become an exfiltration channel for the inventory
// it displays.
//
// `frame-ancestors 'none'` matters because the page carries a mutating rDNS
// purge behind the same-origin checks in requireLoopbackCaller; framing it is
// the classic way to drive a control the operator cannot see.
//
// `'unsafe-inline'` for scripts and styles is deliberate and is the one real
// concession. Both pages are a single inline <script> and <style> by design, and
// the page uses inline event handlers, which a nonce does NOT cover — nonces
// would require rewriting every handler and would still leave 'unsafe-inline'
// as the fallback for older browsers. The XSS control here is html/template's
// contextual escaping, which is what actually stands between tailnet data and
// the DOM; this policy's job is the perimeter.
//
// `img-src data:` covers the inline SVG sparklines and favicon; `connect-src
// 'self'` is the status/flows polling; `base-uri` and `form-action` are 'none'
// because neither page has a form or a <base>.
const adminCSP = "default-src 'none'; " +
	"script-src 'unsafe-inline'; " +
	"style-src 'unsafe-inline'; " +
	"img-src data:; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// securityHeaders wraps an admin handler with the response headers every admin
// response should carry (#322). It is applied to the whole mux rather than to
// individual routes, so a handler added later cannot be born bare — that is the
// failure mode the middleware exists to prevent.
//
// Cache-Control is no-store on everything, probes included. The JSON endpoints
// carry a device inventory an intermediary must not hold, and the probes are
// worthless cached. It costs nothing: every response is generated per request
// from live state anyway.
//
// HSTS is emitted ONLY when the admin listener is actually serving TLS. On a
// plaintext listener it is at best inert, and at worst a foot-gun: a browser
// that sees it once refuses plain http:// to that host and port for its max-age,
// which on a loopback admin server locks an operator out of their own page with
// no obvious cause.
func (a *App) securityHeaders(next http.Handler) http.Handler {
	tlsEnabled := a.cfg.Admin.TLS.CertFile != "" && a.cfg.Admin.TLS.KeyFile != ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", adminCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		// no-referrer, not a same-origin policy: the admin host and port are
		// themselves worth not leaking to anywhere a link might lead.
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if tlsEnabled {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
