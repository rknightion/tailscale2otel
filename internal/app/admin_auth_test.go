package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

const testAdminToken = "s3cret-admin-token"

// adminAuthApp builds an admin server whose status page (and, when pprof is on,
// the pprof handlers) is gated by testAdminToken.
func adminAuthApp(t *testing.T, withPprof bool) *http.Server {
	t.Helper()
	cfg := config.Default() // landing_page defaults true
	cfg.Admin.Auth.Token = testAdminToken
	cfg.Profiling.Pprof.Enabled = withPprof
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	return a.buildAdminServer()
}

// do issues req against srv and returns the recorded response.
func do(srv *http.Server, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	return w
}

func TestAdminAuth_StatusPageRequiresToken(t *testing.T) {
	srv := adminAuthApp(t, false)
	for _, path := range []string{"/", "/api/status.json"} {
		w := do(srv, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without creds = %d, want 401", path, w.Code)
		}
		if got := w.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic") {
			t.Errorf("GET %s WWW-Authenticate = %q, want a Basic challenge", path, got)
		}
	}
}

func TestAdminAuth_StatusPageAcceptsBasicAndBearer(t *testing.T) {
	srv := adminAuthApp(t, false)

	basic := httptest.NewRequest(http.MethodGet, "/", nil)
	basic.SetBasicAuth("admin", testAdminToken)
	if w := do(srv, basic); w.Code != http.StatusOK {
		t.Errorf("GET / with correct Basic auth = %d, want 200", w.Code)
	}

	bearer := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	bearer.Header.Set("Authorization", "Bearer "+testAdminToken)
	if w := do(srv, bearer); w.Code != http.StatusOK {
		t.Errorf("GET /api/status.json with correct Bearer token = %d, want 200", w.Code)
	}
}

func TestAdminAuth_StatusPageRejectsWrongToken(t *testing.T) {
	srv := adminAuthApp(t, false)

	wrongBasic := httptest.NewRequest(http.MethodGet, "/", nil)
	wrongBasic.SetBasicAuth("admin", "nope")
	if w := do(srv, wrongBasic); w.Code != http.StatusUnauthorized {
		t.Errorf("GET / with wrong Basic password = %d, want 401", w.Code)
	}

	wrongBearer := httptest.NewRequest(http.MethodGet, "/", nil)
	wrongBearer.Header.Set("Authorization", "Bearer nope")
	if w := do(srv, wrongBearer); w.Code != http.StatusUnauthorized {
		t.Errorf("GET / with wrong Bearer token = %d, want 401", w.Code)
	}
}

func TestAdminAuth_DefaultConfigNoTokenIsRejected(t *testing.T) {
	// Default config binds the wildcard :9091 with no token: this must now fail
	// closed (#227) rather than serve the status page to any network peer.
	cfg := config.Default()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()
	for _, path := range []string{"/", "/api/status.json"} {
		w := do(srv, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("GET %s on default config (no token, wildcard bind) = %d, want 403", path, w.Code)
		}
		// This is misconfiguration, not a missing credential: no Basic challenge,
		// so a browser does not prompt for a password that does not exist.
		if got := w.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("GET %s set WWW-Authenticate=%q, want none (403, not 401)", path, got)
		}
	}
}

func TestAdminAuth_ProbesStayOpenWhenAuthRequired(t *testing.T) {
	// Even when the status page/API is fail-closed for lack of a token, the
	// probes registered outside requireAdminAuth must stay open (never gated).
	// /healthz is unconditional; /readyz reflects real startup/receiver state
	// (#57) so a fresh test app may report unready, but never 401/403.
	cfg := config.Default()
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	w := do(srv, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Errorf("GET /healthz on default config = %d, want 200 (never gated)", w.Code)
	}

	w = do(srv, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
		t.Errorf("GET /readyz on default config = %d, want NOT 401/403 (probes are never auth-gated)", w.Code)
	}
}

// loopbackNoTokenServer builds the tokenless loopback-bind admin server — the
// deliberate no-credential escape hatch that the Host gate protects.
func loopbackNoTokenServer(t *testing.T) *http.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	return a.buildAdminServer()
}

// loopbackReq builds a request carrying a loopback Host, as a browser or curl
// pointed at the loopback listener would. Any admin-server test in this package
// that drives a tokenless loopback bind must use it (or set Host itself):
// requireAdminAuth rejects a non-loopback Host in that mode, and
// httptest.NewRequest defaults Host to "example.com".
func loopbackReq(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Host = "127.0.0.1:9091"
	return r
}

