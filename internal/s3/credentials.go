package s3

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/tailscale2otel/v5/internal/httpguard"
	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
)

// Credentials is one set of S3 credentials. SessionToken is set only for
// temporary credentials (IRSA, an instance profile, or an explicitly supplied
// STS session); it is a SIGNED header, not merely a sent one.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	// Expires is when these stop working. Zero means "never" — which is the
	// truthful answer for static keys and the wrong answer for anything else.
	Expires time.Time
}

// expiryMargin is how far ahead of expiry credentials are refreshed. Temporary
// credentials are typically minted for an hour, and a signature computed with
// credentials that expire in flight is a 403 rather than a retryable error, so
// the margin is generous.
const expiryMargin = 5 * time.Minute

// Provider supplies credentials, refreshing them when they are close to
// expiring. Retrieve is called once per request, so an implementation must be
// cheap in the steady state.
type Provider interface {
	Retrieve(ctx context.Context) (Credentials, error)
}

// StaticProvider returns the same credentials forever. This is what
// TS2OTEL_..._ACCESS_KEY_ID and its siblings produce.
type StaticProvider struct{ Credentials Credentials }

// Retrieve implements Provider.
func (p StaticProvider) Retrieve(context.Context) (Credentials, error) {
	if p.Credentials.AccessKeyID == "" || p.Credentials.SecretAccessKey == "" {
		return Credentials{}, errors.New("static credentials are incomplete")
	}
	return p.Credentials, nil
}

// cachingProvider wraps a fetch function with expiry-aware caching, so the
// underlying HTTP call happens once per credential lifetime rather than once per
// request.
type cachingProvider struct {
	fetch func(ctx context.Context) (Credentials, error)
	now   func() time.Time

	mu     sync.Mutex
	cached Credentials
}

func (p *cachingProvider) Retrieve(ctx context.Context) (Credentials, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now
	if p.now != nil {
		now = p.now
	}
	if p.cached.AccessKeyID != "" && now().Add(expiryMargin).Before(p.cached.Expires) {
		return p.cached, nil
	}
	c, err := p.fetch(ctx)
	if err != nil {
		// Deliberately keep serving cached credentials that have not actually
		// expired yet: a transient failure to refresh should not stop ingestion
		// while the current ones still work.
		if p.cached.AccessKeyID != "" && now().Before(p.cached.Expires) {
			return p.cached, nil
		}
		return Credentials{}, err
	}
	p.cached = c
	return c, nil
}

// The ambient environment this package understands. Deliberately a short list —
// see the package doc for what is NOT supported and why.
const (
	envAccessKey   = "AWS_ACCESS_KEY_ID"
	envSecretKey   = "AWS_SECRET_ACCESS_KEY" //nolint:gosec // the NAME of a variable, not a value
	envSessionTok  = "AWS_SESSION_TOKEN"
	envRoleARN     = "AWS_ROLE_ARN"
	envTokenFile   = "AWS_WEB_IDENTITY_TOKEN_FILE" //nolint:gosec // the NAME of a variable, not a value
	envRoleSession = "AWS_ROLE_SESSION_NAME"
	envSTSLegacy   = "AWS_STS_REGIONAL_ENDPOINTS"
	envRegion      = "AWS_REGION"
	// envIMDSDisabled is honored because probing a link-local address from a
	// host that is not on EC2 costs a connection timeout on every refresh.
	envIMDSDisabled = "AWS_EC2_METADATA_DISABLED"
)

// AmbientProvider resolves credentials from the environment the process runs in,
// in the order AWS itself uses, restricted to the deployment shapes this
// exporter actually runs in:
//
//  1. static credentials in the environment;
//  2. web identity (IRSA on EKS, workload identity federation);
//  3. the container credential endpoint (ECS task roles, EKS Pod Identity);
//  4. the EC2 instance profile, via IMDSv2.
//
// (2), (3) and (4) are UNSIGNED HTTP calls, which is what makes them tractable
// without an SDK: what they return is used to sign later requests, but obtaining
// them needs no signature and so no chicken-and-egg.
//
// (3) is the only step whose address comes out of the environment rather than being
// fixed, so it carries its own host allow-list and dial-time guard — see
// containercreds.go.
//
// httpClient and now are injectable for tests; nil selects the real ones.
func AmbientProvider(httpClient *http.Client, now func() time.Time) Provider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	// The container endpoint gets its own derived client: its address comes out of
	// the environment, so that fetch alone refuses redirects and re-checks the
	// destination at dial time.
	containerHC := containerHTTPClient(httpClient)
	stsHC := httpguard.NoRedirectClient(httpClient)
	imdsHC := imdsHTTPClient(httpClient)
	// Resolved separately from what cachingProvider is given, so the caching
	// behavior of every existing provider is left exactly as it was.
	containerNow := now
	if containerNow == nil {
		containerNow = time.Now
	}
	return &cachingProvider{now: now, fetch: func(ctx context.Context) (Credentials, error) {
		if ak, sk := os.Getenv(envAccessKey), os.Getenv(envSecretKey); ak != "" && sk != "" {
			return Credentials{
				AccessKeyID:     ak,
				SecretAccessKey: sk,
				SessionToken:    os.Getenv(envSessionTok),
			}, nil
		}
		if os.Getenv(envRoleARN) != "" && os.Getenv(envTokenFile) != "" {
			return webIdentity(ctx, stsHC)
		}
		// The container endpoint sits after web identity and before IMDS, which is
		// where AWS puts it. That ordering is load-bearing on EKS: a workload
		// migrating from IRSA to Pod Identity keeps using IRSA until its web
		// identity setup is removed, and AWS documents that as the safe migration
		// path. It is also checked BEFORE the IMDS opt-out, because a task or pod
		// that has switched IMDS off still has a container endpoint.
		endpoint, ok, err := containerEndpoint()
		if err != nil {
			return Credentials{}, err
		}
		if ok {
			return containerCredentials(ctx, containerHC, endpoint, containerNow)
		}
		if os.Getenv(envIMDSDisabled) == "true" {
			return Credentials{}, errors.New("no credentials: none in the environment, no web identity, " +
				"no container credentials endpoint, and IMDS is disabled")
		}
		return instanceProfile(ctx, imdsHC)
	}}
}

