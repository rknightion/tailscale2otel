package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This file implements #310: resolving every filesystem-path-bearing config
// field relative to its CONFIGURATION SOURCE, rather than the process's
// working directory.
//
// The frozen contract:
//
//   - A relative path that came from the YAML FILE resolves against the
//     file's own directory, not the process CWD. An absolute path is used
//     as-is.
//   - A path supplied via a TS2OTEL_* ENVIRONMENT VARIABLE keeps process-CWD
//     semantics, deliberately and unconditionally: the environment layer is
//     set by whoever launches the process (systemd, a container entrypoint,
//     a shell), which is a different actor from whoever wrote the config
//     file, and silently reinterpreting a path THEY supplied against a
//     directory they never mentioned would be worse than the pre-#310
//     behavior.
//   - No implicit filesystem searching: one candidate path, resolved once.
//   - With no config file in play at all (env-only operation, no -config
//     flag), a relative path keeps CWD semantics -- there is no config
//     directory to resolve against.
//   - Every path-bearing field follows the same rule; a partial fix would
//     make the rule unpredictable per field, which is worse than not fixing
//     it at all. See pathFields and TestPathFieldsCoversEveryPathBearingField.

// pathField is one path-bearing string field on Config, registered so Load
// can resolve it and so a later "no such file" error can name both the
// configured and resolved path.
//
// key is the dotted config path, using the same lower_snake_case leaf names
// as the TS2OTEL_ environment-variable convention (e.g. "admin.tls.cert_file",
// or "tailnets[0].auth.apikey_file" for a list entry, which cannot be set via
// the environment at all -- see structSliceEnvKeys). It doubles as the lookup
// key into Config.pathResolutions.
type pathField struct {
	key   string
	value *string
}

// pathResolution records what resolveConfigPaths did to one field: exactly
// what the operator wrote (Configured) and what was actually opened
// (Resolved). The two differ only when a relative YAML-sourced path was
// rewritten against the config file's directory. Keeping both lets a later
// "no such file" error show the operator both the value they wrote AND the
// path this process actually tried -- the difference between a five-second
// fix and an hour when those two differ.
type pathResolution struct {
	Configured string
	Resolved   string
}

