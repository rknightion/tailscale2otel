package telemetry

import "testing"

// The module import paths in this package intentionally include /v5 and must
// follow a future major bump; they are compile-time references. scopeName is
// the only runtime identifier shaped like the repository path here. Product
// metric names, resource identity keys, and semantic-convention keys are
// independent telemetry contracts and are not applicable to module-path
// changes.
func TestInstrumentationScopeNameStable(t *testing.T) {
	const want = "github.com/rknightion/tailscale2otel"
	if scopeName != want {
		t.Fatalf("instrumentation scope changed from %q to %q; operator queries depend on this value remaining stable across module major versions", want, scopeName)
	}
}
