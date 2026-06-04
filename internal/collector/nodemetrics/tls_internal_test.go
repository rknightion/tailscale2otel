package nodemetrics

import (
	"crypto/tls"
	"testing"
)

// TestBuildTLSConfigPinsMinVersionTLS12 guards the per-target scrape client TLS
// config against the semgrep missing-ssl-minversion finding: buildTLSConfig must
// floor the negotiated version at TLS 1.2 so a downgrade to TLS 1.0/1.1 is never
// possible. 1.2 (not 1.3) is the deliberate floor for proxied / self-signed
// scrape targets.
func TestBuildTLSConfigPinsMinVersionTLS12(t *testing.T) {
	cfg, err := buildTLSConfig(&TLSClientConfig{})
	if err != nil {
		t.Fatalf("buildTLSConfig returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("buildTLSConfig returned nil config")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("cfg.MinVersion = %#x, want tls.VersionTLS12 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}
}
