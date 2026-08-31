package app

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

func applyAdminMTLS(f *mtlsFixture, c *config.Config, clientAuth string) {
	c.Admin.TLS.CertFile = f.serverCert
	c.Admin.TLS.KeyFile = f.serverKey
	c.Admin.TLS.ClientCAFile = f.clientCA
	c.Admin.TLS.ClientAuth = clientAuth
}

func serveAdminTLS(t *testing.T, a *App) string {
	t.Helper()
	srv := a.buildAdminServer()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return "https://" + ln.Addr().String() + "/healthz"
}

func TestAdminClientCertificateAuthHandshake(t *testing.T) {
	f := newMTLSFixture(t)
	cfg := config.Default()
	applyAdminMTLS(f, cfg, "") // client CA alone defaults to require_and_verify
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	url := serveAdminTLS(t, a)

	t.Run("no client certificate is rejected", func(t *testing.T) {
		if _, err := f.mtlsClient(nil).Get(url); err == nil {
			t.Fatal("admin served a client with no certificate")
		}
	})
	t.Run("certificate from another CA is rejected", func(t *testing.T) {
		if _, err := f.mtlsClient(&f.rogueClient).Get(url); err == nil {
			t.Fatal("admin served a client certificate signed by an untrusted CA")
		}
	})
	t.Run("certificate from configured CA is served", func(t *testing.T) {
		resp, err := f.mtlsClient(&f.goodClient).Get(url)
		if err != nil {
			t.Fatalf("trusted admin client was refused: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(body) != "ok" {
			t.Fatalf("trusted admin response = %d %q, want 200 ok", resp.StatusCode, body)
		}
	})
}

func TestAdminClientCertificateAndTokenAuthCompose(t *testing.T) {
	f := newMTLSFixture(t)
	cfg := config.Default()
	applyAdminMTLS(f, cfg, "require_and_verify")
	cfg.Admin.Auth.Token = testAdminToken
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	url := strings.TrimSuffix(serveAdminTLS(t, a), "/healthz") + "/"
	client := f.mtlsClient(&f.goodClient)

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("trusted mTLS client could not reach token gate: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("trusted client without admin token = %d, want 401", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testAdminToken)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("trusted mTLS client with token was refused: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trusted mTLS client with token = %d, want 200", resp.StatusCode)
	}
}

func TestAdminClientAuthModesMatchMetricsListener(t *testing.T) {
	for _, mode := range []string{"", "require_and_verify", "verify_if_given", "require", "request", "none"} {
		if got, want := adminClientAuthType(mode, "clients.pem"), metricsClientAuthType(mode, "clients.pem"); got != want {
			t.Errorf("mode %q: admin=%v metrics=%v", mode, got, want)
		}
	}
}

func TestAdminUnreadableClientCAFailsClosed(t *testing.T) {
	f := newMTLSFixture(t)
	cfg := config.Default()
	applyAdminMTLS(f, cfg, "request")
	if err := os.Remove(f.clientCA); err != nil {
		t.Fatal(err)
	}
	a := baseTestApp(t, cfg, "http://127.0.0.1:0", telemetrytest.New())
	srv := a.buildAdminServer()
	if srv.TLSConfig == nil {
		t.Fatal("TLSConfig is nil")
	}
	if got := srv.TLSConfig.ClientAuth; got != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v, want RequireAndVerifyClientCert fail-closed", got)
	}
	if srv.TLSConfig.ClientCAs == nil {
		t.Fatal("ClientCAs is nil; unreadable bundle did not install an empty fail-closed pool")
	}
}
