package tsapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestReadIDTokenFile_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := readIDTokenFile(path)
	if err == nil {
		t.Fatal("expected an error for a missing token file, got nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name the missing path %q", err, path)
	}
}

func TestReadIDTokenFile_TrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("jwt-value\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := readIDTokenFile(path)
	if err != nil {
		t.Fatalf("readIDTokenFile: %v", err)
	}
	if got != "jwt-value" {
		t.Fatalf("got %q, want %q", got, "jwt-value")
	}
}

// TestReadIDTokenFile_Rotation pins the requirement that the ID token is
// re-read from disk on every call, never cached — Kubernetes projected
// service-account tokens rotate the file in place.
func TestReadIDTokenFile_Rotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := readIDTokenFile(path)
	if err != nil {
		t.Fatalf("readIDTokenFile (v1): %v", err)
	}
	if got != "v1" {
		t.Fatalf("got %q, want %q", got, "v1")
	}

	if err := os.WriteFile(path, []byte("v2"), 0o600); err != nil {
		t.Fatalf("overwrite WriteFile: %v", err)
	}
	got, err = readIDTokenFile(path)
	if err != nil {
		t.Fatalf("readIDTokenFile (v2): %v", err)
	}
	if got != "v2" {
		t.Fatalf("got %q after rotation, want %q", got, "v2")
	}
}

