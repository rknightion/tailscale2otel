package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The admin surface served an operational inventory — device names, addresses,
// the user each device belongs to — plus a mutating rDNS purge, with no CSP, no
// frame policy, no nosniff and no cache policy on any response (#322).

func headerApp(t *testing.T) *App {
	t.Helper()
	a := verdictApp(t)
	a.cfg.Admin.Enabled = true
	a.cfg.Admin.LandingPage = true
	a.cfg.Admin.Listen = "127.0.0.1:9091"
	return a
}

func adminHeaders(t *testing.T, a *App, path string) http.Header {
	t.Helper()
	h := a.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9091"+path, nil)
	req.Host = "127.0.0.1:9091"
	h.ServeHTTP(rec, req)
	return rec.Result().Header
}

func TestAdminResponsesCarryDefensiveHeaders(t *testing.T) {
	a := headerApp(t)
	for _, path := range []string{"/", "/api/status.json", "/api/flows.json", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			h := adminHeaders(t, a, path)
			if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if got := h.Get("Referrer-Policy"); got != "no-referrer" {
				t.Errorf("Referrer-Policy = %q, want no-referrer", got)
			}
			csp := h.Get("Content-Security-Policy")
			if csp == "" {
				t.Fatal("no Content-Security-Policy")
			}
			if !strings.Contains(csp, "frame-ancestors 'none'") {
				t.Errorf("CSP = %q, want frame-ancestors 'none': the page carries a mutating "+
					"rDNS purge and must not be framable", csp)
			}
			if !strings.Contains(csp, "default-src 'none'") {
				t.Errorf("CSP = %q, want default-src 'none'", csp)
			}
		})
	}
}

// The page is documented as entirely self-contained so it renders on an
// air-gapped tailnet. Nothing enforced that; a CSP does.
func TestAdminCSPForbidsEveryExternalOrigin(t *testing.T) {
	csp := adminHeaders(t, headerApp(t), "/").Get("Content-Security-Policy")
	if !strings.Contains(csp, "font-src 'self'") {
		t.Errorf("CSP = %q, want font-src 'self' for the embedded console fonts", csp)
	}
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if directive == "" {
			continue
		}
		for _, tok := range strings.Fields(directive)[1:] {
			switch tok {
			case "'none'", "'self'", "'unsafe-inline'", "data:":
				continue
			}
			t.Errorf("CSP directive %q allows source %q — the admin UI loads no remote asset and "+
				"an allowed origin is also an exfiltration destination", directive, tok)
		}
	}
}

// A shared intermediary caching /api/status.json would serve one operator's
// device inventory to the next request that reaches it.
func TestAdminResponsesAreNotCacheable(t *testing.T) {
	h := adminHeaders(t, headerApp(t), "/api/status.json")
	cc := h.Get("Cache-Control")
	if !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// HSTS on a plaintext listener is at best inert and at worst a foot-gun: a
// browser that sees it once refuses http:// to that host:port afterwards, which
// on a loopback admin server locks the operator out of their own page.
func TestHSTSOnlyWhenTLSIsConfigured(t *testing.T) {
	plain := headerApp(t)
	if got := adminHeaders(t, plain, "/").Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q on a plaintext listener, want none", got)
	}

	tlsApp := headerApp(t)
	tlsApp.cfg.Admin.TLS.CertFile = "/tmp/cert.pem"
	tlsApp.cfg.Admin.TLS.KeyFile = "/tmp/key.pem"
	if got := adminHeaders(t, tlsApp, "/").Get("Strict-Transport-Security"); got == "" {
		t.Error("no Strict-Transport-Security with admin TLS configured")
	}
}

// The middleware has to be on the real server, not merely available: wrapping
// the mux is the whole point, and a handler registered outside it would be bare.
func TestBuiltAdminServerAppliesTheHeaders(t *testing.T) {
	a := headerApp(t)
	srv := a.buildAdminServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9091/healthz", nil)
	req.Host = "127.0.0.1:9091"
	srv.Handler.ServeHTTP(rec, req)
	if got := rec.Result().Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("the built admin server serves /healthz with X-Content-Type-Options = %q; "+
			"the middleware is not wrapping the mux", got)
	}
}
