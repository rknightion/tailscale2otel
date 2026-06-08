package statusdata

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestStatus_ProviderField asserts that the Provider and Capabilities fields
// are present on Status and serialize correctly to JSON.
func TestStatus_ProviderField(t *testing.T) {
	s := Status{
		Provider:     "headscale",
		Capabilities: []string{"devices", "users", "keys", "acl"},
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	out := string(b)

	if !strings.Contains(out, `"provider":"headscale"`) {
		t.Errorf("serialized JSON missing provider field; got %s", out)
	}
	if !strings.Contains(out, `"capabilities"`) {
		t.Errorf("serialized JSON missing capabilities field; got %s", out)
	}
	if !strings.Contains(out, `"devices"`) {
		t.Errorf("serialized JSON missing capabilities value 'devices'; got %s", out)
	}
}

// TestStatus_ProviderOmitEmpty asserts that an empty Capabilities slice omits
// the field from JSON output.
func TestStatus_ProviderOmitEmpty(t *testing.T) {
	s := Status{
		Provider: "tailscale",
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	out := string(b)

	if !strings.Contains(out, `"provider":"tailscale"`) {
		t.Errorf("serialized JSON missing provider field; got %s", out)
	}
	if strings.Contains(out, `"capabilities"`) {
		t.Errorf("serialized JSON should omit empty capabilities field; got %s", out)
	}
}
