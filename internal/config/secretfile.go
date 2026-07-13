package config

import (
	"fmt"
	"os"
	"strings"
)

// secretFileField pairs a Secret-valued config field with its "*_file" sibling
// for the value-XOR-file resolution performed by resolveSecretFiles. name is the
// dotted key of the VALUE field (e.g. "tailscale.auth.apikey"); its file sibling
// is always name+"_file" by the #169 seam-freeze convention.
type secretFileField struct {
	name  string
	value *Secret
	file  string
}

// resolveSecretFiles implements the Docker-secrets-style "*_file" convention
// for every Secret-bearing field (the #169 seam freeze: value XOR file). It
// mirrors the collectors.node_metrics.targets[].bearer_token_file precedent,
// but with a different lifecycle — these are read ONCE here, at Load, rather
// than per use.
//
// For each pair whose *_file sibling is set, the file is read once (trimmed of
// surrounding whitespace) and assigned to the value field. If the value field
// is ALSO already set, that is a conflict: it is NOT reported here so the rest
// of the fields still resolve in the same pass; instead it is recorded on
// Config.secretFileConflicts and reported by Validate (alongside every other
// configuration error) once resolution finishes. An unreadable/missing file has
// no reasonable fallback, so it IS a hard Load error naming the path.
//
// Called from Load after file+environment layering (so a *_file path set via
// either layer is honored) and before Validate's dependent-rule checks (several
// of which, e.g. admin.auth.token for pprof, need the resolved value).
func (c *Config) resolveSecretFiles() error {
	fields := []secretFileField{
		{"tailscale.auth.apikey", &c.Tailscale.Auth.APIKey, c.Tailscale.Auth.APIKeyFile},
		{"tailscale.auth.oauth.client_secret", &c.Tailscale.Auth.OAuth.ClientSecret, c.Tailscale.Auth.OAuth.ClientSecretFile},
		{"headscale.api_key", &c.Headscale.APIKey, c.Headscale.APIKeyFile},
		{"otlp.grafana_cloud.token", &c.OTLP.GrafanaCloud.Token, c.OTLP.GrafanaCloud.TokenFile},
		{"admin.auth.token", &c.Admin.Auth.Token, c.Admin.Auth.TokenFile},
		{"prometheus.auth.token", &c.Prometheus.Auth.Token, c.Prometheus.Auth.TokenFile},
		{"streaming.token", &c.Streaming.Token, c.Streaming.TokenFile},
		{"webhook.secret", &c.Webhook.Secret, c.Webhook.SecretFile},
		{"profiling.pyroscope.basic_auth_password", &c.Profiling.Pyroscope.BasicAuthPassword, c.Profiling.Pyroscope.BasicAuthPasswordFile},
	}
	// tailnets[] entries embed TailscaleAuth, so their apikey_file /
	// oauth.client_secret_file siblings get the same resolution for free (per
	// the seam freeze: "that list is file-only config anyway").
	for i := range c.Tailnets {
		t := &c.Tailnets[i]
		fields = append(fields,
			secretFileField{fmt.Sprintf("tailnets[%d].auth.apikey", i), &t.Auth.APIKey, t.Auth.APIKeyFile},
			secretFileField{fmt.Sprintf("tailnets[%d].auth.oauth.client_secret", i), &t.Auth.OAuth.ClientSecret, t.Auth.OAuth.ClientSecretFile},
		)
	}

	for _, f := range fields {
		if f.file == "" {
			continue
		}
		if *f.value != "" {
			c.secretFileConflicts = append(c.secretFileConflicts, fmt.Sprintf("%s and %s_file", f.name, f.name))
			continue
		}
		data, err := os.ReadFile(f.file)
		if err != nil {
			return fmt.Errorf("%s_file %q: %w", f.name, f.file, err)
		}
		*f.value = Secret(strings.TrimSpace(string(data)))
	}
	return nil
}
