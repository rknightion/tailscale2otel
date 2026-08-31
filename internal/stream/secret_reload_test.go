package stream_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/stream"
)

// A receiver's token provider is sampled for each request. Rotating the
// provider must cut over authentication on the same Server/Handler; a process
// restart would discard the receiver's in-memory state and is exactly what
// file-backed credentials are meant to avoid.
func TestTokenProviderRotationCutsOverWithoutRestart(t *testing.T) {
	var token atomic.Value
	token.Store(testToken)
	s, _ := newServer(t, stream.Options{
		Listen: "127.0.0.1:0",
		Token:  testToken,
		TokenProvider: func() string {
			return token.Load().(string)
		},
	})

	if got := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(captureFlowRecord)).Code; got != http.StatusOK {
		t.Fatalf("initial token status = %d, want 200", got)
	}

	const rotated = "rotated-stream-token"
	token.Store(rotated)

	if got := post(t, s.Handler(), http.MethodPost, "/services/collector/event", authHeader(), strings.NewReader(captureFlowRecord)).Code; got != http.StatusUnauthorized {
		t.Fatalf("old token status after rotation = %d, want 401", got)
	}
	newAuth := http.Header{}
	newAuth.Set("Authorization", "Splunk "+rotated)
	if got := post(t, s.Handler(), http.MethodPost, "/services/collector/event", newAuth, strings.NewReader(captureFlowRecord)).Code; got != http.StatusOK {
		t.Fatalf("new token status after rotation = %d, want 200", got)
	}
}
