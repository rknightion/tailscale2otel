package app

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// /metrics serves every series this exporter produces, including device names,
// flow endpoints and audit identities. It used to be wide open whenever no token
// was set — on ANY bind — with only a startup WARN standing between a default
// wildcard listener and full disclosure to anything that could reach the port.
//
// It now fails closed on a network-reachable bind, matching the admin surface,
// with one difference: scraping from another host is a legitimate deployment, so
// there is an explicit acknowledgement flag rather than a flat refusal (#315).

func metricsApp(t *testing.T, listen, token string, ack bool) *App {
	t.Helper()
	cfg := config.Default()
	cfg.Prometheus.Enabled = true
	cfg.Prometheus.Listen = listen
	cfg.Prometheus.Auth.Token = config.Secret(token)
	cfg.Prometheus.Auth.AllowUnauthenticated = ack
	return &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}
}

func getMetrics(t *testing.T, a *App, host, authHeader string) *httptest.ResponseRecorder {
	return getMetricsWithHeaders(t, a, host, authHeader, nil)
}

func getMetricsWithHeaders(t *testing.T, a *App, host, authHeader string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := a.requireMetricsAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP something\n"))
	}))
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/metrics", nil)
	req.Host = host
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMetricsLoopbackEscapeHatchRejectsBrowserRebinding(t *testing.T) {
	a := metricsApp(t, "127.0.0.1:2112", "", false)
	tests := []struct {
		name    string
		host    string
		headers map[string]string
	}{
		{name: "foreign host", host: "attacker.example:2112"},
		{name: "foreign origin", host: "127.0.0.1:2112", headers: map[string]string{"Origin": "https://attacker.example"}},
		{name: "cross site fetch", host: "127.0.0.1:2112", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if rec := getMetricsWithHeaders(t, a, tc.host, "", tc.headers); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestMetricsExplicitOpenAndTokenModesDoNotRequireLoopbackHost(t *testing.T) {
	if rec := getMetrics(t, metricsApp(t, "0.0.0.0:2112", "", true), "metrics.example.internal:2112", ""); rec.Code != http.StatusOK {
		t.Fatalf("explicit unauthenticated status = %d, want 200", rec.Code)
	}
	if rec := getMetrics(t, metricsApp(t, "0.0.0.0:2112", "secret", false), "metrics.example.internal:2112", "Bearer secret"); rec.Code != http.StatusOK {
		t.Fatalf("token-authenticated status = %d, want 200", rec.Code)
	}
}

func TestMetricsFailsClosedOnNetworkBindWithNoToken(t *testing.T) {
	a := metricsApp(t, "0.0.0.0:2112", "", false)
	rec := getMetrics(t, a, "example.internal:2112", "")
	if rec.Code == http.StatusOK {
		t.Fatal("/metrics served every series on a wildcard bind with no token and no explicit " +
			"acknowledgement. This is the whole of #315: the endpoint discloses device names, flow " +
			"endpoints and audit identities to anything that can reach the port.")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403. A 401 would make a browser prompt for a password that "+
			"does not exist; this is misconfiguration, not a missing credential.", rec.Code)
	}
	if body := rec.Body.String(); body == "" {
		t.Error("the refusal carries no body, so an operator has no idea which knob to set")
	}
}

func TestMetricsStaysOpenOnLoopbackWithNoToken(t *testing.T) {
	a := metricsApp(t, "127.0.0.1:2112", "", false)
	rec := getMetrics(t, a, "127.0.0.1:2112", "")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200. Local development against a loopback bind must stay "+
			"credential-free — only this host can reach it, which is the deliberate escape hatch.",
			rec.Code)
	}
}

func TestMetricsAcknowledgementReopensNetworkBind(t *testing.T) {
	a := metricsApp(t, "0.0.0.0:2112", "", true)
	rec := getMetrics(t, a, "example.internal:2112", "")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200. An operator who explicitly set "+
			"prometheus.auth.allow_unauthenticated has acknowledged the exposure — in-cluster "+
			"scraping behind a NetworkPolicy is a real deployment and must stay possible.", rec.Code)
	}
}

func TestMetricsTokenStillGatesEveryBind(t *testing.T) {
	a := metricsApp(t, "0.0.0.0:2112", "s3cret", false)

	if rec := getMetrics(t, a, "example.internal:2112", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no credential: status = %d, want 401", rec.Code)
	}
	if rec := getMetrics(t, a, "example.internal:2112", "Bearer wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong credential: status = %d, want 401", rec.Code)
	}
	if rec := getMetrics(t, a, "example.internal:2112", "Bearer s3cret"); rec.Code != http.StatusOK {
		t.Errorf("correct credential: status = %d, want 200", rec.Code)
	}
}

// The acknowledgement must not become a way to leave a TOKEN unused: if a token
// is set it is still enforced, acknowledged or not.
func TestAcknowledgementDoesNotDisableAConfiguredToken(t *testing.T) {
	a := metricsApp(t, "0.0.0.0:2112", "s3cret", true)
	if rec := getMetrics(t, a, "example.internal:2112", ""); rec.Code == http.StatusOK {
		t.Error("allow_unauthenticated bypassed a configured token; it must only cover the " +
			"no-token case")
	}
}
