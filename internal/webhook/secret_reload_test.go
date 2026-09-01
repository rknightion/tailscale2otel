package webhook

import (
	"strings"
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

// TestRouterSecretProviderRotationAcrossTokenlessBoundary proves that a
// Router observes a provider's current empty/non-empty boundary, not the
// value it saw while being constructed. Both directions matter: stale
// tokenless state would reject a signed request on a non-loopback Host, while
// stale signed state would reject an unsigned loopback delivery.
func TestRouterSecretProviderRotationAcrossTokenlessBoundary(t *testing.T) {
	const rotated = "rotated-webhook-secret"
	now := time.Date(2026, 6, 2, 10, 6, 0, 0, time.UTC)

	t.Run("empty to signed", func(t *testing.T) {
		var secret atomic.Value
		secret.Store("")
		s := New(Options{
			Listen: "127.0.0.1:0",
			Path:   "/webhook",
			SecretProvider: func() string {
				return secret.Load().(string)
			},
			Tolerance: 0,
		}, telemetrytest.New().Emitter(), discard())
		router := NewRouter([]Route{{Tailnet: "example.com", Server: s}})

		if got := doPost(t, router.Handler(), "/webhook", twoEventBody, "").StatusCode; got != 200 {
			t.Fatalf("initial tokenless status = %d, want 200", got)
		}

		secret.Store(rotated)
		signed := signBody(rotated, now, twoEventBody)
		if got := tokenlessRequest(router.Handler(), "webhook.example.internal", "application/json",
			map[string]string{signatureHeader: signed}, strings.NewReader(twoEventBody)).Code; got != 200 {
			t.Fatalf("signed status after empty-to-signed rotation = %d, want 200", got)
		}
		if got := doPost(t, router.Handler(), "/webhook", twoEventBody, "").StatusCode; got != 401 {
			t.Fatalf("unsigned status after empty-to-signed rotation = %d, want 401", got)
		}
	})

	t.Run("signed to empty", func(t *testing.T) {
		var secret atomic.Value
		secret.Store(testSecret)
		s := New(Options{
			Listen: "127.0.0.1:0",
			Path:   "/webhook",
			SecretProvider: func() string {
				return secret.Load().(string)
			},
			Tolerance: 0,
		}, telemetrytest.New().Emitter(), discard())
		router := NewRouter([]Route{{Tailnet: "example.com", Server: s}})

		if got := doPost(t, router.Handler(), "/webhook", twoEventBody, signBody(testSecret, now, twoEventBody)).StatusCode; got != 200 {
			t.Fatalf("initial signed status = %d, want 200", got)
		}

		secret.Store("")
		if got := doPost(t, router.Handler(), "/webhook", twoEventBody, "").StatusCode; got != 200 {
			t.Fatalf("tokenless status after signed-to-empty rotation = %d, want 200", got)
		}
	})
}
