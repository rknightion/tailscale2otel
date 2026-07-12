# Security & data handling

This document covers two things: how to **report a vulnerability** in `tailscale2otel`
(immediately below), and the **operational security posture** of the service — what data
it handles, where that data goes, and the configuration levers and footguns operators
should be aware of. For the user-facing pitch see `README.md`; for the full signal catalog
see `docs/metrics.md`.

## Reporting a vulnerability

**Report privately. Do not open a public issue for a security bug.**

Use GitHub's private vulnerability reporting, which is enabled on this repository:

- **<https://github.com/rknightion/tailscale2otel/security/advisories/new>**

That channel is private between you and the maintainer until a fix ships, and it is the
only supported way to report. A useful report includes the affected version or commit, the
configuration involved (redact secrets), what an attacker gains, and the steps to reproduce
it. A proof of concept helps but is not required.

### Supported versions

Only the **latest release** is supported. Fixes land on `main` and ship in the next
release; there are no backports to older tags.

### Disclosure process and timelines

This is a single-maintainer hobby project, so these are honest targets rather than a
commercial SLA:

| Stage | Target |
| --- | --- |
| Acknowledge your report | within 5 working days |
| Initial assessment (accepted / rejected, with reasoning) | within 10 working days |
| Fix released for an accepted vulnerability | within 90 days of acknowledgement, and sooner where severity warrants it |

Coordinated disclosure: please give me the 90 days to ship a fix before disclosing
publicly. Once a fix is released I will publish a GitHub Security Advisory (which assigns
a CVE) and credit you by name unless you would rather stay anonymous. If a report is
rejected I will say why, and you are then free to disclose as you see fit. If I have gone
quiet past the targets above, treat that as a reason to chase me, not as a reason to sit on
the bug indefinitely.

### Scope

In scope: the `tailscale2otel` binary, its container images, and the Helm chart in this
repository. Notably in scope are the inbound receivers (the Splunk-HEC `streaming` endpoint
and the `webhook` endpoint), the `prometheus` pull endpoint, the admin HTTP server, and any
path where a secret could leak into logs, metrics, or the status page.

Out of scope: vulnerabilities in the Tailscale API or the Tailscale client themselves
(report those to [Tailscale](https://tailscale.com/security)), vulnerabilities in your OTLP
backend, and the operator footguns documented below — the empty-secret auth behaviour and
`streaming.auto_configure` are **known, intentional, and documented**, so they are
configuration hazards rather than vulnerabilities.

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

- `cardinality.flow.source_port` / `cardinality.flow.destination_port` (both default
  `false`) — keep ports **off** flow *metrics*. Note that ports are always present on
  flow *logs* regardless of these settings.
- `cardinality.flow.collapse_external` (default `true`) — buckets unresolved IPs as
  `external`/`unknown` rather than emitting them as distinct series/labels.
- `cardinality.flow.node_dims` (default `true`) — set `false` to omit src/dst
  device names from flow metrics.

Disabling the `devices` collector does **not** remove IPs from the payload — it
only degrades IP→name enrichment, so flow/audit records fall back to
`unknown`/`external` for names while the raw addresses are still exported.

## Receiver authentication footguns

The optional `streaming` (Splunk-HEC) and `webhook` receivers accept inbound POSTs, and
the optional `prometheus` pull endpoint serves `GET /metrics`. Their auth is **opt-in by
presence of a secret**, so an empty value silently disables it (these behaviours are also
noted in `config.example.yaml`):

- Leaving `webhook.secret` empty **skips HMAC verification entirely** —
  unauthenticated POSTs are accepted.
- Leaving `streaming.token` empty **disables receiver authentication**.
- Leaving `prometheus.auth.token` empty **serves `/metrics` unauthenticated** — every
  series, including device hostnames, flow identifiers, and the tailnet name, to anyone
  who can reach the port (`prometheus.listen`, default `:2112`). A startup warning fires
  for this only when the listener is also bound to a wildcard address; an unauthenticated
  loopback or tailnet-bound endpoint is silent.

Always set these when exposing a receiver, especially on a wildcard/all-interfaces
bind or without TLS. Tailscale requires HTTPS for the streaming sink; a
`tailscale cert` works for private tailnet endpoints.

> Any field can be set via a `TS2OTEL_*` environment variable (the env layer
> overrides the file), and an **empty credential silently disables auth** — for
> example a mistyped variable name (`TS2OTEL_WEBHOOK__SECRT`) leaves the secret
> empty rather than failing loudly. The startup log WARNs on a `TS2OTEL_*`
> variable that matches no config key, but double-check that auth credentials are
> actually set.

## `streaming.auto_configure` footgun

When `streaming.auto_configure: true`, the service registers **this** receiver as
the tailnet's log-streaming sink on startup, and **overwrites any existing sink**
configured for the tailnet. Never enable it against a tailnet whose streaming
configuration you do not intend to replace. It is off by default.

## Secrets handling

- Keep secrets in **`TS2OTEL_*` environment variables** (the env layer overrides
  the file), never as literal values in YAML. `config.local.yaml`, `.env.local`,
  and `.secrets/` are gitignored for this reason.
- The admin **status page redacts secret values** — it emits only `*Set` booleans
  (e.g. `webhook_secret_set`) and OTLP header key names, never the values.
- Secrets are **never logged**.

> Note: enabling `profiling.pprof.enabled` requires `admin.enabled` **and**
> `admin.auth.token`, because heap/goroutine dumps can expose in-memory secrets.
