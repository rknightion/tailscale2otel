package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

func TestHealthcheckURL(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *config.Config)
		want    string
		wantErr string // substring; empty means no error expected
	}{
		{
			name:   "default listen, no TLS",
			mutate: func(c *config.Config) {},
			want:   "http://127.0.0.1:9091/readyz",
		},
		{
			name: "wildcard 0.0.0.0 rewritten to loopback",
			mutate: func(c *config.Config) {
				c.Admin.Listen = "0.0.0.0:9091"
			},
			want: "http://127.0.0.1:9091/readyz",
		},
		{
			name: "wildcard bracketed IPv6 rewritten to loopback",
			mutate: func(c *config.Config) {
				c.Admin.Listen = "[::]:9091"
			},
			want: "http://127.0.0.1:9091/readyz",
		},
		{
			name: "explicit host preserved",
			mutate: func(c *config.Config) {
				c.Admin.Listen = "127.0.0.1:9091"
			},
			want: "http://127.0.0.1:9091/readyz",
		},
		{
			name: "explicit IPv6 literal stays bracketed",
			mutate: func(c *config.Config) {
				c.Admin.Listen = "[::1]:9091"
			},
			want: "http://[::1]:9091/readyz",
		},
		{
			name: "TLS cert+key set -> https",
			mutate: func(c *config.Config) {
				c.Admin.TLS.CertFile = "/tmp/cert.pem"
				c.Admin.TLS.KeyFile = "/tmp/key.pem"
			},
			want: "https://127.0.0.1:9091/readyz",
		},
		{
			name: "only cert set -> http, not https",
			mutate: func(c *config.Config) {
				c.Admin.TLS.CertFile = "/tmp/cert.pem"
			},
			want: "http://127.0.0.1:9091/readyz",
		},
		{
			name: "only key set -> http, not https",
			mutate: func(c *config.Config) {
				c.Admin.TLS.KeyFile = "/tmp/key.pem"
			},
			want: "http://127.0.0.1:9091/readyz",
		},
		{
			name: "admin disabled -> error naming admin.enabled",
			mutate: func(c *config.Config) {
				c.Admin.Enabled = false
			},
			wantErr: "admin.enabled",
		},
		{
			name: "garbage listen -> error, not a guess",
			mutate: func(c *config.Config) {
				c.Admin.Listen = "not-a-host-port"
			},
			wantErr: "not-a-host-port",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load("")
			if err != nil {
				t.Fatalf("config.Load(\"\") failed: %v", err)
			}
			tc.mutate(cfg)

			got, err := healthcheckURL(cfg)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("healthcheckURL() = %q, nil error; want error containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("healthcheckURL() error = %q, want it to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("healthcheckURL() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("healthcheckURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProbeReady(t *testing.T) {
	t.Run("200 -> hcReady", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
		}))
		defer srv.Close()

		var stdout, stderr bytes.Buffer
		got := probeReady(context.Background(), srv.Client(), srv.URL, &stdout, &stderr)
		if got != hcReady {
			t.Errorf("probeReady() = %d, want hcReady (%d); stderr=%q", got, hcReady, stderr.String())
		}
		if stdout.Len() == 0 {
			t.Error("probeReady() wrote nothing to stdout on success")
		}
	})

	t.Run("503 -> hcUnready", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: waiting for first poll"))
		}))
		defer srv.Close()

		var stdout, stderr bytes.Buffer
		got := probeReady(context.Background(), srv.Client(), srv.URL, &stdout, &stderr)
		if got != hcUnready {
			t.Errorf("probeReady() = %d, want hcUnready (%d)", got, hcUnready)
		}
		if !strings.Contains(stderr.String(), "503") {
			t.Errorf("probeReady() stderr = %q, want it to mention the 503 status", stderr.String())
		}
	})

	t.Run("TLS server with InsecureSkipVerify -> hcReady", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		client := &http.Client{Transport: probeTransport()}
		var stdout, stderr bytes.Buffer
		got := probeReady(context.Background(), client, srv.URL, &stdout, &stderr)
		if got != hcReady {
			t.Errorf("probeReady() against TLS server = %d, want hcReady (%d); stderr=%q", got, hcReady, stderr.String())
		}
	})

	t.Run("closed listener -> hcTransport", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		url := srv.URL
		srv.Close() // close before probing

		var stdout, stderr bytes.Buffer
		got := probeReady(context.Background(), http.DefaultClient, url, &stdout, &stderr)
		if got != hcTransport {
			t.Errorf("probeReady() against closed listener = %d, want hcTransport (%d)", got, hcTransport)
		}
		if stderr.Len() == 0 {
			t.Error("probeReady() wrote nothing to stderr on transport error")
		}
	})

	t.Run("blocked handler past deadline -> hcTransport", func(t *testing.T) {
		block := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-block
			w.WriteHeader(http.StatusOK)
		}))
		// srv.Close() blocks until outstanding handlers return, so the block
		// channel MUST be closed (unblocking the handler) before srv.Close()
		// runs. Defers are LIFO, so register Close() first and the unblock
		// second, ensuring the unblock fires first on the way out.
		defer srv.Close()
		defer close(block)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		var stdout, stderr bytes.Buffer
		got := probeReady(ctx, srv.Client(), srv.URL, &stdout, &stderr)
		if got != hcTransport {
			t.Errorf("probeReady() against a blocked handler = %d, want hcTransport (%d)", got, hcTransport)
		}
	})
}

func TestRunHealthcheck_AdminDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("admin:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	got := runHealthcheck(path, 5*time.Second, &stdout, &stderr)
	if got != hcTransport {
		t.Errorf("runHealthcheck() = %d, want hcTransport (%d)", got, hcTransport)
	}
	if !strings.Contains(stderr.String(), "admin.enabled") {
		t.Errorf("runHealthcheck() stderr = %q, want it to mention admin.enabled", stderr.String())
	}
}
