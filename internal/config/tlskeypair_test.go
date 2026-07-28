package config

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
	"strings"
	"testing"
	"time"
)

// A listener's TLS material is only useful if it actually loads. Checking that
// the files are READABLE proves almost nothing — /dev/null is readable, and a
// cert paired with someone else's key is readable too. Both cases used to pass
// -validate and fail at ListenAndServeTLS, which happens after startup, on a
// goroutine, where the failure is a log line rather than a refusal to run (#305).

// writeKeypair writes a valid self-signed cert/key pair and returns their paths.
func writeKeypair(t *testing.T, dir, name string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPath = filepath.Join(dir, name+".crt")
	keyPath = filepath.Join(dir, name+".key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestValidTLSKeypairIsAccepted(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeKeypair(t, dir, "ok")
	if err := validateTLSFiles("admin", cert, key); err != nil {
		t.Errorf("a valid self-signed keypair was rejected: %v", err)
	}
}

func TestUnparseableTLSKeypairIsRejected(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "empty.crt")
	key := filepath.Join(dir, "empty.key")
	for _, p := range []string{cert, key} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := validateTLSFiles("admin", cert, key)
	if err == nil {
		t.Fatal("an empty cert/key pair passed validation. This is the /dev/null case from #305: " +
			"readable, parseable as nothing, and it fails only once the listener tries to serve.")
	}
	if !strings.Contains(err.Error(), "admin.tls") {
		t.Errorf("error does not name the listener block: %v", err)
	}
}

func TestMismatchedTLSKeypairIsRejected(t *testing.T) {
	dir := t.TempDir()
	cert, _ := writeKeypair(t, dir, "a")
	_, otherKey := writeKeypair(t, dir, "b")

	err := validateTLSFiles("prometheus", cert, otherKey)
	if err == nil {
		t.Fatal("a certificate paired with a DIFFERENT key passed validation. Both files parse " +
			"and both are readable; only loading them together catches it.")
	}
	if !strings.Contains(err.Error(), "prometheus.tls") {
		t.Errorf("error does not name the listener block: %v", err)
	}
}

func TestTLSErrorDoesNotLeakKeyMaterial(t *testing.T) {
	dir := t.TempDir()
	cert, _ := writeKeypair(t, dir, "a")
	_, otherKey := writeKeypair(t, dir, "b")

	err := validateTLSFiles("admin", cert, otherKey)
	if err == nil {
		t.Fatal("expected a mismatch error")
	}
	body, readErr := os.ReadFile(otherKey)
	if readErr != nil {
		t.Fatal(readErr)
	}
	// The PEM body, minus its header lines — the part that is actually secret.
	for _, line := range strings.Split(string(body), "\n") {
		if len(line) < 20 || strings.HasPrefix(line, "-----") {
			continue
		}
		if strings.Contains(err.Error(), line) {
			t.Fatalf("the validation error embeds private key material. Config errors are logged "+
				"and pasted into issues; they must name PATHS, never contents.\nerror: %v", err)
		}
	}
}

// Streaming was simply not validated at all: no both-or-neither check, no
// readability check, no keypair check. Worse than the others, because
// stream.Run serves plain HTTP whenever either field is empty — so a
// half-configured listener silently downgrades to plaintext on a receiver that
// accepts log data (#305).
func TestStreamingTLSIsValidated(t *testing.T) {
	c := Default()
	c.Streaming.Enabled = true
	c.Streaming.TLS.CertFile = "/dev/null" // key deliberately absent

	err := c.Validate()
	if err == nil {
		t.Fatal("streaming.tls.cert_file with no key_file passed validation. stream.Run serves " +
			"plain HTTP unless BOTH are set, so this configuration silently downgrades a log " +
			"receiver to plaintext while looking configured for TLS.")
	}
	if !strings.Contains(err.Error(), "streaming.tls") {
		t.Errorf("error does not name streaming.tls: %v", err)
	}
}

func TestStreamingTLSKeypairMustLoad(t *testing.T) {
	dir := t.TempDir()
	cert, _ := writeKeypair(t, dir, "a")
	_, otherKey := writeKeypair(t, dir, "b")

	c := Default()
	c.Streaming.Enabled = true
	c.Streaming.TLS.CertFile = cert
	c.Streaming.TLS.KeyFile = otherKey

	if err := c.Validate(); err == nil {
		t.Fatal("a mismatched streaming keypair passed validation")
	}
}
