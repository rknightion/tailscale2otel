package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
)

// The ambient environment the container credential provider reads. AWS sets these
// for you: ECS sets the relative URI when a task IAM role is attached, and EKS Pod
// Identity sets the full URI plus a token file.
const (
	envContainerRelativeURI = "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"
	envContainerFullURI     = "AWS_CONTAINER_CREDENTIALS_FULL_URI"
	envContainerAuthToken   = "AWS_CONTAINER_AUTHORIZATION_TOKEN" //nolint:gosec // G101: the NAME of a variable, not a value
	envContainerAuthFile    = "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE"
)

// defaultECSContainerBase is the fixed host the ECS agent serves task-role
// credentials on. The relative-URI form supplies only a path, so on ECS the host
// is not operator-controlled and cannot be pointed anywhere else.
const defaultECSContainerBase = "http://169.254.170.2"

// ecsContainerBase is overridden in tests. There is deliberately no way to change
// it in production: making the ECS host configurable would reintroduce exactly the
// SSRF surface the fixed address removes.
var ecsContainerBase = defaultECSContainerBase

// The only non-loopback addresses this provider will talk to. These are the
// addresses AWS's own agents serve on:
//
//	169.254.170.2   the ECS agent (task IAM roles)
//	169.254.170.23  the EKS Pod Identity Agent, IPv4
//	fd00:ec2::23    the EKS Pod Identity Agent, IPv6
var (
	ecsContainerIP  = net.IPv4(169, 254, 170, 2)
	eksContainerIP  = net.IPv4(169, 254, 170, 23)
	eksContainerIP6 = net.ParseIP("fd00:ec2::23")
)

// containerLookupHost is overridden in tests. Name resolution is part of the SSRF
// boundary, so it has to be substitutable to be testable at all.
var containerLookupHost = net.LookupHost

func containerIPAllowed(ip net.IP) bool {
	return ip != nil && (ip.IsLoopback() ||
		ip.Equal(ecsContainerIP) || ip.Equal(eksContainerIP) || ip.Equal(eksContainerIP6))
}

func containerHostRefused(host, addr string) error {
	where := strconv.Quote(host)
	if addr != host {
		where += " (resolves to " + addr + ")"
	}
	return fmt.Errorf("refusing container credentials endpoint host %s: only loopback, "+
		"%s (ECS) and %s / %s (EKS Pod Identity) are permitted",
		where, ecsContainerIP, eksContainerIP, eksContainerIP6)
}

// containerResolveAllowed returns the addresses host may be dialed at, refusing
// unless EVERY address it resolves to is in the allow-list. One bad answer in a
// round-robin set is enough to send the request — and the authorization token —
// somewhere it should not go, so "any" is not good enough here.
func containerResolveAllowed(host string) ([]string, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !containerIPAllowed(ip) {
			return nil, containerHostRefused(host, host)
		}
		return []string{host}, nil
	}
	addrs, err := containerLookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("resolve container credentials endpoint host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("container credentials endpoint host %q resolves to no addresses", host)
	}
	for _, a := range addrs {
		if !containerIPAllowed(net.ParseIP(a)) {
			return nil, containerHostRefused(host, a)
		}
	}
	return addrs, nil
}

// validateContainerEndpoint is the SSRF gate on the full-URI form — the one place
// an operator-supplied (or injected) host reaches this code.
//
// This is deliberately STRICTER than the AWS SDKs, which apply the host allow-list
// only to http:// URLs and let https:// reach any host at all. An exporter with an
// outbound-credential-fetch primitive pointed at an arbitrary TLS host is an egress
// channel, and no real ECS or EKS deployment needs one: both agents are on the
// documented link-local addresses over plain HTTP. Fail closed.
func validateContainerEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse container credentials endpoint: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("container credentials endpoint scheme %q is not http or https", u.Scheme)
	}
	// "http://169.254.170.2@evil.example.com/" has host evil.example.com. The
	// allow-list catches that anyway; refusing userinfo outright removes the
	// lookalike from error messages and logs too.
	if u.User != nil {
		return errors.New("container credentials endpoint must not carry userinfo")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("container credentials endpoint has no host")
	}
	_, err = containerResolveAllowed(host)
	return err
}