// tokenExchangeServer fakes POST /api/v2/oauth/token-exchange, recording the
// client_id/jwt form values it received and returning a fresh access token
// that echoes the jwt back (so tests can see which JWT round-tripped through
// the exchange without a real Tailscale backend).
func tokenExchangeServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/oauth/token-exchange") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		atomic.AddInt32(hits, 1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		clientID := r.PostForm.Get("client_id")
		jwt := r.PostForm.Get("jwt")
		if clientID == "" || jwt == "" {
			http.Error(w, "missing client_id or jwt", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"access-for-%s","token_type":"Bearer","expires_in":0}`, jwt)
	}))
}

// TestWorkloadIdentityTokenSource_ExchangeContract pins the documented,
// out-of-OpenAPI token-exchange contract. The endpoint is intentionally absent
// from the generated operation ledger, so this is the guard that must fail if
// its path or wire shape drifts.
func TestWorkloadIdentityTokenSource_ExchangeContract(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("signed-oidc-jwt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v2/oauth/token-exchange" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/api/v2/oauth/token-exchange")
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Errorf("parse Content-Type: %v", err)
		} else if mediaType != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", mediaType)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.PostForm.Get("client_id"); got != "federated-client-id" {
			t.Errorf("client_id = %q, want %q", got, "federated-client-id")
		}
		if got := r.PostForm.Get("jwt"); got != "signed-oidc-jwt" {
			t.Errorf("jwt = %q, want %q", got, "signed-oidc-jwt")
		}
		if got := len(r.PostForm); got != 2 {
			t.Errorf("form has %d fields (%v), want exactly client_id and jwt", got, r.PostForm)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"short-lived-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	src := &workloadIdentityTokenSource{
		ctx:         context.Background(),
		baseURL:     srv.URL,
		clientID:    "federated-client-id",
		idTokenFile: tokenPath,
	}
	tok, err := src.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "short-lived-token" || tok.TokenType != "Bearer" {
		t.Fatalf("token = %+v, want the exchange response's bearer token", tok)
	}
	if time.Until(tok.Expiry) < 50*time.Minute {
		t.Fatalf("token expiry = %v, want about one hour from now", tok.Expiry)
	}
}

func TestWorkloadIdentityTokenSource_MessageBearing4xx(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("signed-oidc-jwt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		// A proxy or broken endpoint must not be able to reflect the assertion into
		// logs merely by putting it in the otherwise-documented message field.
		_, _ = w.Write([]byte(`{"message":"Unauthorized. Visit the admin console for details; assertion=signed-oidc-jwt"}`))
	}))
	defer srv.Close()

	src := &workloadIdentityTokenSource{
		ctx:         context.Background(),
		baseURL:     srv.URL,
		clientID:    "federated-client-id",
		idTokenFile: tokenPath,
	}
	_, err := src.Token()
	if err == nil {
		t.Fatal("Token err = nil, want the exchange rejection")
	}
	for _, want := range []string{"HTTP 401", "Unauthorized. Visit the admin console for details"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "signed-oidc-jwt") {
		t.Errorf("exchange diagnostic leaks the submitted JWT: %q", err)
	}
	var retrieve *oauth2.RetrieveError
	if !errors.As(err, &retrieve) {
		t.Fatalf("error does not unwrap to oauth2.RetrieveError; auth failures would be retried and misclassified: %v", err)
	}
	if retrieve.Response == nil || retrieve.Response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("retrieve error response = %+v, want HTTP 401", retrieve.Response)
	}
	if len(retrieve.Body) != 0 {
		t.Fatalf("retrieve error retains the raw response body: %q", retrieve.Body)
	}
}

func TestWorkloadIdentityTokenSource_EmptyIDTokenFile(t *testing.T) {
	var hits int32
	srv := tokenExchangeServer(t, &hits)
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" \n\t"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	src := &workloadIdentityTokenSource{
		ctx:         context.Background(),
		baseURL:     srv.URL,
		clientID:    "federated-client-id",
		idTokenFile: tokenPath,
	}
	_, err := src.Token()
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("Token err = %v, want a clear empty-token-file diagnostic", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("exchange endpoint hit %d times, want 0 for an empty issuer token", got)
	}
}

func TestWorkloadIdentityTokenSource_Exchange(t *testing.T) {
	var hits int32
	srv := tokenExchangeServer(t, &hits)
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("jwt-v1"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	src := &workloadIdentityTokenSource{
		ctx:         context.Background(),
		baseURL:     srv.URL,
		clientID:    "wif-client-id",
		idTokenFile: tokenPath,
	}
	tok, err := src.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "access-for-jwt-v1" {
		t.Fatalf("AccessToken = %q, want %q", tok.AccessToken, "access-for-jwt-v1")
	}
	if hits != 1 {
		t.Fatalf("token-exchange endpoint hit %d times, want 1", hits)
	}

	// Rotate the token file; the next Token() call must exchange the NEW jwt,
	// not a cached one.
	if err := os.WriteFile(tokenPath, []byte("jwt-v2"), 0o600); err != nil {
		t.Fatalf("overwrite WriteFile: %v", err)
	}
	tok, err = src.Token()
	if err != nil {
		t.Fatalf("Token (after rotation): %v", err)
	}
	if tok.AccessToken != "access-for-jwt-v2" {
		t.Fatalf("AccessToken after rotation = %q, want %q", tok.AccessToken, "access-for-jwt-v2")
	}
	if hits != 2 {
		t.Fatalf("token-exchange endpoint hit %d times after rotation, want 2", hits)
	}
}

func TestWorkloadIdentityTokenSource_MissingFile(t *testing.T) {
	var hits int32
	srv := tokenExchangeServer(t, &hits)
	defer srv.Close()

	missing := filepath.Join(t.TempDir(), "absent")
	src := &workloadIdentityTokenSource{
		ctx:         context.Background(),
		baseURL:     srv.URL,
		clientID:    "wif-client-id",
		idTokenFile: missing,
	}
	_, err := src.Token()
	if err == nil {
		t.Fatal("expected an error for a missing ID token file, got nil")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name the missing path %q", err, missing)
	}
	if hits != 0 {
		t.Fatalf("token-exchange endpoint was hit %d times, want 0 (should fail before the network call)", hits)
	}
}

// TestBuildHTTPClient_WorkloadIdentity_AttachesBearerToken confirms
// buildHTTPClient wires the workload-identity case into an *oauth2.Transport
// whose exchanged access token is actually attached as a Bearer header on a
// downstream API call — the same shape of assertion client_test.go makes for
// the OAuth client-credentials path.
func TestBuildHTTPClient_WorkloadIdentity_AttachesBearerToken(t *testing.T) {
	var exchangeHits int32
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token-exchange") {
			atomic.AddInt32(&exchangeHits, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wif-access-token","token_type":"Bearer","expires_in":3600}`))
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("k8s-projected-jwt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := buildHTTPClient(Options{
		WorkloadIdentityClientID:    "wif-client-id",
		WorkloadIdentityIDTokenFile: tokenPath,
		BaseURL:                     srv.URL,
		MaxAttempts:                 1,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if exchangeHits != 1 {
		t.Fatalf("token-exchange endpoint hit %d times, want 1", exchangeHits)
	}
	if gotAuth != "Bearer wif-access-token" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer wif-access-token")
	}
}