// stsResponse is the subset of AssumeRoleWithWebIdentity's XML that matters.
type stsResponse struct {
	XMLName xml.Name `xml:"AssumeRoleWithWebIdentityResponse"`
	Result  struct {
		Credentials struct {
			AccessKeyID     string    `xml:"AccessKeyId"`
			SecretAccessKey string    `xml:"SecretAccessKey"`
			SessionToken    string    `xml:"SessionToken"`
			Expiration      time.Time `xml:"Expiration"`
		} `xml:"Credentials"`
	} `xml:"AssumeRoleWithWebIdentityResult"`
}

// webIdentity exchanges a projected service-account token for temporary
// credentials — how IRSA works on EKS: the pod is given a short-lived OIDC token
// on disk, and STS trades it for keys.
//
// The token file is re-read on every refresh rather than cached, because the
// kubelet rotates it in place and a cached copy expires silently.
func webIdentity(ctx context.Context, hc *http.Client) (Credentials, error) {
	endpoint, err := stsEndpoint()
	if err != nil {
		return Credentials{}, err
	}
	token, err := safefile.ReadRegular(os.Getenv(envTokenFile), safefile.MaxSecretBytes, safefile.AllowSymlink)
	if err != nil {
		return Credentials{}, fmt.Errorf("read web identity token: %w", err)
	}
	sessionName := os.Getenv(envRoleSession)
	if sessionName == "" {
		sessionName = "tailscale2otel"
	}

	q := url.Values{
		"Action":           {"AssumeRoleWithWebIdentity"},
		"Version":          {"2011-06-15"},
		"RoleArn":          {os.Getenv(envRoleARN)},
		"RoleSessionName":  {sessionName},
		"WebIdentityToken": {strings.TrimSpace(string(token))},
	}
	// Unsigned by definition: the OIDC token IS the credential being presented.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(q.Encode()))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := hc.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("sts assume-role-with-web-identity: %w", err)
	}
	body, err := readAllClose(resp)
	if err != nil {
		return Credentials{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("sts assume-role-with-web-identity: %s", resp.Status)
	}
	var out stsResponse
	if err := xml.Unmarshal(body, &out); err != nil {
		return Credentials{}, fmt.Errorf("decode sts response: %w", err)
	}
	c := out.Result.Credentials
	if c.AccessKeyID == "" {
		return Credentials{}, errors.New("sts returned no credentials")
	}
	return Credentials{
		AccessKeyID:     c.AccessKeyID,
		SecretAccessKey: c.SecretAccessKey,
		SessionToken:    c.SessionToken,
		Expires:         c.Expiration,
	}, nil
}

// stsHost is overridden in tests. There is no reason to make it configurable in
// production — AWS_REGION already selects the regional endpoint.
var stsHost = ""

// stsEndpoint prefers the regional endpoint, which is what AWS recommends and
// what a VPC endpoint policy is written against.
var awsRegionPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)

func stsEndpoint() (string, error) {
	if stsHost != "" {
		return stsHost, nil
	}
	if region := os.Getenv(envRegion); region != "" && os.Getenv(envSTSLegacy) != "legacy" {
		if len(region) > 63 || !awsRegionPattern.MatchString(region) {
			return "", errors.New("AWS_REGION is not a valid AWS region identifier")
		}
		raw := "https://sts." + region + ".amazonaws.com/"
		u, err := url.Parse(raw)
		wantHost := "sts." + region + ".amazonaws.com"
		if err != nil || u.Scheme != "https" || u.Host != wantHost || u.Hostname() != wantHost || u.Port() != "" {
			return "", errors.New("AWS_REGION did not produce an approved STS endpoint")
		}
		return raw, nil
	}
	return "https://sts.amazonaws.com/", nil
}

