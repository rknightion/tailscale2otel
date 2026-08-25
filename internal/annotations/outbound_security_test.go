package annotations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewClientRejectsCredentialedOrRemotePlaintextOrigins(t *testing.T) {
	for _, raw := range []string{
		"http://grafana.example.com",
		"https://user:secret@grafana.example.com",
		"https://grafana.example.com/secret-in-path",
		"https://grafana.example.com?token=secret",
	} {
		if _, err := NewClient(ClientConfig{URL: raw, Token: "token"}); err == nil {
			t.Errorf("NewClient accepted %q", raw)
		}
	}
}

func TestPublishRefusesRedirectAndDoesNotReflectBody(t *testing.T) {
	const canary = "reflected-bearer-token"
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == annotationsPath {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(http.StatusTemporaryRedirect)
			_, _ = w.Write([]byte(canary))
		}
	}))
	defer srv.Close()

	c, err := NewClient(ClientConfig{URL: srv.URL, Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = srv.Client().Transport
	err = c.Publish(context.Background(), Annotation{}, nil)
	if err == nil {
		t.Fatal("redirecting publish succeeded")
	}
	if redirected.Load() != 0 {
		t.Fatalf("redirect target contacted %d times", redirected.Load())
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error reflected response body: %v", err)
	}
}
