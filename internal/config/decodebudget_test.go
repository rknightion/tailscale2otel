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

// ── headscale.max_response_bytes (#488) ─────────────────────────────────────────
// The Headscale client had the same post-hoc decode cap tsapi did, hard-coded at
// 32 MiB. It follows the tailscale.max_response_bytes precedent exactly:
// fleet-wide, validated > 0, advisory above 64 MiB, settable from the
// environment as TS2OTEL_HEADSCALE__MAX_RESPONSE_BYTES.

func TestDefaultHeadscaleDecodeBudget(t *testing.T) {
	c := config.Default()
	if got, want := c.Headscale.MaxResponseBytes, int64(4<<20); got != want {
		t.Fatalf("headscale.max_response_bytes default = %d, want %d", got, want)
	}
}

func TestValidateRejectsNonPositiveHeadscaleDecodeBudget(t *testing.T) {
	for _, v := range []int64{0, -1} {
		c := config.Default()
		c.Provider = "headscale"
		c.Headscale.URL = "https://headscale.example.org"
		c.Headscale.APIKey = "k"
		c.Headscale.MaxResponseBytes = v
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "headscale.max_response_bytes") {
			t.Fatalf("MaxResponseBytes=%d: Validate() = %v, want an error naming headscale.max_response_bytes", v, err)
		}
	}
}

// A non-positive value is only rejected under provider: headscale, mirroring how
// the tailscale budgets are only checked under provider: tailscale — an unused
// block must not block startup.
func TestValidateIgnoresHeadscaleDecodeBudgetUnderTailscaleProvider(t *testing.T) {
	c := config.Default()
	c.Tailscale.Auth.Method = "apikey"
	c.Tailscale.Auth.APIKey = "k"
	c.Headscale.MaxResponseBytes = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil (headscale block unused under provider: tailscale)", err)
	}
}

func TestWarningsFlagsOversizedHeadscaleDecodeBudget(t *testing.T) {
	c := config.Default()
	c.Provider = "headscale"
	c.Headscale.URL = "https://headscale.example.org"
	c.Headscale.APIKey = "k"
	c.Headscale.MaxResponseBytes = 512 << 20
	var found bool
	for _, w := range c.Warnings() {
		if strings.Contains(w, "headscale.max_response_bytes") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Warnings() = %v, want an advisory about headscale.max_response_bytes", c.Warnings())
	}
	for _, w := range config.Default().Warnings() {
		if strings.Contains(w, "headscale.max_response_bytes") {
			t.Fatalf("default config warned about the headscale decode budget: %s", w)
		}
	}
}

func TestHeadscaleDecodeBudgetFromEnv(t *testing.T) {
	t.Setenv("TS2OTEL_HEADSCALE__MAX_RESPONSE_BYTES", "8388608")
	c, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := c.Headscale.MaxResponseBytes, int64(8<<20); got != want {
		t.Fatalf("headscale.max_response_bytes = %d, want %d (TS2OTEL_HEADSCALE__MAX_RESPONSE_BYTES)", got, want)
	}
}
