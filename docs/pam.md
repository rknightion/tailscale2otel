---
title: Tailscale PAM
description: Configure Border0 PAM inventory and session collection, with privacy controls and diagnostic limits.
---

# Tailscale PAM

The optional `pam` collector reads the separate Border0 API used by Tailscale PAM. It collects
connector, service, policy and identity inventory, organization settings, subscription limits,
and session telemetry. Tailscale OAuth credentials do not authenticate these requests.

## Enable collection

Supply a Border0 service-account bearer token with read access to the required PAM resources.
The client makes GET requests and does not refresh the token. Keep it in the environment:

```sh
export TS2OTEL_PAM__TOKEN='<border0-service-account-token>'
```

Add this to an otherwise configured exporter:

```yaml
collectors:
  pam:
    enabled: true
    interval: 10m
    sessions_interval: 1m
    snapshot_enabled: false
    session_log_enabled: false
```

There is no `pam.token_file` setting. `pam.api_url` defaults to
`https://api.border0.com/api/v1`. In a multi-tailnet configuration, set `pam.tailnet` to the exact
name of the runtime that should own the signals. Empty selects the first configured runtime;
unknown names fail validation. One process has one PAM configuration, not one per tailnet.

Check configuration with `-validate`, then use the [preflight workflow](getting-started.md#check-a-configuration-before-a-rollout)
to check collection. Inspect the collector's status and API error classes. A 403 is `scope_denied`,
not evidence that PAM is unavailable; a token needs access to each resource being collected.

## Inventory, snapshots and sessions

Inventory runs every ten minutes by default. Session collection has its own one-minute schedule.
The [PAM metrics catalog](metrics.md#tailscale-pam-tailscalepam) lists each signal and its labels.
Tailscale Service VIPs and ports belong to the `services` collector; PAM-related configuration
audit changes belong to `auditlogs`.

Two additional log outputs are disabled by default:

- `collectors.pam.snapshot_enabled` emits `tailscale.pam.snapshot` when the safe configuration
  shape changes and on a heartbeat, which defaults to 24 hours. The snapshot excludes credential
  material and is bounded by `snapshot_body_bytes`, default 32 KiB.
- `collectors.pam.session_log_enabled` emits `tailscale.pam.session` for accepted session records.
  Identifier fields follow `pii_filter`. Categories retain identifiers by default; set the relevant
  categories to `false` before exporting records that must omit them.

Session results describe authorization outcomes for sessions that reached a connector. They do
not measure connection health or all access attempts: grant-layer denials are absent from the
session feed. The active-session gauge covers the newest-first prefix read by the poller, so it
must not be treated as a complete census of arbitrarily old sessions.

Use the [configuration reference](configuration.md#pam-tailscale-pam-border0-api-connection) for
connection settings and [collector settings](configuration.md#snapshot-collectors) for output limits.
