package tsapi

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mustOrigin(t *testing.T, raw string) origin {
	t.Helper()
	o, err := parseOrigin(raw)
	if err != nil {
		t.Fatalf("parseOrigin(%q): %v", raw, err)
	}
	return o
}

// TestOriginEqual pins the normalization rules the whole #466/#467 control
// rests on. Each "not equal" row is a trust boundary a redirect must not cross.
func TestOriginEqual(t *testing.T) {
	cases := []struct {
		name  string
		a, b  string
		equal bool
	}{
		{"identical", "https://api.tailscale.com", "https://api.tailscale.com", true},
		{"implicit vs explicit default port", "https://api.tailscale.com", "https://api.tailscale.com:443", true},
		{"case-insensitive scheme and host", "HTTPS://API.Tailscale.COM", "https://api.tailscale.com", true},
		{"path and query are not part of the origin", "https://api.tailscale.com/a?x=1", "https://api.tailscale.com/b", true},
		{"scheme downgrade", "https://api.tailscale.com", "http://api.tailscale.com", false},
		{"alternate port", "https://api.tailscale.com", "https://api.tailscale.com:8443", false},
		{"different host", "https://api.tailscale.com", "https://evil.example", false},
		{"subdomain is a different origin", "https://api.tailscale.com", "https://evil.api.tailscale.com", false},
		{"userinfo makes it a distinct origin", "https://api.tailscale.com", "https://user:pw@api.tailscale.com", false},
		{"different userinfo", "https://a:1@api.tailscale.com", "https://b:2@api.tailscale.com", false},
		// The classic smuggle: everything before the last '@' is userinfo, so the
		// real host here is evil.example, not api.tailscale.com.
		{"userinfo smuggling the expected host", "https://api.tailscale.com", "https://api.tailscale.com@evil.example", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := mustOrigin(t, c.a), mustOrigin(t, c.b)
			if got := a.equal(b); got != c.equal {
				t.Errorf("%q equal %q = %v, want %v (a=%+v b=%+v)", c.a, c.b, got, c.equal, a, b)
			}
			if got := b.equal(a); got != c.equal {
				t.Errorf("equal is not symmetric for %q / %q", c.a, c.b)
			}
		})
	}
}

// TestOriginStringNeverPrintsUserinfo keeps the observability requirement
// honest: the refusal names origins, and an origin carrying embedded
// credentials reports their presence, never their value.
func TestOriginStringNeverPrintsUserinfo(t *testing.T) {
	o := mustOrigin(t, "https://client:S3CR3T@api.tailscale.com/api/v2/oauth/token?x=1")
	got := o.String()
	if strings.Contains(got, "S3CR3T") || strings.Contains(got, "client") {
		t.Errorf("origin.String() leaks userinfo: %q", got)
	}
	if strings.Contains(got, "/api/v2/oauth/token") || strings.Contains(got, "x=1") {
		t.Errorf("origin.String() leaks the destination path/query: %q", got)
	}
	if !strings.Contains(got, "api.tailscale.com") {
		t.Errorf("origin.String() = %q, want the host to stay visible", got)
	}
}

// TestParseOriginRejectsUnboundBaseURL pins fail-closed behavior: a base URL
// without a scheme or host must be an error, never an origin that matches
// everything.
func TestParseOriginRejectsUnboundBaseURL(t *testing.T) {
	for _, raw := range []string{"", "api.tailscale.com", "/api/v2", "https://"} {
		if _, err := parseOrigin(raw); err == nil {
			t.Errorf("parseOrigin(%q) err = nil, want an error", raw)
		}
	}
}