// imdsBase is the link-local address every EC2 instance serves its metadata on.
// Overridden in tests.
const imdsLiteralBase = "http://169.254.169.254"

var imdsBase = imdsLiteralBase

// imdsHTTPClient prevents proxies, redirects, DNS and caller transport policy
// from moving metadata credentials off the literal IMDS address. Tests that
// override imdsBase keep their injected transport so they can use httptest.
func imdsHTTPClient(base *http.Client) *http.Client {
	c := httpguard.NoRedirectClient(base)
	if imdsBase != imdsLiteralBase {
		return c
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if base != nil && base.Transport != nil {
		if configured, ok := base.Transport.(*http.Transport); ok {
			transport = configured.Clone()
		}
	}
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, "169.254.169.254:80")
	}
	c.Transport = transport
	return c
}

// imdsCredentials is the JSON the instance-profile endpoint returns.
type imdsCredentials struct {
	AccessKeyID     string    `json:"AccessKeyId"`
	SecretAccessKey string    `json:"SecretAccessKey"`
	Token           string    `json:"Token"`
	Expiration      time.Time `json:"Expiration"`
}

// instanceProfile reads credentials from the EC2 instance metadata service.
//
// IMDSv2 only. The session-token handshake is what makes the endpoint immune to
// the SSRF class that made IMDSv1 a liability, every current instance supports
// it, and an instance configured to require v2 rejects v1 outright — so there is
// no fallback that is both safe and useful.
func instanceProfile(ctx context.Context, hc *http.Client) (Credentials, error) {
	tokReq, err := http.NewRequestWithContext(ctx, http.MethodPut, imdsBase+"/latest/api/token", nil)
	if err != nil {
		return Credentials{}, err
	}
	tokReq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	tokResp, err := hc.Do(tokReq)
	if err != nil {
		return Credentials{}, fmt.Errorf("imds token: %w", err)
	}
	token, err := readAllClose(tokResp)
	if err != nil {
		return Credentials{}, fmt.Errorf("imds token: %w", err)
	}
	if tokResp.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("imds token: %s", tokResp.Status)
	}

	role, err := imdsGet(ctx, hc, string(token), "/latest/meta-data/iam/security-credentials/")
	if err != nil {
		return Credentials{}, err
	}
	// The listing can carry several roles, one per line; an instance profile
	// holds exactly one, and the first is it.
	roleName := strings.TrimSpace(strings.SplitN(string(role), "\n", 2)[0])
	if roleName == "" {
		return Credentials{}, errors.New("imds: instance has no role attached")
	}

	body, err := imdsGet(ctx, hc, string(token), "/latest/meta-data/iam/security-credentials/"+roleName)
	if err != nil {
		return Credentials{}, err
	}
	var c imdsCredentials
	if err := json.Unmarshal(body, &c); err != nil {
		return Credentials{}, fmt.Errorf("decode imds credentials: %w", err)
	}
	if c.AccessKeyID == "" {
		return Credentials{}, errors.New("imds returned no credentials")
	}
	return Credentials{
		AccessKeyID:     c.AccessKeyID,
		SecretAccessKey: c.SecretAccessKey,
		SessionToken:    c.Token,
		Expires:         c.Expiration,
	}, nil
}

func imdsGet(ctx context.Context, hc *http.Client, token, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("imds %s: %w", path, err)
	}
	body, err := readAllClose(resp)
	if err != nil {
		return nil, fmt.Errorf("imds %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("imds %s: %s", path, resp.Status)
	}
	return body, nil
}

// maxMetadataResponseBytes caps every credential and metadata response this
// package reads: the IMDSv2 token handshake, the IMDS metadata documents, the STS
// web-identity exchange, and the container credential endpoint. Each of those is a
// few hundred bytes on the wire — the container path re-caps itself to 64 KiB on
// top (containerMaxCredentialBody) — so 1 MiB is already ~1000x anything
// legitimate, and exists only so a hostile or misconfigured endpoint cannot make
// the process read forever.
//
// It is deliberately SEPARATE from maxListResponseBytes and must stay small. One
// shared cap for both was the #291 bug: object listings legitimately need a much
// larger bound, and with a single constant, widening it for them would hand the
// same allowance to endpoints whose address comes out of the environment.
const maxMetadataResponseBytes = 1 << 20

// readAllClose drains and closes a response body, bounded so a hostile or
// misconfigured endpoint cannot make the process read forever.
//
// This is the buffering path, for documents small enough that holding one in
// memory is free. Object listings do NOT come through here: they are stream
// decoded against their own, larger bound — see decodeListResponse in s3.go.
func readAllClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxMetadataResponseBytes))
}
