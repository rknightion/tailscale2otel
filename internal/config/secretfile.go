package config

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/rknightion/tailscale2otel/v5/internal/safefile"
)

// secretFileField pairs a Secret-valued config field with its "*_file" sibling
// for the value-XOR-file resolution performed by resolveSecretFiles. name is the
// dotted key of the VALUE field (e.g. "tailscale.auth.apikey"); its file sibling
// is always name+"_file" by the #169 seam-freeze convention.
type secretFileField struct {
	name     string
	value    *Secret
	file     string
	valueEnv string
}

// secretFileConflict records the sources of a value-XOR-file collision without
// retaining either secret value. An environment variable name or file path is
// safe diagnostic metadata and is what an operator needs to correct the
// configuration layering mistake.
type secretFileConflict struct {
	value string
	file  string
}

func (c secretFileConflict) String() string {
	return c.value + " and " + c.file
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
		{name: "tailscale.auth.apikey", value: &c.Tailscale.Auth.APIKey, file: c.Tailscale.Auth.APIKeyFile},
		{name: "tailscale.auth.oauth.client_secret", value: &c.Tailscale.Auth.OAuth.ClientSecret, file: c.Tailscale.Auth.OAuth.ClientSecretFile},
		{name: "headscale.api_key", value: &c.Headscale.APIKey, file: c.Headscale.APIKeyFile},
		{name: "otlp.grafana_cloud.token", value: &c.OTLP.GrafanaCloud.Token, file: c.OTLP.GrafanaCloud.TokenFile},
		{name: "admin.auth.token", value: &c.Admin.Auth.Token, file: c.Admin.Auth.TokenFile},
		{name: "prometheus.auth.token", value: &c.Prometheus.Auth.Token, file: c.Prometheus.Auth.TokenFile},
		{name: "streaming.token", value: &c.Streaming.Token, file: c.Streaming.TokenFile},
		{name: "webhook.secret", value: &c.Webhook.Secret, file: c.Webhook.SecretFile},
		{name: "profiling.pyroscope.basic_auth_password", value: &c.Profiling.Pyroscope.BasicAuthPassword, file: c.Profiling.Pyroscope.BasicAuthPasswordFile},
		{name: "collectors.flowlogs.objectstore.access_key_id", value: &c.Collectors.Flowlogs.ObjectStore.AccessKeyID, file: c.Collectors.Flowlogs.ObjectStore.AccessKeyIDFile},
		{name: "collectors.flowlogs.objectstore.secret_access_key", value: &c.Collectors.Flowlogs.ObjectStore.SecretAccessKey, file: c.Collectors.Flowlogs.ObjectStore.SecretAccessKeyFile},
		{name: "collectors.flowlogs.objectstore.session_token", value: &c.Collectors.Flowlogs.ObjectStore.SessionToken, file: c.Collectors.Flowlogs.ObjectStore.SessionTokenFile},
		// Auditlogs and K8sAudit share ObjectStoreConfig with flowlogs, so they
		// have the same *_file siblings and runtime consumers. Every destination
		// must be registered here: omitting one silently hands an empty credential
		// to its S3 client and surfaces much later as a bucket-auth failure.
		{name: "collectors.auditlogs.objectstore.access_key_id", value: &c.Collectors.Auditlogs.ObjectStore.AccessKeyID, file: c.Collectors.Auditlogs.ObjectStore.AccessKeyIDFile},
		{name: "collectors.auditlogs.objectstore.secret_access_key", value: &c.Collectors.Auditlogs.ObjectStore.SecretAccessKey, file: c.Collectors.Auditlogs.ObjectStore.SecretAccessKeyFile},
		{name: "collectors.auditlogs.objectstore.session_token", value: &c.Collectors.Auditlogs.ObjectStore.SessionToken, file: c.Collectors.Auditlogs.ObjectStore.SessionTokenFile},
		{name: "collectors.k8s_audit.objectstore.access_key_id", value: &c.Collectors.K8sAudit.ObjectStore.AccessKeyID, file: c.Collectors.K8sAudit.ObjectStore.AccessKeyIDFile},
		{name: "collectors.k8s_audit.objectstore.secret_access_key", value: &c.Collectors.K8sAudit.ObjectStore.SecretAccessKey, file: c.Collectors.K8sAudit.ObjectStore.SecretAccessKeyFile},
		{name: "collectors.k8s_audit.objectstore.session_token", value: &c.Collectors.K8sAudit.ObjectStore.SessionToken, file: c.Collectors.K8sAudit.ObjectStore.SessionTokenFile},
		{name: "enrichment.geoip.download.license_key", value: &c.Enrichment.GeoIP.Download.LicenseKey, file: c.Enrichment.GeoIP.Download.LicenseKeyFile},
		{name: "grafana_annotations.token", value: &c.GrafanaAnnotations.Token, file: c.GrafanaAnnotations.TokenFile},
	}
	// tailnets[] entries embed TailscaleAuth, so their apikey_file /
	// oauth.client_secret_file siblings get the same resolution for free (per
	// the seam freeze: "that list is file-only config anyway").
	//
	// Their objectstore.flow credentials need the pointers spelling out, but get
	// the identical value-XOR-file contract — and for a list entry the *_file path
	// is the ONLY way to supply a static credential without writing it into YAML,
	// since a list-of-structs key has no TS2OTEL_* env form (#79).
	for i := range c.Tailnets {
		t := &c.Tailnets[i]
		flow := &t.ObjectStore.Flow
		audit := &t.ObjectStore.Audit
		k8sAudit := &t.ObjectStore.K8sAudit
		fields = append(fields,
			secretFileField{name: fmt.Sprintf("tailnets[%d].auth.apikey", i), value: &t.Auth.APIKey, file: t.Auth.APIKeyFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].auth.oauth.client_secret", i), value: &t.Auth.OAuth.ClientSecret, file: t.Auth.OAuth.ClientSecretFile, valueEnv: configuredTailnetOAuthSecretEnv(t.Name)},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.flow.access_key_id", i), value: &flow.AccessKeyID, file: flow.AccessKeyIDFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.flow.secret_access_key", i), value: &flow.SecretAccessKey, file: flow.SecretAccessKeyFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.flow.session_token", i), value: &flow.SessionToken, file: flow.SessionTokenFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.audit.access_key_id", i), value: &audit.AccessKeyID, file: audit.AccessKeyIDFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.audit.secret_access_key", i), value: &audit.SecretAccessKey, file: audit.SecretAccessKeyFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.audit.session_token", i), value: &audit.SessionToken, file: audit.SessionTokenFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.k8s_audit.access_key_id", i), value: &k8sAudit.AccessKeyID, file: k8sAudit.AccessKeyIDFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.k8s_audit.secret_access_key", i), value: &k8sAudit.SecretAccessKey, file: k8sAudit.SecretAccessKeyFile},
			secretFileField{name: fmt.Sprintf("tailnets[%d].objectstore.k8s_audit.session_token", i), value: &k8sAudit.SessionToken, file: k8sAudit.SessionTokenFile},
		)
	}
	for i := range c.Streaming.Routes {
		r := &c.Streaming.Routes[i]
		fields = append(fields, secretFileField{
			name:  fmt.Sprintf("streaming.routes[%d].token", i),
			value: &r.Token,
			file:  r.TokenFile,
		})
	}
	for i := range c.Webhook.Routes {
		r := &c.Webhook.Routes[i]
		fields = append(fields, secretFileField{
			name:  fmt.Sprintf("webhook.routes[%d].secret", i),
			value: &r.Secret,
			file:  r.SecretFile,
		})
	}

	for _, f := range fields {
		if f.file == "" {
			continue
		}
		if *f.value != "" {
			c.secretFileConflicts = append(c.secretFileConflicts, secretFileConflict{
				value: secretValueSource(f),
				file:  secretFileSource(f),
			})
			continue
		}
		data, err := safefile.ReadRegular(f.file, safefile.MaxSecretBytes, safefile.AllowSymlink)
		if err != nil {
			// f.file already holds the #310-resolved path (resolveConfigPaths runs
			// before resolveSecretFiles). When resolution actually rewrote it --
			// a relative YAML-sourced path against the config file's directory --
			// show the operator both what they wrote and what this process
			// actually tried to open; a bare resolved path alone can look totally
			// unrelated to the file the operator names in their config.
			return fmt.Errorf("%s_file %s: %w", f.name, c.pathForError(f.name+"_file", f.file), err)
		}
		*f.value = Secret(strings.TrimSpace(string(data)))
	}
	return nil
}

