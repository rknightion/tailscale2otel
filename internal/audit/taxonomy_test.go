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
		if _, liveOnly := liveAuditTargetProperties[property]; !contains(properties, property) && !liveOnly {
			t.Errorf("categorized property %q is absent from both the vendored schema and live vocabulary", property)
		}
	}
	for property := range propertyExclusions {
		if !contains(properties, property) {
			t.Errorf("excluded property %q is absent from the vendored schema", property)
		}
	}
}

// TestLivePropertiesAbsentFromVendoredSchemaAreTaxonomized guards properties
// observed from live audit records which the Tailscale OpenAPI schema cannot
// describe. Without it, additions such as Border0's PAM provisioning toggle
// would silently bypass the taxonomy guard above.
func TestLivePropertiesAbsentFromVendoredSchemaAreTaxonomized(t *testing.T) {
	properties := vendoredAuditTargetProperties(t)
	for property := range liveAuditTargetProperties {
		if contains(properties, property) {
			continue
		}
		if !knownProperty[property] {
			t.Errorf("live ConfigurationAuditLog.target.property %q, absent from the vendored schema, lacks a category or explicit exclusion rationale", property)
		}
	}
}

func TestAuditVocabularyCoversVendoredSchema(t *testing.T) {
	vocabulary := vendoredAuditEnums(t)
	for field, values := range map[string][]string{
		"action":     vocabulary.Action,
		"origin":     vocabulary.Origin,
		"actor_type": vocabulary.ActorType,
	} {
		known := map[string]bool{}
		switch field {
		case "action":
			known = knownActions
		case "origin":
			known = knownOrigins
		case "actor_type":
			known = knownActorTypes
		}
		for _, value := range values {
			if !known[value] {
				t.Errorf("vendored ConfigurationAuditLog.%s enum %q lacks a classification", field, value)
			}
		}
	}
	for _, property := range vocabulary.Property {
		if !knownProperty[property] {
			t.Errorf("vendored ConfigurationAuditLog.target.property enum %q lacks a classification", property)
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
	return vendoredAuditEnums(t).Property
}

type auditEnums struct {
	Action    []string
	Origin    []string
	ActorType []string
	Property  []string
}

func vendoredAuditEnums(t *testing.T) auditEnums {
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
					Enum       []string `json:"enum"`
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
	properties := schema.Components.Schemas["ConfigurationAuditLog"].Properties
	values := auditEnums{
		Action:    properties["action"].Enum,
		Origin:    properties["origin"].Enum,
		ActorType: properties["actor"].Properties["type"].Enum,
		Property:  properties["target"].Properties["property"].Enum,
	}
	for field, enum := range map[string][]string{
		"action": values.Action, "origin": values.Origin,
		"actor.type": values.ActorType, "target.property": values.Property,
	} {
		if len(enum) == 0 {
			t.Fatalf("vendored ConfigurationAuditLog.%s enum is empty or missing", field)
		}
		sort.Strings(enum)
	}
	return values
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
