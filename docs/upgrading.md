---
title: Upgrading
description: Upgrade notes and breaking changes, including the v2.0.0 semantic-convention attribute renames
tags:
  - Deployment
  - Configuration
---

# Upgrading

This page collects the behaviour changes worth knowing about when moving between
`tailscale2otel` releases. Each config key references the full dotted key path; see
[Configuration](configuration.md) for defaults and the `TS2OTEL_*` env-var equivalents,
and [Metrics](metrics.md) for the signal catalogue.

## Upgrade and rollback checklist {#upgrade-and-rollback-checklist}

Use this procedure for every release, image, or binary change. The version-specific sections
below describe only behaviour changes and link back here for the operational sequence.

### 1. Record the current state

- Choose the target artifact and read its version-specific entry below. Keep the previous artifact,
  its configuration, and the deployment manifest available until post-upgrade verification passes.
- Confirm that only one instance targets each tailnet. A replacement must not overlap the old
  instance: both would poll and emit the same data.
- Record the running version before stopping it:

  ```sh
  tailscale2otel -version
  ```

- Identify every state path in the effective configuration. Back up the paths that exist before
  changing the artifact or configuration:

  | State | When to back it up | Consideration |
  | --- | --- | --- |
  | `checkpoint.file_path` | `checkpoint.store: file` or `checkpoint.evidence_store: file` | Preserves polled log high-water marks and ACL semantic evidence. Existing combined files remain readable; copy the containing persistent volume or directory while the service is stopped. |
  | `ingress_wal.directory` | `ingress_wal.enabled: true` | Contains accepted receiver bodies waiting for replay. Preserve it if those bodies must survive the change; do not edit or discard entries by hand. |
  | `flows.store.directory` | The path is non-empty | Contains SQLite flow history, including the `-wal`/`-shm` sidecars when present. Stop the service before copying the directory. The rows can contain user identities, so protect this backup like the live data. |
  | In-memory stores | No persistent path is configured | There is no file to back up; that history is lost on restart. |

  A volume or filesystem snapshot is suitable when it is consistent. Keep the backup and the
  restore procedure available until the new instance has passed delivery verification. A backup is
  a recovery point, not evidence that the new artifact can read the state.

### 2. Validate and review the target

Install or unpack the target artifact beside the running one, then run the target binary against the
configuration it will use. If the deployment is env-only, omit `-config <file>` from both commands.

```sh
/path/to/target/tailscale2otel -validate -config <file>
/path/to/target/tailscale2otel -print-effective-config \
  -print-effective-config-provenance -config <file> > effective-config.target.json
```

`-validate` does not start the exporter or touch the network. It exits `0` after reporting any
advisories when there are no hard errors; an error exits `1`. Treat a validation error as a stop
condition. Use `-warnings-as-errors` when the deployment policy requires a warning-free rollout.

Review the effective configuration before the restart. Compare it with a copy captured from the
current artifact if useful:

```sh
tailscale2otel -print-effective-config -print-effective-config-provenance \
  -config <file> > effective-config.current.json
diff -u effective-config.current.json effective-config.target.json
```

The output uses the same redaction as the admin status page: secret values are represented by their
redacted state and source, never their contents. It can still contain tailnet names, endpoints,
paths, and enabled features. Keep these files local and redact those identifiers before sharing
them.

Pay particular attention to `checkpoint`, `ingress_wal`, `flows.store.directory`, listener binds,
delivery mode, and whether the target is reading the intended environment variables. A green
`-validate` proves configuration validity only; it does not prove credentials, collection, state
compatibility, or backend delivery.

### 3. Classify the rollback boundary

**Rollback-safe change.** A binary/image replacement or compatible configuration change can use the
previous artifact again when no persistent-format or persistent-path migration has run and the
previous artifact is documented to read the retained state. If the target fails validation,
startup, or readiness, stop it first, restore the previous artifact and configuration, and reuse
the unchanged state volume. If the target may have written state before failing, restore the
pre-upgrade backup before starting the previous artifact.