// containerEndpoint reports the URL the container credential provider should call,
// or ok=false when the environment configures no such endpoint.
//
// The relative form wins over the full form, which is the precedence AWS
// documents: AWS_CONTAINER_CREDENTIALS_FULL_URI "will only be used if
// AWS_CONTAINER_CREDENTIALS_RELATIVE_URI is not set".
func containerEndpoint() (string, bool, error) {
	if rel := os.Getenv(envContainerRelativeURI); rel != "" {
		// A path, not a URL. Without this gate a value of "@evil.example.com/creds"
		// composes into "http://169.254.170.2@evil.example.com/creds", whose host is
		// evil.example.com — the AWS SDKs do not check the composed relative form at
		// all, so this is the gap that closes it.
		if !strings.HasPrefix(rel, "/") {
			return "", false, fmt.Errorf("%s must be a path starting with %q", envContainerRelativeURI, "/")
		}
		endpoint := ecsContainerBase + rel
		// Belt and braces: validate what was composed, not what was intended.
		if err := validateContainerEndpoint(endpoint); err != nil {
			return "", false, err
		}
		return endpoint, true, nil
	}
	if full := os.Getenv(envContainerFullURI); full != "" {
		if err := validateContainerEndpoint(full); err != nil {
			return "", false, err
		}
		return full, true, nil
	}
	return "", false, nil
}

