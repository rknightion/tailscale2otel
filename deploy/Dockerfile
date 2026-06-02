# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/tailscale2otel ./cmd/tailscale2otel

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tailscale2otel /usr/local/bin/tailscale2otel
COPY config.example.yaml /etc/tailscale2otel/config.yaml
USER nonroot:nonroot
# Streaming (HEC) and webhook receivers, if enabled in config.
EXPOSE 8088 8089
ENTRYPOINT ["/usr/local/bin/tailscale2otel"]
CMD ["-config", "/etc/tailscale2otel/config.yaml"]
