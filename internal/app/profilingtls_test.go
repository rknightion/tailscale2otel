package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// writePEM writes a certificate/key pair from an httptest TLS server's own
// generated certificate into files, so the transport options can be exercised
// against REAL files the way an operator configures them.
func writeCAFile(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	buf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	return path
}

// TestPyroscopeTLS_CustomCA proves a custom CA bundle makes an otherwise
// untrusted TLS server reachable, and that omitting it fails. Without both halves
// a test can pass because verification was never happening at all.
func TestPyroscopeTLS_CustomCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	caPath := writeCAFile(t, srv.Certificate())

	t.Run("without the CA the handshake fails and classifies as tls", func(t *testing.T) {
		health := newProfilingHealth()
		client, err := newProfilingUploadClient(pyroscopeTransportOptions{}, health, nil, nil)
		if err != nil {
			t.Fatalf("newProfilingUploadClient: %v", err)
		}
		resp, err := client.Do(postTo(t, srv.URL))
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("handshake against an untrusted TLS server succeeded, want failure")
		}
		if got := health.snapshot().LastErrorClass; got != "tls" {
			t.Errorf("LastErrorClass = %q, want tls", got)
		}
	})

	t.Run("with the CA the upload succeeds", func(t *testing.T) {
		health := newProfilingHealth()
		client, err := newProfilingUploadClient(pyroscopeTransportOptions{CAFile: caPath}, health, nil, nil)
		if err != nil {
			t.Fatalf("newProfilingUploadClient: %v", err)
		}
		resp, err := client.Do(postTo(t, srv.URL))
		if err != nil {
			t.Fatalf("upload with the custom CA failed: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
		}
		snap := health.snapshot()
		if snap.Failures != 0 || snap.LastSuccessAt == "" {
			t.Errorf("snapshot = %+v, want a clean success", snap)
		}
	})

	t.Run("insecure_skip_verify also reaches it, without the CA", func(t *testing.T) {
		client, err := newProfilingUploadClient(pyroscopeTransportOptions{InsecureSkipVerify: true}, newProfilingHealth(), nil, nil)
		if err != nil {
			t.Fatalf("newProfilingUploadClient: %v", err)
		}
		resp, err := client.Do(postTo(t, srv.URL))
		if err != nil {
			t.Fatalf("upload with insecure_skip_verify failed: %v", err)
		}
		_ = resp.Body.Close()
	})
}

// testPKI is a throwaway CA plus the server and client leaves it signed, written
// to PEM files so the transport options are exercised the way an operator
// configures them: real files, a real chain, real verification on both ends.
type testPKI struct {
	caPath     string
	serverPair tls.Certificate
	caPool     *x509.CertPool
	certPath   string
	keyPath    string
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tailscale2otel test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("self-sign ca: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}
	caPath := filepath.Join(dir, "ca.pem")
	writePEMFile(t, caPath, "CERTIFICATE", caDER)

	leaf := func(name string, serial int64, server bool) (tls.Certificate, string, string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("%s key: %v", name, err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: name},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
		}
		if server {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
			tmpl.DNSNames = []string{"localhost"}
		} else {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatalf("sign %s: %v", name, err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("marshal %s key: %v", name, err)
		}
		certPath := filepath.Join(dir, name+".pem")
		keyPath := filepath.Join(dir, name+"-key.pem")
		writePEMFile(t, certPath, "CERTIFICATE", der)
		writePEMFile(t, keyPath, "PRIVATE KEY", keyDER)
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			t.Fatalf("load %s pair: %v", name, err)
		}
		return pair, certPath, keyPath
	}

	serverPair, _, _ := leaf("server", 2, true)
	_, certPath, keyPath := leaf("client", 3, false)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return testPKI{caPath: caPath, serverPair: serverPair, caPool: pool, certPath: certPath, keyPath: keyPath}
}