func TestAdminAuth_LoopbackBindNoTokenStaysOpen(t *testing.T) {
	// A loopback bind with no token is the deliberate escape hatch: only the
	// local host can reach it, so it stays usable without a credential — as long
	// as the request is genuinely addressed to loopback (see the Host gate).
	srv := loopbackNoTokenServer(t)
	if w := do(srv, loopbackReq(http.MethodGet, "/")); w.Code != http.StatusOK {
		t.Errorf("GET / on loopback bind with no token = %d, want 200 (opt-in escape hatch)", w.Code)
	}
}

func TestAdminAuth_TokenlessLoopbackAcceptsLoopbackHosts(t *testing.T) {
	// Every spelling of "this machine" a browser or client can put in Host must
	// keep the escape hatch usable.
	srv := loopbackNoTokenServer(t)
	for _, host := range []string{
		"127.0.0.1:9091", "127.0.0.1", "127.5.6.7:9091",
		"localhost:9091", "localhost", "LocalHost:9091", "localhost.:9091",
		"[::1]:9091", "[::1]", "::1",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		if w := do(srv, req); w.Code != http.StatusOK {
			t.Errorf("GET / with Host %q = %d, want 200 (loopback host)", host, w.Code)
		}
	}
}

func TestAdminAuth_TokenlessLoopbackRejectsForeignHost(t *testing.T) {
	// GHSA-gvm7-8848-7hcq: a remote origin can DNS-rebind its own name to
	// 127.0.0.1 and have the victim's browser read the status page, the flow
	// data or invoke the RDNS purge. The bind proves the *listener* is local; it
	// proves nothing about who the request was addressed to. Reject any Host
	// that is not a loopback literal (or localhost).
	srv := loopbackNoTokenServer(t)
	for _, host := range []string{
		"rebind.evil.example", "rebind.evil.example:9091",
		"example.com", "admin.local:9091",
		"100.64.0.1:9091", // a tailnet peer name/address is not loopback either
		"",
	} {
		for _, path := range []string{"/", "/api/status.json", "/api/config.json"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = host
			w := do(srv, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("GET %s with Host %q = %d, want 403 (DNS-rebinding guard)", path, host, w.Code)
			}
			// Misaddressed request, not a missing credential: no Basic challenge,
			// so a browser does not prompt for a password that does not exist.
			if got := w.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("GET %s Host %q set WWW-Authenticate=%q, want none", path, host, got)
			}
		}
	}
}

func TestAdminAuth_TokenlessLoopbackRejectsRDNSPurgeFromForeignHost(t *testing.T) {
	// The purge route is the one mutating endpoint on the admin server; it must
	// be behind the same Host gate as the read routes.
	srv := loopbackNoTokenServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/rdns/purge", nil)
	req.Host = "rebind.evil.example"
	if w := do(srv, req); w.Code != http.StatusForbidden {
		t.Errorf("POST /api/rdns/purge with a rebound Host = %d, want 403", w.Code)
	}
}

func TestAdminAuth_TokenlessLoopbackRejectsCrossSiteFetch(t *testing.T) {
	// Defense in depth: even with a correct loopback Host, a page on a remote
	// origin fetching http://127.0.0.1:9091/ is a cross-site read of local data.
	// A browser cannot forge fetch metadata, so Sec-Fetch-Site is trustworthy.
	srv := loopbackNoTokenServer(t)

	crossSite := loopbackReq(http.MethodGet, "/api/status.json")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSite.Header.Set("Origin", "http://evil.example")
	if w := do(srv, crossSite); w.Code != http.StatusForbidden {
		t.Errorf("cross-site GET /api/status.json = %d, want 403", w.Code)
	}

	// The page's own fetches and a typed-in navigation must still work.
	for _, site := range []string{"same-origin", "none"} {
		ok := loopbackReq(http.MethodGet, "/api/status.json")
		ok.Header.Set("Sec-Fetch-Site", site)
		if w := do(srv, ok); w.Code != http.StatusOK {
			t.Errorf("GET /api/status.json with Sec-Fetch-Site=%s = %d, want 200", site, w.Code)
		}
	}

	// A non-browser client (curl) sends no fetch metadata and no Origin: allowed.
	if w := do(srv, loopbackReq(http.MethodGet, "/api/status.json")); w.Code != http.StatusOK {
		t.Errorf("GET /api/status.json with no fetch metadata = %d, want 200", w.Code)
	}
}

