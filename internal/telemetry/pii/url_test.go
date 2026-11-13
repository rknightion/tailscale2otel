package pii

import "testing"

func TestRedactKeyDropsURLWithDisabledIPHost(t *testing.T) {
	for _, tc := range []struct {
		value string
		cat   Category
	}{
		{"http://100.64.0.7:5252", CatTailscaleIPs},
		{"http://[fd7a:115c:a1e0::7]:5252", CatTailscaleIPs},
		{"http://192.0.2.7:5252", CatExternalIPs},
		{"http://10.0.0.7:5252", CatInternalIPs},
	} {
		if _, ok := New(Categories{tc.cat: false}).Merge(map[string]any{"url.full": tc.value})["url.full"]; ok {
			t.Errorf("disabled %s URL kept", tc.cat)
		}
		if _, ok := New(nil).Merge(map[string]any{"url.full": tc.value})["url.full"]; !ok {
			t.Error("default URL dropped")
		}
	}
	for _, value := range []string{"https://example.com/path", "http://10.0.0.7:5252"} {
		if _, ok := New(Categories{CatTailscaleIPs: false}).Merge(map[string]any{"url.full": value})["url.full"]; !ok {
			t.Error("unrelated category dropped URL")
		}
	}
}