func secretValueSource(f secretFileField) string {
	if f.valueEnv != "" {
		return fmt.Sprintf("%s (from %s)", f.name, f.valueEnv)
	}
	if envName := envVarForKey(f.name); envName != "" {
		return fmt.Sprintf("%s (from %s)", f.name, envName)
	}
	return f.name
}

func secretFileSource(f secretFileField) string {
	fileKey := f.name + "_file"
	if envName := envVarForKey(fileKey); envName != "" {
		return fmt.Sprintf("%s (from %s)", fileKey, envName)
	}
	return fmt.Sprintf("%s (file %q)", fileKey, f.file)
}

// envVarForKey returns the actual TS2OTEL_* spelling that supplied key. A
// config key ordinarily has one canonical uppercase spelling, but sorting
// keeps diagnostics deterministic even on an unusual environment containing
// multiple case variants for the same key.
func envVarForKey(key string) string {
	var matches []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(name, EnvPrefix) && envKey(name) == key {
			matches = append(matches, name)
		}
	}
	slices.Sort(matches)
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func configuredTailnetOAuthSecretEnv(tailnet string) string {
	name := tailnetEnvPrefix + tailnetEnvName(tailnet) + tailnetOAuthSecretEnvSuffix
	if _, ok := os.LookupEnv(name); ok {
		return name
	}
	return ""
}