func writePEMFile(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	buf := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPyroscopeTLS_MutualTLS proves the client certificate is actually presented
// and accepted by a server that REQUIRES and VERIFIES one, and that omitting it
// fails. Both halves matter: a one-sided test passes just as well when nothing is
// being verified at all.
func TestPyroscopeTLS_MutualTLS(t *testing.T) {
	pki := newTestPKI(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pki.serverPair},
		ClientCAs:    pki.caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	srv.StartTLS()
	defer srv.Close()

	t.Run("without a client certificate the server rejects the connection", func(t *testing.T) {
		health := newProfilingHealth()
		client, err := newProfilingUploadClient(pyroscopeTransportOptions{CAFile: pki.caPath}, health, nil, nil)
		if err != nil {
			t.Fatalf("newProfilingUploadClient: %v", err)
		}
		resp, err := client.Do(postTo(t, srv.URL))
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("mTLS server accepted a connection with no client certificate")
		}
		if got := health.snapshot().LastErrorClass; got == "" {
			t.Error("no error class recorded for the rejected handshake")
		}
	})

	t.Run("with the client certificate the upload succeeds", func(t *testing.T) {
		health := newProfilingHealth()
		client, err := newProfilingUploadClient(pyroscopeTransportOptions{
			CAFile:   pki.caPath,
			CertFile: pki.certPath,
			KeyFile:  pki.keyPath,
		}, health, nil, nil)
		if err != nil {
			t.Fatalf("newProfilingUploadClient: %v", err)
		}
		resp, err := client.Do(postTo(t, srv.URL))
		if err != nil {
			t.Fatalf("mTLS upload failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("StatusCode = %d, want 200 (the client certificate was not accepted)", resp.StatusCode)
		}
		if snap := health.snapshot(); snap.Failures != 0 {
			t.Errorf("snapshot = %+v, want no failures", snap)
		}
	})

	t.Run("the custom CA is required even with a client certificate", func(t *testing.T) {
		// Proves CAFile is genuinely driving SERVER verification rather than the
		// handshake succeeding for some unrelated reason.
		client, err := newProfilingUploadClient(pyroscopeTransportOptions{
			CertFile: pki.certPath,
			KeyFile:  pki.keyPath,
		}, newProfilingHealth(), nil, nil)
		if err != nil {
			t.Fatalf("newProfilingUploadClient: %v", err)
		}
		resp, err := client.Do(postTo(t, srv.URL))
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatal("connected to a server signed by an untrusted CA")
		}
	})
}

// TestPyroscopeTLS_UnusableMaterial checks the read-error paths report rather than
// panic. Config validation guarantees well-formed material, but the files can
// change between validation and use.
func TestPyroscopeTLS_UnusableMaterial(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		opts pyroscopeTransportOptions
	}{
		{"missing ca file", pyroscopeTransportOptions{CAFile: filepath.Join(dir, "absent.pem")}},
		{"ca file with no certificate", pyroscopeTransportOptions{CAFile: junk}},
		{"cert without key", pyroscopeTransportOptions{CertFile: junk}},
		{"key without cert", pyroscopeTransportOptions{KeyFile: junk}},
		{"unusable keypair", pyroscopeTransportOptions{CertFile: junk, KeyFile: junk}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.opts.tlsConfig(); err == nil {
				t.Fatal("tlsConfig() returned no error for unusable material")
			}
			if _, err := newProfilingUploadClient(c.opts, newProfilingHealth(), nil, nil); err == nil {
				t.Error("newProfilingUploadClient returned no error for unusable material")
			}
		})
	}

	t.Run("no TLS customization yields no tls.Config", func(t *testing.T) {
		cfg, err := pyroscopeTransportOptions{}.tlsConfig()
		if err != nil {
			t.Fatalf("tlsConfig() = %v, want nil", err)
		}
		if cfg != nil {
			t.Errorf("tlsConfig() = %+v, want nil (stdlib defaults)", cfg)
		}
	})
}

