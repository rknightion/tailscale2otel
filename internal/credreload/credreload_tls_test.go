package credreload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genCAPEM returns a self-signed CA certificate as PEM bytes, generated
// in-process so tests never depend on an external openssl binary.
func genCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "credreload-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// genKeypairPEM returns a self-signed leaf certificate + private key as PEM,
// suitable for tls.X509KeyPair.
func genKeypairPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "credreload-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestNew_LoadsValidTLSTriple(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if err := os.WriteFile(caPath, genCAPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := genKeypairPEM(t)
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := New(Options{Sources: Sources{
		CAFile:   caPath,
		CertFile: certPath,
		KeyFile:  keyPath,
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Stop()

	cfg := r.TLSConfig()
	if cfg == nil {
		t.Fatal("TLSConfig() = nil, want a config")
	}
	if cfg.RootCAs == nil {
		t.Error("TLSConfig().RootCAs = nil, want the loaded CA pool")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("TLSConfig().Certificates = %d entries, want 1", len(cfg.Certificates))
	}
	if cfg.GetClientCertificate == nil {
		t.Error("TLSConfig().GetClientCertificate = nil, want the dynamic hook wired")
	}
	cert, err := r.ClientCertificate(nil)
	if err != nil {
		t.Fatalf("ClientCertificate: %v", err)
	}
	if cert == nil {
		t.Error("ClientCertificate() = nil cert")
	}
}

func TestNew_RejectsEmptyCAPool(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	writeFile(t, caPath, "not a pem certificate at all")

	_, err := New(Options{Sources: Sources{CAFile: caPath}})
	if err == nil {
		t.Fatal("New: want error for a CA file with no usable PEM certificate")
	}
}

func TestNew_RejectsMismatchedKeypair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	cert1, _ := genKeypairPEM(t)
	_, key2 := genKeypairPEM(t)
	if err := os.WriteFile(certPath, cert1, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key2, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(Options{Sources: Sources{CertFile: certPath, KeyFile: keyPath}})
	if err == nil {
		t.Fatal("New: want error for a cert/key that do not form a keypair")
	}
}

func TestNew_RejectsLoneCertOrKey(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	certPEM, _ := genKeypairPEM(t)
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(Options{Sources: Sources{CertFile: certPath}})
	if err == nil {
		t.Fatal("New: want error when cert_file is set without key_file")
	}
}

func TestNew_CAOnlyTrustWithNoClientCert(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, genCAPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := New(Options{Sources: Sources{CAFile: caPath}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Stop()

	cfg := r.TLSConfig()
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("want a TLSConfig with RootCAs set")
	}
	if len(cfg.Certificates) != 0 {
		t.Errorf("Certificates = %d, want 0 (no client cert configured)", len(cfg.Certificates))
	}
	if _, err := r.ClientCertificate(nil); err == nil {
		t.Error("ClientCertificate: want error, no client cert configured")
	}
}