**Forward-only migration.** Treat a change as forward-only once it has stamped, renamed, migrated,
or otherwise changed persistent state in a way the previous artifact does not document as
compatible. Stop the failed target and follow the target release's recovery procedure. Do not
point the previous artifact at a migrated database just because the process did not become ready.
The pre-v4 flow-store adoption below is the worked example.

### 4. Replace one instance and wait for readiness

- Stop the current instance gracefully and wait for it to exit. Preserve the configured state
  volume, including any persistent checkpoint, ingress-WAL, or flow-store paths.
- Replace the image or binary and apply the already-validated configuration. Start exactly one
  target instance. Use the deployment supervisor's controlled replacement operation; for example,
  a Compose deployment can stop and start the service, while a Kubernetes deployment can wait for
  its rollout:

  ```sh
  # Compose: stop and start the service with the same state volume.
  docker compose stop tailscale2otel
  docker compose up -d tailscale2otel
  # Kubernetes: after applying the image/config change, wait for the rollout.
  kubectl rollout status deployment/<deployment> --timeout=5m
  ```

  The commands are examples; use the equivalent operation for the actual deployment and keep its
  persistent volume attached.
- Wait for `/readyz` to return `2xx`. For a local binary or a container with the admin listener in
  its own network namespace, the built-in probe is:

  ```sh
  tailscale2otel -healthcheck -healthcheck-timeout 10s
  ```

  A direct probe is also possible when the admin listener is reachable:

  ```sh
  curl --fail http://127.0.0.1:9091/readyz
  ```

  A successful readiness check means the process has completed its startup readiness conditions
  and its enabled components are serving. It does not prove that the OTLP backend accepted data.
  Inspect startup logs and the admin status page for configuration advisories, authentication
  failures, checkpoint fallback, receiver/WAL failures, and flow-store errors.

### 5. Verify delivery after the restart

Choose the delivery path the deployment actually uses. Do not call a local listener check proof of
an external scrape or OTLP ingest.

**OTLP push.** After at least one normal export interval, query the OTLP backend for a fresh
`tailscale2otel_up_ratio` sample. When self-observability is enabled, also check that
`tailscale2otel_build_info_ratio{version="<target-version>"}` is present and that
`tailscale2otel_export_failures_total` is not increasing. Confirm the expected log signal as well
when OTLP logs are enabled. These backend observations prove delivery from the running target;
`-validate` and `/readyz` cannot.

**Prometheus pull.** When `prometheus.enabled` or a Prometheus/dual delivery mode is configured,
run the bounded local check with the target binary and then check the actual scraper:

```sh
/path/to/target/tailscale2otel -prometheus-check -json -config <file>
PROMETHEUS_URL=http://127.0.0.1:2112 # default example; replace with prometheus.listen and scheme
curl --fail "$PROMETHEUS_URL/metrics"
```

`-prometheus-check` runs one side-effect-free collection cycle and validates the exposition without
binding the listener. A successful `curl` proves only that the listener responds. Verify the
scraper target is healthy and query its store for a fresh `tailscale2otel_up_ratio`; check
`tailscale2otel_build_info_ratio{version="<target-version>"}` when self-observability is enabled.
Use the configured TLS and authentication for a non-loopback metrics listener.

If readiness or delivery verification fails, stop the target before attempting recovery. Use the
rollback-safe path above only when no forward-only migration has run; otherwise stay on the target
format and follow its forward-recovery instructions.

### Worked forward-only migration: pre-v4 flow-store adoption

A persistent flow store written before v4 uses `flows-<tailnet>.db` and has no tailnet identity
row. v4 and later require the identity and a digest-qualified filename, so the service refuses the
legacy file automatically rather than guessing which tailnet owns its user and device identities.
This migration is forward-only for the running state.

1. Stop `tailscale2otel` and every other reader of the flow-store directory. Back up the entire
   directory before changing it. Adoption refuses a non-empty legacy `-wal`; ensure no writer or
   other reader is active, then rerun it.
