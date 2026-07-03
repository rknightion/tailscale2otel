package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSecretRedactsInFormattingAndLogs is the structural guarantee: a Secret must
// never render its value through fmt verbs or slog, while Reveal still returns it.
func TestSecretRedactsInFormattingAndLogs(t *testing.T) {
	const raw = "tskey-supersecret-value-9f3a"
	s := Secret(raw)

	if s.Reveal() != raw {
		t.Fatalf("Reveal() = %q, want %q", s.Reveal(), raw)
	}

	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		if out := fmt.Sprintf(verb, s); strings.Contains(out, raw) {
			t.Errorf("fmt %s leaked the secret: %q", verb, out)
		}
	}

	// A struct embedding the Secret must redact it too (the realistic accident:
	// dumping the whole config).
	type holder struct {
		Name   string
		APIKey Secret
	}
	if out := fmt.Sprintf("%+v", holder{Name: "n", APIKey: s}); strings.Contains(out, raw) {
		t.Errorf("struct %%+v leaked the secret: %q", out)
	}

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "secret", s)
	if strings.Contains(buf.String(), raw) {
		t.Errorf("slog leaked the secret: %q", buf.String())
	}
}

func TestSecretRedactsInJSONAndYAML(t *testing.T) {
	s := Secret("hunter2")
	j, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(j) != `"REDACTED"` {
		t.Errorf("json.Marshal(Secret) = %s, want \"REDACTED\"", j)
	}
	if bytes.Contains(j, []byte("hunter2")) {
		t.Errorf("json.Marshal leaked the secret: %s", j)
	}
	y, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if bytes.Contains(y, []byte("hunter2")) {
		t.Errorf("yaml.Marshal leaked the secret: %s", y)
	}
	// Empty stays empty (matches redact()'s contract).
	if j, _ := json.Marshal(Secret("")); string(j) != `""` {
		t.Errorf("json.Marshal(empty Secret) = %s, want \"\"", j)
	}
}

// TestConfigDoesNotLeakSecretsWhenFormatted is the end-to-end guarantee: dumping
// a fully-populated Config (the realistic accidental leak) exposes no credential
// value. Guards against a future secret field added as a plain string.
func TestConfigDoesNotLeakSecretsWhenFormatted(t *testing.T) {
	c := Default()
	c.Tailscale.Auth.APIKey = "APIKEY-leak-canary"
	c.Tailscale.Auth.OAuth.ClientSecret = "OAUTHSECRET-leak-canary"
	c.OTLP.GrafanaCloud.Token = "GCLOUDTOKEN-leak-canary"
	c.Streaming.Token = "STREAMTOKEN-leak-canary"
	c.Webhook.Secret = "WEBHOOKSECRET-leak-canary"
	c.Admin.Auth.Token = "ADMINTOKEN-leak-canary"
	c.Profiling.Pyroscope.BasicAuthPassword = "PYROPASS-leak-canary"
	c.OTLP.Headers = map[string]Secret{"Authorization": "OTLPHEADER-leak-canary"}
	c.Collectors.NodeMetrics.Targets = []NodeMetricsTarget{{
		BearerToken: "BEARER-leak-canary",
		Headers:     map[string]Secret{"X-Scope-OrgID": "NMHEADER-leak-canary"},
	}}

	dump := fmt.Sprintf("%+v\n%#v", c, c)
	for _, secret := range []string{
		"APIKEY-leak-canary", "OAUTHSECRET-leak-canary", "GCLOUDTOKEN-leak-canary",
		"STREAMTOKEN-leak-canary", "WEBHOOKSECRET-leak-canary", "ADMINTOKEN-leak-canary",
		"PYROPASS-leak-canary", "BEARER-leak-canary", "OTLPHEADER-leak-canary", "NMHEADER-leak-canary",
	} {
		if strings.Contains(dump, secret) {
			t.Errorf("config dump leaked secret %q", secret)
		}
	}
	if got := c.Webhook.Secret.Reveal(); got != "WEBHOOKSECRET-leak-canary" {
		t.Errorf("Reveal() = %q, want the real value preserved", got)
	}
	// The header Secrets must still Reveal their real values at the point of use.
	if got := c.OTLP.Headers["Authorization"].Reveal(); got != "OTLPHEADER-leak-canary" {
		t.Errorf("OTLP header Reveal() = %q, want the real value preserved", got)
	}
	if got := c.Collectors.NodeMetrics.Targets[0].Headers["X-Scope-OrgID"].Reveal(); got != "NMHEADER-leak-canary" {
		t.Errorf("node-metrics header Reveal() = %q, want the real value preserved", got)
	}
}
