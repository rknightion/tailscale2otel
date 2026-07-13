package config_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/config"
)

func TestResolvedTailnetsSingleMode(t *testing.T) {
	c := config.Default()
	c.Tailscale.Tailnet = "acme.example.com"
	c.Tailscale.Auth.Method = "oauth"
	c.Tailscale.Auth.OAuth.ClientID = "id"
	c.Tailscale.Auth.OAuth.ClientSecret = "sec"

	got := c.ResolvedTailnets()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Name != "acme.example.com" {
		t.Errorf("Name = %q, want acme.example.com", got[0].Name)
	}
	if got[0].Auth.Method != "oauth" {
		t.Errorf("Auth.Method = %q, want oauth", got[0].Auth.Method)
	}
}

func TestResolvedTailnetsMultiMode(t *testing.T) {
	c := config.Default()
	c.Tailscale = config.TailscaleConfig{} // multi mode: top-level tailscale name unset
	c.Tailnets = []config.TailnetConfig{
		{Name: "acme.example.com", Auth: config.TailscaleAuth{Method: "oauth",
			OAuth: config.OAuthConfig{ClientID: "a", ClientSecret: "s"}}},
		{Name: "beta.example.com", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k"}},
	}
	got := c.ResolvedTailnets()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "acme.example.com" || got[1].Name != "beta.example.com" {
		t.Errorf("names = %q,%q", got[0].Name, got[1].Name)
	}
	if got[1].Auth.Method != "apikey" {
		t.Errorf("got[1].Auth.Method = %q, want apikey", got[1].Auth.Method)
	}
}

// TestResolvedTailnetsBackfillsHTTPDefaults pins issue #104: a tailnets[] entry
// with no http: block must inherit real retry/timeout defaults (max_attempts=4,
// not the zero that tsapi clamps to 1, silently disabling retries).
func TestResolvedTailnetsBackfillsHTTPDefaults(t *testing.T) {
	c := config.Default()
	c.Tailscale = config.TailscaleConfig{} // multi mode: top-level block unset
	c.Tailnets = []config.TailnetConfig{
		{Name: "acme.example.com", Auth: config.TailscaleAuth{Method: "oauth"}},
	}
	got := c.ResolvedTailnets()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	def := config.Default().Tailscale.HTTP
	if got[0].HTTP.Retry.MaxAttempts != def.Retry.MaxAttempts {
		t.Errorf("MaxAttempts = %d, want default %d", got[0].HTTP.Retry.MaxAttempts, def.Retry.MaxAttempts)
	}
	if got[0].HTTP.Retry.BaseDelay != def.Retry.BaseDelay {
		t.Errorf("BaseDelay = %v, want default %v", got[0].HTTP.Retry.BaseDelay.D(), def.Retry.BaseDelay.D())
	}
	if got[0].HTTP.Timeout != def.Timeout {
		t.Errorf("Timeout = %v, want default %v", got[0].HTTP.Timeout.D(), def.Timeout.D())
	}
}

// TestResolvedTailnetsHTTPFleetPolicy pins the fleet-wide-policy half of #104:
// a value set on the top-level tailscale.http block backfills entries that omit
// http:, while an explicit per-entry value still wins.
func TestResolvedTailnetsHTTPFleetPolicy(t *testing.T) {
	c := config.Default()
	c.Tailscale.HTTP.Retry.MaxAttempts = 9 // fleet-wide policy
	c.Tailnets = []config.TailnetConfig{
		{Name: "inherits.example.com", Auth: config.TailscaleAuth{Method: "oauth"}},
		{Name: "overrides.example.com", Auth: config.TailscaleAuth{Method: "oauth"},
			HTTP: config.TailscaleHTTPConfig{Retry: config.RetryConfig{MaxAttempts: 2}}},
	}
	got := c.ResolvedTailnets()
	if got[0].HTTP.Retry.MaxAttempts != 9 {
		t.Errorf("entry[0] MaxAttempts = %d, want 9 (inherited fleet policy)", got[0].HTTP.Retry.MaxAttempts)
	}
	if got[1].HTTP.Retry.MaxAttempts != 2 {
		t.Errorf("entry[1] MaxAttempts = %d, want 2 (explicit override)", got[1].HTTP.Retry.MaxAttempts)
	}
}

// TestResolvedTailnetsDefaultsOAuthScopes pins #127: a tailnets[] OAuth entry with
// no scopes resolves to the least-privilege all:read default (an unset scope list
// would otherwise request an unscoped, all-privileges token); an explicit list
// wins, and a non-oauth entry is untouched.
func TestResolvedTailnetsDefaultsOAuthScopes(t *testing.T) {
	c := config.Default()
	c.Tailnets = []config.TailnetConfig{
		{Name: "a.example.com", Auth: config.TailscaleAuth{Method: "oauth"}},
		{Name: "b.example.com", Auth: config.TailscaleAuth{Method: "oauth",
			OAuth: config.OAuthConfig{Scopes: []string{"devices:core:read"}}}},
		{Name: "c.example.com", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k"}},
	}
	got := c.ResolvedTailnets()
	if len(got[0].Auth.OAuth.Scopes) != 1 || got[0].Auth.OAuth.Scopes[0] != "all:read" {
		t.Errorf("entry[0] scopes = %v, want [all:read]", got[0].Auth.OAuth.Scopes)
	}
	if len(got[1].Auth.OAuth.Scopes) != 1 || got[1].Auth.OAuth.Scopes[0] != "devices:core:read" {
		t.Errorf("entry[1] explicit scopes overwritten: %v", got[1].Auth.OAuth.Scopes)
	}
	if len(got[2].Auth.OAuth.Scopes) != 0 {
		t.Errorf("apikey entry[2] got oauth scopes: %v", got[2].Auth.OAuth.Scopes)
	}
}

func TestValidateRejectsBothSingleAndList(t *testing.T) {
	c := config.Default()
	c.Tailscale.Tailnet = "acme.example.com"
	c.Tailscale.Auth.Method = "oauth"
	c.Tailscale.Auth.OAuth.ClientID = "a"
	c.Tailscale.Auth.OAuth.ClientSecret = "s"
	c.Tailnets = []config.TailnetConfig{{Name: "beta", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want mutual-exclusion error")
	}
}

func TestValidateMultiTailnetRequiresNameAndAuth(t *testing.T) {
	c := config.Default()
	c.Tailscale = config.TailscaleConfig{}
	c.Tailnets = []config.TailnetConfig{{Name: "", Auth: config.TailscaleAuth{Method: "oauth"}}}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want missing-name error")
	}
}

func TestValidateMultiTailnetRejectsDuplicateName(t *testing.T) {
	c := config.Default()
	c.Tailscale = config.TailscaleConfig{}
	c.Tailnets = []config.TailnetConfig{
		{Name: "acme", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k1"}},
		{Name: "acme", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k2"}},
	}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want duplicate-name error")
	}
}

func TestValidateStreamingRequiresSingleTailnet(t *testing.T) {
	c := config.Default()
	c.Tailscale = config.TailscaleConfig{}
	c.Tailnets = []config.TailnetConfig{
		{Name: "acme", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k1"}},
		{Name: "beta", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k2"}},
	}
	c.Streaming.Enabled = true
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want streaming-requires-single-tailnet error")
	}
}