func TestRedirectPolicy(t *testing.T) {
	newVia := func(raw string) []*http.Request {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatalf("NewRequest(%q): %v", raw, err)
		}
		return []*http.Request{req}
	}
	policy := redirectPolicy(nil)

	t.Run("same origin is allowed", func(t *testing.T) {
		next, _ := http.NewRequest(http.MethodGet, "https://api.tailscale.com:443/other", nil)
		if err := policy(next, newVia("https://api.tailscale.com/api/v2/tailnet/x/devices")); err != nil {
			t.Fatalf("same-origin redirect refused: %v", err)
		}
	})

	const secretPath = "/redirect-destination-path"
	for _, c := range []struct{ name, from, to string }{
		{"different host", "https://api.tailscale.com" + secretPath, "https://evil.example" + secretPath},
		{"scheme downgrade", "https://api.tailscale.com" + secretPath, "http://api.tailscale.com" + secretPath},
		{"alternate port", "https://api.tailscale.com" + secretPath, "https://api.tailscale.com:8443" + secretPath},
		{"userinfo injected", "https://api.tailscale.com" + secretPath, "https://who:S3CR3T@api.tailscale.com" + secretPath},
	} {
		t.Run("refused: "+c.name, func(t *testing.T) {
			next, _ := http.NewRequest(http.MethodGet, c.to, nil)
			err := policy(next, newVia(c.from))
			if err == nil {
				t.Fatalf("%s redirect was allowed, want refusal", c.name)
			}
			if !errors.Is(err, ErrCrossOriginRedirect) {
				t.Errorf("err = %v, want it to match ErrCrossOriginRedirect", err)
			}
			var cor *CrossOriginRedirectError
			if !errors.As(err, &cor) {
				t.Fatalf("err = %T, want *CrossOriginRedirectError", err)
			}
			if strings.Contains(err.Error(), "S3CR3T") {
				t.Errorf("refusal error leaks userinfo: %q", err)
			}
			if strings.Contains(err.Error(), secretPath) {
				t.Errorf("refusal error leaks the full destination path: %q", err)
			}
		})
	}

	t.Run("redirect chain is still capped", func(t *testing.T) {
		via := make([]*http.Request, 0, maxRedirects)
		for range maxRedirects {
			req, _ := http.NewRequest(http.MethodGet, "https://api.tailscale.com/a", nil)
			via = append(via, req)
		}
		next, _ := http.NewRequest(http.MethodGet, "https://api.tailscale.com/a", nil)
		if err := policy(next, via); !errors.Is(err, ErrTooManyRedirects) {
			t.Fatalf("err = %v, want ErrTooManyRedirects at the cap", err)
		}
	})
}

// TestRedirectPolicyLogsRefusalWithoutSecrets covers the #466 acceptance line
// "the redirect failure is observable without logging credentials or full
// destinations".
func TestRedirectPolicyLogsRefusalWithoutSecrets(t *testing.T) {
	var buf bytes.Buffer
	policy := redirectPolicy(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	from, _ := http.NewRequest(http.MethodGet, "https://api.tailscale.com/api/v2/tailnet/example.com/devices", nil)
	to, _ := http.NewRequest(http.MethodGet, "https://who:S3CR3T@evil.example/steal?token=abc", nil)
	if err := policy(to, []*http.Request{from}); err == nil {
		t.Fatal("policy allowed a cross-origin redirect")
	}
	out := buf.String()
	for _, bad := range []string{"S3CR3T", "/steal", "token=abc"} {
		if strings.Contains(out, bad) {
			t.Errorf("refusal log leaks %q: %s", bad, out)
		}
	}
	for _, want := range []string{`"error_class":"redirect_refused"`, "evil.example", `"endpoint":"devices"`} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal log lost %s: %s", want, out)
		}
	}
}

// TestAuthKeyTransportAttachesOnlyOnConfiguredOrigin unit-tests the #466 fix
// below the http.Client: even handed an off-origin request directly, the
// transport must not attach the credential.
func TestAuthKeyTransportAttachesOnlyOnConfiguredOrigin(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"configured origin", "https://api.tailscale.com/api/v2/tailnet/x/devices", true},
		{"explicit default port", "https://api.tailscale.com:443/api/v2/tailnet/x/devices", true},
		{"uppercase host", "https://API.TAILSCALE.COM/api/v2/tailnet/x/devices", true},
		{"other host", "https://evil.example/api/v2/tailnet/x/devices", false},
		{"scheme downgrade", "http://api.tailscale.com/api/v2/tailnet/x/devices", false},
		{"alternate port", "https://api.tailscale.com:8443/api/v2/tailnet/x/devices", false},
		{"userinfo injected", "https://who:pw@api.tailscale.com/api/v2/tailnet/x/devices", false},
		{"userinfo smuggling the expected host", "https://api.tailscale.com@evil.example/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotAuth string
			tr := &authKeyTransport{
				base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					gotAuth = r.Header.Get("Authorization")
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}, nil
				}),
				key:    "testkey",
				origin: mustOrigin(t, "https://api.tailscale.com"),
			}
			req, _ := http.NewRequest(http.MethodGet, c.url, nil)
			resp, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip: %v", err)
			}
			_ = resp.Body.Close()
			if got := gotAuth == "Bearer testkey"; got != c.want {
				t.Fatalf("Authorization attached = %v (%q), want %v", got, gotAuth, c.want)
			}
		})
	}
}

