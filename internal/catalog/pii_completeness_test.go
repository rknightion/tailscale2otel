package catalog_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/catalog"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
)

// TestEveryCatalogAttributeIsClassified asserts that every attribute key declared
// on any catalog metric or log event is classified in internal/telemetry/pii
// (either in the keyCategory registry, the ipValueKeys set, or the nonIdentifier
// allowlist). An unclassified key would silently escape PII redaction.
func TestEveryCatalogAttributeIsClassified(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range catalog.Metrics() {
		for _, a := range m.Attributes {
			seen[a] = true
		}
	}
	for _, l := range catalog.LogEvents() {
		for _, a := range l.Attributes {
			seen[a] = true
		}
	}
	for key := range seen {
		if !pii.IsClassified(key) {
			t.Errorf("attribute %q is not classified in internal/telemetry/pii (add it to the registry or the non-identifier allowlist)", key)
		}
	}
}
