package certreload

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
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
	"github.com/rknightion/tailscale2otel/v5/internal/telemetrytest"
)

// writeCertKeyPair writes a throwaway self-signed cert/key pair to certFile/
// keyFile, ATOMICALLY (write-then-rename, like cert-manager/certbot do a real
// rotation) so tests can exercise Reloader's file-change detection. cn and
// notAfter are varied across calls specifically to force the DER byte size (cn
// length) and the notAfter field to differ between an "old" and "new" pair,
// so the mtime+size signature check cannot coincidentally see them as
// unchanged regardless of filesystem timestamp resolution.
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

// writeAtomic writes data to path via a temp-file-then-rename, mirroring how a
// real rotation replaces a file: the reloader must never observe a partially
// written file this way (it observes either the whole old file or the whole
// new one).
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

// TestReloader_InitialLoad verifies the constructor performs a synchronous
// initial load: GetCertificate returns a certificate immediately (no reload
// wait needed) and Status reports the loaded certificate's validity window,
// a fingerprint, and a reload timestamp, with no failure recorded.
func TestReloader_InitialLoad(t *testing.T) {
	certFile, keyFile := newTestCertPaths(t)
	notAfter := time.Now().Add(time.Hour).Truncate(time.Second)
	writeCertKeyPair(t, certFile, keyFile, "initial", notAfter)

	rec := telemetrytest.New()
	r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, rec.Emitter())

	cert, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil {
		t.Fatal("GetCertificate returned a nil certificate with no error")
	}

	st := r.Status()
	if st.Name != appcatalog.ComponentAdmin {
		t.Errorf("Status.Name = %q, want %q", st.Name, appcatalog.ComponentAdmin)
	}
	if st.NotAfter == "" {
		t.Error("Status.NotAfter is empty after a successful initial load")
	}
	if st.Fingerprint == "" {
		t.Error("Status.Fingerprint is empty after a successful initial load")
	}
	if st.LastReloadAt == "" {
		t.Error("Status.LastReloadAt is empty after a successful initial load")
	}
	if st.LastReloadFailureAt != "" {
		t.Errorf("Status.LastReloadFailureAt = %q, want empty (no failure has occurred)", st.LastReloadFailureAt)
	}

	if got := len(rec.MetricPoints(appcatalog.MetricTLSCertNotAfter)); got != 1 {
		t.Errorf("tls.cert.not_after points = %d, want 1", got)
	}
}

// TestReloader_RateLimitedUntilInterval pins the frozen design's core
// mechanism: a rotated file is invisible to GetCertificate until at least
// MinRecheckInterval has passed since the last check, and visible
// immediately after. Uses synctest so the interval elapses without a real
// sleep (Go 1.27 fake clock, matching internal/app/heartbeat_test.go).
func TestReloader_RateLimitedUntilInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		certFile, keyFile := newTestCertPaths(t)
		oldNotAfter := time.Now().Add(time.Hour).Truncate(time.Second)
		writeCertKeyPair(t, certFile, keyFile, "old", oldNotAfter)

		rec := telemetrytest.New()
		r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, rec.Emitter())
		before := r.Status()

		// Atomically replace with a distinguishable new pair (longer CN forces a
		// different DER size regardless of signature-length variance; a very
		// different notAfter is the field a client actually cares about).
		newNotAfter := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
		writeCertKeyPair(t, certFile, keyFile, strings.Repeat("rotated", 40), newNotAfter)

		// Immediately after the rotation, still inside the recheck interval: the
		// OLD certificate must still be served.
		if _, err := r.GetCertificate(nil); err != nil {
			t.Fatalf("GetCertificate immediately after rotation: %v", err)
		}
		if got := r.Status(); got.NotAfter != before.NotAfter {
			t.Fatalf("NotAfter changed before MinRecheckInterval elapsed: got %q, want unchanged %q",
				got.NotAfter, before.NotAfter)
		}

		// Advance the fake clock past the recheck interval; the next handshake
		// must now pick up the rotated certificate.
		time.Sleep(MinRecheckInterval)

		if _, err := r.GetCertificate(nil); err != nil {
			t.Fatalf("GetCertificate after the recheck interval: %v", err)
		}
		after := r.Status()
		if after.NotAfter == before.NotAfter {
			t.Fatal("NotAfter did not change after MinRecheckInterval elapsed — rotation was not picked up")
		}
		wantNotAfter := newNotAfter.UTC().Format(time.RFC3339)
		if after.NotAfter != wantNotAfter {
			t.Errorf("NotAfter = %q, want %q", after.NotAfter, wantNotAfter)
		}
		if after.Fingerprint == before.Fingerprint {
			t.Error("Fingerprint did not change after rotation")
		}
	})
}