// TestSanitizePyroscopeHeaders is the header-precedence contract: RESERVED
// HEADERS ALWAYS WIN. It matters because pyroscope-go applies HTTPHeaders AFTER
// the auth and tenant headers, so anything left in the map overrides identity.
func TestSanitizePyroscopeHeaders(t *testing.T) {
	t.Run("Authorization is dropped when basic auth is configured", func(t *testing.T) {
		kept, dropped := sanitizePyroscopeHeaders(map[string]string{
			"Authorization": "Bearer attacker",
			"X-Team":        "platform",
		}, true, false)
		if _, ok := kept["Authorization"]; ok {
			t.Error("Authorization survived with basic auth configured — it would override the credential")
		}
		if kept["X-Team"] != "platform" {
			t.Errorf("kept = %v, want X-Team preserved", kept)
		}
		if !slices.Contains(dropped, "Authorization") {
			t.Errorf("dropped = %v, want it to report Authorization", dropped)
		}
	})

	t.Run("a lowercase authorization is dropped too", func(t *testing.T) {
		// Header.Set canonicalizes, so a literal match would let this through and
		// it would then overwrite the real Authorization header.
		kept, _ := sanitizePyroscopeHeaders(map[string]string{"authorization": "Bearer attacker"}, true, false)
		if len(kept) != 0 {
			t.Errorf("kept = %v, want empty (case-insensitive reservation)", kept)
		}
	})

	t.Run("Authorization passes through when basic auth is NOT configured", func(t *testing.T) {
		kept, dropped := sanitizePyroscopeHeaders(map[string]string{"Authorization": "Bearer token"}, false, false)
		if kept["Authorization"] != "Bearer token" {
			t.Errorf("kept = %v, want the bearer-token escape hatch preserved", kept)
		}
		if len(dropped) != 0 {
			t.Errorf("dropped = %v, want none", dropped)
		}
	})

	t.Run("the tenant header is reserved only when tenant_id is set", func(t *testing.T) {
		kept, _ := sanitizePyroscopeHeaders(map[string]string{pyroscopeTenantHeader: "other-tenant"}, false, true)
		if len(kept) != 0 {
			t.Errorf("kept = %v, want the tenant header dropped when tenant_id is set", kept)
		}
		// Kept names are CANONICAL, and X-Scope-OrgID does not survive
		// canonicalization unchanged (-> X-Scope-Orgid). That is correct rather
		// than lossy: net/http's Header.Set canonicalizes too, so the canonical
		// form is what goes on the wire either way — and it is exactly why the
		// reserved comparison has to be canonical, or the SDK's own
		// Set("X-Scope-OrgID") and a user's "X-Scope-OrgID" would be treated as
		// different names here while colliding on the wire.
		canonical := http.CanonicalHeaderKey(pyroscopeTenantHeader)
		if canonical == pyroscopeTenantHeader {
			t.Fatalf("precondition changed: %q is now canonical, revisit this reasoning", pyroscopeTenantHeader)
		}
		kept, _ = sanitizePyroscopeHeaders(map[string]string{pyroscopeTenantHeader: "chosen"}, false, false)
		if kept[canonical] != "chosen" {
			t.Errorf("kept = %v, want the tenant header preserved under %q with no tenant_id", kept, canonical)
		}
		// Whatever casing the operator used must hit the same reservation.
		for _, spelling := range []string{"x-scope-orgid", "X-SCOPE-ORGID", "X-Scope-Orgid"} {
			if kept, _ := sanitizePyroscopeHeaders(map[string]string{spelling: "sneaky"}, false, true); len(kept) != 0 {
				t.Errorf("spelling %q bypassed the tenant reservation: %v", spelling, kept)
			}
		}
	})

	t.Run("request framing headers are always reserved", func(t *testing.T) {
		kept, dropped := sanitizePyroscopeHeaders(map[string]string{
			"Content-Type":      "text/plain",
			"Content-Length":    "1",
			"Transfer-Encoding": "chunked",
		}, false, false)
		if len(kept) != 0 {
			t.Errorf("kept = %v, want all framing headers dropped", kept)
		}
		if len(dropped) != 3 {
			t.Errorf("dropped = %v, want all three reported", dropped)
		}
	})

	t.Run("nil and empty inputs", func(t *testing.T) {
		if kept, dropped := sanitizePyroscopeHeaders(nil, true, true); kept != nil || dropped != nil {
			t.Errorf("nil input gave %v/%v, want nil/nil", kept, dropped)
		}
		if kept, _ := sanitizePyroscopeHeaders(map[string]string{"  ": "x"}, false, false); len(kept) != 0 {
			t.Errorf("blank header name kept: %v", kept)
		}
	})
}

