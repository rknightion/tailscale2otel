package s3

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clearAWSEnv isolates a test from whatever AWS variables the developer's shell
// or CI runner happens to carry. Without this the chain's order is untestable:
// a machine with AWS_ACCESS_KEY_ID set would short-circuit every case.
func clearAWSEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		envAccessKey, envSecretKey, envSessionTok, envRoleARN, envTokenFile,
		envRoleSession, envRegion, envSTSLegacy, envIMDSDisabled,
		envContainerRelativeURI, envContainerFullURI, envContainerAuthToken, envContainerAuthFile,
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestStaticProvider(t *testing.T) {
	p := StaticProvider{Credentials: Credentials{AccessKeyID: "AK", SecretAccessKey: "SK"}}
	c, err := p.Retrieve(context.Background())
	if err != nil || c.AccessKeyID != "AK" {
		t.Fatalf("Retrieve = %+v, %v", c, err)
	}
	if _, err := (StaticProvider{Credentials: Credentials{AccessKeyID: "AK"}}).Retrieve(context.Background()); err == nil {
		t.Error("incomplete static credentials were accepted")
	}
}

// The environment comes first, and it is the only path that works with no
// network at all — which is what makes it the right default for a container.
func TestAmbient_EnvironmentFirst(t *testing.T) {
	clearAWSEnv(t)
	t.Setenv(envAccessKey, "AKENV")
	t.Setenv(envSecretKey, "SKENV")
	t.Setenv(envSessionTok, "TOKENV")
	// Set up web identity too: the environment must win over it.
	t.Setenv(envRoleARN, "arn:aws:iam::1:role/r")
	t.Setenv(envTokenFile, "/nonexistent")

	c, err := AmbientProvider(nil, nil).Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if c.AccessKeyID != "AKENV" || c.SessionToken != "TOKENV" {
		t.Errorf("credentials = %+v, want the environment's", c)
	}
}

const stsOK = `<AssumeRoleWithWebIdentityResponse>
 <AssumeRoleWithWebIdentityResult><Credentials>
  <AccessKeyId>ASIAWEB</AccessKeyId>
  <SecretAccessKey>SECRETWEB</SecretAccessKey>
  <SessionToken>TOKENWEB</SessionToken>
  <Expiration>2026-07-24T15:00:00Z</Expiration>
 </Credentials></AssumeRoleWithWebIdentityResult>
</AssumeRoleWithWebIdentityResponse>`

// IRSA on EKS: a projected OIDC token on disk, traded with STS for keys.
func TestAmbient_WebIdentity(t *testing.T) {
	clearAWSEnv(t)
	var form string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form = string(b)
		// The exchange is unsigned by design: the OIDC token is the credential.
		if r.Header.Get("Authorization") != "" {
			t.Error("the web identity exchange was signed; it has no credentials to sign with")
		}
		_, _ = io.WriteString(w, stsOK)
	}))
	defer srv.Close()
	stsHost = srv.URL
	t.Cleanup(func() { stsHost = "" })

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("OIDCTOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envRoleARN, "arn:aws:iam::1:role/flows")
	t.Setenv(envTokenFile, tokenPath)

	c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if c.AccessKeyID != "ASIAWEB" || c.SessionToken != "TOKENWEB" {
		t.Errorf("credentials = %+v, want STS's", c)
	}
	if want := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC); !c.Expires.Equal(want) {
		t.Errorf("Expires = %v, want %v — without it the credentials are never refreshed", c.Expires, want)
	}
	// The trailing newline the kubelet writes must not reach STS.
	if !strings.Contains(form, "WebIdentityToken=OIDCTOKEN&") && !strings.HasSuffix(form, "WebIdentityToken=OIDCTOKEN") {
		t.Errorf("form = %q, want the token trimmed", form)
	}
	if !strings.Contains(form, "Action=AssumeRoleWithWebIdentity") {
		t.Errorf("form = %q", form)
	}
}

// A rotated token file must be re-read: the kubelet replaces it in place, and a
// cached copy stops working the moment the original expires.
func TestAmbient_WebIdentityRereadsTheTokenFile(t *testing.T) {
	clearAWSEnv(t)
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		for _, kv := range strings.Split(string(b), "&") {
			if v, ok := strings.CutPrefix(kv, "WebIdentityToken="); ok {
				tokens = append(tokens, v)
			}
		}
		_, _ = io.WriteString(w, stsOK)
	}))
	defer srv.Close()
	stsHost = srv.URL
	t.Cleanup(func() { stsHost = "" })

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("FIRST"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envRoleARN, "arn:aws:iam::1:role/flows")
	t.Setenv(envTokenFile, tokenPath)

	// A clock past the credentials' expiry, so the second call refreshes.
	p := AmbientProvider(srv.Client(), func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) })
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("SECOND"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 || tokens[1] != "SECOND" {
		t.Errorf("tokens sent = %v, want the rotated one on the refresh", tokens)
	}
}