func TestAdminAuth_TokenlessLoopbackProbesIgnoreHost(t *testing.T) {
	// Cluster health checks legitimately send an arbitrary Host (a Service DNS
	// name, a node IP, or none at all). The probes are registered outside the
	// gate and must answer regardless.
	srv := loopbackNoTokenServer(t)
	for _, host := range []string{"rebind.evil.example", "tailscale2otel.default.svc.cluster.local", ""} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Host = host
		w := do(srv, req)
		if w.Code != http.StatusOK || w.Body.String() != "ok" {
			t.Errorf("GET /healthz with Host %q = %d %q, want 200 ok (never gated)", host, w.Code, w.Body.String())
		}

		req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
		req.Host = host
		if w := do(srv, req); w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
			t.Errorf("GET /readyz with Host %q = %d, want NOT 401/403 (probes are never auth-gated)", host, w.Code)
		}
	}
}

func TestAdminAuth_ForeignHostRejectionEmitsMetric(t *testing.T) {
	rec := telemetrytest.New()
	cfg := config.Default() // self-observability defaults on
	cfg.Admin.Listen = "127.0.0.1:9091"
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)
	srv := a.buildAdminServer()

	rebound := httptest.NewRequest(http.MethodGet, "/", nil)
	rebound.Host = "rebind.evil.example"
	do(srv, rebound)

	crossSite := loopbackReq(http.MethodGet, "/")
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	do(srv, crossSite)

	pts := rec.MetricPoints("tailscale2otel.admin.auth.rejected")
	if len(pts) == 0 {
		t.Fatal("expected tailscale2otel.admin.auth.rejected on a rebinding/cross-site rejection")
	}
	var total float64
	reasons := map[string]bool{}
	for _, p := range pts {
		total += p.Value
		reasons[p.Attrs["reason"]] = true
	}
	if total != 2 {
		t.Errorf("rejected total = %v, want 2", total)
	}
	if !reasons[reasonUntrustedHost] || !reasons[reasonCrossSiteRequest] {
		t.Errorf("reasons = %v, want both %s and %s", reasons, reasonUntrustedHost, reasonCrossSiteRequest)
	}
}

func TestAdminAuth_TokenConfiguredIgnoresHost(t *testing.T) {
	// A tokened admin server is legitimately reachable behind a reverse proxy
	// under any hostname, and a token is not replayable by a rebinding page.
	// The Host gate must not apply there.
	srv := adminAuthApp(t, false)
	req := httptest.NewRequest(http.MethodGet, "/api/status.json", nil)
	req.Host = "admin.internal.example"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.SetBasicAuth("admin", testAdminToken)
	if w := do(srv, req); w.Code != http.StatusOK {
		t.Errorf("authenticated GET with a proxy Host = %d, want 200", w.Code)
	}
}

