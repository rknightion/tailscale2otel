package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestPropertyTaxonomyCoversVendoredSchema(t *testing.T) {
	properties := vendoredAuditTargetProperties(t)
	for _, property := range properties {
		if _, categorized := propertyCategories[property]; categorized {
			continue
		}
		if rationale := propertyExclusions[property]; rationale != "" {
			continue
		}
		t.Errorf("vendored ConfigurationAuditLog.target.property %q lacks a category or explicit exclusion rationale", property)
	}
	for property := range propertyCategories {
		if !contains(properties, property) {
			t.Errorf("categorized property %q is absent from the vendored schema", property)
		}
	}
	for property := range propertyExclusions {
		if !contains(properties, property) {
			t.Errorf("excluded property %q is absent from the vendored schema", property)
		}
	}
}

func TestPropertyTaxonomyCategories(t *testing.T) {
	for property, want := range map[string]string{
		"NETWORK_FLOW_LOGGING":    "network_flow_logging",
		"LOG_EXIT_FLOWS":          "exit_flow_logging",
		"MACHINE_APPROVAL_NEEDED": "machine_approval",
		"USER_APPROVAL_REQUIRED":  "user_approval",
		"MAX_KEY_DURATION":        "key_duration",
		"FILE_SHARING":            "file_sharing",
		"HTTPS":                   "https",
		"SCIM":                    "scim",
		"SUBSCRIBED_EVENTS":       "webhook_subscription",
		"SECURITY_EMAIL":          "security_contact",
	} {
		if got := propertyCategories[property]; got != want {
			t.Errorf("category for %q = %q, want %q", property, got, want)
		}
	}
}

func vendoredAuditTargetProperties(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "spec", "tailscale-api.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vendored schema %q: %v", path, err)
	}

	var schema struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Properties map[string]struct {
						Enum []string `json:"enum"`
					} `json:"properties"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatalf("decode vendored schema: %v", err)
	}
	properties := schema.Components.Schemas["ConfigurationAuditLog"].Properties["target"].Properties["property"].Enum
	if len(properties) == 0 {
		t.Fatal("vendored ConfigurationAuditLog.target.property enum is empty or missing")
	}
	sort.Strings(properties)
	return properties
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