// TestPyroscopeConfig_ReservedHeadersNeverReachTheSDK closes the loop at the
// mapping: the map handed to pyroscope.Config must already be free of reserved
// names, because the SDK's own precedence is the reverse of ours.
func TestPyroscopeConfig_ReservedHeadersNeverReachTheSDK(t *testing.T) {
	opts := pyroscopeTransportOptions{
		Headers:      map[string]string{"Authorization": "Bearer attacker", "X-Team": "platform"},
		BasicAuthSet: true,
	}
	kept, _ := sanitizePyroscopeHeaders(opts.Headers, opts.BasicAuthSet, opts.TenantSet)
	if _, ok := kept["Authorization"]; ok {
		t.Fatal("Authorization would reach pyroscope.Config.HTTPHeaders and overwrite the basic-auth credential")
	}
	if kept["X-Team"] != "platform" {
		t.Errorf("kept = %v, want X-Team", kept)
	}
}

// TestPyroscopeHeaderValuesRedactFromLogs proves a header value cannot reach a
// log line through the Pyroscope SDK's logger, which formats arbitrary text.
func TestPyroscopeHeaderValuesRedactFromLogs(t *testing.T) {
	const secret = "glc_eyJvIjoiSEVBREVSLVNFQ1JFVCJ9"
	opts := pyroscopeTransportOptions{Headers: map[string]string{"X-Auth": secret}}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pl := pyroscopeLogger{l: logger, redact: redactSecretsFunc(opts.secretValues())}

	pl.Errorf("failed to upload: (401) 'rejected header %s'", secret)
	pl.Infof("configured with %s", secret)
	pl.Debugf("uploading with header value %s", secret)

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("header value leaked into logs:\n%s", out)
	}
	if !strings.Contains(out, redactedPlaceholder) {
		t.Errorf("no redaction placeholder in logs:\n%s", out)
	}
}

// TestPyroscopeHeaderValuesRedactFromStatus proves the status surface reports
// header NAMES only.
func TestPyroscopeHeaderValuesRedactFromStatus(t *testing.T) {
	const secret = "HEADER-VALUE-leak-canary"
	opts := pyroscopeTransportOptions{
		Headers:      map[string]string{"x-auth": secret, "Authorization": secret, "X-Team": "platform"},
		BasicAuthSet: true,
	}
	names := opts.headerNames()
	if slices.Contains(names, "Authorization") {
		t.Errorf("headerNames = %v, want the reserved Authorization excluded", names)
	}
	if !slices.Contains(names, "X-Auth") || !slices.Contains(names, "X-Team") {
		t.Errorf("headerNames = %v, want canonicalized X-Auth and X-Team", names)
	}
	if !slices.IsSorted(names) {
		t.Errorf("headerNames = %v, want sorted (a stable status table)", names)
	}
	for _, n := range names {
		if strings.Contains(n, secret) {
			t.Errorf("header value leaked into the status name list: %q", n)
		}
	}
}

// TestRedactSecretsFuncIgnoresTrivialValues checks a very short value is not
// turned into a substring filter that would mangle unrelated log text.
func TestRedactSecretsFuncIgnoresTrivialValues(t *testing.T) {
	f := redactSecretsFunc([]string{"1", "ab"})
	if got := f("tenant 1 and ab and abc"); got != "tenant 1 and ab and abc" {
		t.Errorf("short secrets were redacted: %q", got)
	}
	f = redactSecretsFunc([]string{"longsecret", "longsecretextra"})
	if got := f("x longsecretextra y"); strings.Contains(got, "longsecret") {
		t.Errorf("overlapping secret left a fragment: %q", got)
	}
}

