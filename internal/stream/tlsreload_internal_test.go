package stream

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/telemetrytest"
)

func writeCertKeyPair(t *testing.T, certFile, keyFile, cn string, notAfter time.Time) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
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
	writeAtomic(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeAtomic(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}))
}

func writeAtomic(t *testing.T, path string, data []byte) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename %s -> %s: %v", tmp, path, err)
	}
}

func newTestCertPaths(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
}

// TestServer_TLSStatus_WiresReloader confirms New() builds a tlsReloader when
// both TLS files are configured, and (*Server).TLSStatus surfaces it.
func TestServer_TLSStatus_WiresReloader(t *testing.T) {
	certFile, keyFile := newTestCertPaths(t)
	writeCertKeyPair(t, certFile, keyFile, "server-wiring", time.Now().Add(time.Hour))

	s := New(Options{TLSCertFile: certFile, TLSKeyFile: keyFile}, nil, nil, telemetrytest.New().Emitter(), nil)
	st, ok := s.TLSStatus()
	if !ok {
		t.Fatal("TLSStatus ok = false, want true when TLS files are configured")
	}
	if st.NotAfter == "" {
		t.Error("TLSStatus().NotAfter is empty")
	}
}

// TestServer_TLSStatus_NoTLSConfigured confirms a plain-HTTP server (no TLS
// files) reports no TLS status row at all, rather than a zero-value one.
func TestServer_TLSStatus_NoTLSConfigured(t *testing.T) {
	s := New(Options{}, nil, nil, telemetrytest.New().Emitter(), nil)
	if _, ok := s.TLSStatus(); ok {
		t.Error("TLSStatus ok = true for a server with no TLS configured, want false")
	}
}

// TestRouter_TLSStatus_DelegatesToBase confirms Router.TLSStatus reads
// through to its base Server, matching how every route shares one TLS config.
func TestRouter_TLSStatus_DelegatesToBase(t *testing.T) {
	certFile, keyFile := newTestCertPaths(t)
	writeCertKeyPair(t, certFile, keyFile, "router-wiring", time.Now().Add(time.Hour))

	base := New(Options{TLSCertFile: certFile, TLSKeyFile: keyFile}, nil, nil, telemetrytest.New().Emitter(), nil)
	router := NewRouter([]Route{{Path: "/x", Server: base}})

	st, ok := router.TLSStatus()
	if !ok {
		t.Fatal("Router.TLSStatus ok = false, want true (base server has TLS configured)")
	}
	if st.NotAfter == "" {
		t.Error("Router.TLSStatus().NotAfter is empty")
	}
}