// TestReloader_BrokenReplacementKeepsPreviousCert is the acceptance-
// criterion test: "Broken replacements retain the last valid certificate."
// After a good initial load, the cert file is atomically replaced with
// garbage; GetCertificate must keep returning the ORIGINAL certificate and
// the failure must be recorded (Status + a reload-failure metric), rather
// than the listener losing its certificate or erroring out.
func TestReloader_BrokenReplacementKeepsPreviousCert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		certFile, keyFile := newTestCertPaths(t)
		goodNotAfter := time.Now().Add(time.Hour).Truncate(time.Second)
		writeCertKeyPair(t, certFile, keyFile, "good", goodNotAfter)

		rec := telemetrytest.New()
		r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, rec.Emitter())
		goodCert, err := r.GetCertificate(nil)
		if err != nil {
			t.Fatalf("GetCertificate (good): %v", err)
		}

		// Simulate a cert-manager/certbot writer caught mid-write: the cert file
		// now contains neither the old nor a complete new certificate.
		writeAtomic(t, certFile, []byte("not a certificate"))

		time.Sleep(MinRecheckInterval)

		gotCert, err := r.GetCertificate(nil)
		if err != nil {
			t.Fatalf("GetCertificate after a broken replacement must still succeed with the previous cert, got error: %v", err)
		}
		if gotCert != goodCert {
			t.Error("GetCertificate returned a different certificate after a broken replacement — the previous good certificate was not retained")
		}

		st := r.Status()
		if st.LastReloadFailureAt == "" {
			t.Error("Status.LastReloadFailureAt is empty after a broken replacement")
		}
		if st.LastReloadFailureReason == "" {
			t.Error("Status.LastReloadFailureReason is empty after a broken replacement")
		}
		if st.NotAfter != goodNotAfter.UTC().Format(time.RFC3339) {
			t.Errorf("Status.NotAfter = %q, want the ORIGINAL cert's %q (still in service)",
				st.NotAfter, goodNotAfter.UTC().Format(time.RFC3339))
		}

		if got := len(rec.MetricPoints(appcatalog.MetricTLSCertReloadFailures)); got != 1 {
			t.Errorf("tls.cert.reload.failures points = %d, want 1", got)
		}
	})
}

// TestReloader_InitialLoadFailure covers the one case with no "previous
// good" to fall back to: construction against files that do not exist at all.
// GetCertificate must return an explicit error rather than a nil certificate
// with no error (which would panic tls.Config's caller) or a panic.
func TestReloader_InitialLoadFailure(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "missing.crt")
	keyFile := filepath.Join(dir, "missing.key")

	rec := telemetrytest.New()
	r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, rec.Emitter())

	if _, err := r.GetCertificate(nil); err == nil {
		t.Fatal("GetCertificate with no certificate ever loaded returned no error")
	}
	st := r.Status()
	if st.NotAfter != "" {
		t.Errorf("Status.NotAfter = %q, want empty (nothing has ever loaded)", st.NotAfter)
	}
	if st.LastReloadFailureAt == "" {
		t.Error("Status.LastReloadFailureAt is empty after a failed initial load")
	}
}

