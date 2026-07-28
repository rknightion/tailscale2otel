---
title: Collector Gateway
description: Run tailscale2otel behind a Grafana Alloy or OpenTelemetry Collector gateway for outage tolerance, enrichment and routing
tags:
  - Deployment
  - Grafana Cloud
---

# Collector gateway

`tailscale2otel` can export OTLP straight to your backend, or through a **gateway** — a Grafana
Alloy or OpenTelemetry Collector instance that receives OTLP from the exporter, buffers it, and
forwards it on. This page covers when the gateway is worth it, and ships a validated recipe for
both Docker and Kubernetes.

The Docker recipe lives in
[`deploy/alloy/`](https://github.com/rknightion/tailscale2otel/tree/main/deploy/alloy) and is
validated against a pinned Alloy release. Its
[README](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alloy/README.md) carries the
tuning notes, the version-pinning traps and the full smoke-test transcript.

!!! warning "Two unrelated things are called \"gateway\" here"
    In the Helm chart, the top-level `gateway:` value renders **Gateway API `HTTPRoute`
    resources** for the exporter's own inbound listeners (the HEC stream receiver and the webhook
    receiver). That is ingress plumbing and has nothing to do with this page. OTLP gateway mode is
    configured entirely through `config.otlp.*`, and needs no `gateway:` value at all.

## Direct export or gateway?

Both modes are supported. Direct export is the default and stays the right answer for plenty of
deployments.

| | **Direct export** (default) | **Gateway mode** |
| --- | --- | --- |
| Path | exporter → backend | exporter → Alloy/Collector → backend |
| Moving parts | one container | two |
| Backend outage tolerance | limited — in-process retry only, bounded by the export interval | a disk-backed queue that holds the backlog and drains on recovery |
| Survives an exporter restart | no | the *gateway's* backlog does |
| Backend credential lives in | the exporter | the gateway only |
| Enrichment, filtering, tail sampling | not available | processors in the pipeline |
| Fan-out to several backends | one endpoint only | multiple exporters |
| Egress shape | every exporter talks to the internet | one egress point to allow-list |

