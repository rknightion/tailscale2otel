package hsapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRefusesRedirectAndErrorOmitsResponseBody(t *testing.T) {
	const canary = "reflected-api-key"
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte(canary))
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "secret", Timeout: time.Second})
	_, err := c.Nodes(t.Context())
	if err == nil {
		t.Fatal("redirecting request succeeded")
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target contacted %d times", redirected.Load())
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error reflected response body: %v", err)
	}
}

func TestClientRejectsRemotePlaintextOriginBeforeRequest(t *testing.T) {
	c := NewClient(Options{URL: "http://headscale.example.com", APIKey: "secret", Timeout: time.Second})
	if _, err := c.Nodes(t.Context()); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("Nodes = %v, want HTTPS policy error", err)
	}
}
