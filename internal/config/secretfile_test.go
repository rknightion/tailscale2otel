package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/config"
)

// secretFileCase describes one of the "*_file" secret siblings from the
// #169 seam freeze. yamlFile/yamlConflict build a minimal, otherwise-valid
// config document exercising just that field; get extracts the resolved
// Secret so the trim/read behavior can be asserted.
type secretFileCase struct {
	name         string // the dotted VALUE key, e.g. "tailscale.auth.apikey" (matches the Load/Validate error text)
	yamlFile     func(path string) string
	yamlConflict func(value, path string) string
	get          func(c *config.Config) config.Secret
}

func secretFileCases() []secretFileCase {
	return []secretFileCase{
		{
			name: "tailscale.auth.apikey",
			yamlFile: func(p string) string {
				return "tailscale:\n  tailnet: acme.org\n  auth:\n    method: apikey\n    apikey_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "tailscale:\n  tailnet: acme.org\n  auth:\n    method: apikey\n    apikey: " + v + "\n    apikey_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Tailscale.Auth.APIKey },
		},
		{
			name: "tailscale.auth.oauth.client_secret",
			yamlFile: func(p string) string {
				return "tailscale:\n  tailnet: acme.org\n  auth:\n    oauth:\n      client_secret_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "tailscale:\n  tailnet: acme.org\n  auth:\n    oauth:\n      client_secret: " + v + "\n      client_secret_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Tailscale.Auth.OAuth.ClientSecret },
		},
		{
			name: "headscale.api_key",
			yamlFile: func(p string) string {
				return "provider: headscale\nheadscale:\n  url: https://hs.example.com\n  api_key_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "provider: headscale\nheadscale:\n  url: https://hs.example.com\n  api_key: " + v + "\n  api_key_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Headscale.APIKey },
		},
		{
			name: "otlp.grafana_cloud.token",
			yamlFile: func(p string) string {
				return "otlp:\n  grafana_cloud:\n    token_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "otlp:\n  grafana_cloud:\n    token: " + v + "\n    token_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.OTLP.GrafanaCloud.Token },
		},
		{
			name: "admin.auth.token",
			yamlFile: func(p string) string {
				return "admin:\n  auth:\n    token_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "admin:\n  auth:\n    token: " + v + "\n    token_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Admin.Auth.Token },
		},
		{
			name: "prometheus.auth.token",
			yamlFile: func(p string) string {
				return "prometheus:\n  auth:\n    token_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "prometheus:\n  auth:\n    token: " + v + "\n    token_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Prometheus.Auth.Token },
		},
		{
			name: "streaming.token",
			yamlFile: func(p string) string {
				return "streaming:\n  token_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "streaming:\n  token: " + v + "\n  token_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Streaming.Token },
		},
		{
			name: "webhook.secret",
			yamlFile: func(p string) string {
				return "webhook:\n  secret_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "webhook:\n  secret: " + v + "\n  secret_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Webhook.Secret },
		},
		{
			name: "profiling.pyroscope.basic_auth_password",
			yamlFile: func(p string) string {
				return "profiling:\n  pyroscope:\n    basic_auth_password_file: " + p + "\n"
			},
			yamlConflict: func(v, p string) string {
				return "profiling:\n  pyroscope:\n    basic_auth_password: " + v + "\n    basic_auth_password_file: " + p + "\n"
			},
			get: func(c *config.Config) config.Secret { return c.Profiling.Pyroscope.BasicAuthPassword },
		},
		{
			name: "collectors.flowlogs.objectstore.access_key_id",
			yamlFile: func(p string) string {
				return objectStoreSecretYAML("access_key_id_file: "+p, "")
			},
			yamlConflict: func(v, p string) string {
				return objectStoreSecretYAML("access_key_id: "+v, "access_key_id_file: "+p)
			},
			get: func(c *config.Config) config.Secret {
				return c.Collectors.Flowlogs.ObjectStore.AccessKeyID
			},
		},
		{
			name: "collectors.flowlogs.objectstore.secret_access_key",
			yamlFile: func(p string) string {
				return objectStoreSecretYAML("secret_access_key_file: "+p, "")
			},
			yamlConflict: func(v, p string) string {
				return objectStoreSecretYAML("secret_access_key: "+v, "secret_access_key_file: "+p)
			},
			get: func(c *config.Config) config.Secret {
				return c.Collectors.Flowlogs.ObjectStore.SecretAccessKey
			},
		},
		{
			name: "collectors.flowlogs.objectstore.session_token",
			yamlFile: func(p string) string {
				return objectStoreSecretYAML("session_token_file: "+p, "")
			},
			yamlConflict: func(v, p string) string {
				return objectStoreSecretYAML("session_token: "+v, "session_token_file: "+p)
			},
			get: func(c *config.Config) config.Secret {
				return c.Collectors.Flowlogs.ObjectStore.SessionToken
			},
		},
	}
}

func objectStoreSecretYAML(lines ...string) string {
	var b strings.Builder
	b.WriteString("collectors:\n  flowlogs:\n    source: objectstore\n    objectstore:\n")
	b.WriteString("      endpoint: https://s3.eu-west-2.amazonaws.com\n")
	b.WriteString("      region: eu-west-2\n      bucket: flows\n")
	for _, line := range lines {
		if line != "" {
			b.WriteString("      " + line + "\n")
		}
	}
	return b.String()
}

// writeSecretFile writes content to a file in a fresh temp dir and returns its
// path, mirroring writeTemp's pattern in config_test.go for the config file
// itself.
func writeSecretFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	return p
}