// TestProfilingUploadClientKeepsSDKClientPolicy pins the two client settings
// supplying a custom HTTPClient would otherwise silently discard: the upload
// timeout, and never following redirects (net/http strips Authorization across a
// redirect, which the upstream SDK comments on at length).
func TestProfilingUploadClientKeepsSDKClientPolicy(t *testing.T) {
	client, err := newProfilingUploadClient(pyroscopeTransportOptions{}, newProfilingHealth(), nil, nil)
	if err != nil {
		t.Fatalf("newProfilingUploadClient: %v", err)
	}
	if client.base.Timeout != pyroscopeUploadTimeout {
		t.Errorf("Timeout = %v, want %v", client.base.Timeout, pyroscopeUploadTimeout)
	}
	if client.base.CheckRedirect == nil {
		t.Fatal("CheckRedirect is nil — a redirect would strip the Authorization header")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.base.CheckRedirect(req, nil); !errors.Is(got, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect = %v, want http.ErrUseLastResponse", got)
	}
}

// TestPyroscopeTransportOptionsFromConfig pins the reserved-header flags derived
// from the existing config fields. The TLS/header assignments are the documented
// wiring hand-off (config.ProfilingPyroscope does not carry them yet).
func TestPyroscopeTransportOptionsFromConfig(t *testing.T) {
	cfg := config.Default().Profiling.Pyroscope
	cfg.BasicAuthUser = "12345"
	cfg.BasicAuthPassword = "glc_token"
	cfg.TenantID = "tenant-x"
	got := pyroscopeTransportOptionsFromConfig(cfg)
	if !got.BasicAuthSet {
		t.Error("BasicAuthSet = false with user+password set")
	}
	if !got.TenantSet {
		t.Error("TenantSet = false with tenant_id set")
	}

	cfg.BasicAuthPassword = ""
	if pyroscopeTransportOptionsFromConfig(cfg).BasicAuthSet {
		t.Error("BasicAuthSet = true with no password — the SDK only sets Authorization when BOTH are present")
	}
}

// TestPyroscopeTransportOptionsFromConfig_WiresTLSAndHeaders proves the real
// config fields reach the transport: the TLS paths verbatim, and the header map
// with its Secret values revealed at the point of use.
func TestPyroscopeTransportOptionsFromConfig_WiresTLSAndHeaders(t *testing.T) {
	p := config.Default().Profiling.Pyroscope
	p.TLS = config.PyroscopeTLS{
		InsecureSkipVerify: true,
		CAFile:             "/etc/ca.pem",
		CertFile:           "/etc/client.pem",
		KeyFile:            "/etc/client-key.pem",
	}
	p.Headers = map[string]config.Secret{"X-Api-Key": "PYROHEADER-secret"}

	got := pyroscopeTransportOptionsFromConfig(p)
	if !got.InsecureSkipVerify {
		t.Error("InsecureSkipVerify not wired")
	}
	if got.CAFile != "/etc/ca.pem" || got.CertFile != "/etc/client.pem" || got.KeyFile != "/etc/client-key.pem" {
		t.Errorf("TLS paths = %q/%q/%q, want the configured trio", got.CAFile, got.CertFile, got.KeyFile)
	}
	if got.Headers["X-Api-Key"] != "PYROHEADER-secret" {
		t.Errorf("Headers = %v, want the Secret revealed at the point of use", got.Headers)
	}

	// No headers configured -> nil map, so nothing is handed to the SDK at all.
	p.Headers = nil
	if h := pyroscopeTransportOptionsFromConfig(p).Headers; h != nil {
		t.Errorf("Headers = %v with none configured, want nil", h)
	}
}

// TestPyroscopeConfig_ConfiguredHeadersReachTheSDKSanitized closes the loop from
// real config through to pyroscope.Config: the operator's header survives, the
// reserved one does not.
func TestPyroscopeConfig_ConfiguredHeadersReachTheSDKSanitized(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://profiles.example"
	cfg.Profiling.Pyroscope.BasicAuthUser = "12345"
	cfg.Profiling.Pyroscope.BasicAuthPassword = "glc_token"
	cfg.Profiling.Pyroscope.TenantID = "tenant-x"
	cfg.Profiling.Pyroscope.Headers = map[string]config.Secret{
		"X-Api-Key":     "operator-key",
		"Authorization": "Bearer attacker",
		"x-scope-orgid": "other-tenant",
		"Content-Type":  "text/plain",
	}

	pc := pyroscopeConfig(cfg, "v1")
	if pc.HTTPHeaders["X-Api-Key"] != "operator-key" {
		t.Errorf("HTTPHeaders = %v, want X-Api-Key preserved", pc.HTTPHeaders)
	}
	for _, reserved := range []string{"Authorization", "X-Scope-Orgid", "Content-Type"} {
		if _, ok := pc.HTTPHeaders[reserved]; ok {
			t.Errorf("reserved header %q reached pyroscope.Config.HTTPHeaders — the SDK applies these AFTER auth/tenant, so it would win: %v",
				reserved, pc.HTTPHeaders)
		}
	}
	// The reserved values the SDK will set itself are still configured.
	if pc.BasicAuthUser != "12345" || pc.BasicAuthPassword != "glc_token" || pc.TenantID != "tenant-x" {
		t.Errorf("reserved identity lost: user=%q tenant=%q", pc.BasicAuthUser, pc.TenantID)
	}
}

// TestPyroscopeConfig_TLSFromConfigReachesTheClient proves the configured CA
// actually lands on the upload client's TLS config, driving a real handshake
// against a server signed by that CA.
func TestPyroscopeConfig_TLSFromConfigReachesTheClient(t *testing.T) {
	pki := newTestPKI(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pki.serverPair}}
	srv.StartTLS()
	defer srv.Close()

	prev := profilingHealthState
	profilingHealthState = newProfilingHealth()
	defer func() { profilingHealthState = prev }()

	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = srv.URL
	cfg.Profiling.Pyroscope.TLS.CAFile = pki.caPath

	pc := mustPyroscopeConfigWithUploadClient(t, cfg, "v1")
	client, ok := pc.HTTPClient.(*profilingUploadClient)
	if !ok {
		t.Fatalf("HTTPClient is %T, want *profilingUploadClient", pc.HTTPClient)
	}
	resp, err := client.Do(postTo(t, srv.URL))
	if err != nil {
		t.Fatalf("upload over the configured custom CA failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
}

// TestProfilingInfo_ReportsTLSFlagsAndHeaderNames checks the status surface
// reflects the configured TLS controls as flags and the headers as names.
func TestProfilingInfo_ReportsTLSFlagsAndHeaderNames(t *testing.T) {
	cfg := config.Default()
	cfg.Profiling.Pyroscope.Enabled = true
	cfg.Profiling.Pyroscope.ServerAddress = "https://profiles.example"
	cfg.Profiling.Pyroscope.TLS = config.PyroscopeTLS{
		InsecureSkipVerify: true,
		CAFile:             "/etc/pyro-ca.pem",
		CertFile:           "/etc/pyro-client.pem",
		KeyFile:            "/etc/pyro-client-key.pem",
	}
	cfg.Profiling.Pyroscope.Headers = map[string]config.Secret{"x-api-key": "STATUS-HEADER-canary"}

	a := &App{cfg: cfg, logger: slog.New(slog.DiscardHandler)}
	got := a.profilingInfo()
	if !got.PyroscopeTLSCustomCA || !got.PyroscopeTLSClientCert || !got.PyroscopeTLSSkipVerify {
		t.Errorf("TLS flags = %+v, want all three set", got)
	}
	if !slices.Equal(got.PyroscopeHeaderNames, []string{"X-Api-Key"}) {
		t.Errorf("PyroscopeHeaderNames = %v, want [X-Api-Key]", got.PyroscopeHeaderNames)
	}
	// No file path and no header value may appear anywhere in the DTO.
	for _, forbidden := range []string{"STATUS-HEADER-canary", "/etc/pyro-ca.pem", "/etc/pyro-client-key.pem"} {
		if strings.Contains(fmt.Sprintf("%+v", got), forbidden) {
			t.Errorf("%q leaked into ProfilingInfo: %+v", forbidden, got)
		}
	}
}
