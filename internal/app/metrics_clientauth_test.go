package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// testCA is a throwaway certificate authority. internal/config's tlskeypair_test
// has an equivalent writeKeypair helper, but it lives in another package and only
// produces self-signed leaves — mutual TLS needs a real issuer so a client
// certificate can be signed by the CA the listener trusts, and a SECOND,
// untrusted CA so "presents a certificate" can be told apart from "presents a
// certificate we trust".
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
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
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf certificate for the given extended key usage, returning it
// as a ready-to-use tls.Certificate plus its PEM encoding.
func (ca *testCA) issue(t *testing.T, name string, usage x509.ExtKeyUsage, ips []net.IP) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("sign leaf cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("build keypair: %v", err)
	}
	return pair, certPEM, keyPEM
}

func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mtlsFixture is a listener trusting one CA, a client certificate it accepts, and
// a client certificate from a different CA that it must not.
type mtlsFixture struct {
	ca          *testCA
	rogue       *testCA
	serverCert  string
	serverKey   string
	clientCA    string
	goodClient  tls.Certificate
	rogueClient tls.Certificate
}

func newMTLSFixture(t *testing.T) *mtlsFixture {
	t.Helper()
	dir := t.TempDir()
	ca := newTestCA(t, "tailscale2otel test CA")
	rogue := newTestCA(t, "somebody else's CA")

	_, srvCertPEM, srvKeyPEM := ca.issue(t, "127.0.0.1", x509.ExtKeyUsageServerAuth, []net.IP{net.IPv4(127, 0, 0, 1)})
	good, _, _ := ca.issue(t, "prometheus scraper", x509.ExtKeyUsageClientAuth, nil)
	bad, _, _ := rogue.issue(t, "impostor scraper", x509.ExtKeyUsageClientAuth, nil)

	return &mtlsFixture{
		ca:          ca,
		rogue:       rogue,
		serverCert:  writeFile(t, dir, "server.crt", srvCertPEM),
		serverKey:   writeFile(t, dir, "server.key", srvKeyPEM),
		clientCA:    writeFile(t, dir, "clients.pem", ca.pem),
		goodClient:  good,
		rogueClient: bad,
	}
}

// apply wires the fixture's server keypair and client CA into cfg.
func (f *mtlsFixture) apply(c *config.Config, clientAuth string) {
	c.Prometheus.TLS.CertFile = f.serverCert
	c.Prometheus.TLS.KeyFile = f.serverKey
	c.Prometheus.TLS.ClientCAFile = f.clientCA
	c.Prometheus.TLS.ClientAuth = clientAuth
}

// serveMetricsTLS starts a's Prometheus listener on a real loopback socket, so
// the assertions exercise an actual TLS handshake rather than a config struct.
func serveMetricsTLS(t *testing.T, a *App) string {
	t.Helper()
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "t2o_mtls_probe_total", Help: "probe"})
	reg.MustRegister(c)
	c.Inc()

	srv := a.buildMetricsServer(reg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return "https://" + ln.Addr().String() + "/metrics"
}

// mtlsClient trusts the fixture's server CA and optionally presents a client
// certificate.
//
// The certificate is supplied through GetClientCertificate rather than
// Certificates on purpose. Go's TLS client filters Certificates against the
// acceptable-CA list in the server's CertificateRequest and sends NOTHING when
// nothing matches — so a rogue-CA certificate configured the obvious way never
// reaches the wire, and the server rejects the handshake for "no certificate"
// instead of "untrusted certificate". That passes the test while proving the
// wrong thing. GetClientCertificate skips the filtering, so an untrusted
// certificate is genuinely offered and genuinely refused.
func (f *mtlsFixture) mtlsClient(clientCert *tls.Certificate) *http.Client {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(f.ca.pem)
	cfg := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	if clientCert != nil {
		cert := *clientCert
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &cert, nil
		}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 10 * time.Second}
}

func TestMetricsClientCertificateAuth(t *testing.T) {
	f := newMTLSFixture(t)
	a, _, _ := selfObsScrapeApp(t, func(c *config.Config) { f.apply(c, "require_and_verify") })
	url := serveMetricsTLS(t, a)

	t.Run("no client certificate is rejected", func(t *testing.T) {
		_, err := f.mtlsClient(nil).Get(url)
		if err == nil {
			t.Fatal("a scraper with no client certificate was served. With client_ca_file configured " +
				"the point is that only holders of a CA-signed certificate can read every series " +
				"this exporter produces.")
		}
	})

	t.Run("a certificate from another CA is rejected", func(t *testing.T) {
		_, err := f.mtlsClient(&f.rogueClient).Get(url)
		if err == nil {
			t.Fatal("a client certificate signed by an untrusted CA was accepted; presenting ANY " +
				"certificate is not the same as presenting a trusted one")
		}
	})

	t.Run("a certificate signed by the configured CA is served", func(t *testing.T) {
		resp, err := f.mtlsClient(&f.goodClient).Get(url)
		if err != nil {
			t.Fatalf("the trusted scraper was refused: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("code = %d, want 200; body:\n%s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), "t2o_mtls_probe_total 1") {
			t.Errorf("served body lost its sample line:\n%s", body)
		}
	})
}