// TestSecretFile_ResolvesAndTrims covers every *_file field from the #169 seam
// freeze: the file is read once at Load and its content is trimmed of
// surrounding whitespace (the classic docker-secrets trailing-newline
// papercut) before it lands in the paired Secret field.
func TestSecretFile_ResolvesAndTrims(t *testing.T) {
	for _, tc := range secretFileCases() {
		t.Run(tc.name, func(t *testing.T) {
			secretPath := writeSecretFile(t, "s3cr3t-value\n\n")
			cfgPath := writeTemp(t, tc.yamlFile(secretPath))

			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got, want := tc.get(cfg).Reveal(), "s3cr3t-value"; got != want {
				t.Errorf("%s = %q, want %q (trailing whitespace must be trimmed)", tc.name, got, want)
			}
		})
	}
}

// TestSecretFile_MissingFileIsLoadError covers the "unreadable/missing file is
// a load error naming the path" half of the #169 seam freeze.
func TestSecretFile_MissingFileIsLoadError(t *testing.T) {
	for _, tc := range secretFileCases() {
		t.Run(tc.name, func(t *testing.T) {
			missing := filepath.Join(t.TempDir(), "does-not-exist.txt")
			cfgPath := writeTemp(t, tc.yamlFile(missing))

			_, err := config.Load(cfgPath)
			if err == nil {
				t.Fatal("Load: want error for a missing secret file, got nil")
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("Load error %q does not name the missing path %q", err.Error(), missing)
			}
		})
	}
}

// TestSecretFile_ValueAndFileConflict covers the value-XOR-file half: setting
// both the plain value and its *_file sibling is a Validate error naming the
// key pair.
func TestSecretFile_ValueAndFileConflict(t *testing.T) {
	for _, tc := range secretFileCases() {
		t.Run(tc.name, func(t *testing.T) {
			secretPath := writeSecretFile(t, "unused\n")
			cfgPath := writeTemp(t, tc.yamlConflict("inline-value", secretPath))

			_, err := config.Load(cfgPath)
			if err == nil {
				t.Fatal("Load: want a value-XOR-file conflict error, got nil")
			}
			if !strings.Contains(err.Error(), tc.name) || !strings.Contains(err.Error(), tc.name+"_file") {
				t.Errorf("Load error %q does not name both %s and %s_file", err.Error(), tc.name, tc.name)
			}
			if !strings.Contains(err.Error(), "value XOR file") {
				t.Errorf("Load error %q does not explain the value-XOR-file rule", err.Error())
			}
		})
	}
}

// TestSecretFile_EnvValueAndFileConflictNamesSources keeps the value-XOR-file
// guard, while making the common "secret in env, path in YAML" collision
// actionable without disclosing either credential value.
func TestSecretFile_EnvValueAndFileConflictNamesSources(t *testing.T) {
	secretPath := writeSecretFile(t, "unused\n")
	const envName = "TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET"
	t.Setenv(envName, "injected-value")

	_, err := config.Load(writeTemp(t, "tailscale:\n  auth:\n    oauth:\n      client_secret_file: "+secretPath+"\n"))
	if err == nil {
		t.Fatal("Load: want a value-XOR-file conflict")
	}
	if !strings.Contains(err.Error(), envName) {
		t.Errorf("Load error %q does not name environment source %q", err, envName)
	}
	if !strings.Contains(err.Error(), secretPath) {
		t.Errorf("Load error %q does not name file source %q", err, secretPath)
	}
}

// TestSecretFile_SettableViaEnvVar proves a *_file key gets the standard
// TS2OTEL_* environment-variable convention for free, same as every other
// plain string config field — no special-casing needed in env.go.
func TestSecretFile_SettableViaEnvVar(t *testing.T) {
	secretPath := writeSecretFile(t, "  from-env-var  \n")
	t.Setenv("TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN_FILE", secretPath)

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.OTLP.GrafanaCloud.Token.Reveal(), "from-env-var"; got != want {
		t.Errorf("otlp.grafana_cloud.token = %q, want %q", got, want)
	}
}

func TestSecretFile_ObjectStoreSettableViaEnvVar(t *testing.T) {
	secretPath := writeSecretFile(t, "  from-objectstore-env-file  \n")
	t.Setenv("TS2OTEL_COLLECTORS__FLOWLOGS__OBJECTSTORE__SECRET_ACCESS_KEY_FILE", secretPath)

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Collectors.Flowlogs.ObjectStore.SecretAccessKey.Reveal(), "from-objectstore-env-file"; got != want {
		t.Errorf("objectstore.secret_access_key = %q, want %q", got, want)
	}
}