func TestLoopbackHostHeader(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:9091":  true,
		"127.0.0.1":       true,
		"127.0.0.53":      true,
		"localhost":       true,
		"localhost:9091":  true,
		"LOCALHOST:9091":  true,
		"localhost.":      true,
		"[::1]:9091":      true,
		"[::1]":           true,
		"::1":             true,
		"":                false,
		"example.com":     false,
		"example.com:80":  false,
		"0.0.0.0:9091":    false,
		"100.64.0.1:9091": false,
		"localhosts":      false,
		"notlocalhost":    false,
		"::1.example.com": false,
	}
	for host, want := range tests {
		if got := loopbackHostHeader(host); got != want {
			t.Errorf("loopbackHostHeader(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestAdminAuth_NoTokenRejectionEmitsMetric(t *testing.T) {
	rec := telemetrytest.New()
	cfg := config.Default() // self-observability defaults on
	// The bind is set explicitly: since #314 the DEFAULT is loopback, which takes
	// the tokenless-loopback path and rejects on Host rather than on auth. This
	// test is about the auth_required reason, so it needs the network bind.
	cfg.Admin.Listen = "0.0.0.0:9091"
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)
	srv := a.buildAdminServer()

	do(srv, httptest.NewRequest(http.MethodGet, "/", nil))

	pts := rec.MetricPoints("tailscale2otel.admin.auth.rejected")
	if len(pts) == 0 {
		t.Fatal("expected tailscale2otel.admin.auth.rejected to be emitted when no token is configured on a non-loopback bind")
	}
	var total float64
	for _, p := range pts {
		total += p.Value
		if p.Attrs["reason"] != "auth_required" {
			t.Errorf("reason = %q, want auth_required", p.Attrs["reason"])
		}
	}
	if total != 1 {
		t.Errorf("rejected total = %v, want 1", total)
	}
}

func TestAdminAuth_ProbesAlwaysOpen(t *testing.T) {
	// Health probes must never be gated, even when a token is configured.
	// /healthz is unconditional liveness; /readyz reflects real readiness
	// (#57), which is "starting" for a freshly built app whose collectors
	// haven't ticked yet — but crucially neither probe demands credentials.
	srv := adminAuthApp(t, false)

	w := do(srv, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Errorf("GET /healthz with a token set = %d %q, want 200 ok (never gated)", w.Code, w.Body.String())
	}

	w = do(srv, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code == http.StatusUnauthorized {
		t.Errorf("GET /readyz with a token set = %d, want NOT 401 (probes are never auth-gated)", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("GET /readyz set WWW-Authenticate=%q, want none (never auth-gated)", got)
	}
}

func TestAdminAuth_RejectionEmitsMetric(t *testing.T) {
	rec := telemetrytest.New()
	cfg := config.Default() // self-observability defaults on
	cfg.Admin.Auth.Token = testAdminToken
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", rec)
	srv := a.buildAdminServer()

	// One rejection with no credentials, one with a wrong password.
	do(srv, httptest.NewRequest(http.MethodGet, "/", nil))
	bad := httptest.NewRequest(http.MethodGet, "/", nil)
	bad.SetBasicAuth("admin", "nope")
	do(srv, bad)

	pts := rec.MetricPoints("tailscale2otel.admin.auth.rejected")
	if len(pts) == 0 {
		t.Fatal("expected tailscale2otel.admin.auth.rejected to be emitted on rejection")
	}
	var total float64
	reasons := map[string]bool{}
	for _, p := range pts {
		total += p.Value
		reasons[p.Attrs["reason"]] = true
	}
	if total != 2 {
		t.Errorf("rejected total = %v, want 2", total)
	}
	if !reasons["missing_credentials"] || !reasons["bad_credentials"] {
		t.Errorf("reasons = %v, want both missing_credentials and bad_credentials", reasons)
	}
}

func TestAdminAuth_PprofGatedByToken(t *testing.T) {
	srv := adminAuthApp(t, true)

	if w := do(srv, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)); w.Code != http.StatusUnauthorized {
		t.Errorf("GET /debug/pprof/ without creds = %d, want 401", w.Code)
	}

	authed := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	authed.SetBasicAuth("admin", testAdminToken)
	if w := do(srv, authed); w.Code != http.StatusOK {
		t.Errorf("GET /debug/pprof/ with correct creds = %d, want 200", w.Code)
	}
}

// TestAdminAuth_DefaultConfigServesTheLandingPage is the acceptance criterion of
// #314 stated as behavior rather than as a config value: with NOTHING configured,
// a local operator opening the status page gets the status page.
//
// It used to get 403. admin.enabled and admin.landing_page both default true, but
// admin.listen defaulted to the wildcard ":9091", and the page fails closed on a
// network-reachable bind with no token — so the exporter's headline UI did not
// work under the exporter's own defaults.
func TestAdminAuth_DefaultConfigServesTheLandingPage(t *testing.T) {
	a := baseTestApp(t, config.Default(), "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	for _, path := range []string{"/", "/api/status.json"} {
		w := do(srv, loopbackReq(http.MethodGet, path))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s with default config = %d, want 200. The landing page is enabled by "+
				"default, so a default install must serve it to a local caller without first "+
				"requiring a token.", path, w.Code)
		}
	}
}

// Every route that serves flow data must sit behind the admin gate. The export
// routes were added last (#299) and are the easiest to register bare, since
// their handlers are tested directly and would pass either way — an unguarded
// one hands the whole connection list, with node names, users and tags, to
// anyone who can reach the port.
func TestAdminAuth_FlowRoutesRequireToken(t *testing.T) {
	cfg := config.Default()
	cfg.Admin.Auth.Token = testAdminToken
	cfg.Flows.Enabled = true
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()

	for _, path := range []string{
		"/flows",
		"/api/flows.json",
		"/api/flows/export.csv",
		"/api/flows/export.json",
	} {
		w := do(srv, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code == http.StatusNotFound {
			t.Fatalf("GET %s = 404: the route is not registered, so this test proves nothing", path)
		}
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without creds = %d, want 401", path, w.Code)
		}
	}
}
