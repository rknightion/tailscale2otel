// Separate CI-only module so it never affects the main module's `go build ./...`.
// It validates config files (config.example.yaml and the Helm-rendered configmap)
// by invoking the real internal/config.Load, which enforces the cross-field rules
// that JSON Schema draft-07 cannot express.
module github.com/rknightion/tailscale2otel/v2/tools/configcheck

go 1.26.5

require github.com/rknightion/tailscale2otel/v2 v2.0.0

require (
	github.com/fatih/structs v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/knadh/koanf/maps v0.1.2 // indirect
	github.com/knadh/koanf/parsers/yaml v1.1.0 // indirect
	github.com/knadh/koanf/providers/env/v2 v2.0.0 // indirect
	github.com/knadh/koanf/providers/file v1.2.1 // indirect
	github.com/knadh/koanf/providers/structs v1.0.0 // indirect
	github.com/knadh/koanf/v2 v2.3.5 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.47.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/rknightion/tailscale2otel/v2 => ../..