// IMDSv2 only: the PUT handshake first, then the credentials with that token.
func TestAmbient_InstanceProfile(t *testing.T) {
	clearAWSEnv(t)
	var sawToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/latest/api/token":
			if r.Header.Get("X-aws-ec2-metadata-token-ttl-seconds") == "" {
				t.Error("the IMDSv2 handshake carried no TTL header")
			}
			_, _ = io.WriteString(w, "IMDSTOKEN")
		case r.URL.Path == "/latest/meta-data/iam/security-credentials/":
			sawToken = r.Header.Get("X-aws-ec2-metadata-token")
			_, _ = io.WriteString(w, "flows-role\n")
		case r.URL.Path == "/latest/meta-data/iam/security-credentials/flows-role":
			_, _ = io.WriteString(w, `{"AccessKeyId":"ASIAIMDS","SecretAccessKey":"SECRETIMDS",
			  "Token":"TOKENIMDS","Expiration":"2026-07-24T15:00:00Z"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	imdsBase = srv.URL
	t.Cleanup(func() { imdsBase = "http://169.254.169.254" })

	c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if c.AccessKeyID != "ASIAIMDS" || c.SessionToken != "TOKENIMDS" {
		t.Errorf("credentials = %+v, want the instance profile's", c)
	}
	if sawToken != "IMDSTOKEN" {
		t.Errorf("metadata token = %q; the v2 session token was not presented", sawToken)
	}
	if c.Expires.IsZero() {
		t.Error("no expiry; instance-profile credentials would never be refreshed")
	}
}

// Probing a link-local address from a machine that is not on EC2 costs a
// connection timeout on every refresh, so the documented opt-out is honored
// with a message naming what was tried.
func TestAmbient_IMDSCanBeDisabled(t *testing.T) {
	clearAWSEnv(t)
	t.Setenv(envIMDSDisabled, "true")

	_, err := AmbientProvider(nil, nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("credentials resolved with nothing configured")
	}
	for _, want := range []string{"environment", "web identity", "IMDS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestCachingProvider_RefreshesOnlyNearExpiry(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	calls := 0
	p := &cachingProvider{
		now: func() time.Time { return now },
		fetch: func(context.Context) (Credentials, error) {
			calls++
			return Credentials{AccessKeyID: "AK", SecretAccessKey: "SK", Expires: now.Add(time.Hour)}, nil
		},
	}
	for range 5 {
		if _, err := p.Retrieve(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want once — credentials valid for an hour", calls)
	}

	// Inside the margin: a signature computed now could expire in flight.
	now = now.Add(time.Hour - expiryMargin)
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("fetched %d times, want a refresh inside the expiry margin", calls)
	}
}

// A momentary failure to refresh must not stop ingestion while the credentials
// in hand still work — the alternative is an outage caused by a blip on a
// metadata endpoint.
func TestCachingProvider_KeepsWorkingCredentialsThroughARefreshFailure(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	fail := false
	p := &cachingProvider{
		now: func() time.Time { return now },
		fetch: func(context.Context) (Credentials, error) {
			if fail {
				return Credentials{}, errors.New("metadata endpoint unreachable")
			}
			return Credentials{AccessKeyID: "AK", SecretAccessKey: "SK", Expires: now.Add(time.Hour)}, nil
		},
	}
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}

	fail = true
	now = now.Add(time.Hour - time.Minute) // inside the margin, before expiry
	c, err := p.Retrieve(context.Background())
	if err != nil || c.AccessKeyID != "AK" {
		t.Errorf("Retrieve = %+v, %v; want the still-valid cached credentials", c, err)
	}

	// Past expiry the cached ones are worthless, and pretending otherwise turns
	// a clear error into an unexplained 403.
	now = now.Add(2 * time.Minute)
	if _, err := p.Retrieve(context.Background()); err == nil {
		t.Error("expired credentials were served after a refresh failure")
	}
}

// endlessReader never returns EOF: an endpoint that keeps talking forever, which
// is what the bound on readAllClose exists to survive.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// Splitting the listing bound out (#291) is only worth anything if the metadata
// bound stays where it was. Everything that reads a credential goes through
// readAllClose — the IMDSv2 handshake, the IMDS documents, the STS web-identity
// exchange, and the container endpoint whose address comes out of the environment
// — so a shared constant would mean the listing path's much larger allowance
// silently became theirs too.
func TestReadAllClose_MetadataBoundStaysSmallAndSeparate(t *testing.T) {
	if maxMetadataResponseBytes >= maxListResponseBytes {
		t.Errorf("metadata bound %d is not smaller than the listing bound %d; the two must not track each other",
			maxMetadataResponseBytes, maxListResponseBytes)
	}
	if maxMetadataResponseBytes > 1<<20 {
		t.Errorf("metadata bound = %d bytes; credential documents are a few hundred bytes, so this stays at ~1 MiB",
			maxMetadataResponseBytes)
	}
	// Tightest first: the container endpoint re-caps itself on top of the shared
	// bound, and that ordering is what keeps the environment-supplied address the
	// most constrained of the three.
	if containerMaxCredentialBody >= maxMetadataResponseBytes {
		t.Errorf("container bound %d is not tighter than the shared metadata bound %d",
			containerMaxCredentialBody, maxMetadataResponseBytes)
	}

	body, err := readAllClose(&http.Response{Body: io.NopCloser(endlessReader{})})
	if err != nil {
		t.Fatalf("readAllClose: %v", err)
	}
	if len(body) != maxMetadataResponseBytes {
		t.Errorf("read %d bytes from an endless body, want it stopped at the %d-byte metadata bound",
			len(body), maxMetadataResponseBytes)
	}
}

// The regional endpoint is what AWS recommends and what a VPC endpoint policy is
// written against.
func TestSTSEndpoint(t *testing.T) {
	clearAWSEnv(t)
	if got := stsEndpoint(); got != "https://sts.amazonaws.com/" {
		t.Errorf("no region: %q", got)
	}
	t.Setenv(envRegion, "eu-west-2")
	if got := stsEndpoint(); got != "https://sts.eu-west-2.amazonaws.com/" {
		t.Errorf("with region: %q", got)
	}
	t.Setenv(envSTSLegacy, "legacy")
	if got := stsEndpoint(); got != "https://sts.amazonaws.com/" {
		t.Errorf("legacy opt-out: %q", got)
	}
}