// TestSecretFile_TailnetObjectStoreCredentialFiles covers the per-tailnet
// object-store credentials from #284. A tailnets[] entry has no TS2OTEL_* env
// form, so the *_file sibling is the ONLY way to keep a static S3 credential out
// of the YAML — it must resolve, trim, and honor value-XOR-file per entry, and
// entry 0's file must never land on entry 1.
func TestSecretFile_TailnetObjectStoreCredentialFiles(t *testing.T) {
	for _, tc := range []struct {
		key   string
		field string
		get   func(config.TailnetConfig) config.Secret
	}{
		{"access_key_id", "access_key_id_file", func(t config.TailnetConfig) config.Secret {
			return t.ObjectStore.Flow.AccessKeyID
		}},
		{"secret_access_key", "secret_access_key_file", func(t config.TailnetConfig) config.Secret {
			return t.ObjectStore.Flow.SecretAccessKey
		}},
		{"session_token", "session_token_file", func(t config.TailnetConfig) config.Secret {
			return t.ObjectStore.Flow.SessionToken
		}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			alphaPath := writeSecretFile(t, "alpha-"+tc.key+"\n\n")
			betaPath := writeSecretFile(t, "  beta-"+tc.key+"  ")
			cfgPath := writeTemp(t, tailnetObjectStoreYAML(
				[]string{tc.field + ": " + alphaPath},
				[]string{tc.field + ": " + betaPath}))

			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got, want := tc.get(cfg.Tailnets[0]).Reveal(), "alpha-"+tc.key; got != want {
				t.Errorf("tailnets[0] %s = %q, want %q (trimmed)", tc.key, got, want)
			}
			if got, want := tc.get(cfg.Tailnets[1]).Reveal(), "beta-"+tc.key; got != want {
				t.Errorf("tailnets[1] %s = %q, want %q (trimmed)", tc.key, got, want)
			}

			// value XOR file, per entry, named by index.
			conflict := writeTemp(t, tailnetObjectStoreYAML(
				nil,
				[]string{tc.key + ": inline-value", tc.field + ": " + betaPath}))
			_, err = config.Load(conflict)
			if err == nil {
				t.Fatal("Load: want a value-XOR-file conflict for the tailnets[1] destination")
			}
			want := "tailnets[1].objectstore.flow." + tc.key
			if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "value XOR file") {
				t.Errorf("Load error %q does not name %s and the value-XOR-file rule", err.Error(), want)
			}

			// A missing file is a hard Load error naming the path.
			missing := filepath.Join(t.TempDir(), "does-not-exist.txt")
			gone := writeTemp(t, tailnetObjectStoreYAML(nil, []string{tc.field + ": " + missing}))
			if _, err := config.Load(gone); err == nil || !strings.Contains(err.Error(), missing) {
				t.Errorf("Load error = %v, want it to name the missing path %q", err, missing)
			}
		})
	}
}

// tailnetObjectStoreYAML builds a two-tailnet document where each entry owns a
// complete object-store destination, with extra "key: value" credential lines
// spliced into each entry's objectstore.flow block.
func tailnetObjectStoreYAML(alphaExtra, betaExtra []string) string {
	return `
collectors:
  flowlogs:
    source: objectstore
tailnets:
  - name: alpha.example.com
    auth:
      method: apikey
      apikey: alpha-key
    objectstore:
      flow:
        endpoint: https://s3.eu-west-2.amazonaws.com
        region: eu-west-2
        bucket: alpha-flows
` + flowLines(alphaExtra) + `  - name: beta.example.com
    auth:
      method: apikey
      apikey: beta-key
    objectstore:
      flow:
        endpoint: https://s3.us-east-1.amazonaws.com
        region: us-east-1
        bucket: beta-flows
` + flowLines(betaExtra)
}

// flowLines indents bare "key: value" pairs to the objectstore.flow mapping
// level used by tailnetObjectStoreYAML.
func flowLines(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("        " + l + "\n")
	}
	return b.String()
}

// TestSecretFile_TailnetsEntryInheritsFileFields confirms the seam-freeze
// claim that tailnets[] entries get apikey_file / oauth.client_secret_file for
// free by embedding TailscaleAuth, without any extra resolution wiring.
func TestSecretFile_TailnetsEntryInheritsFileFields(t *testing.T) {
	secretPath := writeSecretFile(t, "tailnet-secret\n")
	cfgPath := writeTemp(t, `
tailnets:
  - name: acme
    auth:
      method: apikey
      apikey_file: `+secretPath+`
`)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tailnets) != 1 {
		t.Fatalf("tailnets = %d, want 1", len(cfg.Tailnets))
	}
	if got, want := cfg.Tailnets[0].Auth.APIKey.Reveal(), "tailnet-secret"; got != want {
		t.Errorf("tailnets[0].auth.apikey = %q, want %q", got, want)
	}
}