2. Confirm that the target configuration uses the same `flows.store.directory` and that the named
   tailnet is present in `tailscale:` or `tailnets:`. Run the target `-validate` command from step 2.
3. With the service still stopped, run the target binary once:

   ```sh
   /path/to/target/tailscale2otel -config <file> -adopt-flow-db <tailnet>
   ```

   The command asserts ownership from the supplied tailnet name, migrates the database schema,
   stamps its identity row, moves the legacy file to the digest-qualified name, reports the rows
   retained, and exits. It is safe to interrupt and rerun. It refuses an owner mismatch, both
   legacy and qualified files, or a live write-ahead log; resolve the named condition and rerun it.
4. Start the target once the command succeeds, then complete the readiness and OTLP or Prometheus
   verification above. A no-op adoption result means the legacy file is absent, because it was
   already adopted or there was no persistent database for that tailnet.

After adoption, a pre-v4 binary expects the legacy filename and must not be pointed at the migrated
database. If the target fails after adoption, keep the target format and recover forward. Returning
to the pre-v4 artifact requires stopping the target and restoring the untouched pre-adoption backup,
including its legacy filename, before starting the old artifact; that is a deliberate state restore,
not a rollback of the migrated file in place.

## Upgrading to v5.0.0

Before applying the version-specific changes in this section, follow the
[upgrade and rollback checklist](#upgrade-and-rollback-checklist).

### Network receivers now require credentials at startup

An enabled streaming or webhook receiver on a non-loopback listener must now have its required
credential before the process starts. This applies to the legacy receiver fields and every
multi-tailnet route: set `streaming.token` (or `streaming.token_file`) for streaming, and
`webhook.secret` (or `webhook.secret_file`) for webhooks. In v4, an empty credential on a
network-reachable listener started the process but made the receiver refuse every request with
HTTP `403`; v5 rejects that configuration during `Config.Validate` instead.

Credential-free loopback listeners remain intentionally supported for local development or a
trusted local proxy. They still warn because any process on the host can inject records or events;
use a credential when that is not an acceptable trust boundary.

**Action:** before deploying v5, run `tailscale2otel -validate -config <file>` with the target
binary. Set the corresponding secret via `TS2OTEL_STREAMING__TOKEN` or
`TS2OTEL_WEBHOOK__SECRET` (or their `*_FILE` forms) for every non-loopback receiver route. Go
consumers must also update their module requirement and imports to
`github.com/rknightion/tailscale2otel/v5`.

## Upgrading to v4.0.0

Before applying the version-specific changes in this section, follow the
[upgrade and rollback checklist](#upgrade-and-rollback-checklist).

### The Prometheus pull endpoint gained real defaults

`prometheus.max_requests_in_flight`, `prometheus.timeout` and `prometheus.coalesce_gather`
shipped as `0` / `0s` / `false` — an unbounded, untimed `/metrics` handler. A `Gather` walks
every series in the registry, so concurrent slow scrapes each paid for a full walk with
nothing to shed them. The three now default to `4`, `8s` and `true`, and
`max_requests_in_flight: 0` is **no longer accepted** while `prometheus.enabled` is true: the
old "0 = unlimited" reading is exactly the state the cap exists to prevent, so it is refused
loudly rather than honoured silently.

| Key | Old default | New default |
| --- | --- | --- |
| `prometheus.max_requests_in_flight` | `0` (unlimited) | `4` |
| `prometheus.timeout` | `0s` (none) | `8s` |
| `prometheus.coalesce_gather` | `false` | `true` |

`prometheus.timeout: 0` still means "no timeout" and remains valid — only the request cap
lost its zero.

**Action:** if your config carries `prometheus.max_requests_in_flight: 0` and the endpoint is
enabled, set it to `4` (or higher if several scrapers hit one instance). `config.example.yaml`
recommended `0` until this release, so a config copied from it needs this edit. The value is
**unchecked while `prometheus.enabled` is false**, so a copied example with the endpoint
switched off starts unchanged; raise the other two only if a scrape legitimately runs longer
than 8s.

## Upgrading to v2.0.0

Before applying the version-specific changes in this section, follow the
[upgrade and rollback checklist](#upgrade-and-rollback-checklist).

`v2.0.0` is a **breaking** release with a single, contained change: five telemetry
attributes carrying user/actor identity and error text are renamed to their stable
OpenTelemetry semantic-convention equivalents. OTel deprecated the `enduser.*` namespace in
favour of the ECS-aligned `user.*` registry (`user.id`, `user.name`, `user.full_name`), and
`error.message` is the stable key for a human-readable error string. The old names are
**gone** — this is a hard cutover with no duplicate-attribute deprecation window.

Nothing else changed: no metric names, units, config keys, endpoints, or Helm values move
in `2.0.0`, and no other attributes are renamed. Tailscale-specific concepts (DERP, exit
nodes, subnet routes, tailnet identity, actor *type*) keep their `tailscale.*` names because
no semantic convention covers them.

### Renamed attributes

The rename affects the audit log records, the device-invite log event (devices collector),
and the users collector's per-user gauges and log events. In PromQL/LogQL you query the
**Prometheus-normalized** label name (right-hand columns), not the OTel attribute key:

| old attribute | new attribute | old Prom/Loki label | new Prom/Loki label |
| --- | --- | --- | --- |
| `enduser.id` | `user.id` | `enduser_id` | `user_id` |
| `tailscale.actor.login` | `user.name` | `tailscale_actor_login` | `user_name` |
| `tailscale.actor.display` | `user.full_name` | `tailscale_actor_display` | `user_full_name` |
| `tailscale.user.login` | `user.name` | `tailscale_user_login` | `user_name` |
| `error` | `error.message` | `error` | `error_message` |

Both `tailscale.actor.login` (audit actor / device-invite acceptor) and
`tailscale.user.login` (users collector) collapse onto the single `user.name` key, since
both are the same "short login/username" concept the `user.name` convention describes.

**Action:** update any dashboard, alert rule, or saved query that references an old label to
its new name. The shipped Grafana dashboards and alert rules in `deploy/` are already updated
— re-import them to pick up the new labels. The `pii_filter` toggles are unchanged: the same
category still gates each attribute (`user.id` → user IDs, `user.name` → emails,
`user.full_name` → display names, `error.message` → free-text details), so no PII
configuration needs changing.

### Log `event.name` was already present

The `feat!` motivation also referenced OTel's March 2026 deprecation of the Span Events API
in favour of log records carrying an event name. No migration is needed there: the flow and
audit log records have **always** set the native OTLP LogRecord `EventName` field
(`tailscale.network.flow` and `tailscale.config.audit`) — that is the OTel-blessed
post-Span-Events mechanism, and it did not change in `2.0.0`. It is called out here only so
the pre-2.0 behaviour is on record.

## Upgrading to v1.0.0

Before applying the version-specific changes in this section, follow the
[upgrade and rollback checklist](#upgrade-and-rollback-checklist).

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

## Version references

This page covers the notable migrations. For the full, per-release detail:

- **[Releases on GitHub](https://github.com/rknightion/tailscale2otel/releases)** — every version,
  with its notes and downloadable binaries.
- **[CHANGELOG.md](https://github.com/rknightion/tailscale2otel/blob/main/CHANGELOG.md)** — the
  complete generated changelog (also mirrored at [Changelog](changelog.md)).
- **[Compare any two versions](https://github.com/rknightion/tailscale2otel/compare)** — useful when
  jumping several releases at once.

Container images are tagged per release at
[`ghcr.io/rknightion/tailscale2otel`](https://github.com/rknightion/tailscale2otel/pkgs/container/tailscale2otel),
and the Helm chart at
[`ghcr.io/rknightion/charts/tailscale2otel`](https://github.com/rknightion/tailscale2otel/pkgs/container/charts%2Ftailscale2otel).
Pin a tag rather than tracking `latest` if you want controlled upgrades.

If an upgrade breaks something not documented here, please
[open an issue](https://github.com/rknightion/tailscale2otel/issues/new).
