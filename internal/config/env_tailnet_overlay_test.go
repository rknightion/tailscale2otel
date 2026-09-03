package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/config"
)

func TestLoad_TailnetOAuthSecretEnvironmentOverlay(t *testing.T) {
	const envName = "TS2OTEL_TAILNET_FLEET_A__AUTH__OAUTH__CLIENT_SECRET"
	t.Setenv(envName, "injected")

	cfg, err := config.Load(writeTemp(t, `
tailnets:
  - name: fleet-a
    auth:
      method: oauth
      oauth:
        client_id: client-id
        client_secret: file-value
`))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := string(cfg.Tailnets[0].Auth.OAuth.ClientSecret); got != "injected" {
		t.Errorf("tailnets[0].auth.oauth.client_secret = %q, want environment overlay to win", got)
	}
}

func TestLoad_TailnetOAuthSecretEnvironmentOverlayRejectsUnknownTailnet(t *testing.T) {
	const envName = "TS2OTEL_TAILNET_NOT_CONFIGURED__AUTH__OAUTH__CLIENT_SECRET"
	t.Setenv(envName, "injected")

	_, err := config.Load(writeTemp(t, `
tailnets:
  - name: fleet-a
    auth:
      method: oauth
      oauth:
        client_id: client-id
        client_secret: file-value
`))
	if err == nil {
		t.Fatal("Load() succeeded, want unknown-tailnet environment overlay error")
	}
	if !strings.Contains(err.Error(), envName) || !strings.Contains(err.Error(), "no configured tailnet") {
		t.Errorf("Load() error = %q, want env name and no-configured-tailnet explanation", err)
	}
}

func TestLoad_TailnetOAuthSecretEnvironmentOverlayConflictNamesEnvAndFile(t *testing.T) {
	const envName = "TS2OTEL_TAILNET_FLEET_A__AUTH__OAUTH__CLIENT_SECRET"
	t.Setenv(envName, "injected")
	secretFile := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(secretFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	_, err := config.Load(writeTemp(t, `
tailnets:
  - name: fleet-a
    auth:
      method: oauth
      oauth:
        client_id: client-id
        client_secret_file: `+secretFile+`
`))
	if err == nil {
		t.Fatal("Load() succeeded, want value-XOR-file conflict")
	}
	if !strings.Contains(err.Error(), envName) || !strings.Contains(err.Error(), secretFile) {
		t.Errorf("Load() error = %q, want both %q and %q", err, envName, secretFile)
	}
}

func TestLoad_HeadscaleDefaultsDeviceVersionChecksOffUnlessExplicit(t *testing.T) {
	base := `
provider: headscale
headscale:
  url: https://headscale.invalid
  api_key: test-only
`

	implicit, err := config.Load(writeTemp(t, base))
	if err != nil {
		t.Fatalf("Load(implicit) = %v", err)
	}
	if implicit.VersionChecks.Devices.Enabled {
		t.Error("implicit Headscale version_checks.devices.enabled = true, want false")
	}

	explicit, err := config.Load(writeTemp(t, base+"\nversion_checks:\n  devices:\n    enabled: true\n"))
	if err != nil {
		t.Fatalf("Load(explicit) = %v", err)
	}
	if !explicit.VersionChecks.Devices.Enabled {
		t.Error("explicit Headscale version_checks.devices.enabled = false, want true")
	}
}
