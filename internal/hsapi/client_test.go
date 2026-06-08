package hsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientNodesSendsBearerAndDecodes(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nodes":[{"id":"7","name":"host","givenName":"host","online":true,"ipAddresses":["100.64.0.1"]}]}`))
	}))
	defer srv.Close()

	c := NewClient(Options{URL: srv.URL, APIKey: "secret", Timeout: 5 * time.Second})
	nodes, err := c.Nodes(context.Background())
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth header = %q, want %q", gotAuth, "Bearer secret")
	}
	if gotPath != "/api/v1/node" {
		t.Errorf("path = %q, want /api/v1/node", gotPath)
	}
	if len(nodes) != 1 || nodes[0].ID != "7" || !nodes[0].Online {
		t.Errorf("decoded nodes = %+v", nodes)
	}
}

func TestClientNon200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(Options{URL: srv.URL, APIKey: "x", Timeout: time.Second})
	if _, err := c.Nodes(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}
}
