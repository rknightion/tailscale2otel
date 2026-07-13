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
