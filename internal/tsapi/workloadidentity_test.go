package tsapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
