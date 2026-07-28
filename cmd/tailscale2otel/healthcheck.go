package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// Exit codes. Distinct on purpose: an orchestrator can tell "the process is
// up but not serving yet" from "the process is not reachable at all".
const (
	hcReady     = 0 // /readyz returned 2xx
	hcUnready   = 1 // reached the listener, but it reported not-ready
	hcTransport = 2 // could not probe at all (config, dial, TLS, timeout)
)

// hcMaxBodyRead bounds how much of a non-2xx /readyz body is read for the
// stderr message. /readyz responses are small (a one-line reason); this is
// generous headroom, not a real limit.
const hcMaxBodyRead = 4 * 1024

// runHealthcheck loads the config at configPath, builds the local admin
// /readyz URL, probes it within timeout, and reports the outcome to stdout/
// stderr. It is the implementation behind `-healthcheck` and is exercised
// directly by tests so the exit-code contract can be pinned without spawning
// a subprocess.
func runHealthcheck(configPath string, timeout time.Duration, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: config invalid: %v\n", err)
		return hcTransport
	}

	url, err := healthcheckURL(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return hcTransport
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{
		// Belt-and-braces alongside the context deadline: a stuck TLS
		// handshake or a transport that ignores context cancellation must
		// not outlive -healthcheck-timeout.
		Timeout:   timeout,
		Transport: probeTransport(),
	}

	return probeReady(ctx, client, url, stdout, stderr)
}

// probeTransport returns an http.Transport configured to probe THIS
// PROCESS'S OWN admin listener over loopback.
//
// InsecureSkipVerify is correct here, not a weakening: the destination is the
// trust boundary (a loopback address in the same network namespace as this
// process), not the certificate. There is no third party to impersonate on
// loopback, and no flag is offered to change this — a kubelet httpGet probe
// with scheme: HTTPS makes exactly the same choice for exactly the same
// reason (it has no way to be handed the server's CA either).
func probeTransport() *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback self-probe; see doc comment
	}
}

// healthcheckURL builds the /readyz URL for this process's own admin server
// from its resolved configuration.
func healthcheckURL(cfg *config.Config) (string, error) {
	if !cfg.Admin.Enabled {
		return "", errors.New("admin.enabled is false: there is no readiness endpoint to probe; " +
			"set admin.enabled: true (or TS2OTEL_ADMIN__ENABLED=true) to use -healthcheck")
	}

	host, port, err := net.SplitHostPort(cfg.Admin.Listen)
	if err != nil {
		return "", fmt.Errorf("admin.listen %q is not a valid host:port: %w", cfg.Admin.Listen, err)
	}

	// An empty host, "0.0.0.0", or "::" means "all interfaces" — not a
	// dialable destination on every stack (notably when IPv6 is disabled, or
	// under some container network configurations). This process's own
	// listener is always reachable via loopback, so rewrite to it.
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}

	scheme := "http"
	if cfg.Admin.TLS.CertFile != "" && cfg.Admin.TLS.KeyFile != "" {
		scheme = "https"
	}

	return scheme + "://" + net.JoinHostPort(host, port) + "/readyz", nil
}

// probeReady GETs url and classifies the result into the hc* exit codes,
// writing a short human-readable line to stdout (success) or stderr
// (unready/transport failure). It never panics and always closes the
// response body.
func probeReady(ctx context.Context, client *http.Client, url string, stdout, stderr io.Writer) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: building request for %s: %v\n", url, err)
		return hcTransport
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: probing %s: %v\n", url, err)
		return hcTransport
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Fprintf(stdout, "healthcheck: ready (%s -> %d)\n", url, resp.StatusCode)
		return hcReady
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, hcMaxBodyRead))
	fmt.Fprintf(stderr, "healthcheck: not ready (%s -> %d): %s\n", url, resp.StatusCode, body)
	return hcUnready
}