// TestAuthKeyTransportZeroOriginFailsClosed pins the fail-closed invariant: a
// transport built without an origin attaches nothing (401s), rather than
// reverting to the unconditional pre-#466 behavior.
func TestAuthKeyTransportZeroOriginFailsClosed(t *testing.T) {
	var gotAuth string
	tr := &authKeyTransport{
		base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("Authorization")
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}, nil
		}),
		key: "testkey",
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.tailscale.com/api/v2/tailnet/x/devices", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if gotAuth != "" {
		t.Fatalf("Authorization = %q with an unbound origin, want none (fail closed)", gotAuth)
	}
}

// TestEveryBuiltClientRefusesCrossOriginRedirects is the anti-drift guard for
// the "define the policy once" decision: all three auth paths must produce a
// client with the shared CheckRedirect installed.
func TestEveryBuiltClientRefusesCrossOriginRedirects(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("jwt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cases := map[string]Options{
		"api key":           {APIKey: "k"},
		"oauth":             {OAuthClientID: "id", OAuthClientSecret: "secret"},
		"workload identity": {WorkloadIdentityClientID: "id", WorkloadIdentityIDTokenFile: tokenPath},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			opts.BaseURL = "https://api.tailscale.com"
			opts.MaxAttempts = 1
			c, err := buildHTTPClient(opts)
			if err != nil {
				t.Fatalf("buildHTTPClient: %v", err)
			}
			if c.CheckRedirect == nil {
				t.Fatal("client has no CheckRedirect; the shared redirect policy is not wired")
			}
			from, _ := http.NewRequest(http.MethodGet, "https://api.tailscale.com/api/v2/tailnet/x/devices", nil)
			to, _ := http.NewRequest(http.MethodGet, "https://evil.example/api/v2/tailnet/x/devices", nil)
			if err := c.CheckRedirect(to, []*http.Request{from}); !errors.Is(err, ErrCrossOriginRedirect) {
				t.Fatalf("CheckRedirect err = %v, want ErrCrossOriginRedirect", err)
			}
		})
	}
}

// TestBoundedTokenFetchClientAlwaysCarriesTheRedirectPolicy pins that the token
// fetch client cannot be constructed without one (#467) — the JWT/client-secret
// body must never be replayable.
func TestBoundedTokenFetchClientAlwaysCarriesTheRedirectPolicy(t *testing.T) {
	c := newBoundedTokenFetchClient(time.Second, redirectPolicy(nil))
	if c.CheckRedirect == nil {
		t.Fatal("token-fetch client has no CheckRedirect")
	}
	from, _ := http.NewRequest(http.MethodPost, "https://api.tailscale.com/api/v2/oauth/token", nil)
	to, _ := http.NewRequest(http.MethodPost, "https://evil.example/api/v2/oauth/token", nil)
	if err := c.CheckRedirect(to, []*http.Request{from}); !errors.Is(err, ErrCrossOriginRedirect) {
		t.Fatalf("CheckRedirect err = %v, want ErrCrossOriginRedirect", err)
	}
}

// TestBuildHTTPClientRejectsUnboundBaseURL: a BaseURL that cannot be normalized
// into an origin must fail construction rather than leave the credential
// unbound.
func TestBuildHTTPClientRejectsUnboundBaseURL(t *testing.T) {
	if _, err := buildHTTPClient(Options{APIKey: "k", BaseURL: "api.tailscale.com"}); err == nil {
		t.Fatal("buildHTTPClient err = nil for a schemeless BaseURL, want an error")
	}
}

