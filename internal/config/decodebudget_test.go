package config_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

func TestDefaultDecodeBudgets(t *testing.T) {
	c := config.Default()
	if got, want := c.Tailscale.MaxResponseBytes, int64(4<<20); got != want {
		t.Fatalf("tailscale.max_response_bytes default = %d, want %d", got, want)
	}
	if got, want := c.Tailscale.MaxLogResponseBytes, int64(32<<20); got != want {
		t.Fatalf("tailscale.max_log_response_bytes default = %d, want %d", got, want)
	}
}

func TestResolvedTailnetsCarriesDecodeBudgets_Single(t *testing.T) {
	c := config.Default()
	c.Tailscale.MaxResponseBytes = 7 << 20
	c.Tailscale.MaxLogResponseBytes = 11 << 20
	rts := c.ResolvedTailnets()
	if len(rts) != 1 {
		t.Fatalf("got %d resolved tailnets", len(rts))
	}
	if rts[0].MaxResponseBytes != 7<<20 || rts[0].MaxLogResponseBytes != 11<<20 {
		t.Fatalf("budgets = %d/%d, want %d/%d",
			rts[0].MaxResponseBytes, rts[0].MaxLogResponseBytes, 7<<20, 11<<20)
	}
}

// The budgets are fleet-wide: every entry of a tailnets: list gets the top-level
// value, which is also how TS2OTEL_TAILSCALE__MAX_* reaches multi-tailnet mode
// (a tailnets[] field would be file-only).
func TestResolvedTailnetsCarriesDecodeBudgets_Multi(t *testing.T) {
	c := config.Default()
	c.Tailscale.Tailnet = ""
	c.Tailscale.MaxResponseBytes = 5 << 20
	c.Tailscale.MaxLogResponseBytes = 9 << 20
	c.Tailnets = []config.TailnetConfig{
		{Name: "alpha", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k1"}},
		{Name: "beta", Auth: config.TailscaleAuth{Method: "apikey", APIKey: "k2"}},
	}
	for i, rt := range c.ResolvedTailnets() {
		if rt.MaxResponseBytes != 5<<20 || rt.MaxLogResponseBytes != 9<<20 {
			t.Fatalf("tailnets[%d] budgets = %d/%d, want %d/%d",
				i, rt.MaxResponseBytes, rt.MaxLogResponseBytes, 5<<20, 9<<20)
		}
	}
}

// A zero (e.g. a hand-written config predating the keys) must resolve to the
// built-in default, never to "no bytes may be decoded".
func TestResolvedTailnetsBackfillsZeroDecodeBudgets(t *testing.T) {
	c := config.Default()
	c.Tailscale.MaxResponseBytes = 0
	c.Tailscale.MaxLogResponseBytes = 0
	rt := c.ResolvedTailnets()[0]
	if rt.MaxResponseBytes != 4<<20 || rt.MaxLogResponseBytes != 32<<20 {
		t.Fatalf("budgets = %d/%d, want the built-in defaults", rt.MaxResponseBytes, rt.MaxLogResponseBytes)
	}
}

func TestValidateRejectsNonPositiveDecodeBudgets(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*config.Config)
		want string
	}{
		{"max_response_bytes zero", func(c *config.Config) { c.Tailscale.MaxResponseBytes = 0 }, "tailscale.max_response_bytes"},
		{"max_response_bytes negative", func(c *config.Config) { c.Tailscale.MaxResponseBytes = -1 }, "tailscale.max_response_bytes"},
		{"max_log_response_bytes zero", func(c *config.Config) { c.Tailscale.MaxLogResponseBytes = 0 }, "tailscale.max_log_response_bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			c.Tailscale.Auth.Method = "apikey"
			c.Tailscale.Auth.APIKey = "k"
			tc.set(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error naming %s", err, tc.want)
			}
		})
	}
}

func TestWarningsFlagsOversizedDecodeBudget(t *testing.T) {
	c := config.Default()
	c.Tailscale.MaxLogResponseBytes = 512 << 20
	var found bool
	for _, w := range c.Warnings() {
		if strings.Contains(w, "tailscale.max_log_response_bytes") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Warnings() = %v, want an advisory about tailscale.max_log_response_bytes", c.Warnings())
	}
	// The defaults must be quiet.
	for _, w := range config.Default().Warnings() {
		if strings.Contains(w, "max_response_bytes") || strings.Contains(w, "max_log_response_bytes") {
			t.Fatalf("default config warned about a decode budget: %s", w)
		}
	}
}
