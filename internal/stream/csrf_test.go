package stream_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/flowlog"
	"github.com/rknightion/tailscale2otel/v5/internal/stream"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// reasonCrossSite is the rejection reason added by GHSA-cvp7-f3mx-m68x. Declared
// here (the hardening reasons live next to the tests that drive them) and
// mirrored by the unexported constant of the same name inside the package.
const reasonCrossSite = "cross_site"

// postRaw drives the handler with a fully-controlled request: caller sets Host
// and headers verbatim, with no helper fixups. The CSRF gate keys on exactly
// those fields, so the shared `post` helper (which normalizes Host to a loopback
// value) would hide what is under test.
func postRaw(t *testing.T, h http.Handler, host string, header http.Header, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/collector/event", strings.NewReader(body))
	req.Host = host
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// assertCrossSiteRefused asserts the request was refused by the CSRF gate with
// nothing ingested.
func assertCrossSiteRefused(t *testing.T, w *httptest.ResponseRecorder, rec *telemetrytest.Recorder) {
	t.Helper()
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRejected), map[string]string{attrReason: reasonCrossSite}); p.Value != 1 {
		t.Fatalf("%s{reason=cross_site} = %v, want 1", metricRejected, p.Value)
	}
	if pts := rec.MetricPoints(metricRecords); len(pts) != 0 {
		t.Fatalf("records emitted despite the cross_site refusal: %+v", pts)
	}
	if pts := rec.MetricPoints(flowlog.MetricIO); len(pts) != 0 {
		t.Fatalf("flow metrics emitted despite the cross_site refusal: %+v", pts)
	}
}

// TestCrossSite_BrowserOriginatedRequestsRefused is the GHSA-cvp7-f3mx-m68x
// control. The supported tokenless loopback mode authorizes on reachability
// alone, so a remote web page could use a victim's browser as a confused deputy
// and write forged flow/audit records — it never needs to read the response, so
// the same-origin policy does not help. Every marker a browser attaches to such a
// request must refuse it.
func TestCrossSite_BrowserOriginatedRequestsRefused(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		header http.Header
	}{
		// A cross-origin fetch/XHR/form POST always carries Origin.
		{"origin", "127.0.0.1:9099", hdr("Origin", "https://evil.example")},
		// Even a same-site-looking Origin is refused: nothing legitimate sends one.
		{"origin-loopback", "127.0.0.1:9099", hdr("Origin", "http://127.0.0.1:9099")},
		// Fetch metadata, sent by every current browser.
		{"sec-fetch-site-cross", "127.0.0.1:9099", hdr("Sec-Fetch-Site", "cross-site")},
		{"sec-fetch-site-same-site", "127.0.0.1:9099", hdr("Sec-Fetch-Site", "same-site")},
		{"sec-fetch-mode-navigate", "127.0.0.1:9099", hdr("Sec-Fetch-Mode", "navigate")},
		{"sec-fetch-mode-no-cors", "127.0.0.1:9099", hdr("Sec-Fetch-Mode", "no-cors")},
		{"sec-fetch-dest-iframe", "127.0.0.1:9099", hdr("Sec-Fetch-Dest", "iframe")},
		// The three CORS-safelisted media types are exactly the ones a cross-origin
		// request can set with NO preflight, so they are the CSRF-reachable set.
		{"ct-text-plain", "127.0.0.1:9099", hdr("Content-Type", "text/plain;charset=UTF-8")},
		{"ct-form-urlencoded", "127.0.0.1:9099", hdr("Content-Type", "application/x-www-form-urlencoded")},
		{"ct-multipart", "127.0.0.1:9099", hdr("Content-Type", "multipart/form-data; boundary=x")},
		// DNS rebinding: evil.example resolves to 127.0.0.1, so the request really
		// does reach the loopback listener, but the browser sends the attacker's
		// name in Host.
		{"rebound-host", "evil.example", nil},
		{"rebound-host-port", "evil.example:9099", nil},
		{"empty-host", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := newServer(t, stream.Options{Listen: loopbackListen})
			w := postRaw(t, s.Handler(), tc.host, tc.header, captureFlowRecord)
			assertCrossSiteRefused(t, w, rec)
		})
	}
}

// TestCrossSite_RefusalNamesTheRemedy keeps the 403 actionable: an operator
// hitting it must be able to fix it without reading source.
func TestCrossSite_RefusalNamesTheRemedy(t *testing.T) {
	s, _ := newServer(t, stream.Options{Listen: loopbackListen})
	w := postRaw(t, s.Handler(), "evil.example", nil, captureFlowRecord)
	if !strings.Contains(w.Body.String(), "streaming.token") {
		t.Fatalf("body = %q, want it to name streaming.token", w.Body.String())
	}
}

// TestCrossSite_LocalNonBrowserClientStillServes keeps the compatibility
// requirement honest: the tokenless loopback mode itself is NOT removed. A
// local, non-browser sender — no Origin, no fetch metadata, a loopback Host —
// still ingests normally.
func TestCrossSite_LocalNonBrowserClientStillServes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		host   string
		header http.Header
	}{
		{"bare-loopback", "127.0.0.1:9099", nil},
		{"localhost", "localhost:9099", nil},
		{"ipv6-loopback", "[::1]:9099", nil},
		{"host-without-port", "127.0.0.1", nil},
		{"json-content-type", "127.0.0.1:9099", hdr("Content-Type", "application/json")},
		// A HEC sender that happens to run in the same page context would set
		// same-origin fetch metadata; that is not cross-site, so it is allowed.
		{"same-origin-fetch-metadata", "127.0.0.1:9099", hdr("Sec-Fetch-Site", "same-origin", "Sec-Fetch-Mode", "cors", "Sec-Fetch-Dest", "empty")},
		{"sec-fetch-site-none", "127.0.0.1:9099", hdr("Sec-Fetch-Site", "none")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := newServer(t, stream.Options{Listen: loopbackListen})
			w := postRaw(t, s.Handler(), tc.host, tc.header, captureFlowRecord)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%q", w.Code, w.Body.String())
			}
			if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow}); p.Value != 1 {
				t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
			}
		})
	}
}

// TestCrossSite_GateAppliesOnlyWithoutAToken pins the scope of the gate. A
// configured token is an unguessable secret a browser cannot supply, so CSRF is
// already impossible there — and applying the Host check to tokened deployments
// would break every reverse-proxied install, where Host is the proxy's name.
func TestCrossSite_GateAppliesOnlyWithoutAToken(t *testing.T) {
	s, rec := newServer(t, stream.Options{Listen: "0.0.0.0:9099", Token: testToken})

	h := authHeader()
	h.Set("Origin", "https://evil.example")
	h.Set("Sec-Fetch-Site", "cross-site")
	h.Set("Content-Type", "text/plain")
	w := postRaw(t, s.Handler(), "hec.example.com", h, captureFlowRecord)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the gate is tokenless-mode only); body=%q", w.Code, w.Body.String())
	}
	if p := findPoint(t, rec.MetricPoints(metricRecords), map[string]string{attrType: typeFlow}); p.Value != 1 {
		t.Fatalf("%s{type=flow} = %v, want 1", metricRecords, p.Value)
	}
}
