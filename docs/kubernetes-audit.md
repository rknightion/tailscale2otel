# Kubernetes audit (tsrecorder)

Export OpenTelemetry metrics and logs from the Kubernetes API requests that Tailscale's
**tsrecorder** captures for traffic proxied through the Kubernetes operator's API-server proxy.

The intent is SIEM-shaped: answer *who reached which resource in which namespace, from which device,
with which client* — and surface the request patterns worth investigating, such as secret reads,
`exec` into a pod, and permission enumeration.

## Read this first: there is no response side

The records tsrecorder writes carry **no response status, no latency and no byte count**. The
API-server proxy logs each request as it forwards it, and nothing on the way back.

That means the following are **not derivable from this feed at all**, and no amount of configuration
will produce them:

- allowed vs. denied (there is no status code)
- error rates
- request latency
- response sizes

Every metric here counts **attempts**. A `delete` counted is a delete *requested*, which RBAC may well
have refused. If you need outcomes, you need the Kubernetes API server's own audit log — a different
source with a different pipeline.

## Enabling it

Two things must be true.

**1. Tailscale must be recording the events.** This is an ACL grant, not a chart value. Add
`enableEvents` to the `tailscale.com/cap/kubernetes` app capability:

```json
"grants": [{
  "src": ["group:engineering"],
  "dst": ["tag:k8s-operator"],
  "app": {
    "tailscale.com/cap/kubernetes": [{
      "recorder": ["tag:tsrecorder"],
      "enforceRecorder": true,
      "enableEvents": true
    }]
  }
}]
```

`enableEvents` is what produces the `.event` objects this collector reads; without it the recorder
still captures terminal sessions but logs no API requests. It is a **beta** upstream feature.

**2. Point the collector at the recorder's bucket.**

```yaml
collectors:
  k8s_audit:
    enabled: true
    objectstore:
      endpoint: https://s3.eu-west-1.amazonaws.com
      region: eu-west-1
      bucket: my-tsrecorder-bucket
      layout: recorder
```

Credentials come from the ambient chain (environment, IRSA/web identity, ECS/EKS Pod Identity, EC2
instance profile). To set them explicitly, use environment variables, never YAML:

```
TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__ACCESS_KEY_ID
TS2OTEL_COLLECTORS__K8S_AUDIT__OBJECTSTORE__SECRET_ACCESS_KEY
```

### Things that will trip you up

**The bucket is never inherited.** A `collectors.flowlogs.objectstore` or
`collectors.auditlogs.objectstore` destination does not apply here. This is a separate bucket written
by a different producer with a different key layout, and configuration validation refuses to start
without its own destination.

**`layout` must be `recorder`.** `partitioned` and `flat` are rejected. Those describe Tailscale's own
log exports, which are organized under `YYYY/MM/DD/` partitions with fixed-width basenames.
tsrecorder writes neither.

**`prefix` is usually empty.** Keys are `<stableID>/events/<timestamp>.event` and
`<stableID>/<timestamp>.cast`, where `<stableID>` is the recorder node's stable ID — it differs per
recorder replica, so it cannot be pinned in a prefix.

**Raise `max_object_decompressed_bytes` if you record long terminal sessions.** Only the first line of
a `.cast` object is read for meaning, but the whole object is still streamed, and one exceeding the
limit is quarantined rather than partially read.

**There is no `source` key.** Object storage is the only surface tsrecorder exposes — no read API, no
stream, no push — so unlike `flowlogs` and `auditlogs` there is nothing to select between.

## What it reads

| Object | What is read | What is never read |
|---|---|---|
| `<stableID>/events/<ts>.event` | The whole record: verb, resource, namespace, object name, selectors, source identity, user agent | The raw `request.path`, and the request body |
| `<stableID>/<ts>.cast` | **The header line only** — namespace, pod, container, session type, command, recorder | Every output frame. Terminal output is never inspected for meaning and never emitted |

Session recordings are ingested at session **start**. There is no documented way to tell a finished
`.cast` from one still being written, so nothing here depends on a recording being complete.

## Privacy

Three properties are enforced by tests, not just intent.

**The raw request path is never emitted.** `request.path` carries the full query string, and for an
`exec` that query string contains the command line, URL-encoded:

```
/api/v1/namespaces/prod/pods/api-0/exec?command=sh&command=-c&command=...
```

Only `kubernetes.Path` — the same path with no query — is exported.

**High-cardinality values never reach a metric.** Object names, paths, label and field selectors, pod
and container names and the exec command line are log attributes only. Every metric attribute is
normalized against a closed admit-set, with anything unrecognized folded to `other`, because the user
agent, resource names and verbs are all controlled by the client.

**Request bodies are never emitted** in any form.

### Exec command text

The verbatim command line is exported by default on the `tailscale.k8s.session` and
`tailscale.k8s.api_request` log records, under `tailscale.k8s.command`. It has its own redaction
category because it is the one attribute a human types at a shell, so it can contain a pasted secret
that appears nowhere else in your telemetry:

```yaml
pii_filter:
  command_text: false
```

Turning it off **keeps** `tailscale.k8s.command_class` — a bounded classification of the same command
(`interactive_shell`, `recon`, `credential_read`, `package_mgmt`, `net_tool`, `file_transfer`, `none`,
`other`) that carries no free text. The exec metrics are built on the class, so they keep working with
the raw text switched off.

## Investigating

The metrics give you the shape; the `tailscale.k8s.api_request` log record in Loki gives you the
detail. Some starting points:

```logql
# Who read secrets, and from where
{service_name="tailscale2otel"} | event_name="tailscale.k8s.api_request"
  | tailscale_k8s_resource="secrets"

# Every exec/attach/port-forward, with the command classification
{service_name="tailscale2otel"} | event_name="tailscale.k8s.api_request"
  | tailscale_k8s_subresource=~"exec|attach|portforward"

# Permission enumeration: a burst of these from an unexpected user agent is the signal.
# Normal for UI clients, which is why no alert ships for it by default.
{service_name="tailscale2otel"} | event_name="tailscale.k8s.api_request"
  | tailscale_k8s_resource=~"selfsubject.*"

# Reads of the recorder's own logs — an anti-forensics indicator
{service_name="tailscale2otel"} | event_name="tailscale.k8s.api_request"
  | tailscale_k8s_subresource="log" | tailscale_k8s_namespace="tailscale"
```

No dashboard or alert rule ships for this feed yet. Useful thresholds for the sensitive-read and
RBAC-probe counters depend heavily on a cluster's own baseline, and an arbitrary one would page on
ordinary operator traffic.

## Schema stability

The event schema is **unversioned**. Upstream added it in late 2025, has only ever grown it, and
publishes no stability guarantee; the object written to the bucket is a server-side wrapper around
upstream's type, and tsrecorder's server is not open source.

`tailscale.k8s.schema_drift` counts records that do not match the expected shape — today, any event
`type` other than `kubernetes-api-request`, which is the only value upstream emits. A healthy feed
reports nothing at all, so watch it after upgrading the operator or the recorder.

See [Metrics](metrics.md#kubernetes-audit) for the full signal catalog.
