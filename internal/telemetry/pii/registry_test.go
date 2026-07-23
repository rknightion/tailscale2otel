package pii

import "testing"

func TestRegistryCoversKnownKeys(t *testing.T) {
	keyCat := map[string]Category{
		"tailscale.user":         CatEmails,
		"user.name":              CatEmails,
		"user.full_name":         CatUserDisplayNames,
		"user.id":                CatUserIDs,
		"host.name":              CatHostnames,
		"host.id":                CatNodeIDs,
		"tailscale.service.name": CatServiceAddrs,
		"endpoint":               CatEndpointPaths,
		"tailscale.route.cidr":   CatNetworkTopology,
		"tailscale.tailnet":      CatTailnetName,
		"tailscale.audit.old":    CatFreeTextDetails,
	}
	for k, want := range keyCat {
		got, ok := keyCategory[k]
		if !ok {
			t.Errorf("key %q missing from keyCategory", k)
			continue
		}
		if got != want {
			t.Errorf("key %q = %v, want %v", k, got, want)
		}
	}
	for _, k := range []string{"source.address", "destination.address", "tailscale.dns.resolver.address", "tailscale.src.node", "tailscale.dst.node"} {
		if !ipValueKeys[k] {
			t.Errorf("key %q should be IP-value-classified", k)
		}
	}
	for _, k := range []string{"network.io.direction", "tailscale.key.type", "http.response.status_code", "category"} {
		if !nonIdentifier[k] {
			t.Errorf("key %q should be in nonIdentifier allowlist", k)
		}
	}
	// No key may be double-classified.
	for k := range keyCategory {
		if ipValueKeys[k] || nonIdentifier[k] {
			t.Errorf("key %q is double-classified", k)
		}
	}
	for k := range ipValueKeys {
		if nonIdentifier[k] {
			t.Errorf("key %q is double-classified (ip + nonident)", k)
		}
	}
}

// TestSpanAttributeKeysAreClassified pins the classification of every attribute
// key emitted onto a SPAN (#212). Span attributes are not declared in
// internal/catalog, so TestEveryCatalogAttributeIsClassified cannot see them —
// this table is the guard for that surface and must be extended whenever a new
// span.SetAttributes / AddEvent key is added in internal/tsapi, internal/stream,
// internal/webhook or internal/collector.
func TestSpanAttributeKeysAreClassified(t *testing.T) {
	spanKeys := map[string]Category{
		// internal/tsapi/transport.go (observe + the retry event)
		"tailscale.endpoint":           CatEndpointPaths,
		"url.full":                     CatEndpointPaths,
		"http.request.method":          "",
		"server.address":               "",
		"http.request.resend_count":    "",
		"tailscale.rate_limit.wait_ms": "",
		"http.response.status_code":    "",
		"attempt":                      "",
		"sleep_ms":                     "",
		// span.RecordError
		"exception.message":    CatFreeTextDetails,
		"exception.stacktrace": CatFreeTextDetails,
		"exception.type":       "",
		// internal/stream + internal/webhook receiver spans
		"tailscale.stream.flows":   "",
		"tailscale.stream.audits":  "",
		"tailscale.stream.skipped": "",
		"tailscale.webhook.events": "",
		"http.request.body.size":   "",
		// internal/collector/scheduler.go + the const attrs stamped on every span
		"tailscale.collector":     "",
		"tailscale.tailnet":       CatTailnetName,
		"tailscale2otel.provider": "",
	}
	for k, want := range spanKeys {
		if !IsClassified(k) {
			t.Errorf("span attribute %q is not classified in the pii registry", k)
			continue
		}
		got, isPII := keyCategory[k]
		switch {
		case want == "" && isPII:
			t.Errorf("span attribute %q classified as PII category %v, want non-identifier", k, got)
		case want == "" && !nonIdentifier[k]:
			t.Errorf("span attribute %q should be in the nonIdentifier allowlist", k)
		case want != "" && got != want:
			t.Errorf("span attribute %q = %v, want %v", k, got, want)
		}
	}
}
