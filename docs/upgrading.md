---
title: Upgrading
description: Upgrade notes and breaking changes, including the pre-1.0 to v1.0.0 migration
tags:
  - Deployment
  - Configuration
---

# Upgrading

This page collects the behaviour changes worth knowing about when moving between
`tailscale2otel` releases. Each config key references the full dotted key path; see
[Configuration](configuration.md) for defaults and the `TS2OTEL_*` env-var equivalents,
and [Metrics](metrics.md) for the signal catalogue.

## Upgrading to v1.0.0

`v1.0.0` is the first **stable** release. It contains **no new breaking changes** over the
`0.6.0` line — every fix and behaviour change below already shipped across the `0.x`
releases and is simply consolidated here. The `1.0.0` tag marks the point at which the
configuration surface, metric names, HTTP endpoints, and Helm values are considered stable
and will follow [semantic versioning](https://semver.org/) going forward: breaking changes
now require a major-version bump.

If you are already running `0.6.0`, upgrading to `1.0.0` is a no-op — pull the new
tag/chart and restart. If you are coming from an **earlier `0.x`**, review the items below,
which are the accumulated behaviour changes since the start of the `0.x` series.

!!! tip "Fastest path"
    Run the new binary against your existing config once with `otlp.protocol: stdout`.
    Startup validation (see below) will print any config that is no longer accepted, and
    you can confirm the metrics/labels you depend on before pointing it at your backend.

### Configuration is validated more strictly

Startup config validation was tightened across the `0.x` line. Configs that were previously
accepted silently may now **fail fast at startup** with an actionable error instead of
misbehaving later:

- Receiver paths, the OTLP gRPC endpoint shape, and a required tailnet are now validated.
- `logging.level` is validated against a fixed enum.
- Per-tailnet HTTP settings inherit fleet-wide `tailscale.http` defaults when omitted.
- Headscale receiver combinations that cannot work now error (or warn) at startup rather
  than failing at runtime.
- Env vars that index a list-of-structs config key now produce an actionable error instead
  of being silently ignored.

**Action:** start the new version once and fix anything validation reports. Nothing here
changes the meaning of a valid config — it only rejects configs that were already broken.

### Least-privilege OAuth scopes

The app now requests the least-privilege `all:read` OAuth scope for tailnet API access.

**Action:** if you provisioned an OAuth client with narrower or hand-picked scopes, ensure
it grants `all:read` (read-only). No write scopes are needed.

### `/readyz` now reflects real readiness

`/readyz` previously aliased `/healthz` and always returned `200`. It now reports **actual
readiness**: non-`200` while the app is still starting up (before the first successful
collection tick / receiver bind) and non-`200` if an enabled receiver (stream/webhook) has
terminally failed to bind or has stopped. `/healthz` remains pure process liveness (always
`200` once the process is up).

**Action:** this is the intended behaviour for a Kubernetes `readinessProbe`, and the Helm
chart already points its readiness probe at `/readyz`. If you wrapped `/readyz` in external
tooling that assumed an unconditional `200`, expect it to now gate on startup and receiver
health. Give the readiness probe enough `initialDelaySeconds`/`failureThreshold` to cover
first-tick startup.

### Webhook body cap lowered, and now configurable

The webhook receiver's pre-authentication body cap default dropped from **64 MiB to 1 MiB**,
and a new `webhook.max_body_bytes` knob was added (mirroring `streaming.max_body_bytes`).
Real Tailscale webhook payloads are KB-scale, so the lower default is safely proportionate.

**Action:** none for normal use. If you have an unusual reason to accept larger webhook
bodies, raise `webhook.max_body_bytes`.

### Per-entity gauges now drop out instead of ghosting

Churning per-entity gauges — `tailscale.device.online`, `tailscale.node.up`, and the
`tailscale.dns.*` info gauges — were migrated to observable snapshots so that when an entity
disappears (device removed, renamed, resolver dropped) its series **stops being exported**
rather than lingering at its last value forever. This fixes ghost devices in dashboards and
cardinality-slot exhaustion under sustained churn.

**Action:** any dashboard/alert that relied on a departed entity's series *staying present*
at its last value will now see it **go absent**. Where you need an explicit zero for absent
series, use a fallback such as `... or on() vector(0)` in your PromQL.

### `instance_source: hostname` labels are now stable

For the **non-default** `nodemetrics` `instance_source: hostname` setting, colliding instance
labels are now disambiguated stably as `host@address` across refresh cycles and against
static targets, instead of flapping based on which sibling devices happened to be online in a
given discovery batch. The default `instance_source: name` is unique and unaffected.

**Action:** only relevant if you set `instance_source: hostname`. The `tailscale.node` label
value for colliding hosts is now consistently address-suffixed; update any dashboard queries
pinned to the old flapping label.

### Metric additions and `api.duration` scope change

- New metric `tailscale2otel.api.rate_limit.wait` (histogram) records time spent waiting on
  the client-side rate limiter.
- That rate-limiter wait is now **excluded** from `tailscale2otel.api.duration`, so
  `api.duration` reflects server round-trip time only. Its observed values may drop after
  upgrade — this is a scope correction, not a regression.
- New metric `tailscale.stream.skipped` counts records the stream receiver skipped.

**Action:** if you alert on `api.duration`, re-baseline it; the rate-limit component now
lives in its own metric.

### Shipped dashboards and alert rules corrected

The bundled Grafana dashboards and Prometheus alert rules had several PromQL/recording-rule
expressions corrected (key-expiry buckets, VIP-service HA aggregation, recording-rule
reduce nodes, and others).

**Action:** re-import the shipped dashboards and alert rules from `deploy/` to pick up the
corrected expressions.

### Helm chart

The chart gained a memory-derived `GOMEMLIMIT` default, a pod `fsGroup`, conditional
liveness/readiness probes, and `extraVolumes`/`extraVolumeMounts`. See the chart README for
the current values.

**Action:** review your `values.yaml` overrides against the current chart defaults on
upgrade; nothing requires a change, but the pod security context and probe behaviour are
now set out of the box.

### User-Agent

Outbound Tailscale API requests now send a `tailscale2otel/<version>` User-Agent.

**Action:** only relevant if you filter Tailscale admin/API logs by User-Agent.
