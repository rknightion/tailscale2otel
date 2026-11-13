package pii

import "testing"

func TestRedactKeyClassifiesAddressSuffixedNode(t *testing.T) {
	for _, value := range []string{"synthetic-node@100.64.0.7", "synthetic-node:5252@100.64.0.7", "synthetic-node@fd7a:115c:a1e0::7", "synthetic-node@[fd7a:115c:a1e0::7]:5252"} {
		for _, cat := range []Category{CatTailscaleIPs, CatHostnames} {
			r := New(Categories{cat: false})
			attrs := map[string]any{"tailscale.node": value}
			if _, ok := r.Merge(attrs)["tailscale.node"]; ok {
				t.Errorf("disabled %s combined identity kept", cat)
			}
			if kept, suppress := r.Identity(attrs); !suppress || len(kept) != 0 {
				t.Errorf("disabled %s identity=%v suppress=%v", cat, kept, suppress)
			}
			attrs["host.id"] = "synthetic-id"
			kept, suppress := r.Identity(attrs)
			if suppress || kept["host.id"] != "synthetic-id" {
				t.Error("remaining enabled identity must survive")
			}
			if _, ok := kept["tailscale.node"]; ok {
				t.Error("combined identity must still be removed")
			}
		}
		if got := New(allOn()).Merge(map[string]any{"tailscale.node": value})["tailscale.node"]; got != value {
			t.Error("enabled combined identity changed")
		}
	}
	for _, value := range []string{"synthetic@not-an-address", "unverified:synthetic-node", "synthetic-node"} {
		if got := New(Categories{CatTailscaleIPs: false}).Merge(map[string]any{"tailscale.node": value})["tailscale.node"]; got != value {
			t.Error("non-address suffix lost hostname fallback")
		}
		if _, ok := New(Categories{CatHostnames: false}).Merge(map[string]any{"tailscale.node": value})["tailscale.node"]; ok {
			t.Error("hostname fallback bypassed")
		}
	}
}