// Token auth and mutual TLS are independent gates and must compose: the
// certificate proves which machine is calling, the token proves it is authorized.
// Satisfying one must never waive the other.
func TestMetricsClientCertComposesWithTokenAuth(t *testing.T) {
	f := newMTLSFixture(t)
	a, _, _ := selfObsScrapeApp(t, func(c *config.Config) {
		f.apply(c, "require_and_verify")
		c.Prometheus.Auth.Token = config.Secret("s3cret")
	})
	url := serveMetricsTLS(t, a)

	t.Run("trusted certificate without the token is 401", func(t *testing.T) {
		resp, err := f.mtlsClient(&f.goodClient).Get(url)
		if err != nil {
			t.Fatalf("handshake failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("code = %d, want 401: mutual TLS must not waive a configured token", resp.StatusCode)
		}
	})

	t.Run("trusted certificate with the token is 200", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer s3cret")
		resp, err := f.mtlsClient(&f.goodClient).Do(req)
		if err != nil {
			t.Fatalf("handshake failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("code = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("the token does not buy a way past the certificate check", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer s3cret")
		if _, err := f.mtlsClient(&f.rogueClient).Do(req); err == nil {
			t.Error("a correct token got an untrusted certificate through; TLS is the outer gate " +
				"and must reject before any header is read")
		}
	})
}

// The five documented mode strings each map onto exactly one crypto/tls
// ClientAuthType, and EMPTY means require_and_verify when a client CA is
// configured (the documented default) and NoClientCert when one is not.
func TestMetricsClientAuthModeMapping(t *testing.T) {
	f := newMTLSFixture(t)
	cases := []struct {
		mode    string
		withCA  bool
		want    tls.ClientAuthType
		wantCAs bool
	}{
		{"require_and_verify", true, tls.RequireAndVerifyClientCert, true},
		{"verify_if_given", true, tls.VerifyClientCertIfGiven, true},
		{"require", true, tls.RequireAnyClientCert, true},
		{"request", true, tls.RequestClientCert, true},
		{"none", true, tls.NoClientCert, true},
		{"", true, tls.RequireAndVerifyClientCert, true},
		{"", false, tls.NoClientCert, false},
	}
	for _, tc := range cases {
		name := tc.mode
		if name == "" {
			name = "empty"
		}
		if !tc.withCA {
			name += " without a client CA"
		}
		t.Run(name, func(t *testing.T) {
			a, _, _ := selfObsScrapeApp(t, func(c *config.Config) {
				c.Prometheus.TLS.CertFile = f.serverCert
				c.Prometheus.TLS.KeyFile = f.serverKey
				c.Prometheus.TLS.ClientAuth = tc.mode
				if tc.withCA {
					c.Prometheus.TLS.ClientCAFile = f.clientCA
				}
			})
			srv := a.buildMetricsServer(prometheus.NewRegistry())
			if srv.TLSConfig == nil {
				t.Fatal("no TLSConfig on a listener with a server keypair configured")
			}
			if got := srv.TLSConfig.ClientAuth; got != tc.want {
				t.Errorf("ClientAuth = %v, want %v", got, tc.want)
			}
			if gotCAs := srv.TLSConfig.ClientCAs != nil; gotCAs != tc.wantCAs {
				t.Errorf("ClientCAs present = %v, want %v", gotCAs, tc.wantCAs)
			}
		})
	}
}

// verify_if_given is the migration mode: a scraper without a certificate still
// works, but one that presents a bad certificate is still refused.
func TestMetricsVerifyIfGivenAllowsNoCertButNotABadOne(t *testing.T) {
	f := newMTLSFixture(t)
	a, _, _ := selfObsScrapeApp(t, func(c *config.Config) { f.apply(c, "verify_if_given") })
	url := serveMetricsTLS(t, a)

	resp, err := f.mtlsClient(nil).Get(url)
	if err != nil {
		t.Fatalf("verify_if_given refused a certificate-less scraper: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("code = %d, want 200", resp.StatusCode)
	}

	if _, err := f.mtlsClient(&f.rogueClient).Get(url); err == nil {
		t.Error("verify_if_given accepted an untrusted certificate; the whole point of the mode is " +
			"that a certificate, once offered, is verified")
	}
}

// A client CA that cannot be read at build time must fail CLOSED. Validate
// already refuses to start on an unreadable or certificate-less bundle, so this
// is the defense for the file disappearing between Validate and bind — the wrong
// answer is a listener that quietly stops asking for certificates.
func TestMetricsUnreadableClientCAFailsClosed(t *testing.T) {
	f := newMTLSFixture(t)
	a, _, logs := selfObsScrapeApp(t, func(c *config.Config) {
		f.apply(c, "request")
		c.Prometheus.TLS.ClientCAFile = filepath.Join(t.TempDir(), "vanished.pem")
	})
	srv := a.buildMetricsServer(prometheus.NewRegistry())
	if got := srv.TLSConfig.ClientAuth; got != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert: an unusable trust store must "+
			"reject everything rather than accept anything", got)
	}
	if logs.String() == "" {
		t.Error("the listener silently fell back to rejecting every scraper with nothing logged")
	}
}