// TestReloader_StatusNeverExposesKeyMaterial asserts the hard security
// requirement: nothing Reloader can surface — Status's fields, or a
// reload-failure error string — ever contains PEM/DER private-key markers,
// and Fingerprint is exactly a SHA-256 hex digest (64 hex chars), never raw
// certificate or key bytes. Exercised across every code path that touches
// key material: a good load, a broken replacement, and a private-key parse
// failure specifically (a mismatched key is the case most likely to make a
// crypto library echo something about the key in its error text).
func TestReloader_StatusNeverExposesKeyMaterial(t *testing.T) {
	assertNoKeyMaterial := func(t *testing.T, label string, r *Reloader) {
		t.Helper()
		st := r.Status()
		haystack := strings.Join([]string{st.Name, st.NotBefore, st.NotAfter, st.Fingerprint,
			st.LastReloadAt, st.LastReloadFailureAt, st.LastReloadFailureReason}, "\n")
		for _, marker := range []string{"PRIVATE KEY", "-----BEGIN", "EC PRIVATE"} {
			if strings.Contains(haystack, marker) {
				t.Errorf("%s: Status leaked key/cert material — found %q in %q", label, marker, haystack)
			}
		}
		if st.Fingerprint != "" && len(st.Fingerprint) != 64 {
			t.Errorf("%s: Fingerprint length = %d, want 64 (a SHA-256 hex digest)", label, len(st.Fingerprint))
		}
	}

	t.Run("good load", func(t *testing.T) {
		certFile, keyFile := newTestCertPaths(t)
		writeCertKeyPair(t, certFile, keyFile, "material-check", time.Now().Add(time.Hour))
		r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, telemetrytest.New().Emitter())
		assertNoKeyMaterial(t, "good load", r)
	})

	t.Run("broken replacement", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			certFile, keyFile := newTestCertPaths(t)
			writeCertKeyPair(t, certFile, keyFile, "material-check", time.Now().Add(time.Hour))
			r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, telemetrytest.New().Emitter())
			writeAtomic(t, certFile, []byte("garbage"))
			time.Sleep(MinRecheckInterval)
			_, _ = r.GetCertificate(nil)
			assertNoKeyMaterial(t, "broken replacement", r)
		})
	})

	t.Run("mismatched key", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			certFile, keyFile := newTestCertPaths(t)
			writeCertKeyPair(t, certFile, keyFile, "material-check", time.Now().Add(time.Hour))
			r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, telemetrytest.New().Emitter())
			// Replace only the key with an unrelated one: LoadX509KeyPair rejects
			// the pair as not matching, which is the case most likely for a TLS
			// library to echo something about the key in its error text.
			otherPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatalf("generate replacement key: %v", err)
			}
			otherKeyBytes, err := x509.MarshalECPrivateKey(otherPriv)
			if err != nil {
				t.Fatalf("marshal replacement key: %v", err)
			}
			writeAtomic(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: otherKeyBytes}))
			time.Sleep(MinRecheckInterval)
			_, _ = r.GetCertificate(nil)
			assertNoKeyMaterial(t, "mismatched key", r)
		})
	})
}

// TestReloader_UnchangedFileDoesNotReload confirms that when neither file
// has changed, checkAndReload's stat-based signature comparison correctly
// treats the pair as unchanged rather than reparsing (and re-emitting) on
// every recheck-interval tick — the reload timestamp must stay put.
func TestReloader_UnchangedFileDoesNotReload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		certFile, keyFile := newTestCertPaths(t)
		writeCertKeyPair(t, certFile, keyFile, "stable", time.Now().Add(time.Hour))

		rec := telemetrytest.New()
		r := New(certFile, keyFile, appcatalog.ComponentAdmin, nil, rec.Emitter())
		before := r.Status()

		time.Sleep(MinRecheckInterval)
		if _, err := r.GetCertificate(nil); err != nil {
			t.Fatalf("GetCertificate: %v", err)
		}

		after := r.Status()
		if after.LastReloadAt != before.LastReloadAt {
			t.Errorf("LastReloadAt changed with no file change: before=%q after=%q", before.LastReloadAt, after.LastReloadAt)
		}
		// Exactly one successful load (the initial one) should ever have emitted
		// tls.cert.not_after; an unnecessary reparse would emit a second point.
		if got := len(rec.MetricPoints(appcatalog.MetricTLSCertNotAfter)); got != 1 {
			t.Errorf("tls.cert.not_after points = %d, want 1 (no spurious reload)", got)
		}
	})
}
