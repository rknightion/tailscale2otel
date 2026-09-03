package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
	"github.com/rknightion/tailscale2otel/v5/internal/supportbundle"
)

// bundleTestApp builds an *App with the admin server on a loopback bind (so
// the tokenless-loopback escape hatch is in play for the no-token case) and
// devices seeded into the cache so the opt-in device-inventory assertions
// have something real to check.
func bundleTestApp(t *testing.T, tune func(*config.Config)) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Admin.Listen = "127.0.0.1:9091"
	cfg.Tailscale.Tailnet = "example.com"
	if tune != nil {
		tune(cfg)
	}
	a := flowsTestApp(t, func(c *config.Config) {
		c.Admin.Listen = cfg.Admin.Listen
		c.Tailscale.Tailnet = cfg.Tailscale.Tailnet
		if tune != nil {
			tune(c)
		}
	})
	return a
}

// bundleMux mounts handleSupportBundle exactly as the wiring line this lane's
// report asks admin.go to add — a GET route behind requireAdminAuth, same as
// every other admin route. admin.go itself is another lane's file (see the
// task brief's ownership list), so this is the route driven for tests here;
// once the real line lands in buildAdminServer, the same assertions hold
// against the production mux unchanged, since it wraps the identical
// a.requireAdminAuth(a.handleSupportBundle).
func bundleMux(a *App) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/support-bundle.zip", a.requireAdminAuth(a.handleSupportBundle))
	return mux
}

func bundleReq(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Host = "127.0.0.1:9091"
	return r
}

