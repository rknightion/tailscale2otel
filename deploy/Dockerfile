# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.26-alpine@sha256:f23e8b227fb4493eabe03bede4d5a32d04092da71962f1fb79b5f7d1e6c2a17f AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
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

# ---- runtime ----
FROM gcr.io/distroless/static-debian13:nonroot@sha256:963fa6c544fe5ce420f1f54fb88b6fb01479f054c8056d0f74cc2c6000df5240
COPY --from=build /out/tailscale2otel /usr/local/bin/tailscale2otel
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
ENTRYPOINT ["/usr/local/bin/tailscale2otel"]
CMD []
