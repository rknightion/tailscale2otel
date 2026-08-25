package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Client-certificate auth and outbound client keypairs repeat the lesson #305
// taught about listener keypairs: "the file is readable" proves nothing. A CA
// bundle that parses to an EMPTY pool trusts nothing, so mutual TLS would reject
// every scraper — and an outbound client would fail every upload — with the
// failure surfacing as a handshake error at request time rather than a refusal
// to start. These tests pin the startup-time refusals instead.

// writeCABundle writes a PEM file containing one self-signed certificate,
// suitable for use as a CA bundle, and returns its path.
func writeCABundle(t *testing.T, dir, name string) string {
	t.Helper()
	certPath, _ := writeKeypair(t, dir, name)
	return certPath
}

func TestValidate_PrometheusClientCARequiresServerTLS(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.Prometheus.TLS.ClientCAFile = writeCABundle(t, dir, "ca")

	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: client_ca_file set without prometheus.tls.cert_file/key_file")
	}
	// TLS only ever requests a client certificate during a handshake, so a client
	// CA on a plaintext listener is inert. Naming both halves is what makes the
	// error actionable.
	if !strings.Contains(err.Error(), "prometheus.tls.client_ca_file") {
		t.Errorf("error %q should name prometheus.tls.client_ca_file", err.Error())
	}
	if !strings.Contains(err.Error(), "cert_file") {
		t.Errorf("error %q should explain that the listener must serve TLS", err.Error())
	}
}

func TestValidate_PrometheusClientCAMustContainACertificate(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cert, key := writeKeypair(t, dir, "server")

	c := Default()
	c.Prometheus.TLS.CertFile = cert
	c.Prometheus.TLS.KeyFile = key
	c.Prometheus.TLS.ClientCAFile = empty

	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: an empty PEM file yields a CertPool that trusts nothing")
	}
	if !strings.Contains(err.Error(), "no usable PEM certificate") {
		t.Errorf("error %q should say the bundle contains no usable PEM certificate", err.Error())
	}
}

func TestValidate_PrometheusClientCAAccepted(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeKeypair(t, dir, "server")

	c := Default()
	c.Prometheus.TLS.CertFile = cert
	c.Prometheus.TLS.KeyFile = key
	c.Prometheus.TLS.ClientCAFile = writeCABundle(t, dir, "ca")
	c.Prometheus.TLS.ClientAuth = "require_and_verify"

	if err := c.Validate(); err != nil {
		t.Fatalf("valid mTLS configuration rejected: %v", err)
	}
}

func TestValidate_PrometheusClientAuthMode(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeKeypair(t, dir, "server")
	ca := writeCABundle(t, dir, "ca")

	t.Run("unknown mode", func(t *testing.T) {
		c := Default()
		c.Prometheus.TLS.ClientAuth = "sometimes"
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: unknown client_auth mode")
		}
		if !strings.Contains(err.Error(), "require_and_verify") {
			t.Errorf("error %q should list the accepted modes", err.Error())
		}
	})

	// Only the two verifying modes check the presented chain, and a check needs
	// something to check against — otherwise the mode is a no-op that reads as
	// enforcement.
	for _, mode := range []string{"require_and_verify", "verify_if_given"} {
		t.Run(mode+" without a CA", func(t *testing.T) {
			c := Default()
			c.Prometheus.TLS.CertFile = cert
			c.Prometheus.TLS.KeyFile = key
			c.Prometheus.TLS.ClientAuth = mode
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error: %s without client_ca_file", mode)
			}
			if !strings.Contains(err.Error(), "client_ca_file") {
				t.Errorf("error %q should name client_ca_file", err.Error())
			}
		})
	}

	// The non-verifying modes are legitimate during a staged rollout, so they
	// must NOT require a CA.
	for _, mode := range []string{"request", "require", "none"} {
		t.Run(mode+" needs no CA", func(t *testing.T) {
			c := Default()
			c.Prometheus.TLS.CertFile = cert
			c.Prometheus.TLS.KeyFile = key
			c.Prometheus.TLS.ClientAuth = mode
			if err := c.Validate(); err != nil {
				t.Fatalf("client_auth %q should be accepted without a CA: %v", mode, err)
			}
		})
	}

	t.Run("empty mode is accepted", func(t *testing.T) {
		c := Default()
		c.Prometheus.TLS.CertFile = cert
		c.Prometheus.TLS.KeyFile = key
		c.Prometheus.TLS.ClientCAFile = ca
		if err := c.Validate(); err != nil {
			t.Fatalf("unset client_auth should defer to the default: %v", err)
		}
	})
}

func TestValidate_PrometheusHandlerLimits(t *testing.T) {
	t.Run("negative max_requests_in_flight", func(t *testing.T) {
		c := Default()
		c.Prometheus.Enabled = true
		c.Prometheus.MaxRequestsInFlight = -1
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: negative max_requests_in_flight")
		}
		if !strings.Contains(err.Error(), "max_requests_in_flight") {
			t.Errorf("error %q should name prometheus.max_requests_in_flight", err.Error())
		}
	})

	t.Run("negative timeout", func(t *testing.T) {
		c := Default()
		c.Prometheus.Enabled = true
		c.Prometheus.Timeout = Duration(-1)
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: negative prometheus.timeout")
		}
		if !strings.Contains(err.Error(), "prometheus.timeout") {
			t.Errorf("error %q should name prometheus.timeout", err.Error())
		}
	})

	t.Run("zero max is rejected while zero timeout remains supported", func(t *testing.T) {
		c := Default()
		c.Prometheus.Enabled = true
		c.Prometheus.MaxRequestsInFlight = 0
		c.Prometheus.Timeout = Duration(0)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "max_requests_in_flight") {
			t.Fatalf("zero max_requests_in_flight error = %v, want named validation failure", err)
		}
		c.Prometheus.MaxRequestsInFlight = 4
		if err := c.Validate(); err != nil {
			t.Fatalf("zero timeout should remain valid with a finite gather cap: %v", err)
		}
	})
}

