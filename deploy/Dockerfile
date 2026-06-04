# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.26-alpine AS build
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

# ---- runtime ----
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/tailscale2otel /usr/local/bin/tailscale2otel
COPY config.example.yaml /etc/tailscale2otel/config.yaml
USER nonroot:nonroot
# Streaming (HEC) and webhook receivers, if enabled in config.
EXPOSE 8088 8089
ENTRYPOINT ["/usr/local/bin/tailscale2otel"]
CMD ["-config", "/etc/tailscale2otel/config.yaml"]
