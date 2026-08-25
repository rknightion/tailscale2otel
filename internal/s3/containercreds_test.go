package s3

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// containerTestServer stands in for the ECS agent or the EKS Pod Identity Agent.
// It also becomes the IMDS base for the duration of the test, so a chain that
// falls through to the instance profile fails immediately and says so rather than
// stalling on a link-local address that is not routable from a test runner.
func containerTestServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ecsContainerBase = srv.URL
	imdsBase = srv.URL
	t.Cleanup(func() {
		ecsContainerBase = defaultECSContainerBase
		imdsBase = "http://169.254.169.254"
	})
	return srv
}

const containerCredsJSON = `{"AccessKeyId":"ASIACONTAINER","SecretAccessKey":"SECRETCONTAINER",
  "Token":"TOKENCONTAINER","Expiration":"2026-07-24T15:00:00Z"}`

// ECS task roles: the agent serves credentials at a path supplied relative to the
// fixed ECS metadata host, so the host is not operator-controlled at all.
func TestAmbient_ContainerRelativeURI(t *testing.T) {
	clearAWSEnv(t)
	var gotURI, gotAccept, gotMethod string
	srv := containerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v2/credentials/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotURI, gotAccept, gotMethod = r.URL.RequestURI(), r.Header.Get("Accept"), r.Method
		_, _ = io.WriteString(w, containerCredsJSON)
	})

	t.Setenv(envContainerRelativeURI, "/v2/credentials/abc?a=1")

	c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if c.AccessKeyID != "ASIACONTAINER" || c.SessionToken != "TOKENCONTAINER" {
		t.Errorf("credentials = %+v, want the container endpoint's", c)
	}
	if c.Expires.IsZero() {
		t.Error("no expiry; container credentials would never be refreshed")
	}
	if gotURI != "/v2/credentials/abc?a=1" {
		t.Errorf("request URI = %q, want the relative URI appended to the ECS host verbatim", gotURI)
	}
	if gotMethod != http.MethodGet || gotAccept != "application/json" {
		t.Errorf("request = %s with Accept %q, want GET with application/json", gotMethod, gotAccept)
	}
}

// EKS Pod Identity supplies a full URI plus a token file. The token is sent as the
// Authorization header value VERBATIM — it is not a bearer token this code wraps,
// which is why AWS's own example value is "Basic abcd".
func TestAmbient_ContainerFullURIWithTokenFromEnv(t *testing.T) {
	clearAWSEnv(t)
	var gotAuth string
	srv := containerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/credentials" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, containerCredsJSON)
	})

	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")
	t.Setenv(envContainerAuthToken, "Basic abcd")

	c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if c.AccessKeyID != "ASIACONTAINER" {
		t.Errorf("credentials = %+v, want the container endpoint's", c)
	}
	if gotAuth != "Basic abcd" {
		t.Errorf("Authorization = %q, want the token verbatim", gotAuth)
	}
}

// The token FILE beats the plain token variable. That is AWS's documented
// precedence and it is the useful way round: the file is what a rotating agent
// updates, so honoring a stale inline value instead would fail after the first
// rotation.
func TestAmbient_ContainerAuthTokenFileWinsAndIsRereadOnRefresh(t *testing.T) {
	clearAWSEnv(t)
	var sent []string
	srv := containerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/credentials" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		sent = append(sent, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, containerCredsJSON)
	})

	tokenPath := filepath.Join(t.TempDir(), "eks-pod-identity-token")
	if err := os.WriteFile(tokenPath, []byte("FIRSTTOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")
	t.Setenv(envContainerAuthToken, "SHOULD-BE-IGNORED")
	t.Setenv(envContainerAuthFile, tokenPath)

	// A clock past the document's expiry, so the second call really refetches.
	p := AmbientProvider(srv.Client(), func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) })
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("SECONDTOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The trailing newline the agent writes must be trimmed, not sent: a newline in
	// a header value is a request-splitting primitive.
	want := []string{"FIRSTTOKEN", "SECONDTOKEN"}
	if len(sent) != len(want) || sent[0] != want[0] || sent[1] != want[1] {
		t.Errorf("Authorization headers = %q, want %q (the file re-read on refresh)", sent, want)
	}
}

// An unreadable token file must fail the fetch rather than quietly authenticating
// with nothing — an endpoint that accepts an unauthenticated request is not the
// endpoint we think we are talking to.
func TestAmbient_ContainerAuthTokenFileUnreadable(t *testing.T) {
	clearAWSEnv(t)
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, containerCredsJSON)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")
	t.Setenv(envContainerAuthFile, filepath.Join(t.TempDir(), "absent"))

	_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("a missing authorization token file was ignored")
	}
	if !strings.Contains(err.Error(), envContainerAuthFile) {
		t.Errorf("error %q does not name the variable that is wrong", err)
	}
}

