# Security & data handling

This document describes the operational security posture of `tailscale2otel`: what
data it handles, where that data goes, and the configuration levers and footguns
operators should be aware of. For the user-facing pitch see `README.md`; for the
full signal catalog see `docs/metrics.md`.

## Telemetry payload sensitivity

`tailscale2otel` exports **network metadata** about your tailnet. Flow logs and
audit logs carry, among other things:

- source and destination IP addresses and ports,
- device names / hostnames,
- user identities (e.g. the actor on an audit event).

All of this is exported over **OTLP to the configured backend** (for example
Grafana Cloud). **Treat the OTLP backend as a trusted data sink** — anyone with
read access to it can see this metadata. Scope backend credentials accordingly.

Levers to reduce what leaves the tailnet (all under the `cardinality:` block in
`config.example.yaml`):

- `cardinality.flow_source_port` / `cardinality.flow_destination_port` (both default
  `false`) — keep ports **off** flow *metrics*. Note that ports are always present on
  flow *logs* regardless of these settings.
- `cardinality.collapse_external` (default `true`) — buckets unresolved IPs as
  `external`/`unknown` rather than emitting them as distinct series/labels.
- `cardinality.flow_node_dims` (default `true`) — set `false` to omit src/dst
  device names from flow metrics.

Disabling the `devices` collector does **not** remove IPs from the payload — it
only degrades IP→name enrichment, so flow/audit records fall back to
`unknown`/`external` for names while the raw addresses are still exported.

## Receiver authentication footguns

The optional `streaming` (Splunk-HEC) and `webhook` receivers accept inbound POSTs.
Their auth is **opt-in by presence of a secret**, so an empty value silently
disables it (these behaviours are also noted in `config.example.yaml`):

- Leaving `webhook.secret` empty **skips HMAC verification entirely** —
  unauthenticated POSTs are accepted.
- Leaving `streaming.token` empty **disables receiver authentication**.

Always set these when exposing a receiver, especially on a wildcard/all-interfaces
bind or without TLS. Tailscale requires HTTPS for the streaming sink; a
`tailscale cert` works for private tailnet endpoints.

> All config string values are `${ENV}`-expanded, and an **undefined `${ENV}`
> reference expands silently to the empty string**. A typo in the env var name
> (e.g. `${TS_WEBOOK_SECRET}`) therefore silently disables auth rather than
> failing loudly. Double-check that the referenced env vars are actually set.

## `streaming.auto_configure` footgun

When `streaming.auto_configure: true`, the service registers **this** receiver as
the tailnet's log-streaming sink on startup, and **overwrites any existing sink**
configured for the tailnet. Never enable it against a tailnet whose streaming
configuration you do not intend to replace. It is off by default.

## Secrets handling

- Keep secrets in **environment variables referenced via `${ENV}`**, never as
  literal values in YAML. `config.local.yaml`, `.env.local`, and `.secrets/` are
  gitignored for this reason.
- The admin **status page redacts secret values** — it emits only `*Set` booleans
  (e.g. `webhook_secret_set`) and OTLP header key names, never the values.
- Secrets are **never logged**.

> Note: enabling `profiling.pprof.enabled` requires `admin.enabled` **and**
> `admin.auth.token`, because heap/goroutine dumps can expose in-memory secrets.