Grafana's own guidance is that [the recommended architecture for production observability uses
Grafana Alloy](https://grafana.com/docs/grafana-cloud/send-data/otlp/send-data-otlp/), for exactly
these reasons — reliability, metadata enrichment, sampling and multi-backend routing — and that
direct SDK export is the quickstart shape rather than the durable one.

**Take the gateway when** you cannot afford to lose telemetry across a backend outage or
maintenance window, you already run one for other services, you want a single egress point, you
need to enrich or route, or you would rather the backend token lived in one place than in every
exporter.

**Stay direct when** a gap during an outage is acceptable, or a second container is not worth the
operational surface. Point `otlp.endpoint` at your backend and you are done — see
[Getting Started](getting-started.md).

### What a gateway does not give you

The queue and retry loop mean an outage costs you far less. They do not make delivery perfect.

**This reduces telemetry loss. It does not eliminate it, and it is not exactly-once delivery.**

- **Retries have a deadline.** A batch not accepted within `retry_on_failure.max_elapsed_time`
  (`30m` in the shipped config) is dropped, persistent queue or not.
- **The queue is finite.** Past `queue_size` the gateway starts returning retryable errors and the
  exporter sheds data.
- **Retries can duplicate.** The gateway acknowledges on enqueue, not on backend acceptance, so a
  batch the backend committed but failed to acknowledge is sent again. That is the direct cost of
  the buffering.

Tolerating an outage of length `T` needs both a `max_elapsed_time` of at least `T` **and** a queue
big enough for the batches produced during `T`. Raising one alone achieves nothing.

## The pipeline

```
otelcol.receiver.otlp          :4317 gRPC / :4318 HTTP, on a private network
  -> otelcol.processor.memory_limiter    soft limit, first in the chain
  -> otelcol.processor.batch             coalesce a poll cycle
  -> otelcol.exporter.otlphttp           retry + sending queue
       -> otelcol.storage.file           optional, disk-backed queue (public preview)
```

Two ordering rules are load-bearing. The **memory limiter goes first** — anything ahead of it
buffers data it can no longer protect — and the **batch processor goes after it**, which is
[Grafana's documented recommendation](https://grafana.com/docs/alloy/latest/reference/components/otelcol/otelcol.processor.batch/).

!!! danger "`otelcol.storage.file` is public preview"
    The persistent-queue component is at Alloy stability level **public preview**: it is subject to
    breaking changes between releases, and Alloy **refuses to start** unless you pass
    `--stability.level=public-preview` (or lower). It is optional. Drop its block and the
    `storage =` line from `sending_queue` and the queue still works — in memory, emptied on every
    restart. Take the preview dependency only if surviving a gateway restart with the backlog
    intact is worth it to you.

## Docker Compose

```sh
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml up -d
```

That compose file **replaces** `deploy/docker-compose.yaml` rather than overlaying it — do not pass
both. It runs Alloy pinned to a specific version tag (never `latest`, because Alloy component
arguments change between minor versions) alongside the exporter, and points the exporter at the
sidecar:

```yaml
TS2OTEL_OTLP__PROTOCOL: grpc
TS2OTEL_OTLP__ENDPOINT: http://alloy:4317
TS2OTEL_OTLP__TLS__INSECURE: "true"
```

`tls.insecure` disables transport security **entirely**, so it is only ever acceptable on a trusted
private hop. It is safe here specifically because there is no credential on that hop — in gateway
mode the backend token belongs to Alloy alone, and no `otlp.grafana_cloud.*` is set on the exporter
at all.

### Credentials

Every secret is supplied externally. The committed `config.alloy` reads its three backend settings
through `sys.env()` and the compose file requires them with `${VAR:?}`, so a missing value fails
`up` with a named error instead of starting an unauthenticated gateway. Put them in the
git-ignored `deploy/.env`:

```sh
GATEWAY_OTLP_ENDPOINT=https://otlp-gateway-prod-us-central-0.grafana.net/otlp
GATEWAY_OTLP_USERNAME=<your-numeric-instance-id>
GATEWAY_OTLP_PASSWORD=<your-access-policy-token>
```

For Grafana Cloud the username is the numeric instance/stack ID and the password is an
access-policy token with the OTLP write scopes; both come from the Cloud Portal (organization
**Overview** → **Launch stack** → **Configure** on the OpenTelemetry tile). Use your own region's
`otlp-gateway-<zone>.grafana.net` host. The exporter appends `/v1/metrics`, `/v1/logs` and
`/v1/traces` to the base URL itself.

## Kubernetes

The `tailscale2otel` chart does **not** bundle Alloy, and deliberately so — a gateway is shared
infrastructure with its own lifecycle, and most clusters that want one already have one. Deploy
Alloy with [its own chart](https://grafana.com/docs/alloy/latest/set-up/install/kubernetes/) (or
`k8s-monitoring`), give it the pipeline from
[`deploy/alloy/config.alloy`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alloy/config.alloy),
then point this chart at its Service.

Assuming Alloy is running as Service `alloy` in namespace `monitoring` with the OTLP receiver on
4317:

```yaml
# values.yaml for the tailscale2otel chart, gateway mode.
config:
  otlp:
    protocol: grpc
    endpoint: http://alloy.monitoring.svc.cluster.local:4317
    tls:
      # Plaintext, in-cluster only. Safe here because gateway mode puts NO
      # credential on this hop - the backend token lives in Alloy. Use a real
      # TLS endpoint (and drop this) if the hop leaves the cluster.
      insecure: true
    # Deliberately absent: grafana_cloud.instance_id / grafana_cloud.token.
    # In gateway mode the backend credential belongs to Alloy, not here.

# The Tailscale credentials are still this chart's business. Supply them from a
# secret you manage rather than inline values.
existingSecret: tailscale2otel-credentials
```

That `existingSecret` must carry the usual `TS2OTEL_*` keys
(`TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID`, `TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET`); see
[Installation](installation.md) for the full key list. Rotating an externally managed secret needs
the pod replaced — Kubernetes never refreshes env in a running container — which is what the
chart's `rolloutTrigger` value is for.

On the Alloy side, three things carry over from the Docker recipe and are easy to miss:

- **Pass `--stability.level=public-preview`** in the Alloy container args if you keep the
  `otelcol.storage.file` block. Without it Alloy will not start.
- **Give the queue a PVC**, not an `emptyDir`. A persistent queue on an `emptyDir` is discarded
  exactly when you needed it, which is worse than not enabling it — the config looks durable and
  is not.
- **Keep `memory_limiter.limit` at roughly 80% of the container memory limit.** A limiter budget
  above the pod's limit means the pod is OOM-killed before the limiter ever engages.

## Verify it works

The gateway is an extra hop that can fail silently, so confirm both ends rather than assuming.

**End to end:** the exporter's own `tailscale2otel.up` gauge (`tailscale2otel_up` once normalized
for Prometheus) appearing in your backend proves the whole chain. The exporter's
[admin status page](getting-started.md) shows OTLP delivery state per signal on its side of the
hop — which tells you whether the exporter is reaching the gateway, not whether the gateway is
reaching the backend.

**The gateway's own health:** Alloy serves a readiness endpoint and its own metrics on port 12345.
Keep that listener on loopback or behind your own auth — it is unauthenticated and exposes pipeline
internals.

```sh
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:12345/-/ready
curl -s http://127.0.0.1:12345/metrics | grep '^otelcol_exporter_queue_size'
```

`otelcol_exporter_queue_size` is the number you actually want on a dashboard: sustained growth
means the backend is rejecting or throttling, and hitting
`otelcol_exporter_queue_capacity` means you are dropping.

**The outage and restart drill** — stop the backend, watch the queue fill, restart the gateway,
watch the backlog survive, restore the backend, watch it drain — is written up step by step with
observed values in the
[`deploy/alloy/` README](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alloy/README.md#outage-and-restart-smoke-test).
Run it once before you rely on the queue.

!!! warning "`alloy validate` is necessary, not sufficient"
    `alloy validate` checks syntax and attribute names. It does **not** prove the component graph
    can be built, and it accepts at least one config that fails at startup (see the
    version-pinning traps in the
    [`deploy/alloy/` README](https://github.com/rknightion/tailscale2otel/blob/main/deploy/alloy/README.md#version-pinning-traps)).
    Only a real `alloy run` that reaches ready proves a config loads.

## Next steps

- [Installation](installation.md) — the direct-export Docker, Helm and binary paths.
- [Configuration](configuration.md) — every `otlp.*` key, including headers and TLS.
- [Troubleshooting](troubleshooting.md) — what to check when nothing arrives.
