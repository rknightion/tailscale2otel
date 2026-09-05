# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.27.1-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# `COPY . .` is safe because .dockerignore at the context root is an ALLOWLIST
# (default-deny): the context holds only go.mod/go.sum, cmd/, internal/, LICENSE,
# config.example.yaml and scripts/notices.*. Local .env / .secrets/ / .capture/ /
# config.local.yaml / checkpoints never reach the daemon, so they cannot land in
# this layer or the build cache. .gitignore does NOT do that — Docker never reads
# it. Adding a top-level path the build needs means re-including it in
# .dockerignore; scripts/check-secret-hygiene.sh gates both directions.
COPY . .
ARG VERSION=dev
# Cache mount for the Go build cache so incremental rebuilds (and CI builds via
# buildkit-cache-dance) skip recompiling unchanged packages. The module cache
# stays in the `go mod download` layer above (gha-layer-cacheable), so it is
# deliberately NOT mounted here — mounting /go/pkg/mod would shadow it.
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/tailscale2otel ./cmd/tailscale2otel
# Create the default checkpoint directory so the distroless runtime image has it
# pre-seeded with the correct ownership (uid/gid 65532 = nonroot).
RUN mkdir -p /var/lib/tailscale2otel
# Third-party notices (LICENSE + NOTICE texts of the linked modules) baked into /licenses/
# in the runtime stage below, so license compliance travels with the image. Pinned go-licenses;
# see scripts/notices.sh. Network is already required by `go mod download` above (no new
# constraint), and the module cache populated there lets go-licenses read each LICENSE/NOTICE.
# alpine ships no bash, so add it for the script (builder stage is discarded — no runtime cost).
ARG GO_LICENSES_VERSION=v1.6.0
RUN --mount=type=cache,target=/root/.cache/go-build \
    apk add --no-cache bash && \
    go install github.com/google/go-licenses@${GO_LICENSES_VERSION} && \
    GO_LICENSES=go-licenses OUT=/src/THIRD_PARTY_NOTICES.md bash scripts/notices.sh

# ---- runtime ----
FROM gcr.io/distroless/static-debian13:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6
COPY --from=build /out/tailscale2otel /usr/local/bin/tailscale2otel
# License compliance travels with the image (OCI /licenses convention): the repo's Apache-2.0
# text + the generated third-party notices for the linked modules.
COPY --from=build /src/LICENSE /licenses/LICENSE
COPY --from=build /src/THIRD_PARTY_NOTICES.md /licenses/THIRD_PARTY_NOTICES.md
LABEL org.opencontainers.image.licenses="Apache-2.0"
# Pre-seeded checkpoint directory (owned by nonroot 65532:65532). Checkpoints
# persist here across container restarts when no external volume is mounted
# (ephemeral — lost on container recreation). Mount a named volume or host
# bind-mount at /var/lib/tailscale2otel for durable persistence.
COPY --from=build --chown=65532:65532 /var/lib/tailscale2otel /var/lib/tailscale2otel
# config.example.yaml is copied for reference; it is NOT loaded by default.
# The exporter runs from built-in defaults + TS2OTEL_* environment variables.
# To use a config file, mount it and add -config /path/to/config.yaml, e.g.:
#   docker run -v ./config.yaml:/etc/tailscale2otel/config.yaml:ro \
#              ghcr.io/rknightion/tailscale2otel:latest \
#              -config /etc/tailscale2otel/config.yaml
COPY config.example.yaml /etc/tailscale2otel/config.example.yaml
USER nonroot:nonroot
# Streaming (HEC) and webhook receivers, if enabled in config.
EXPOSE 8088 8089
# Deliberately NO HEALTHCHECK instruction here (#337). admin.enabled is a
# runtime config choice, and an image-level HEALTHCHECK can't see config — it
# would either assume admin is always on (wrong when it's disabled) or always
# off (no probe when it's the default). The probe ships as a binary flag,
# `tailscale2otel -healthcheck`, so the orchestrator that DOES know the
# effective config wires it up: see the `healthcheck:` block in
# deploy/docker-compose.yaml, or `docker run --health-cmd`.
ENTRYPOINT ["/usr/local/bin/tailscale2otel"]
CMD []