// pathFields enumerates EVERY filesystem-path-bearing field on Config: every
// "*_file" Docker-secrets-style secret sibling, every TLS cert/key/CA file,
// the workload-identity ID token file, the checkpoint file, and the
// ingress-WAL directory.
//
// This is the SINGLE place a new path-bearing field must be registered.
// TestPathFieldsCoversEveryPathBearingField guards it by independently
// deriving the same set from the struct tags via reflection and failing if
// the two disagree -- so a field added to the Config struct but forgotten
// here fails a test instead of silently keeping CWD semantics forever.
func (c *Config) pathFields() []pathField {
	fields := []pathField{
		{"tailscale.auth.apikey_file", &c.Tailscale.Auth.APIKeyFile},
		{"tailscale.auth.oauth.client_secret_file", &c.Tailscale.Auth.OAuth.ClientSecretFile},
		{"tailscale.auth.workload_identity.id_token_file", &c.Tailscale.Auth.WorkloadIdentity.IDTokenFile},
		{"headscale.api_key_file", &c.Headscale.APIKeyFile},
		{"otlp.grafana_cloud.token_file", &c.OTLP.GrafanaCloud.TokenFile},
		{"otlp.tls.ca_file", &c.OTLP.TLS.CAFile},
		{"otlp.tls.cert_file", &c.OTLP.TLS.CertFile},
		{"otlp.tls.key_file", &c.OTLP.TLS.KeyFile},
		// Per-signal TLS overrides follow the same relative-path resolution rule as
		// the common block (#310): a signal routed to its own gateway may need its
		// own CA or client keypair, and a path that resolved differently depending
		// on which block it sat in would be a trap.
		{"otlp.metrics.tls.ca_file", &c.OTLP.Metrics.TLS.CAFile},
		{"otlp.metrics.tls.cert_file", &c.OTLP.Metrics.TLS.CertFile},
		{"otlp.metrics.tls.key_file", &c.OTLP.Metrics.TLS.KeyFile},
		{"otlp.logs.tls.ca_file", &c.OTLP.Logs.TLS.CAFile},
		{"otlp.logs.tls.cert_file", &c.OTLP.Logs.TLS.CertFile},
		{"otlp.logs.tls.key_file", &c.OTLP.Logs.TLS.KeyFile},
		{"otlp.traces.tls.ca_file", &c.OTLP.Traces.TLS.CAFile},
		{"otlp.traces.tls.cert_file", &c.OTLP.Traces.TLS.CertFile},
		{"otlp.traces.tls.key_file", &c.OTLP.Traces.TLS.KeyFile},
		{"admin.tls.cert_file", &c.Admin.TLS.CertFile},
		{"admin.tls.key_file", &c.Admin.TLS.KeyFile},
		{"admin.auth.token_file", &c.Admin.Auth.TokenFile},
		{"prometheus.tls.cert_file", &c.Prometheus.TLS.CertFile},
		{"prometheus.tls.key_file", &c.Prometheus.TLS.KeyFile},
		{"prometheus.tls.client_ca_file", &c.Prometheus.TLS.ClientCAFile},
		{"prometheus.auth.token_file", &c.Prometheus.Auth.TokenFile},
		{"profiling.pyroscope.basic_auth_password_file", &c.Profiling.Pyroscope.BasicAuthPasswordFile},
		{"profiling.pyroscope.tls.ca_file", &c.Profiling.Pyroscope.TLS.CAFile},
		{"profiling.pyroscope.tls.cert_file", &c.Profiling.Pyroscope.TLS.CertFile},
		{"profiling.pyroscope.tls.key_file", &c.Profiling.Pyroscope.TLS.KeyFile},
		{"checkpoint.file_path", &c.Checkpoint.FilePath},
		{"ingress_wal.directory", &c.IngressWAL.Directory},
		{"streaming.tls.cert_file", &c.Streaming.TLS.CertFile},
		{"streaming.tls.key_file", &c.Streaming.TLS.KeyFile},
		{"streaming.token_file", &c.Streaming.TokenFile},
		{"webhook.tls.cert_file", &c.Webhook.TLS.CertFile},
		{"webhook.tls.key_file", &c.Webhook.TLS.KeyFile},
		{"webhook.secret_file", &c.Webhook.SecretFile},
		{"collectors.flowlogs.objectstore.access_key_id_file", &c.Collectors.Flowlogs.ObjectStore.AccessKeyIDFile},
		{"collectors.flowlogs.objectstore.secret_access_key_file", &c.Collectors.Flowlogs.ObjectStore.SecretAccessKeyFile},
		{"collectors.flowlogs.objectstore.session_token_file", &c.Collectors.Flowlogs.ObjectStore.SessionTokenFile},
		{"collectors.auditlogs.objectstore.access_key_id_file", &c.Collectors.Auditlogs.ObjectStore.AccessKeyIDFile},
		{"collectors.auditlogs.objectstore.secret_access_key_file", &c.Collectors.Auditlogs.ObjectStore.SecretAccessKeyFile},
		{"collectors.auditlogs.objectstore.session_token_file", &c.Collectors.Auditlogs.ObjectStore.SessionTokenFile},
		{"enrichment.geoip.country_database", &c.Enrichment.GeoIP.CountryDatabase},
		{"enrichment.geoip.asn_database", &c.Enrichment.GeoIP.ASNDatabase},
		{"enrichment.geoip.download.license_key_file", &c.Enrichment.GeoIP.Download.LicenseKeyFile},
		// The GeoIP download directory is a path the process WRITES to rather
		// than reads, but it resolves by the same rule as every other one (#310):
		// relative to the config file's directory, so a relative path in a
		// mounted config means the same thing wherever the process is started.
		{"enrichment.geoip.download.directory", &c.Enrichment.GeoIP.Download.Directory},
	}

	// tailnets[] entries embed TailscaleAuth and their own per-signal
	// ObjectStore blocks, so they carry the same "*_file" siblings as the
	// single-tailnet fields above -- and, being file-only (no TS2OTEL_* env
	// form; see structSliceEnvKeys), every one of these is unconditionally
	// file-provenance whenever a config file is in play.
	for i := range c.Tailnets {
		t := &c.Tailnets[i]
		flow := &t.ObjectStore.Flow
		audit := &t.ObjectStore.Audit
		fields = append(fields,
			pathField{fmt.Sprintf("tailnets[%d].auth.apikey_file", i), &t.Auth.APIKeyFile},
			pathField{fmt.Sprintf("tailnets[%d].auth.oauth.client_secret_file", i), &t.Auth.OAuth.ClientSecretFile},
			pathField{fmt.Sprintf("tailnets[%d].auth.workload_identity.id_token_file", i), &t.Auth.WorkloadIdentity.IDTokenFile},
			pathField{fmt.Sprintf("tailnets[%d].objectstore.flow.access_key_id_file", i), &flow.AccessKeyIDFile},
			pathField{fmt.Sprintf("tailnets[%d].objectstore.flow.secret_access_key_file", i), &flow.SecretAccessKeyFile},
			pathField{fmt.Sprintf("tailnets[%d].objectstore.flow.session_token_file", i), &flow.SessionTokenFile},
			pathField{fmt.Sprintf("tailnets[%d].objectstore.audit.access_key_id_file", i), &audit.AccessKeyIDFile},
			pathField{fmt.Sprintf("tailnets[%d].objectstore.audit.secret_access_key_file", i), &audit.SecretAccessKeyFile},
			pathField{fmt.Sprintf("tailnets[%d].objectstore.audit.session_token_file", i), &audit.SessionTokenFile},
		)
	}
	for i := range c.Streaming.Routes {
		r := &c.Streaming.Routes[i]
		fields = append(fields, pathField{fmt.Sprintf("streaming.routes[%d].token_file", i), &r.TokenFile})
	}
	for i := range c.Webhook.Routes {
		r := &c.Webhook.Routes[i]
		fields = append(fields, pathField{fmt.Sprintf("webhook.routes[%d].secret_file", i), &r.SecretFile})
	}
	for i := range c.Collectors.NodeMetrics.Targets {
		t := &c.Collectors.NodeMetrics.Targets[i]
		fields = append(fields, pathField{
			fmt.Sprintf("collectors.node_metrics.targets[%d].bearer_token_file", i), &t.BearerTokenFile,
		})
		if t.TLS != nil {
			fields = append(fields,
				pathField{fmt.Sprintf("collectors.node_metrics.targets[%d].tls.ca_file", i), &t.TLS.CAFile},
				pathField{fmt.Sprintf("collectors.node_metrics.targets[%d].tls.cert_file", i), &t.TLS.CertFile},
				pathField{fmt.Sprintf("collectors.node_metrics.targets[%d].tls.key_file", i), &t.TLS.KeyFile},
			)
		}
	}
	return fields
}

