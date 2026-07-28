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

    !!! warning "Pass `--stop-timeout 45` on every `docker run`"
        Shutdown is *staged*, and `docker run`'s default stop timeout is 10
        seconds — long enough to be killed partway through the first stage. See
        [Shutdown budgets](#shutdown-budgets) below for the arithmetic. Both the
        Compose file and the Helm chart set an adequate budget already; a bare
        `docker run` is the one path where you must set it yourself.

    ### Docker Compose

    A ready-to-use [`deploy/docker-compose.yaml`](https://github.com/rknightion/tailscale2otel/blob/main/deploy/docker-compose.yaml)
    is included in the repository. `deploy/.env` — the file sitting next to the
    compose file — is the one canonical place for your credentials:

    ```sh
    # deploy/.env — never commit this file
    TS2OTEL_TAILSCALE__TAILNET=example.com
    TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=...
    TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=...
    TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=...
    TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=...
    ```

    ```sh
    docker compose -f deploy/docker-compose.yaml up
    ```

    Compose loads its env file from the **project directory** — the directory
    holding the compose file — not from your shell's cwd, so `deploy/.env` is
    read whichever directory you run that command from. A `.env` at the
    repository root is *not* picked up on that command line; if you keep
    credentials somewhere else, pass `--env-file /path/to/file` explicitly.

    The compose file mounts a named volume at `/var/lib/tailscale2otel` for
    checkpoint persistence, so polling resumes without gaps after a restart.

    ### Running from a config file

    The compose file needs no config file — every setting has a `TS2OTEL_*`
    variable. To drive it from YAML instead, add the override file as a second
    `-f`:

    ```sh
    cp config.example.yaml deploy/config.yaml   # then edit
    docker compose -f deploy/docker-compose.yaml \
                   -f deploy/docker-compose.config.yaml up
    ```

    Keep credentials in `deploy/.env` even in this mode. Environment variables
    override file values, `deploy/.env` is covered by both `.gitignore` and
    `.dockerignore`, and `deploy/config.yaml` is git-ignored but is not a
    secret-handling path.

    !!! warning "Do not add the mount to the base compose file"
        A Compose service map may carry only one `volumes:` key. Adding a second
        one to `deploy/docker-compose.yaml` is a parse error, not a merge —
        Compose refuses the whole file with `mapping key "volumes" already
        defined`. Earlier versions of that file suggested exactly this in a
        comment (#333). Across two `-f` files Compose *merges* the volume lists
        by mount target, which is why the config mount lives in an override and
        why the checkpoint volume survives it.
        `deploy/tests/compose-tests.sh` asserts both modes resolve and that the
        checkpoint mount is present in each.

    !!! warning "`.gitignore` is not a Docker build-context boundary"
        `deploy/.env` is covered by two *separate* mechanisms, and you need both:

        - **`.gitignore`** stops it being committed. It matches `.env` at the
          repository root and one level down, along with `.secrets/`,
          `config.local.yaml`, `.capture/` and `checkpoints*`.
        - **`.dockerignore`** stops it being uploaded to the Docker daemon.
          Docker never reads `.gitignore` — a git-ignored file is still sent with
          the build context and recorded in the build cache unless
          `.dockerignore` excludes it.

        `.dockerignore` is an **allowlist** (default-deny): the build context is
        only `go.mod`, `go.sum`, `cmd/`, `internal/`, `LICENSE`,
        `config.example.yaml` and `scripts/notices.*`. Compose builds, direct
        `docker build -f deploy/Dockerfile .`, BuildKit and the release pipeline
        all use the repository root as their context, and `.dockerignore` is only
        honoured at the context root — so that single file governs every build
        path. If you add a top-level directory the image needs, re-include it
        there or the build fails with a missing package.

        `scripts/check-secret-hygiene.sh` gates both halves: it asserts every
        documented secret path is git-ignored, that the committed example files
        stay trackable, and — by planting disposable sentinel files and inspecting
        the context from inside the builder — that nothing sensitive reaches a
        build layer.

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

    The chart is published as an OCI artifact.

    !!! danger "Never pass a credential with `--set`"
        Passing a credential as an inline `--set secret.<KEY>` value works,
        which is why it is easy to reach for. It also writes the credential
        into your shell history and
        exposes it in `ps` output to every other user on the machine for the
        duration of the install. Use one of the three modes below instead;
        `scripts/check_doc_commands.py` fails CI if any documented command in
        this repository puts a credential inline.

    ### Preferred: a pre-created Secret (`existingSecret`)

    The credential never passes through Helm, so it is not in the release values
    either — only in the Secret object, under normal Secret RBAC:

    ```sh
    cat > creds.env <<'EOF'
    TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=...
    TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=...
    TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID=...
    TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN=...
    EOF
    chmod 600 creds.env

    kubectl create secret generic tailscale2otel-creds --from-env-file=creds.env
    rm creds.env

    helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
      --set-string config.tailscale.tailnet=example.com \
      --set-string existingSecret=tailscale2otel-creds
    ```

    Use `--from-env-file` or `--from-file`, not `--from-literal` — the latter has
    exactly the same command-line exposure as an inline `--set`.

    Rotating that Secret does not reach a running pod on its own; see
    [Rotating an externally managed Secret](#rotating-an-externally-managed-secret)
    below.

    ### Alternative: a values file the chart turns into a Secret

    If you would rather Helm manage the Secret, keep the values in a file with
    restrictive permissions rather than on the command line:

    ```sh
    cat > secrets.yaml <<'EOF'
    secret:
      TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID: ...
      TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET: ...
      TS2OTEL_OTLP__GRAFANA_CLOUD__INSTANCE_ID: ...
      TS2OTEL_OTLP__GRAFANA_CLOUD__TOKEN: ...
    EOF
    chmod 600 secrets.yaml

    helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
      -f secrets.yaml --set-string config.tailscale.tailnet=example.com
    ```

    A single credential can also come from its own file, which keeps it out of
    both argv and any multi-value file:

    ```sh
    helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
      --set-file secret.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_SECRET=./client-secret.txt
    ```

    Both forms store the value in the Helm release, which lives in a Secret in
    the release namespace — protected by Secret RBAC, but readable by anyone who
    can run `helm get values`. `existingSecret` avoids that; these do not.

    ### Development only: inline

    An inline `--set secret.<KEY>` value is acceptable **only** against a
    throwaway cluster with a throwaway credential. It is not a quick start, and
    it is deliberately not written out here as a copy-pasteable command.

    ---

    The entire application config lives under the `config:` key in `values.yaml`
    and is rendered verbatim as `config.yaml`. Keep credentials out of it: inject
    them as `TS2OTEL_*` environment variables via `existingSecret` or the chart's
    own Secret, as above.

    !!! warning "Credentials never land in a ConfigMap"
        A ConfigMap is readable by anyone holding `get configmaps` in the namespace,
        which is routinely granted far more widely than `get secrets`. If a
        credential-bearing key *is* set inline under `config:` — an OAuth
        `client_secret`, `apikey`, `headscale.api_key`, `grafana_cloud.token`,
        `otlp.headers`, the `objectstore` keys, the `streaming`/`webhook`/
        `prometheus`/`admin` tokens, the Pyroscope password, any `tailnets[]` entry,
        or a `node_metrics` target with a `bearer_token`/`headers` — the chart
        renders the whole `config.yaml` into a Secret instead of a ConfigMap and
        mounts it from there. Credential-free configs keep the ConfigMap. Set
        `configStorage.mode` to `secret` or `configmap` to override; `configmap`
        with a credential set inline makes `helm template` fail and names the keys.

    !!! note "Rotating an externally managed Secret"
        Credentials reach the container through `envFrom`, and Kubernetes never
        refreshes environment variables in a running container. So rotating the
        values in an `existingSecret` you manage yourself does **not** reach the
        running pod — the pod template only references it by name. Force a rollout
        after rotating:

        ```sh
        helm upgrade tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
          --reuse-values --set rolloutTrigger="$(date +%s)"
        ```

        `rolloutTrigger` is an opaque value of your choosing, surfaced as a pod
        annotation — never put a secret value or a hash of one there. For an
        automated path, run [Stakater Reloader](https://github.com/stakater/Reloader)
        and set `podAnnotations."reloader.stakater.com/auto"="true"`; it issues a
        rollout restart, which is what env-injected credentials require. The chart's
        `checksum/config` and `checksum/secret` annotations already cover
        chart-managed config and inline `secret:` values.

    Config fields carry no credentials, so they are fine on the command line —
    combine them with whichever credential mode you chose above:

    ```sh
    helm install tailscale2otel oci://ghcr.io/rknightion/charts/tailscale2otel \
      --set-string existingSecret=tailscale2otel-creds \
      --set-string config.tailscale.tailnet=example.com \
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

    !!! tip "Where checkpoints go on a native run"
        The shipped default, `/var/lib/tailscale2otel/checkpoints.json`, suits a
        **container** — the image pre-seeds that directory for uid 65532, and the
        Helm chart sets it explicitly and mounts a volume there. A native run
        usually cannot write it: on Linux only root can create `/var/lib`
        subdirectories, and macOS and Windows have no `/var/lib` at all, though
        releases ship binaries for both.

        So when the path is **left at its default** and is not writable, the
        exporter uses the platform state directory instead and logs both paths at
        INFO:

        | Platform | Location |
        | --- | --- |
        | Linux/BSD | `$XDG_STATE_HOME/tailscale2otel/`, else `~/.local/state/tailscale2otel/` |
        | macOS | `~/Library/Application Support/tailscale2otel/` |
        | Windows | `%LocalAppData%\tailscale2otel\` |

        Precedence, and what is *not* done:

        - **The configured path always wins when it is usable.** Nothing is ever
          moved or copied, so an existing checkpoint can never be stranded by
          this — relocation only happens where there was no readable checkpoint.
        - **An explicitly configured `checkpoint.file_path` is never relocated.**
          Naming a path is a decision, and it is usually a mounted volume that is
          briefly absent; writing elsewhere would hide that misconfiguration and
          split state across two locations. Those still WARN and fall back to
          in-memory, as before.
        - **Migrating an existing native install is manual and optional.** If you
          were running as root against `/var/lib` and want to move to the
          per-user path, copy `checkpoints.json` there yourself. Doing nothing is
          safe: the old path keeps working while it is writable.

        The effective store, the effective path, and the reason for any
        divergence are all shown on the admin status page and in
        `/api/status.json` (`checkpoint_store`, `checkpoint_path`,
        `checkpoint_reason`), so you never have to read startup logs to find out
        where state went.

    Release binaries (pre-built, multi-arch) are attached to each
    [GitHub Release](https://github.com/rknightion/tailscale2otel/releases) and
    are signed with cosign keyless signatures.

---

## Shutdown budgets

Stopping the exporter is not instantaneous, and cutting it short loses data that
was already accepted. Shutdown runs in **stages**, each separately bounded:

| Stage | Bound | What is lost if it is cut short |
| --- | --- | --- |
| Receivers drain | 10s | Requests already ACKed to Tailscale, still being processed |
| Ingress WAL final drain | 10s | The accepted-but-unexported backlog (replayed next start) |
| OTLP flush and shutdown | 10s | The final flow rollup and the last metric/log export |

Worst case is therefore **30 seconds**, and a deployment budget needs headroom on
top of that — the numbers above are bounds, not durations, and process teardown
lands after them. Every shipped path uses **45 seconds**:

| Path | Setting | Its own default |
| --- | --- | --- |
| Compose | `stop_grace_period: 45s` (set in `deploy/docker-compose.yaml`) | 10s |
| Kubernetes | `terminationGracePeriodSeconds: 45` (chart value) | 30s |
| `docker run` | `--stop-timeout 45` — **you must pass this** | 10s |
| systemd (your own unit — none is shipped yet) | `TimeoutStopSec=45` | 90s, already adequate |

Kubernetes' own default of 30 is worth calling out: it equals the drain exactly,
with zero margin, so it is not a safe value despite looking like one. The chart
**fails to render** below 45 rather than silently truncating a drain.

These numbers are derived, not copied. `internal/app` sums the stage constants
and its tests fail if the Compose file, the chart default, or the chart's
enforced floor stops covering the total — so raising any stage timeout fails the
build with a message naming the files to update, instead of quietly eroding the
margin. Raise the budgets if you raise a timeout; lowering them below the floor
is a data-durability decision the chart will not make silently.

---

## Next steps

- [Getting Started](getting-started.md) — authenticate, point at an OTLP backend, and verify the first metrics arrive.
- [Configuration](configuration.md) — every setting, default, and environment variable reference.
