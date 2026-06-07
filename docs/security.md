---
title: Security
description: Data sensitivity, receiver authentication, secrets handling, and operational footguns
tags:
  - Security
---

# Security

This page describes the operational security posture of `tailscale2otel`: what data it
handles, where that data goes, and the configuration levers and footguns operators should
be aware of.

## Telemetry payload sensitivity

`tailscale2otel` exports **network metadata** about your tailnet. Flow logs and audit logs
carry, among other things:

- source and destination IP addresses and ports,
- device names and hostnames,
- user identities (e.g. the actor on an audit event).

All of this is exported over **OTLP to the configured backend** (for example Grafana Cloud).
**Treat the OTLP backend as a trusted data sink** — anyone with read access to it can see
this metadata. Scope backend credentials accordingly.

Levers to reduce what leaves the tailnet (all under the `cardinality:` block in
[`config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml)):

- `cardinality.flow.source_port` / `cardinality.flow.destination_port` (both default `false`)
  — keep ports **off** flow *metrics*. Ports are always present on flow *logs* regardless of
  these settings.
- `cardinality.flow.collapse_external` (default `true`) — buckets unresolved IPs as
  `external`/`unknown` rather than emitting them as distinct series/labels.
- `cardinality.flow.node_dims` (default `true`) — set `false` to omit src/dst device names
  from flow metrics.

!!! warning "Disabling `devices` does not remove IPs"
    Disabling the `devices` collector does **not** remove IP addresses from the exported
    payload — it only degrades IP→name enrichment, so flow/audit records fall back to
    `unknown`/`external` for names while the raw addresses are still exported.

## Receiver authentication footguns

The optional `streaming` (Splunk-HEC) and `webhook` receivers accept inbound POSTs. Their
authentication is **opt-in by presence of a secret**, so an empty value silently disables
it (these behaviours are also noted in
[`config.example.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/config.example.yaml)):

- Leaving `webhook.secret` empty **skips HMAC verification entirely** — unauthenticated
  POSTs are accepted.
- Leaving `streaming.token` empty **disables receiver authentication**.

!!! danger "Always set credentials before exposing a receiver"
    Always set these when exposing a receiver, especially on a wildcard/all-interfaces bind
    or without TLS. Tailscale requires HTTPS for the streaming sink; a `tailscale cert`
    works for private tailnet endpoints.

!!! warning "Mistyped environment variable names silently leave auth disabled"
    Any field can be set via a `TS2OTEL_*` environment variable (the env layer overrides the
    file), and an **empty credential silently disables auth** — for example a mistyped
    variable name (`TS2OTEL_WEBHOOK__SECRT`) leaves the secret empty rather than failing
    loudly. The startup log WARNs on a `TS2OTEL_*` variable that matches no config key, but
    double-check that auth credentials are actually set.

## `streaming.auto_configure` footgun

!!! danger "`auto_configure` overwrites your existing log-streaming sink"
    When `streaming.auto_configure: true`, the service registers **this** receiver as the
    tailnet's log-streaming sink on startup, and **overwrites any existing sink** configured
    for the tailnet. Never enable it against a tailnet whose streaming configuration you do
    not intend to replace. It is off by default.

## Secrets handling

- Keep secrets in **`TS2OTEL_*` environment variables** (the env layer overrides the file),
  never as literal values in YAML. `config.local.yaml`, `.env.local`, and `.secrets/` are
  gitignored for this reason.
- The admin **status page redacts secret values** — it emits only `*Set` booleans (e.g.
  `webhook_secret_set`) and OTLP header key names, never the values themselves.
- Secrets are **never logged**.

!!! note "pprof requires admin auth"
    Enabling `profiling.pprof.enabled` requires both `admin.enabled` **and**
    `admin.auth.token`, because heap and goroutine dumps can expose in-memory secrets.

See [configuration.md](configuration.md) for the full config reference and
[env-vars.md](env-vars.md) for the complete list of `TS2OTEL_*` environment variables.
