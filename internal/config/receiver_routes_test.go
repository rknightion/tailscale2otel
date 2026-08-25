package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

func multiReceiverConfig() *config.Config {
	c := config.Default()
	c.Tailnets = []config.TailnetConfig{
		{Name: "alpha.example.com", Auth: config.TailscaleAuth{Method: "oauth"}},
		{Name: "beta.example.com", Auth: config.TailscaleAuth{Method: "oauth"}},
	}
	return c
}

func TestValidateReceiverRoutes(t *testing.T) {
	t.Run("valid routes cover each configured runtime", func(t *testing.T) {
		c := multiReceiverConfig()
		c.Streaming.Enabled = true
		c.Streaming.Routes = []config.StreamingRoute{
			{Tailnet: "alpha.example.com", Path: "/hec/alpha", Token: "a"},
			{Tailnet: "beta.example.com", Path: "/hec/beta", Token: "b", AutoConfigure: true, PublicURL: "https://receive.example/hec/beta"},
		}
		c.Webhook.Enabled = true
		c.Webhook.Routes = []config.WebhookRoute{
			{Tailnet: "alpha.example.com", Secret: "a"},
			{Tailnet: "beta.example.com", Secret: "b"},
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("Validate() = %v", err)
		}
	})

	for _, tc := range []struct {
		name string
		edit func(*config.Config)
		want string
	}{
		{"duplicate stream path", func(c *config.Config) {
			c.Streaming.Enabled = true
			c.Streaming.Routes = []config.StreamingRoute{{Tailnet: "alpha.example.com", Path: "/same", Token: "a"}, {Tailnet: "beta.example.com", Path: "/same", Token: "b"}}
		}, "duplicates"},
		{"unknown route tailnet", func(c *config.Config) {
			c.Webhook.Enabled = true
			c.Webhook.Routes = []config.WebhookRoute{{Tailnet: "unknown.example.com", Secret: "a"}}
		}, "does not match"},
		{"stream token xor file", func(c *config.Config) {
			c.Streaming.Enabled = true
			c.Streaming.Routes = []config.StreamingRoute{{Tailnet: "alpha.example.com", Path: "/alpha", Token: "a", TokenFile: "/x"}}
		}, "token and token_file"},
		{"route requires tailnets list", func(c *config.Config) {
			c.Tailnets = nil
			c.Streaming.Enabled = true
			c.Streaming.Routes = []config.StreamingRoute{{Tailnet: "alpha.example.com", Path: "/alpha", Token: "a"}}
		}, "multi-tailnet"},
		{"legacy stream identity conflicts", func(c *config.Config) {
			c.Streaming.Enabled = true
			c.Streaming.Path = "/legacy"
			c.Streaming.Routes = []config.StreamingRoute{{Tailnet: "alpha.example.com", Path: "/alpha", Token: "a"}}
		}, "streaming.path"},
		{"legacy webhook identity conflicts", func(c *config.Config) {
			c.Webhook.Enabled = true
			c.Webhook.Path = "/legacy"
			c.Webhook.Routes = []config.WebhookRoute{{Tailnet: "alpha.example.com", Secret: "a"}}
		}, "webhook.path"},
		{"webhook routes cannot mix tokenless and signed auth", func(c *config.Config) {
			c.Webhook.Enabled = true
			c.Webhook.Routes = []config.WebhookRoute{{Tailnet: "alpha.example.com"}, {Tailnet: "beta.example.com", Secret: "b"}}
		}, "mix tokenless and signed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := multiReceiverConfig()
			tc.edit(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadRejectsEnvVarIndexingIntoReceiverRoutes(t *testing.T) {
	t.Setenv("TS2OTEL_STREAMING__ROUTES__0__TAILNET", "alpha.example.com")
	_, err := config.Load("")
	if err == nil || !strings.Contains(err.Error(), "streaming.routes") {
		t.Fatalf("Load() = %v, want file-only streaming.routes error", err)
	}
}