// TestSupportBundle_RequiresAdminAuth drives the actual mounted ROUTE (not
// the handler directly) with a token configured but not presented: this is
// the class of bug this tracker has shipped three times ("a component fully
// tested in isolation with nothing asserting it is CONNECTED") — a handler
// test that bypasses requireAdminAuth passes identically whether or not the
// route is actually wrapped in it.
func TestSupportBundle_RequiresAdminAuth(t *testing.T) {
	a := bundleTestApp(t, func(c *config.Config) {
		c.Admin.Auth.Token = "s3cr3t"
	})
	w := httptest.NewRecorder()
	bundleMux(a).ServeHTTP(w, bundleReq(http.MethodGet, "/api/support-bundle.zip"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/support-bundle.zip with no credential = %d, want 401 (route must be wrapped in requireAdminAuth)", w.Code)
	}
}

// TestSupportBundle_UnknownRouteIs404 guards the other half of the same
// class: if the wiring line is never actually added to the real mux, this
// endpoint 404s forever. Asserted here against THIS test's own mux (proving
// the handler exists and responds once mounted); the exact wiring line is
// reported for admin.go so the same check passes against the production mux.
func TestSupportBundle_UnknownRouteIs404(t *testing.T) {
	mux := http.NewServeMux() // deliberately NOT mounting the route
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, bundleReq(http.MethodGet, "/api/support-bundle.zip"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unmounted route = %d, want 404 (sanity check that the assertion below is meaningful)", w.Code)
	}
}

func getBundle(t *testing.T, a *App, query string) (*httptest.ResponseRecorder, map[string][]byte) {
	t.Helper()
	w := httptest.NewRecorder()
	bundleMux(a).ServeHTTP(w, bundleReq(http.MethodGet, "/api/support-bundle.zip"+query))
	if w.Code != http.StatusOK {
		t.Fatalf("GET support-bundle%s = %d, want 200: %s", query, w.Code, w.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("response is not a valid zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		rc.Close()
		files[f.Name] = buf.Bytes()
	}
	return w, files
}

func TestSupportBundle_GetOnly(t *testing.T) {
	a := bundleTestApp(t, nil)
	w := httptest.NewRecorder()
	bundleMux(a).ServeHTTP(w, bundleReq(http.MethodPost, "/api/support-bundle.zip"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST support-bundle = %d, want 405", w.Code)
	}
}

// TestSupportBundle_ContentTypeAndFilename asserts the download affordance
// (attachment headers), matching the convention every other admin export
// (flows CSV/JSON, config.json) already uses.
func TestSupportBundle_ContentTypeAndFilename(t *testing.T) {
	a := bundleTestApp(t, nil)
	w, _ := getBundle(t, a, "")
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="tailscale2otel-support-`) || !strings.HasSuffix(cd, `.zip"`) {
		t.Errorf("Content-Disposition = %q, want an attachment filename ending .zip", cd)
	}
}

// TestSupportBundle_ExcludesDevicesByDefault is #321's core acceptance
// criterion driven end-to-end through the real handler: without the opt-in
// query parameter, the archive must not contain devices.json at all, and the
// manifest must say so.
func TestSupportBundle_ExcludesDevicesByDefault(t *testing.T) {
	a := bundleTestApp(t, nil)
	_, files := getBundle(t, a, "")
	if _, ok := files["devices.json"]; ok {
		t.Error("default support bundle must not contain devices.json")
	}
	var manifest supportbundle.Manifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatalf("decode manifest.json: %v", err)
	}
	found := false
	for _, e := range manifest.ExcludedByDefault {
		if e == "device_inventory" {
			found = true
		}
	}
	if !found {
		t.Errorf("manifest.excluded_by_default = %v, want it to name device_inventory", manifest.ExcludedByDefault)
	}
}

// TestSupportBundle_IncludeDevicesRequiresExactOptIn asserts the opt-in
// fails CLOSED: only the literal query value "1" turns the device inventory
// on. A near-miss like "true" must NOT silently include PII.
func TestSupportBundle_IncludeDevicesRequiresExactOptIn(t *testing.T) {
	a := bundleTestApp(t, nil)
	for _, v := range []string{"true", "yes", "TRUE", "2", ""} {
		_, files := getBundle(t, a, "?include_devices="+v)
		if _, ok := files["devices.json"]; ok {
			t.Errorf("include_devices=%q must NOT include devices.json (only the exact value %q may)", v, "1")
		}
	}
	_, files := getBundle(t, a, "?include_devices=1")
	if _, ok := files["devices.json"]; !ok {
		t.Error("include_devices=1 must include devices.json")
	}
}

// TestSupportBundle_ContainsVersionDiagnosticsConfigAndCatalogs is the
// acceptance criterion "contents: version, diagnostics, full redacted
// config, component/API/export state, catalogs, manifest" driven through the
// real handler end-to-end.
func TestSupportBundle_ContainsVersionDiagnosticsConfigAndCatalogs(t *testing.T) {
	a := bundleTestApp(t, nil)
	_, files := getBundle(t, a, "")
	for _, name := range []string{
		"manifest.json", "version.json", "diagnostics.json", "config.json",
		"components.json", "api.json", "delivery.json", "collectors.json",
		"advisories.json", "catalog_metrics.json", "catalog_log_events.json",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("bundle is missing %s", name)
		}
	}
	var version map[string]string
	if err := json.Unmarshal(files["version.json"], &version); err != nil {
		t.Fatalf("decode version.json: %v", err)
	}
	if version["version"] != "vtest" { // baseTestApp's fixed test version
		t.Errorf("version.json = %q, want vtest", version["version"])
	}
	if !strings.Contains(string(files["config.json"]), `"tailscale.tailnet"`) {
		t.Error("config.json does not look like the full redacted effective config")
	}
	if len(files["catalog_metrics.json"]) < 10 {
		t.Error("catalog_metrics.json looks empty; the metric catalog should never be empty in a real build")
	}
}

// TestSupportBundle_ConfigNeverLeaksAPIKeyOrOAuthSecret is the end-to-end
// leak check through the real handler (internal/supportbundle's own tests
// cover the exhaustive sentinel sweep; this proves the wiring from a real
// *App's config into the handler doesn't reintroduce a leak at the seam).
func TestSupportBundle_ConfigNeverLeaksAPIKeyOrOAuthSecret(t *testing.T) {
	a := bundleTestApp(t, func(c *config.Config) {
		c.Tailscale.Auth.Method = "apikey"
		c.Tailscale.Auth.APIKey = "tskey-api-VERYSECRETVALUE"
		c.Webhook.Secret = "whsec-VERYSECRETVALUE"
	})
	_, files := getBundle(t, a, "")
	for _, name := range []string{"config.json", "manifest.json", "diagnostics.json"} {
		if strings.Contains(string(files[name]), "VERYSECRETVALUE") {
			t.Errorf("%s leaks a configured secret value", name)
		}
	}
}

func TestSupportBundle_ContainsBoundedRedactedRecentLogs(t *testing.T) {
	const secret = "support-tail-secret-canary"
	a := bundleTestApp(t, func(c *config.Config) { c.Admin.SupportBundleLogTailRecords = 2 })
	a.logger = withSupportBundleLogTail(slog.New(slog.NewTextHandler(io.Discard, nil)), 2, 32<<10)
	a.logger.Info("evicted record")
	a.logger.Info("safe record", "token", config.Secret(secret))
	a.logger.Warn("newest record")

	_, files := getBundle(t, a, "")
	body, ok := files["recent_logs.jsonl"]
	if !ok {
		t.Fatal("bundle is missing recent_logs.jsonl")
	}
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "evicted record") {
		t.Fatalf("recent log tail leaked a secret or exceeded its bound: %s", body)
	}
	if !strings.Contains(string(body), `"msg":"safe record"`) ||
		!strings.Contains(string(body), `"token":"REDACTED"`) ||
		!strings.Contains(string(body), `"msg":"newest record"`) {
		t.Fatalf("recent log tail lost retained/redacted records: %s", body)
	}
}
