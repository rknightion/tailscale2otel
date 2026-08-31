package webhook

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/telemetrytest"
)

// Secret rotation is an in-process cutover. The same Handler must reject a
// delivery signed with the old value and accept one signed with the new value,
// preserving the receiver (and its dedup window) without a listener restart.
func TestSecretProviderRotationCutsOverWithoutRestart(t *testing.T) {
	var secret atomic.Value
	secret.Store(testSecret)
	s := New(Options{
		Listen: "127.0.0.1:0",
		Path:   "/webhook",
		Secret: testSecret,
		SecretProvider: func() string {
			return secret.Load().(string)
		},
		Tolerance: 0,
	}, telemetrytest.New().Emitter(), discard())

	now := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)
	if got := doPost(t, s.Handler(), "/webhook", twoEventBody, signBody(testSecret, now, twoEventBody)).StatusCode; got != 200 {
		t.Fatalf("initial secret status = %d, want 200", got)
	}

	const rotated = "rotated-webhook-secret"
	secret.Store(rotated)
	if got := doPost(t, s.Handler(), "/webhook", twoEventBody, signBody(testSecret, now, twoEventBody)).StatusCode; got != 401 {
		t.Fatalf("old secret status after rotation = %d, want 401", got)
	}
	if got := doPost(t, s.Handler(), "/webhook", twoEventBody, signBody(rotated, now, twoEventBody)).StatusCode; got != 200 {
		t.Fatalf("new secret status after rotation = %d, want 200", got)
	}
}