func TestBuildHTTPClient_WorkloadIdentity_CachesValidToken(t *testing.T) {
	var exchangeHits int32
	var apiHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == workloadIdentityExchangePath {
			atomic.AddInt32(&exchangeHits, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"cached-token","token_type":"Bearer","expires_in":3600}`))
			return
		}
		atomic.AddInt32(&apiHits, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer cached-token" {
			t.Errorf("Authorization = %q, want cached bearer token", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("jwt-v1"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c, err := buildHTTPClient(Options{
		WorkloadIdentityClientID:    "federated-client-id",
		WorkloadIdentityIDTokenFile: tokenPath,
		BaseURL:                     srv.URL,
		MaxAttempts:                 1,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	for range 2 {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = resp.Body.Close()
	}
	if got := atomic.LoadInt32(&apiHits); got != 2 {
		t.Fatalf("API endpoint hit %d times, want 2", got)
	}
	if got := atomic.LoadInt32(&exchangeHits); got != 1 {
		t.Fatalf("exchange endpoint hit %d times, want 1 while the token remains valid", got)
	}
}

func TestBuildHTTPClient_WorkloadIdentity_RefreshesExpiredTokenAndRereadsJWT(t *testing.T) {
	var exchangeHits int32
	var apiHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == workloadIdentityExchangePath {
			hit := atomic.AddInt32(&exchangeHits, 1)
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm: %v", err)
			}
			wantJWT := fmt.Sprintf("jwt-v%d", hit)
			if got := r.PostForm.Get("jwt"); got != wantJWT {
				t.Errorf("exchange %d jwt = %q, want %q", hit, got, wantJWT)
			}
			expiresIn := 3600
			if hit == 1 {
				expiresIn = 1 // within oauth2's expiry delta, so the next use refreshes
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"access-%d","token_type":"Bearer","expires_in":%d}`, hit, expiresIn)
			return
		}
		hit := atomic.AddInt32(&apiHits, 1)
		wantAuth := fmt.Sprintf("Bearer access-%d", hit)
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("API call %d Authorization = %q, want %q", hit, got, wantAuth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("jwt-v1"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c, err := buildHTTPClient(Options{
		WorkloadIdentityClientID:    "federated-client-id",
		WorkloadIdentityIDTokenFile: tokenPath,
		BaseURL:                     srv.URL,
		MaxAttempts:                 1,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	request := func() {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = resp.Body.Close()
	}
	request()
	if err := os.WriteFile(tokenPath, []byte("jwt-v2"), 0o600); err != nil {
		t.Fatalf("rotate token file: %v", err)
	}
	request()

	if got := atomic.LoadInt32(&apiHits); got != 2 {
		t.Fatalf("API endpoint hit %d times, want 2", got)
	}
	if got := atomic.LoadInt32(&exchangeHits); got != 2 {
		t.Fatalf("exchange endpoint hit %d times, want 2 after access-token expiry", got)
	}
}

// TestWorkloadIdentityTokenFetchIsBounded pins #84 for the workload-identity
// path: a stalled token-exchange call must not hang forever, since
// oauth2.Transport fetches the token ignoring the request context.
func TestWorkloadIdentityTokenFetchIsBounded(t *testing.T) {
	var exchangeHits int32
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token-exchange") {
			atomic.AddInt32(&exchangeHits, 1)
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	defer close(unblock)

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("jwt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := buildHTTPClient(Options{
		WorkloadIdentityClientID:    "wif-client-id",
		WorkloadIdentityIDTokenFile: tokenPath,
		BaseURL:                     srv.URL,
		Timeout:                     150 * time.Millisecond,
		MaxAttempts:                 1,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
		resp, reqErr := c.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- reqErr
	}()

	select {
	case reqErr := <-done:
		if reqErr == nil {
			t.Fatal("expected an error from the stalled token exchange, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request hung well past the 150ms token-fetch bound (#84 regression)")
	}
	if atomic.LoadInt32(&exchangeHits) == 0 {
		t.Fatal("token-exchange endpoint was never hit; test did not exercise the exchange path")
	}
}

// TestWorkloadIdentityTokenFetchBodyStallIsBounded pins #200 for the
// workload-identity path: a token-exchange endpoint that sends valid headers
// and then stalls mid-body must still time out — ResponseHeaderTimeout alone
// does not cover the body read that follows the headers.
func TestWorkloadIdentityTokenFetchBodyStallIsBounded(t *testing.T) {
	var exchangeHits int32
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token-exchange") {
			atomic.AddInt32(&exchangeHits, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_toke`)) // flush headers + a partial body
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-unblock:
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	defer close(unblock)

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("jwt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := buildHTTPClient(Options{
		WorkloadIdentityClientID:    "wif-client-id",
		WorkloadIdentityIDTokenFile: tokenPath,
		BaseURL:                     srv.URL,
		Timeout:                     150 * time.Millisecond,
		MaxAttempts:                 1,
	})
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v2/tailnet/example.com/devices", nil)
		resp, reqErr := c.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- reqErr
	}()

	select {
	case reqErr := <-done:
		if reqErr == nil {
			t.Fatal("expected an error from the body-stalled token exchange, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request hung well past the 150ms token-fetch bound (#200 regression: body read unbounded)")
	}
	if atomic.LoadInt32(&exchangeHits) == 0 {
		t.Fatal("token-exchange endpoint was never hit; test did not exercise the exchange path")
	}
}

// TestWorkloadIdentityExchange_RefusesRedirectToOtherOrigin pins #467: the
// token exchange submits a projected Kubernetes service-account JWT in a POST
// form body. Go's default redirect policy replays that body verbatim on a
// cross-origin 307/308, handing the JWT to whoever the exchange endpoint (or a
// proxy in front of it) names. Every redirect status must be refused before the
// second origin is contacted at all.
func TestWorkloadIdentityExchange_RefusesRedirectToOtherOrigin(t *testing.T) {
	for _, code := range []int{
		http.StatusMovedPermanently,  // 301
		http.StatusFound,             // 302
		http.StatusSeeOther,          // 303
		http.StatusTemporaryRedirect, // 307
		http.StatusPermanentRedirect, // 308
	} {
		t.Run(fmt.Sprintf("%d", code), func(t *testing.T) {
			var targetHits int32
			var targetSawJWT int32
			var targetBody string
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&targetHits, 1)
				b, _ := io.ReadAll(r.Body)
				targetBody = string(b)
				if strings.Contains(targetBody, "projected-jwt-secret") || r.URL.Query().Get("jwt") != "" {
					atomic.AddInt32(&targetSawJWT, 1)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"stolen","token_type":"Bearer"}`))
			}))
			defer target.Close()

			exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/api/v2/oauth/token-exchange", code)
			}))
			defer exchange.Close()

			tokenPath := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(tokenPath, []byte("projected-jwt-secret"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			src := &workloadIdentityTokenSource{
				ctx:         context.WithValue(context.Background(), oauth2.HTTPClient, newBoundedTokenFetchClient(2*time.Second, redirectPolicy(nil))),
				baseURL:     exchange.URL,
				clientID:    "wif-client-id",
				idTokenFile: tokenPath,
			}
			tok, err := src.Token()

			if atomic.LoadInt32(&targetSawJWT) != 0 {
				t.Errorf("JWT replayed to the cross-origin redirect target (body %q)", targetBody)
			}
			if got := atomic.LoadInt32(&targetHits); got != 0 {
				t.Errorf("cross-origin redirect target contacted %d times, want 0", got)
			}
			if err == nil {
				t.Fatalf("Token() err = nil (token %+v), want a refused cross-origin redirect", tok)
			}
			if strings.Contains(err.Error(), "projected-jwt-secret") {
				t.Errorf("error text leaks the JWT: %q", err)
			}
		})
	}
}

