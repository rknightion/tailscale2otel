// Separate CI-only module so it never affects the main module's `go build ./...`.
// It validates config files (config.example.yaml and the Helm-rendered configmap)
// by invoking the real internal/config.Load, which enforces the cross-field rules
// that JSON Schema draft-07 cannot express.
module github.com/rknightion/tailscale2otel/tools/configcheck

go 1.26.4

require github.com/rknightion/tailscale2otel v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

replace github.com/rknightion/tailscale2otel => ../..