// containerGuardedDial re-checks, at connect time, that the destination is still in
// the allow-list, and dials the checked address by literal.
//
// Validating the endpoint URL resolves the name once. Without this, a DNS answer
// that changes between that check and the connection — rebinding — would deliver
// the request and its authorization token somewhere else entirely.
func containerGuardedDial(
	dial func(context.Context, string, string) (net.Conn, error),
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		addrs, err := containerResolveAllowed(host)
		if err != nil {
			return nil, err
		}
		// By literal: letting the dialer resolve again would reopen the very window
		// this check exists to close.
		var lastErr error
		for _, a := range addrs {
			conn, dialErr := dial(ctx, network, net.JoinHostPort(a, port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
}

// containerHTTPClient derives the client used for the credential fetch from the
// shared one, keeping its timeout and TLS configuration but adding the dial guard.
//
// A caller that injects a RoundTripper which is not an *http.Transport cannot be
// wrapped this way; the endpoint URL is still validated, and nothing in this
// repository does that outside tests.
func containerHTTPClient(base *http.Client) *http.Client {
	tr, ok := base.Transport.(*http.Transport)
	if base.Transport == nil {
		tr, ok = http.DefaultTransport.(*http.Transport)
	}
	transport := base.Transport
	if ok {
		clone := tr.Clone()
		dial := clone.DialContext
		if dial == nil {
			dial = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		}
		clone.DialContext = containerGuardedDial(dial)
		transport = clone
	}
	return &http.Client{Transport: transport, Timeout: base.Timeout, CheckRedirect: containerRefuseRedirect}
}

// containerRefuseRedirect refuses every redirect the credential endpoint offers.
//
// Where a redirect goes is the endpoint's choice, not ours. Go drops the
// Authorization header on a cross-domain redirect but keeps it for the same domain
// OR ANY SUBDOMAIN of it (net/http shouldCopyHeaderOnRedirect), so a redirect is
// enough to hand the token to a different host. And regardless of the token, an
// endpoint that answers 302 would otherwise substitute another host's credentials
// for its own — which is exactly what the test for this covers.
//
// The message names no URL on purpose: a Location value the endpoint chose could
// itself carry the token, and nothing downstream redacts this text.
func containerRefuseRedirect(_ *http.Request, via []*http.Request) error {
	return fmt.Errorf("container credentials endpoint returned a redirect after %d request(s); refusing to follow", len(via))
}

// containerAuthTokenPath validates the token-file path before anything opens it,
// and returns the cleaned path that is then read.
//
// AWS documents AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE as an ABSOLUTE path, so
// requiring one is the contract rather than an invention — and it means the value
// cannot be resolved against whatever working directory the process was started in.
// A ".." segment is refused rather than silently cleaned away: reading a different
// file than the one configured is the wrong way for a credential to fail.
func containerAuthTokenPath(raw string) (string, error) {
	if !filepath.IsAbs(raw) {
		return "", fmt.Errorf("%s must be an absolute path", envContainerAuthFile)
	}
	clean := filepath.Clean(raw)
	if clean != raw {
		return "", fmt.Errorf("%s must be a clean absolute path, with no %q or empty segments",
			envContainerAuthFile, "..")
	}
	return clean, nil
}

// containerAuthToken resolves the value to send as the Authorization header, or ""
// when the environment issues no token (plain ECS task roles do not).
//
// The FILE form wins over the inline form, which is AWS's documented precedence
// and the useful way round: the file is what a rotating agent updates, so
// preferring a stale inline value would work exactly once. It is re-read on every
// fetch for the same reason the web identity token file is.
//
// The value is sent verbatim as the whole header value — AWS's own example is
// "Basic abcd" — so this is not a bearer token to be prefixed.
func containerAuthToken() (string, error) {
	token, source := os.Getenv(envContainerAuthToken), envContainerAuthToken
	if raw := os.Getenv(envContainerAuthFile); raw != "" {
		path, err := containerAuthTokenPath(raw)
		if err != nil {
			return "", err
		}
		// containerAuthTokenPath already proved path == filepath.Clean(path), so the
		// Clean here is a no-op at runtime. Keep it: it is what gosec's taint
		// analysis recognizes as the sanitizer for G703, and removing it turns the
		// build red without changing behavior at all.
		b, err := safefile.ReadRegular(filepath.Clean(path), safefile.MaxSecretBytes, safefile.AllowSymlink)
		if err != nil {
			// The path is operator-supplied config, not a secret; the CONTENTS are,
			// and os.ReadFile's error never carries them.
			return "", fmt.Errorf("read %s: %w", envContainerAuthFile, err)
		}
		token, source = string(b), envContainerAuthFile
	}
	// Agents write the token with a trailing newline, so trim before checking.
	token = strings.TrimSpace(token)
	// A control character left inside the value is a request-splitting primitive:
	// whatever wrote the token could append headers of its own. net/http would
	// refuse the header too, but its error names no variable and the guard would be
	// incidental. Reject it here, and never quote the value back.
	if strings.ContainsFunc(token, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("%s contains a control character; refusing to send it as a header value", source)
	}
	return token, nil
}

// containerStaticTTL is the lifetime given to a credential document that carries no
// Expiration. AWS treats that shape as static, but zero here means "expired" to the
// shared cache, which would then re-fetch on every signed request. Long enough to
// clear expiryMargin comfortably, short enough that keys rotating behind a
// silent agent are still picked up.
const containerStaticTTL = 30 * time.Minute

// containerMaxCredentialBody caps what is read from the endpoint. A credential
// document is a few hundred bytes, so this is already generous; the shared reader's
// own limit is orders of magnitude larger than anything legitimate here.
const containerMaxCredentialBody = 64 << 10

// boundedBody re-caps a response body without bypassing the package's single
// drain-and-close path.
type boundedBody struct {
	io.Reader
	io.Closer
}

// containerCredentials fetches one credential document from the ECS agent or the
// EKS Pod Identity Agent.
//
// Like IMDS and the web identity exchange this call is UNSIGNED: the pod's or
// task's position on the network — plus the authorization token where one is
// issued — is the whole proof of identity, so there is no chicken-and-egg.
func containerCredentials(ctx context.Context, hc *http.Client, endpoint string, now func() time.Time) (Credentials, error) {
	token, err := containerAuthToken()
	if err != nil {
		return Credentials{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Credentials{}, fmt.Errorf("container credentials: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("container credentials: %w", err)
	}
	// Cap the body before the shared reader drains it: a credential document cannot
	// legitimately be large, and this is an endpoint whose address came out of the
	// environment.
	resp.Body = boundedBody{Reader: io.LimitReader(resp.Body, containerMaxCredentialBody), Closer: resp.Body}
	body, err := readAllClose(resp)
	if err != nil {
		return Credentials{}, fmt.Errorf("container credentials: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("container credentials: %s", resp.Status)
	}
	// The document is identical in shape to the one IMDS serves.
	var c imdsCredentials
	if err := json.Unmarshal(body, &c); err != nil {
		// Decoder errors can quote attacker-controlled typed values (notably an
		// invalid Expiration passed through time.ParseError). Keep the operational
		// error closed rather than wrapping remote response material.
		return Credentials{}, errors.New("decode container credentials: invalid credential document")
	}
	if c.AccessKeyID == "" {
		return Credentials{}, errors.New("container credentials endpoint returned no credentials")
	}
	expires := c.Expiration
	if expires.IsZero() {
		// A document with no Expiration is "static" to AWS. Give it a lifetime
		// anyway: zero reads as expired to the shared cache, which would turn every
		// signed request into another fetch.
		expires = now().Add(containerStaticTTL)
	}
	return Credentials{
		AccessKeyID:     c.AccessKeyID,
		SecretAccessKey: c.SecretAccessKey,
		SessionToken:    c.Token,
		Expires:         expires,
	}, nil
}