// A bound below the truncation marker's own length is worse than no bound: every
// record collapses to just the marker, which is total data loss that looks like a
// working configuration.
func TestValidate_OTLPLogLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Config)
		want string
	}{
		{"body too small", func(c *Config) { c.OTLP.Limits.LogBodyBytes = 8 }, "log_body_bytes"},
		{"body zero", func(c *Config) { c.OTLP.Limits.LogBodyBytes = 0 }, "log_body_bytes"},
		{"body negative", func(c *Config) { c.OTLP.Limits.LogBodyBytes = -1 }, "log_body_bytes"},
		{"attr too small", func(c *Config) { c.OTLP.Limits.LogAttributeValueBytes = 63 }, "log_attribute_value_bytes"},
		{"attr zero", func(c *Config) { c.OTLP.Limits.LogAttributeValueBytes = 0 }, "log_attribute_value_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.set(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should name otlp.limits.%s", err.Error(), tc.want)
			}
		})
	}

	t.Run("defaults are valid", func(t *testing.T) {
		if err := Default().Validate(); err != nil {
			t.Fatalf("default log limits rejected: %v", err)
		}
	})

	// The floor is a minimum, not a fixed value — a large bound is the documented
	// way to ask for effectively no limit.
	t.Run("large values accepted", func(t *testing.T) {
		c := Default()
		c.OTLP.Limits.LogBodyBytes = 64 << 20
		c.OTLP.Limits.LogAttributeValueBytes = 1 << 20
		if err := c.Validate(); err != nil {
			t.Fatalf("large log limits rejected: %v", err)
		}
	})
}

func TestValidate_PyroscopeClientTLS(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeKeypair(t, dir, "client")

	t.Run("keypair is both-or-neither", func(t *testing.T) {
		c := Default()
		c.Profiling.Pyroscope.TLS.CertFile = cert
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: pyroscope client cert without key")
		}
		if !strings.Contains(err.Error(), "profiling.pyroscope.tls") {
			t.Errorf("error %q should name profiling.pyroscope.tls", err.Error())
		}
	})

	t.Run("mismatched keypair is rejected", func(t *testing.T) {
		_, otherKey := writeKeypair(t, dir, "other")
		c := Default()
		c.Profiling.Pyroscope.TLS.CertFile = cert
		c.Profiling.Pyroscope.TLS.KeyFile = otherKey
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: cert paired with a different key")
		}
		if !strings.Contains(err.Error(), "usable keypair") {
			t.Errorf("error %q should say the pair is not usable", err.Error())
		}
	})

	t.Run("ca_file must contain a certificate", func(t *testing.T) {
		junk := filepath.Join(dir, "junk.pem")
		if err := os.WriteFile(junk, []byte("not a pem\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		c := Default()
		c.Profiling.Pyroscope.TLS.CAFile = junk
		err := c.Validate()
		if err == nil {
			t.Fatal("expected error: ca_file with no PEM certificate")
		}
		if !strings.Contains(err.Error(), "no usable PEM certificate") {
			t.Errorf("error %q should say the bundle contains no usable PEM certificate", err.Error())
		}
	})

	t.Run("valid client TLS is accepted", func(t *testing.T) {
		c := Default()
		c.Profiling.Pyroscope.TLS.CAFile = writeCABundle(t, dir, "pyroca")
		c.Profiling.Pyroscope.TLS.CertFile = cert
		c.Profiling.Pyroscope.TLS.KeyFile = key
		if err := c.Validate(); err != nil {
			t.Fatalf("valid pyroscope client TLS rejected: %v", err)
		}
	})
}

// TestValidate_PrometheusMaxRequestsGatedOnEnabled pins the gate that keeps an
// upgrade from becoming a crash loop. config.example.yaml shipped
// `prometheus.enabled: false` alongside `max_requests_in_flight: 0` (then
// documented as unlimited) until 45e489c made 0 invalid, so every config copied
// from the project's own example carries that pair. The value bounds concurrent
// gathers on a listener that is not running, so rejecting it when the endpoint
// is off refuses to start over a key that controls nothing.
func TestValidate_PrometheusMaxRequestsGatedOnEnabled(t *testing.T) {
	t.Run("zero is accepted while the endpoint is off", func(t *testing.T) {
		c := Default()
		c.Prometheus.Enabled = false
		c.Prometheus.MaxRequestsInFlight = 0
		if err := c.Validate(); err != nil {
			t.Fatalf("max_requests_in_flight is inert while prometheus.enabled is false: %v", err)
		}
	})

	t.Run("zero is still rejected once the endpoint is on", func(t *testing.T) {
		c := Default()
		c.Prometheus.Enabled = true
		c.Prometheus.Listen = "127.0.0.1:2112"
		c.Prometheus.MaxRequestsInFlight = 0
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "max_requests_in_flight") {
			t.Fatalf("Validate() = %v, want a named max_requests_in_flight failure", err)
		}
		// An operator hitting this copied the 0 from the old example, so the
		// message has to say what changed and what to set.
		for _, want := range []string{"unlimited", "4"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should mention %q", err.Error(), want)
			}
		}
	})
}
