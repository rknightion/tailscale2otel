# Alloy gateway recipe

A production OTLP gateway for `tailscale2otel` built on [Grafana
Alloy](https://grafana.com/docs/alloy/), pinned to **v1.18.0**.

```
tailscale2otel --OTLP/gRPC--> Alloy ------------------------------> your OTLP backend
                              receiver.otlp                          (Grafana Cloud, Tempo/Mimir/Loki,
                              -> processor.memory_limiter             any OTLP endpoint)
                              -> processor.batch
                              -> exporter.otlphttp
                                 (retry + persistent sending queue)
```

| File | What it is |
| --- | --- |
| `config.alloy` | The gateway pipeline. Validated against `grafana/alloy:v1.18.0`. |
| `docker-compose.yaml` | Alloy + `tailscale2otel` wired together in gateway mode. |

The operator-facing guide, including the direct-vs-gateway tradeoff and the
Kubernetes/Helm variant, is **[docs/gateway.md](../../docs/gateway.md)**. This
file covers running the recipe in this directory.

## No credentials in this directory

Neither committed file contains a credential, and neither should ever be edited
to hold one. `config.alloy` reads all three backend settings from the process
environment with `sys.env()`, and `docker-compose.yaml` requires them with
`${VAR:?}` so `up` fails loudly on a missing value instead of starting a broken
or unauthenticated gateway.

Put them in `deploy/.env` — the repository's one canonical Compose credential
file, git-ignored and covered by `scripts/check-secret-hygiene.sh`:

```sh
GATEWAY_OTLP_ENDPOINT=https://otlp-gateway-prod-us-central-0.grafana.net/otlp
GATEWAY_OTLP_USERNAME=<your-numeric-instance-id>
GATEWAY_OTLP_PASSWORD=<your-access-policy-token>

TS2OTEL_TAILSCALE__TAILNET=example.com
TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id>
TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret>
```

For Grafana Cloud, `GATEWAY_OTLP_USERNAME` is the numeric instance/stack ID and
`GATEWAY_OTLP_PASSWORD` is an access-policy token with the OTLP write scopes;
both come from the Cloud Portal (organization **Overview** → **Launch stack** →
**Configure** on the OpenTelemetry tile). Use your own region's
`otlp-gateway-<zone>.grafana.net` host — the exporter appends `/v1/metrics`,
`/v1/logs` and `/v1/traces` to whatever base URL you give it.

## Run it

```sh
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml up -d
```

`--env-file` is required rather than optional: Compose loads `.env` from the
directory holding the compose file, which here is `deploy/alloy/`, so the
canonical `deploy/.env` is not picked up implicitly.

This file **replaces** `deploy/docker-compose.yaml`; it is not an overlay for it.
Do not pass both with two `-f` flags — the direct-export path sets
`otlp.grafana_cloud.*` on the exporter, which is exactly what gateway mode moves
out of it.

Alloy's UI and its own `/metrics` land on `http://127.0.0.1:12345`. That listener
is unauthenticated and exposes pipeline internals, which is why it is bound to
loopback. The OTLP receiver ports (4317/4318) are deliberately **not** published:
they are reachable on the Compose network by service name, they are themselves
unauthenticated, and nothing outside the stack needs them.

## What this buys you, and what it does not

The retry loop plus the sending queue mean a backend outage no longer costs you
the telemetry produced during it — the gateway holds the backlog and delivers it
when the backend returns. With `otelcol.storage.file` wired in, the backlog also
survives an Alloy restart.

**This reduces telemetry loss. It does not eliminate it, and it is not
exactly-once delivery.** Three specific ways data still goes missing or gets
duplicated:

- **The retry deadline drops data.** `retry_on_failure.max_elapsed_time` is set
  to `30m`. A batch not accepted within that window is discarded, persistent
  queue or not. An outage longer than your deadline loses whatever aged out.
- **The queue is finite.** `queue_size` is 10000 *batches* (`sizer = "requests"`
  counts batches, not datapoints). Past that, with `block_on_overflow = false`,
  the gateway returns a retryable error and `tailscale2otel` starts shedding.
- **Retries can duplicate.** `wait_for_result = false` means the receiver
  acknowledges on enqueue, not on backend acceptance. If the backend commits a
  batch but the acknowledgement is lost, the retry sends it again. That is the
  price of the buffering, and it is why no exactly-once guarantee is offered.

Ordinary process crashes and unclean kills are a fourth case: `fsync` is left at
its default `false`, so the last few writes can be lost on an unclean stop.

## Outage and restart smoke test

Everything below was run against `grafana/alloy:v1.18.0` and the committed
`config.alloy`, with a local stub backend standing in for Grafana Cloud. The
observed numbers are quoted from that run.

**1. Confirm a clean load and a ready gateway.**

```sh
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml up -d
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml logs alloy | grep -i error   # expect nothing
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:12345/-/ready         # expect 200
```

**2. Confirm the queue exists and is the size you configured.**

```sh
curl -s http://127.0.0.1:12345/metrics | grep '^otelcol_exporter_queue_capacity'
```

Expect `10000` per signal, matching `queue_size` in `config.alloy`.
`otelcol_exporter_queue_size` is the live depth and should sit at `0` while the
backend is healthy.

**3. Break the backend.** Point `GATEWAY_OTLP_ENDPOINT` at a dead port and
recreate the Alloy container, or block egress to the real one — whichever your
environment makes easy. Either way the exporter now fails every send.

**4. Watch the queue fill instead of the data vanishing.** Let
`tailscale2otel` push at least one export cycle (60s by default), or inject a
datapoint yourself:

```sh
cat > /tmp/m.json <<'JSON'
{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"smoke"}}]},
"scopeMetrics":[{"metrics":[{"name":"smoke.gauge","gauge":{"dataPoints":[{"asDouble":1,
"timeUnixNano":"1770000000000000000"}]}}]}]}]}
JSON
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml exec -T alloy true   # gateway is up
curl -s -X POST http://127.0.0.1:4318/v1/metrics \
  -H 'Content-Type: application/json' --data-binary @/tmp/m.json
```

Injecting directly needs `4318` reachable, so either publish it temporarily or
run the `curl` from another container on the Compose network. Then, after the
5s batch timeout:

```sh
curl -s http://127.0.0.1:12345/metrics | grep '^otelcol_exporter_queue_size'
```

Observed: `1` for `data_type="metrics"` — a queued batch, not a dropped one.
Confirm it is on disk too:

```sh
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml exec alloy \
  ls -l /var/lib/alloy/data/otlp-queue/
```

Observed three files, one per signal:
`exporter_otlp_http_otelcol.exporter.otlphttp.backend_{metrics,logs,traces}`.

**5. Restart Alloy while the backend is still down.** This is the step that
actually tests persistence rather than just buffering:

```sh
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml restart alloy
sleep 12
curl -s http://127.0.0.1:12345/metrics | grep '^otelcol_exporter_queue_size'
```

Observed: still `1`. The backlog was reloaded from disk. Remove the
`otelcol.storage.file` block and this step returns `0` instead — the backlog is
gone, which is exactly the difference the component buys.

**6. Restore the backend and watch it drain.**

```sh
sleep 25
curl -s http://127.0.0.1:12345/metrics | grep '^otelcol_exporter_queue_size'
```

Observed: `0`, and the backend received the batch at `/otlp/v1/metrics` carrying
the `Authorization` header. That confirms the whole path in one shot: base-URL
path appending, basic auth, retry, persistent queue, and drain-on-recovery.

## Tuning notes

- **`memory_limiter.limit` and `mem_limit` move together.** The limiter is set
  to `400MiB` against a `512m` container ceiling, roughly 80%. Raising the
  limiter above the cgroup ceiling means the container is OOM-killed before the
  limiter engages, defeating the point of it.
- **The memory limiter must stay first.** Any processor placed ahead of it
  buffers data the limiter can no longer protect.
- **Sizing the outage budget.** Tolerating an outage of length `T` needs both
  `max_elapsed_time >= T` *and* enough `queue_size` for the batches produced
  during `T`. Raising one without the other does nothing.
- **Dropping the preview component.** `otelcol.storage.file` is public preview
  (see below). To run without it, delete its block and the `storage =` line in
  `sending_queue`, and drop `--stability.level=public-preview` from the Alloy
  command. The queue then works, in memory, and empties on restart.

## Version-pinning traps

**The image tag is pinned on purpose — do not move it to `latest`.** Alloy
component arguments change between minor versions, so an unpinned tag can turn a
working `config.alloy` into a container that refuses to start on the next pull.

Two specific traps found while building this recipe, both verified against
v1.18.0:

- **`otelcol.storage.file` is public preview.** It is subject to breaking
  changes, and Alloy refuses to load a config using it unless
  `--stability.level=public-preview` (or lower) is passed. Without the flag the
  startup error names the component and the line.
- **`otelcol.auth.basic` credentials are flat attributes on v1.18.0, not a
  `client_auth` block.** The published `grafana.com/docs/alloy/latest` page
  documents a `client_auth {}` sub-block and marks the flat form deprecated, but
  those docs are built from Alloy's `main` branch and are ahead of this release.
  On v1.18.0 the `client_auth` form fails at startup with `building component:
  no credential source provided`. Worse, **`alloy validate` accepts it** — the
  block name parses and only the credential wiring is missing — so the offline
  gate cannot catch this. Move to `client_auth` only when the pin moves to a
  release that supports it, and prove it with a real `run`.

The second one generalises: `alloy validate` checks syntax and attribute names,
not that the graph can be built. **Only a real `alloy run` proves the config
loads.**

## Validating a change to `config.alloy`

```sh
# Syntax and attribute names. Necessary, NOT sufficient - see the trap above.
docker run --rm -v "$PWD/deploy/alloy/config.alloy:/etc/alloy/config.alloy:ro" \
  --entrypoint /bin/alloy grafana/alloy:v1.18.0 \
  validate --stability.level=public-preview /etc/alloy/config.alloy

# Canonical formatting - this file is byte-identical to `alloy fmt` output.
docker run --rm -v "$PWD/deploy/alloy/config.alloy:/etc/alloy/config.alloy:ro" \
  --entrypoint /bin/alloy grafana/alloy:v1.18.0 fmt /etc/alloy/config.alloy \
  | diff - deploy/alloy/config.alloy

# The one that actually matters: does it build and become ready?
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml up -d alloy
docker compose --env-file deploy/.env -f deploy/alloy/docker-compose.yaml logs alloy | grep -i error
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:12345/-/ready
```

Neither file here is generated, and neither is covered by a CI drift gate — the
`fmt` diff above is a convention, not an enforced check.
