package app

import (
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

// TestWebhookOptionsPlumbsTolerance guards the config->receiver wiring for the
// replay-protection window: the webhook Tolerance must flow from config into the
// server's Options. Without it the staleness check is permanently disabled.
func TestWebhookOptionsPlumbsTolerance(t *testing.T) {
	got := webhookOptions(config.WebhookConfig{
		Listen:    ":8089",
		Path:      "/tailscale/webhook",
		Secret:    "s",
		Tolerance: config.Duration(7 * time.Minute),
	})
	if got.Tolerance != 7*time.Minute {
		t.Fatalf("webhookOptions Tolerance = %v, want 7m (webhook.tolerance must be plumbed)", got.Tolerance)
	}
}