// TestWorkloadIdentityExchange_SameOriginRedirectFollowed keeps the other half
// of #467 honest: a redirect that stays on the exchange endpoint's own origin
// is ordinary behavior and must still complete, JWT body intact.
func TestWorkloadIdentityExchange_SameOriginRedirectFollowed(t *testing.T) {
	var gotJWT string
	var mux http.ServeMux
	mux.HandleFunc("/api/v2/oauth/token-exchange", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/v2/oauth/token-exchange-v2", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/api/v2/oauth/token-exchange-v2", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotJWT = r.PostForm.Get("jwt")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"same-origin-token","token_type":"Bearer"}`))
	})
	srv := httptest.NewServer(&mux)
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("projected-jwt-secret"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	src := &workloadIdentityTokenSource{
		ctx:         context.WithValue(context.Background(), oauth2.HTTPClient, newBoundedTokenFetchClient(2*time.Second, redirectPolicy(nil))),
		baseURL:     srv.URL,
		clientID:    "wif-client-id",
		idTokenFile: tokenPath,
	}
	tok, err := src.Token()
	if err != nil {
		t.Fatalf("same-origin redirect must still be followed: %v", err)
	}
	if tok.AccessToken != "same-origin-token" {
		t.Fatalf("AccessToken = %q, want %q", tok.AccessToken, "same-origin-token")
	}
	if gotJWT != "projected-jwt-secret" {
		t.Fatalf("same-origin redirect target saw jwt = %q, want the projected token re-sent", gotJWT)
	}
}

// TestNewClient_WorkloadIdentity_NoAPIKey confirms NewClient wires opts through
// to a working client when only workload-identity fields are set (no APIKey,
// no OAuthClientID) — i.e. the new auth method is reachable from the public
// entry point, not just buildHTTPClient directly.
func TestNewClient_WorkloadIdentity_NoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token-exchange") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"wif-access-token","token_type":"Bearer","expires_in":3600}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"devices":[]}`)
	}))
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("jwt"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := NewClient(Options{
		Tailnet:                     "example.com",
		BaseURL:                     srv.URL,
		WorkloadIdentityClientID:    "wif-client-id",
		WorkloadIdentityIDTokenFile: tokenPath,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.Devices(context.Background()); err != nil {
		t.Fatalf("Devices: %v", err)
	}
}