// AWS documents the token file as an ABSOLUTE path. Requiring one is therefore the
// contract, not an invention, and it stops a relative value being resolved against
// whatever working directory the process happened to start in. A ".." segment is
// refused rather than cleaned: reading a different file than was configured is the
// wrong way to fail for a credential.
func TestAmbient_ContainerAuthTokenFileMustBeCleanAndAbsolute(t *testing.T) {
	clearAWSEnv(t)
	requests := 0
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = io.WriteString(w, containerCredsJSON)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

	for _, path := range []string{
		"relative/token",
		"./token",
		"../../etc/passwd",
		"/var/run/../../etc/shadow", // absolute, but traverses
		"/var/run//token",           // not clean
	} {
		t.Setenv(envContainerAuthFile, path)
		_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
		if err == nil {
			t.Errorf("%s=%q was accepted", envContainerAuthFile, path)
			continue
		}
		if !strings.Contains(err.Error(), envContainerAuthFile) {
			t.Errorf("error %q does not name the variable that is wrong", err)
		}
	}
	if requests != 0 {
		t.Errorf("%d requests were made despite an unusable token file", requests)
	}

	// The path EKS actually projects must still be accepted, so the guard cannot
	// have been drawn so tightly that it breaks the real deployment.
	if _, err := containerAuthTokenPath(
		"/var/run/secrets/pods.eks.amazonaws.com/serviceaccount/eks-pod-identity-token",
	); err != nil {
		t.Errorf("the path EKS Pod Identity projects was refused: %v", err)
	}
}

// A control character inside the token is a request-splitting primitive: sent as a
// header value it would let whatever wrote that token append headers of its own.
// The request must never be built, and the rejection must not quote the token.
func TestAmbient_ContainerRefusesTokenWithControlCharacter(t *testing.T) {
	clearAWSEnv(t)
	requests := 0
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = io.WriteString(w, containerCredsJSON)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")
	t.Setenv(envContainerAuthToken, "GOODPART\r\nX-Injected: yes")

	_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("a token carrying a newline was accepted")
	}
	if requests != 0 {
		t.Errorf("%d requests were made; the token was rejected too late", requests)
	}
	for _, leak := range []string{"GOODPART", "X-Injected"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q quotes the rejected token", err)
		}
	}
	// net/http would refuse this header too, but leaning on that makes the guard
	// incidental and the diagnosis opaque ("invalid header field value" names no
	// variable). Reject it here, where the error can say which one is wrong.
	if !strings.Contains(err.Error(), "control character") || !strings.Contains(err.Error(), envContainerAuthToken) {
		t.Errorf("error %q does not explain that the configured token is itself malformed", err)
	}
}

// The endpoint is only as trustworthy as whatever set the environment variable
// pointing at it. Its response body must never enter a general-purpose error:
// exact-token replacement cannot cover encoded or otherwise transformed secrets.
func TestAmbient_ContainerErrorOmitsResponseBody(t *testing.T) {
	clearAWSEnv(t)
	const responseCanary = "BODY-CREDENTIAL-CANARY"
	srv := containerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"`+responseCanary+`","message":"rejected `+r.Header.Get("Authorization")+`"}`)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")
	t.Setenv(envContainerAuthToken, "Bearer SUPERSECRETPODTOKEN")

	_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("a 403 from the credential endpoint was treated as success")
	}
	for _, leak := range []string{"SUPERSECRETPODTOKEN", responseCanary, "[redacted]"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("remote response body material %q leaked into the error: %q", leak, err)
		}
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q does not retain the diagnostic HTTP status", err)
	}
}

func TestAmbient_ContainerDecodeErrorOmitsResponseBody(t *testing.T) {
	clearAWSEnv(t)
	const responseCanary = "MALFORMED-BODY-CREDENTIAL-CANARY"
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{not-json:"`+responseCanary+`"}`)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

	_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("malformed credential response was accepted")
	}
	if strings.Contains(err.Error(), responseCanary) {
		t.Fatalf("decode error leaked remote response body: %q", err)
	}
}

