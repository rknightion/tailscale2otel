package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetrytest"
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

func TestAdminAuth_LoopbackBindNoTokenStaysOpen(t *testing.T) {
	// A loopback bind with no token is the deliberate escape hatch: only the
	// local host can reach it, so it stays usable without a credential.
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()
	if w := do(srv, httptest.NewRequest(http.MethodGet, "/", nil)); w.Code != http.StatusOK {
		t.Errorf("GET / on loopback bind with no token = %d, want 200 (opt-in escape hatch)", w.Code)
	}
}

func TestAdminAuth_NoTokenRejectionEmitsMetric(t *testing.T) {
	rec := telemetrytest.New()
	cfg := config.Default() // self-observability defaults on; wildcard bind, no token
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
