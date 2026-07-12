---
title: Installation
description: Install tailscale2otel via Docker, the Helm chart, or a static binary
tags:
  - Deployment
---

# Installation

`tailscale2otel` ships as a single static binary with no runtime dependencies.
Pick the method that fits your environment — Docker Compose for a quick single-host
deployment, Helm for Kubernetes, or a local binary build for testing.

Before you start, you will need:

- A Tailscale [OAuth client](https://tailscale.com/kb/1215/oauth-clients) (recommended) or an API key.
- An OTLP destination — Grafana Cloud, a self-hosted Alloy/Collector, or `stdout` for local debug.

See [Configuration](configuration.md) for the full list of options once you are up and running.

---

=== "Docker"

    ## Docker

    The published image is `ghcr.io/rknightion/tailscale2otel:latest`.

    ### Env-only (no file to mount)

    The config file is optional. Pass `TS2OTEL_*` environment variables and the
    exporter starts from built-in defaults plus those overrides — nothing to mount:

    ```sh
    docker run --rm \
      -e TS2OTEL_TAILSCALE__TAILNET=example.com \
      -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id> \
      -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
      -e TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id> \
      -e TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
      ghcr.io/rknightion/tailscale2otel:latest
    ```

    ### With a config file

    If you prefer YAML for the non-secret fields, mount it and pass `-config`:

    ```sh
    docker run --rm \
      -v "$PWD/config.yaml:/etc/tailscale2otel/config.yaml:ro" \
      -e TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret> \
      -e TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token> \
      ghcr.io/rknightion/tailscale2otel:latest \
      -config /etc/tailscale2otel/config.yaml
    ```

    ### Docker Compose

    A ready-to-use [`deploy/docker-compose.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/docker-compose.yaml)
    is included in the repository. Put your secrets in a `.env` file next to the
    compose file, then bring it up:

    ```sh
    # .env (gitignored — never commit this file)
    TS2OTEL_TAILSCALE__TAILNET=example.com
    TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=...
    TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=...
    TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=...
    TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=...
    ```

    ```sh
    docker compose -f deploy/docker-compose.yaml up
    ```

    The compose file mounts a named volume at `/var/lib/tailscale2otel` for
    checkpoint persistence, so polling resumes without gaps after a restart.

    !!! tip "Checkpoint persistence"
        For polled log collectors (`flowlogs`, `auditlogs`), checkpoints record
        the high-water mark so restarts resume without re-fetching old records.
        The named volume in the compose file handles this automatically. When
        running `docker run` directly, add `-v ts2otel-checkpoints:/var/lib/tailscale2otel`
        to persist checkpoints across restarts. If the path is not writable the
        exporter logs a warning and falls back to in-memory (safe, but the poller
        cold-starts from `initial_lookback` on restart).

=== "Helm"

    ## Helm

    The chart is published as an OCI artifact. Install it with:

    ```sh
    helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
      --set "secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id>" \
      --set "secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<client-secret>" \
      --set "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=<stack-id>" \
      --set "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token>"
    ```

    The entire application config lives under the `config:` key in `values.yaml`
    and is rendered verbatim into a ConfigMap. Secrets are injected as `TS2OTEL_*`
    environment variables via a separate Secret — they never appear in the ConfigMap.

    To also override a config field:

    ```sh
    helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
      --set "secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=..." \
      --set "secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=..." \
      --set "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=..." \
      --set "secret.TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=..." \
      --set config.log_level=debug
    ```

    !!! note "Checkpoint persistence"
        The chart defaults to `config.checkpoint.store: file` with an `emptyDir`
        at `/var/lib/tailscale2otel`. Set `persistence.enabled=true` to create a
        PVC for durable storage across pod rescheduling.

    For the full values table — every knob, type, default, and description — see the
    [chart README on GitHub](https://github.com/rknightion/tailscale2otel/blob/main/deploy/helm/tailscale2otel/README.md).

=== "Binary"

    ## Binary

    Build from source with the Go toolchain (Go 1.26+ required — see `go.mod` for the pinned version):

    ```sh
    git clone https://github.com/rknightion/tailscale2otel.git
    cd tailscale2otel
    go build -o tailscale2otel ./cmd/tailscale2otel
    ```

    Copy the example config and edit it — keep secrets in environment variables,
    not in the YAML file:

    ```sh
    cp config.example.yaml config.yaml
    # edit config.yaml for your tailnet and OTLP endpoint
    export TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=<secret>
    export TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=<token>
    ./tailscale2otel -config config.yaml
    ```

    !!! tip "Local debug without a backend"
        Set `TS2OTEL_OTLP__PROTOCOL=stdout` (or `otlp.protocol: stdout` in the
        YAML) to print metrics and logs to the console — no OTLP backend needed.

    Release binaries (pre-built, multi-arch) are attached to each
    [GitHub Release](https://github.com/rknightion/tailscale2otel/releases) and
    are signed with cosign keyless signatures.

---

## Next steps

- [Getting Started](getting-started.md) — authenticate, point at an OTLP backend, and verify the first metrics arrive.
- [Configuration](configuration.md) — every setting, default, and environment variable reference.