func TestAmbient_ContainerTypedDecodeErrorOmitsRemoteValue(t *testing.T) {
	clearAWSEnv(t)
	const responseCanary = "REMOTE-TIME-VALUE-CREDENTIAL-CANARY"
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"AccessKeyId":"AKIA","SecretAccessKey":"secret","Expiration":"`+responseCanary+`"}`)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

	_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("credential response with invalid typed value was accepted")
	}
	if strings.Contains(err.Error(), responseCanary) {
		t.Fatalf("typed decode error leaked remote response value: %q", err)
	}
}

// Chain placement, guarded in both directions. AWS puts the container provider
// after the environment and web identity and before IMDS, and on EKS that ordering
// is the documented migration path: a workload with an IRSA setup keeps using it
// until that setup is removed, even once a Pod Identity association exists.
func TestAmbient_ContainerChainPlacement(t *testing.T) {
	t.Run("environment wins", func(t *testing.T) {
		clearAWSEnv(t)
		srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("the container endpoint was called although the environment had keys")
			_, _ = io.WriteString(w, containerCredsJSON)
		})
		t.Setenv(envAccessKey, "AKENV")
		t.Setenv(envSecretKey, "SKENV")
		t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

		c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
		if err != nil || c.AccessKeyID != "AKENV" {
			t.Errorf("Retrieve = %+v, %v; want the environment's", c, err)
		}
	})

	t.Run("web identity wins", func(t *testing.T) {
		clearAWSEnv(t)
		srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			t.Error("the container endpoint was called although web identity was configured")
			_, _ = io.WriteString(w, containerCredsJSON)
		})
		sts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, stsOK)
		}))
		defer sts.Close()
		stsHost = sts.URL
		t.Cleanup(func() { stsHost = "" })

		tokenPath := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(tokenPath, []byte("OIDCTOKEN"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(envRoleARN, "arn:aws:iam::1:role/flows")
		t.Setenv(envTokenFile, tokenPath)
		t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

		c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
		if err != nil || c.AccessKeyID != "ASIAWEB" {
			t.Errorf("Retrieve = %+v, %v; want STS's", c, err)
		}
	})

	t.Run("relative URI wins over full URI", func(t *testing.T) {
		clearAWSEnv(t)
		var paths []string
		srv := containerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			_, _ = io.WriteString(w, containerCredsJSON)
		})
		t.Setenv(envContainerRelativeURI, "/relative")
		t.Setenv(envContainerFullURI, srv.URL+"/full")

		if _, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(paths) != 1 || paths[0] != "/relative" {
			t.Errorf("paths = %q, want only the relative form", paths)
		}
	})

	// Switching IMDS off is not a statement about the container endpoint: a task or
	// pod that has done so still has one, and it is the credential source that works.
	t.Run("used even when IMDS is disabled", func(t *testing.T) {
		clearAWSEnv(t)
		srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, containerCredsJSON)
		})
		t.Setenv(envIMDSDisabled, "true")
		t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

		c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
		if err != nil || c.AccessKeyID != "ASIACONTAINER" {
			t.Errorf("Retrieve = %+v, %v; want the container endpoint's", c, err)
		}
	})
}

// Refresh is the shared cachingProvider's job, not a second mechanism bolted on
// here: one fetch while the document is comfortably valid, another once the margin
// is reached.
func TestAmbient_ContainerRefreshesOnlyNearExpiry(t *testing.T) {
	clearAWSEnv(t)
	const expiryText = "2026-07-24T13:00:00Z"
	expiry, err := time.Parse(time.RFC3339, expiryText)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		_, _ = io.WriteString(w, `{"AccessKeyId":"ASIACONTAINER","SecretAccessKey":"S","Token":"T",
		  "Expiration":"`+expiryText+`"}`)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

	now := expiry.Add(-time.Hour)
	p := AmbientProvider(srv.Client(), func() time.Time { return now })
	for range 5 {
		if _, err := p.Retrieve(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if fetches != 1 {
		t.Errorf("fetched %d times, want once — the document is valid for an hour", fetches)
	}

	now = expiry.Add(-expiryMargin)
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fetches != 2 {
		t.Errorf("fetched %d times, want a refresh inside the expiry margin", fetches)
	}
}

// AWS documents a credential document with no Expiration as static. Left at the
// zero time it looks permanently expired to the shared cache, which would then
// re-fetch on every single signed request — one HTTP round trip per S3 GET.
func TestAmbient_ContainerWithoutExpirationIsNotRefetchedPerRequest(t *testing.T) {
	clearAWSEnv(t)
	fetches := 0
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		_, _ = io.WriteString(w, `{"AccessKeyId":"ASIASTATIC","SecretAccessKey":"S"}`)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	p := AmbientProvider(srv.Client(), func() time.Time { return now })
	for range 5 {
		c, err := p.Retrieve(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if c.AccessKeyID != "ASIASTATIC" {
			t.Fatalf("credentials = %+v", c)
		}
	}
	if fetches != 1 {
		t.Errorf("fetched %d times for 5 requests; a document with no expiry must still be cached", fetches)
	}
	// Not cached forever either: an agent that stops issuing an expiry is not a
	// promise that the keys behind it never rotate.
	now = now.Add(containerStaticTTL)
	if _, err := p.Retrieve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fetches != 2 {
		t.Errorf("fetched %d times; the synthesized lifetime never expired", fetches)
	}
}

// stubContainerLookup replaces DNS for the duration of a test. Resolution is part
// of the SSRF boundary, so it has to be deterministic rather than whatever the
// runner's resolver happens to say.
func stubContainerLookup(t *testing.T, fn func(string) ([]string, error)) {
	t.Helper()
	prev := containerLookupHost
	containerLookupHost = fn
	t.Cleanup(func() { containerLookupHost = prev })
}

// The full-URI form is the only place an operator-supplied host reaches this code,
// so it is the whole SSRF surface. Permitted: loopback, and the three documented
// ECS/EKS metadata addresses. Everything else is refused, including over TLS.
func TestValidateContainerEndpoint(t *testing.T) {
	stubContainerLookup(t, func(host string) ([]string, error) {
		switch host {
		case "localhost":
			return []string{"127.0.0.1", "::1"}, nil
		case "agent.internal":
			return []string{"169.254.170.23"}, nil
		case "half-loopback.internal":
			return []string{"127.0.0.1", "203.0.113.9"}, nil
		case "unknown.internal":
			return nil, errors.New("no such host")
		}
		return []string{"203.0.113.9"}, nil
	})

	allowed := []string{
		"http://169.254.170.2/v2/credentials/abc",    // ECS task role
		"http://169.254.170.23/v1/credentials",       // EKS Pod Identity, IPv4
		"http://[fd00:ec2::23]/v1/credentials",       // EKS Pod Identity, IPv6
		"http://127.0.0.1:8080/get-credentials",      // a sidecar on loopback
		"http://[::1]/get-credentials",               // loopback, IPv6
		"https://127.0.0.1/get-credentials",          // TLS to loopback
		"http://localhost/get-credentials",           // resolves entirely to loopback
		"http://agent.internal/v1/credentials",       // resolves to the EKS address
		"http://169.254.170.2:80/v2/credentials/abc", // explicit default port
	}
	for _, raw := range allowed {
		if err := validateContainerEndpoint(raw); err != nil {
			t.Errorf("validateContainerEndpoint(%q) = %v, want accepted", raw, err)
		}
	}

	refused := []string{
		"",                          // nothing at all
		"not a url",                 // no scheme, no host
		"http://",                   // no host
		"/v2/credentials/abc",       // relative, so no host to check
		"ftp://169.254.170.2/creds", // only http(s) may be spoken here
		"file:///etc/passwd",        // a scheme with no host at all
		"http://169.254.169.254/latest/meta-data/",    // IMDS is a DIFFERENT provider
		"http://169.254.170.3/creds",                  // adjacent to ECS, not ECS
		"http://169.254.170.22/creds",                 // adjacent to EKS, not EKS
		"http://[fd00:ec2::24]/creds",                 // adjacent to the EKS IPv6
		"http://10.0.0.1/creds",                       // private, but not ours
		"http://192.168.1.1/creds",                    // ditto
		"https://evil.example.com/creds",              // TLS does not make it ours
		"http://169.254.170.2@evil.example.com/creds", // userinfo that reads like the ECS host
		"http://user:pass@127.0.0.1/creds",            // credentials in a credentials URL
		"http://half-loopback.internal/creds",         // ONE bad address is enough
		"http://unknown.internal/creds",               // unresolvable
		"http://203.0.113.9/creds",                    // plainly elsewhere
	}
	for _, raw := range refused {
		if err := validateContainerEndpoint(raw); err == nil {
			t.Errorf("validateContainerEndpoint(%q) = nil, want refused", raw)
		}
	}
}

// End to end: a disallowed full URI must fail the chain loudly. Silently falling
// through to IMDS would turn a misconfiguration — or an injected variable — into a
// confusing 403 much later instead of an error here.
func TestAmbient_ContainerRefusesDisallowedFullURI(t *testing.T) {
	clearAWSEnv(t)
	requests := 0
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = io.WriteString(w, containerCredsJSON)
	})
	t.Setenv(envContainerFullURI, "http://169.254.169.254/latest/meta-data/iam/security-credentials/")

	_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("an endpoint outside the allow-list was accepted")
	}
	if requests != 0 {
		t.Errorf("%d requests were made; the endpoint was checked too late", requests)
	}
	if !strings.Contains(err.Error(), "169.254.169.254") {
		t.Errorf("error %q does not name the host it refused", err)
	}
}

// A name that passes validation and then resolves somewhere else — DNS rebinding —
// must not get a connection. Validating the URL resolves the name once; the dial
// has to re-check, or the window between them is an egress channel for the token.
func TestAmbient_ContainerRefusesRebindAtDialTime(t *testing.T) {
	clearAWSEnv(t)
	requests := 0
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = io.WriteString(w, containerCredsJSON)
	})

	lookups := 0
	stubContainerLookup(t, func(string) ([]string, error) {
		lookups++
		if lookups == 1 {
			return []string{"127.0.0.1"}, nil // what validation sees
		}
		return []string{"203.0.113.9"}, nil // what the dial would get
	})

	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	t.Setenv(envContainerFullURI, "http://agent.rebinding.invalid:"+port+"/v1/credentials")

	_, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatal("a rebound hostname got a connection")
	}
	if lookups < 2 {
		t.Errorf("%d lookups; the dial reused validation's answer instead of re-checking", lookups)
	}
	if requests != 0 {
		t.Errorf("%d requests were made; the rebind was not caught", requests)
	}
	// It must be OUR refusal that stopped it, not an incidental resolution failure.
	if !strings.Contains(err.Error(), "203.0.113.9") {
		t.Errorf("error %q does not name the address the dial refused", err)
	}
}

// The endpoint must not be able to bounce the fetch anywhere. Go drops the
// Authorization header on a cross-domain redirect but keeps it for the same domain
// or any subdomain, and the target is the endpoint's choice rather than ours — so
// no redirect is followed at all.
func TestAmbient_ContainerRefusesRedirect(t *testing.T) {
	clearAWSEnv(t)
	elsewhere := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere++
		_, _ = io.WriteString(w, `{"AccessKeyId":"ASIAATTACKER","SecretAccessKey":"S","Token":"T",
		  "Expiration":"2026-07-24T15:00:00Z"}`)
	}))
	defer target.Close()

	srv := containerTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/credentials", http.StatusFound)
	})
	t.Setenv(envContainerFullURI, srv.URL+"/v1/credentials")
	t.Setenv(envContainerAuthToken, "Bearer PODTOKEN")

	c, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background())
	if err == nil {
		t.Fatalf("a redirect was followed; got credentials %+v", c)
	}
	if elsewhere != 0 {
		t.Errorf("the redirect target was contacted %d times", elsewhere)
	}
	if c.AccessKeyID != "" {
		t.Errorf("credentials = %+v, want none", c)
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error %q does not say a redirect was refused", err)
	}
	if strings.Contains(err.Error(), "PODTOKEN") {
		t.Errorf("the authorization token leaked into the redirect error: %q", err)
	}
}

// The relative form supplies a path, not a URL. Anything that could move the host
// out from under the fixed ECS address has to be refused before it is composed.
func TestAmbient_ContainerRelativeURIMustBeAPath(t *testing.T) {
	clearAWSEnv(t)
	srv := containerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, containerCredsJSON)
	})
	for _, rel := range []string{"@evil.example.com/creds", "v2/credentials", "http://evil.example.com/creds"} {
		t.Setenv(envContainerRelativeURI, rel)
		if _, err := AmbientProvider(srv.Client(), nil).Retrieve(context.Background()); err == nil {
			t.Errorf("%s=%q was accepted", envContainerRelativeURI, rel)
		}
	}
}

func TestContainerIPAllowed(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "127.0.0.2", "::1", "169.254.170.2", "169.254.170.23", "fd00:ec2::23"} {
		if !containerIPAllowed(net.ParseIP(s)) {
			t.Errorf("containerIPAllowed(%s) = false", s)
		}
	}
	for _, s := range []string{"169.254.169.254", "169.254.170.1", "169.254.170.24", "fd00:ec2::24", "0.0.0.0", "10.1.2.3", "203.0.113.9", "::"} {
		if containerIPAllowed(net.ParseIP(s)) {
			t.Errorf("containerIPAllowed(%s) = true", s)
		}
	}
}
