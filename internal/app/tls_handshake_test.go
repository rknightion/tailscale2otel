package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/internal/config"
)

// generateSelfSignedCertFiles writes a throwaway self-signed cert/key pair
// (valid for 127.0.0.1) to two files under t.TempDir(), for driving a real TLS
// handshake against runAdmin/runMetrics in tests. #170.
func generateSelfSignedCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

// reserveFreeAddr binds an ephemeral port, closes it immediately, and returns
// the address so a caller can bind the real server to the same address
// shortly after. Small reuse race in theory; in practice this is the standard
// "pick a free port" pattern for tests that can't get the bound address back
// from http.Server directly (ListenAndServe(TLS) does its own net.Listen).
func reserveFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close reservation listener: %v", err)
	}
	return addr
}

// waitHTTPSGet polls url with an InsecureSkipVerify TLS client until it gets a
// response or the deadline passes (the server goroutine needs a moment to
// bind after being started).
func waitHTTPSGet(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // test-only client dialing our own throwaway cert
		Timeout:   2 * time.Second,
	}
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			return resp
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GET %s: timed out waiting for server, last error: %v", url, lastErr)
	return nil
}

// TestRunAdmin_ServesHTTPSWhenTLSConfigured pins #170: with admin.tls.cert_file
// and admin.tls.key_file both set, runAdmin must negotiate a real TLS
// handshake on /healthz rather than serving plain HTTP.
func TestRunAdmin_ServesHTTPSWhenTLSConfigured(t *testing.T) {
	certFile, keyFile := generateSelfSignedCertFiles(t)
	addr := reserveFreeAddr(t)

	cfg := &config.Config{}
	cfg.Admin.Listen = addr
	cfg.Admin.TLS.CertFile = certFile
	cfg.Admin.TLS.KeyFile = keyFile
	a := &App{cfg: cfg}
	a.adminSrv = a.buildAdminServer()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.runAdmin(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	resp := waitHTTPSGet(t, "https://"+addr+"/healthz")
	defer func() { _ = resp.Body.Close() }()
	if resp.TLS == nil {
		t.Fatal("response has no TLS connection state — admin server did not negotiate TLS")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestRunAdmin_PlainHTTPWhenTLSUnconfigured pins the "byte-identical when
// unset" requirement from #170: with no admin.tls files configured, runAdmin
// must keep serving plain HTTP (an HTTPS client must fail to handshake).
func TestRunAdmin_PlainHTTPWhenTLSUnconfigured(t *testing.T) {
	addr := reserveFreeAddr(t)

	cfg := &config.Config{}
	cfg.Admin.Listen = addr
	a := &App{cfg: cfg}
	a.adminSrv = a.buildAdminServer()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.runAdmin(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(3 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = client.Get("http://" + addr + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET http://%s/healthz: %v", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestRunMetrics_ServesHTTPSWhenTLSConfigured mirrors
// TestRunAdmin_ServesHTTPSWhenTLSConfigured for the prometheus pull-endpoint
// server.
func TestRunMetrics_ServesHTTPSWhenTLSConfigured(t *testing.T) {
	certFile, keyFile := generateSelfSignedCertFiles(t)
	addr := reserveFreeAddr(t)

	cfg := &config.Config{}
	cfg.Prometheus.Listen = addr
	cfg.Prometheus.TLS.CertFile = certFile
	cfg.Prometheus.TLS.KeyFile = keyFile
	a := &App{cfg: cfg}
	a.metricsSrv = a.buildMetricsServer(prometheus.NewRegistry())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.runMetrics(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	resp := waitHTTPSGet(t, "https://"+addr+"/metrics")
	defer func() { _ = resp.Body.Close() }()
	if resp.TLS == nil {
		t.Fatal("response has no TLS connection state — metrics server did not negotiate TLS")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