// resolveConfigPaths mutates every registered pathField IN PLACE, so every
// later reader -- resolveSecretFiles, Validate, and the app/collector layers
// reading the Config after Load returns -- sees the resolved path without
// needing any resolution logic of its own.
//
// configFilePath is the path Load was given ("" when running from
// defaults+environment alone). envSet is the set of dotted config keys a
// TS2OTEL_* environment variable set this run (see envSetKeys): a key present
// there keeps CWD semantics unconditionally, per the frozen contract, even
// though the field's tag is a plain string field with no built-in provenance
// of its own.
//
// The return value is keyed by pathField.key so a caller that fails to open
// the resolved path (resolveSecretFiles today; Validate's TLS/workload-
// identity/ingress-WAL checks are a sibling concern in validate.go) can
// report both the configured and resolved path.
func resolveConfigPaths(c *Config, configFilePath string, envSet map[string]bool) map[string]pathResolution {
	out := make(map[string]pathResolution)
	for _, f := range c.pathFields() {
		configured := *f.value
		resolved := configured
		if configured != "" && configFilePath != "" && !envSet[f.key] && !filepath.IsAbs(configured) {
			resolved = filepath.Join(filepath.Dir(configFilePath), configured)
		}
		*f.value = resolved
		out[f.key] = pathResolution{Configured: configured, Resolved: resolved}
	}
	return out
}

// envSetKeys returns the dotted config keys set by any TS2OTEL_* environment
// variable in the CURRENT process environment, using the identical
// name-to-key mapping Load's own environment layer uses (envKey, env.go) --
// so a key present here means exactly "this value came from the environment
// layer", the provenance resolveConfigPaths needs to keep CWD semantics for
// an env-sourced path.
func envSetKeys() map[string]bool {
	set := map[string]bool{}
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, EnvPrefix) {
			continue
		}
		set[envKey(name)] = true
	}
	return set
}

// pathForError renders a filesystem path for an error message, naming BOTH
// what the operator configured and what this process actually opened whenever
// #310's config-relative resolution rewrote it.
//
// Showing only the resolved path is the hardest failure to act on: the
// operator looks at their config, sees the path is right, and reads "no such
// file" about a file they can see exists. Showing only the configured path is
// worse still — it hides the one piece of information that explains the error.
//
// key is the dotted pathFields key (e.g. "admin.tls.cert_file"); fallback is
// the already-resolved path, used verbatim when resolution changed nothing.
// This is the ONE definition of the format — it had drifted into two.
func (c *Config) pathForError(key, fallback string) string {
	if pr, ok := c.pathResolutions[key]; ok && pr.Configured != pr.Resolved {
		return fmt.Sprintf("%q (resolved to %q)", pr.Configured, pr.Resolved)
	}
	return fmt.Sprintf("%q", fallback)
}
