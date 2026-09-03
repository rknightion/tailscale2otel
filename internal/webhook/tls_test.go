package webhook

import (
	"context"
	"crypto/ed25519"
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

	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

func TestServerRun_ServesTLSAndShutsDown(t *testing.T) {
	certFile, keyFile := testTLSFiles(t)
	addr := unusedTCPAddress(t)
	s := New(Options{
		Listen:      addr,
		Path:        "/webhook",
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	}, telemetrytest.New().Emitter(), discard())

	assertRunServesTLSAndShutsDown(t, addr, "/webhook", s.Run)
}

func TestRouterRun_ServesTLSAndShutsDown(t *testing.T) {
	certFile, keyFile := testTLSFiles(t)
	addr := unusedTCPAddress(t)
	s := New(Options{
		Listen:      addr,
		Path:        "/webhook",
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	}, telemetrytest.New().Emitter(), discard())
	r := NewRouter([]Route{{Tailnet: "example.com", Server: s}})

	assertRunServesTLSAndShutsDown(t, addr, "/webhook", r.Run)
}

func assertRunServesTLSAndShutsDown(t *testing.T, addr, path string, run func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx) }()

	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // test-only ephemeral certificate
	}
	t.Cleanup(client.CloseIdleConnections)
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := client.Get("https://" + addr + path)
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("TLS server never accepted connections: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func unusedTCPAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test address: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}
	return addr
}

func testTLSFiles(t *testing.T) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test TLS key: %v", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create test TLS certificate: %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal test TLS key: %v", err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write test TLS certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		t.Fatalf("write test TLS key: %v", err)
	}
	return certFile, keyFile
}

// #316 covered the admin, prometheus and streaming listeners but left this one
// loading its keypair exactly once, so a rotated webhook certificate stayed
// invisible until a restart. "Applies consistently to every TLS listener" is
// the acceptance criterion, and a rule that holds for three listeners out of
// four is worse than no rule, because nobody can remember which.
func TestServer_ServesTLSViaReloader(t *testing.T) {
	certFile, keyFile := testTLSFiles(t)
	s := New(Options{
		Listen:      "127.0.0.1:0",
		Path:        "/webhook",
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	}, telemetrytest.New().Emitter(), discard())

	st, ok := s.TLSStatus()
	if !ok {
		t.Fatal("a webhook receiver configured with a cert and key must report TLS status")
	}
	if st.NotAfter == "" || st.Fingerprint == "" {
		t.Errorf("TLS status is empty, so no certificate was loaded through the reloader: %+v", st)
	}
}

func TestServer_NoTLSStatusWithoutCertificates(t *testing.T) {
	s := New(Options{Listen: "127.0.0.1:0", Path: "/webhook"},
		telemetrytest.New().Emitter(), discard())
	if _, ok := s.TLSStatus(); ok {
		t.Error("a plaintext webhook receiver must not claim TLS status")
	}
}
