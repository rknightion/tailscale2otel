# Changelog

## 0.2.0 — BREAKING: config now lives under `config:`

The entire application config now lives under a single top-level `config:` key in
`values.yaml` and is rendered verbatim into the ConfigMap. This is the single
source of truth (kept in sync with `config.example.yaml`), eliminating the
chart<->config drift that existed when the `tailscale2otel.config` helper
hand-rolled a fallback config from individual values.

### What changed

- The `tailscale2otel.config` helper is now **unconditional**: it always renders
  `.Values.config | toYaml`. The hand-rolled `else` fallback branch (which
  duplicated config keys and drifted from `config.example.yaml`) was deleted.
- `values.yaml` now carries the FULL config map under `config:`, with `${ENV}`
  placeholders for secrets. The existing Secret + `envFrom` injection still
  expands them at runtime — no change to how credentials are supplied.
- `values.schema.json` (JSON Schema **draft-07**, the only draft Helm validates)
  was added as a baseline. CI regenerates it with
  [losisin/helm-values-schema-json](https://github.com/losisin/helm-values-schema-json)
  in fail-on-diff mode.

### Migration

If you previously overrode config via the old per-field values
(`tailscale.tailnet`, `tailscale.authMethod`, `otlp.protocol`, `otlp.endpoint`,
`otlp.metricInterval`, `selfObservability.enabled`), move those under `config:`
using the real config keys. Helm deep-merges maps, so you only need to set the
keys you change. Examples:

```yaml
# OLD (pre-0.2.0)
otlp:
  endpoint: "https://my-otlp.example/otlp"
  metricInterval: 30s
selfObservability:
  enabled: false

# NEW (0.2.0+)
config:
  otlp:
    endpoint: "https://my-otlp.example/otlp"
    metric_interval: 30s
  self_observability:
    enabled: false
```

Or via `--set`:

```sh
helm upgrade ... \
  --set config.otlp.endpoint=https://my-otlp.example/otlp \
  --set config.otlp.metric_interval=30s \
  --set config.self_observability.enabled=false
```

Secrets are unchanged: set `secret.TS_OAUTH_CLIENT_ID`, etc., or point
`existingSecret` at your own Secret.