// TestOAuthTokenFetchRefusesCrossOriginRedirect is the client-credentials
// counterpart of the workload-identity #467 tests: the OAuth client secret
// travels in the same replayable POST body.
func TestOAuthTokenFetchRefusesCrossOriginRedirect(t *testing.T) {
	var targetHits int32
	var targetBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		b, _ := io.ReadAll(r.Body)
		targetBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stolen","token_type":"Bearer"}`))
	}))
	defer target.Close()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v2/oauth/token", http.StatusPermanentRedirect)
	}))
	defer tokenSrv.Close()

	c, err := buildHTTPClient(Options{
		OAuthClientID:     "id",
		OAuthClientSecret: "S3CR3T-CLIENT-SECRET",
		BaseURL:           tokenSrv.URL,
		Timeout:           2 * time.Second,
		MaxAttempts:       1,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, tokenSrv.URL+"/api/v2/tailnet/example.com/devices", nil)
	resp, reqErr := c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if strings.Contains(targetBody, "S3CR3T-CLIENT-SECRET") {
		t.Errorf("client secret replayed to the cross-origin redirect target: %q", targetBody)
	}
	if got := atomic.LoadInt32(&targetHits); got != 0 {
		t.Errorf("cross-origin redirect target contacted %d times, want 0", got)
	}
	if reqErr == nil {
		t.Fatal("Do err = nil, want the token fetch to fail on the refused redirect")
	}
	if strings.Contains(reqErr.Error(), "S3CR3T-CLIENT-SECRET") {
		t.Errorf("error text leaks the client secret: %q", reqErr)
	}
}

// TestAPIKeyBearerNotSentAcrossOriginRedirect pins #466: authKeyTransport
// re-attached the API key on every RoundTrip, including the redirected request
// the http.Client issues after stripping the cross-origin Authorization header.
// The redirect target must never observe the credential, and the redirect
// itself must be refused.
func TestAPIKeyBearerNotSentAcrossOriginRedirect(t *testing.T) {
	var targetAuth string
	var targetHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		targetAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v2/tailnet/example.com/devices", http.StatusFound)
	}))
	defer origin.Close()

	c, err := buildHTTPClient(Options{
		APIKey:      "testkey",
		BaseURL:     origin.URL,
		MaxAttempts: 1,
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, origin.URL+"/api/v2/tailnet/example.com/devices", nil)
	resp, err := c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if strings.Contains(targetAuth, "testkey") {
		t.Errorf("API key leaked to the cross-origin redirect target: %q", targetAuth)
	}
	if targetHits != 0 {
		t.Errorf("cross-origin redirect target was contacted %d times, want 0", targetHits)
	}
	if err == nil {
		t.Fatal("Do err = nil, want a refused cross-origin redirect")
	}
	if strings.Contains(err.Error(), "testkey") {
		t.Errorf("redirect error text leaks the credential: %q", err)
	}
}

// TestAPIKeySameOriginRedirectStillFollowed guards the other half of #466: a
// redirect that stays on the configured origin must keep working, credential
// included.
func TestAPIKeySameOriginRedirectStillFollowed(t *testing.T) {
	var finalAuth string
	var mux http.ServeMux
	mux.HandleFunc("/api/v2/tailnet/example.com/devices", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/v2/tailnet/example.com/devices-moved", http.StatusFound)
	})
	mux.HandleFunc("/api/v2/tailnet/example.com/devices-moved", func(w http.ResponseWriter, r *http.Request) {
		finalAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(&mux)
	defer srv.Close()

	c, err := buildHTTPClient(Options{
		APIKey:      "testkey",
		BaseURL:     srv.URL,
		MaxAttempts: 1,
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("same-origin redirect must still be followed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if finalAuth != "Bearer testkey" {
		t.Fatalf("Authorization after same-origin redirect = %q, want %q", finalAuth, "Bearer testkey")
	}
}
