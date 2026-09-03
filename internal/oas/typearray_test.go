package oas_test

import (
	"testing"

	"github.com/rknightion/tailscale2otel/v5/internal/oas"
)

// Tailscale's spec is OpenAPI **3.1.0**, where a nullable field is spelled as a
// type ARRAY — `"type": ["boolean","null"]` — and the 3.0 `nullable: true`
// keyword does not appear at all (0 occurrences in the vendored spec; 16 type
// arrays do appear, every one of them inside a schema reached by a consumed GET).
//
// Parsing a type array into a plain string silently fails, leaving Type == "".
// That is not merely cosmetic: Classify's type-change check is guarded on both
// sides being non-empty, so with Type == "" on both the OLD and NEW spec a field
// flipping from `["boolean","null"]` to `["string","null"]` upstream is
// **undetectable** — the exact drift this tool exists to catch (#431).

const typeArraySpec = `{
  "openapi": "3.1.0",
  "paths": {
    "/tailnet/{tailnet}/devices": {
      "get": {
        "operationId": "listTailnetDevices",
        "responses": {"200": {"content": {"application/json": {"schema": {
          "$ref": "#/components/schemas/DeviceList"
        }}}}}
      }
    }
  },
  "components": {"schemas": {
    "DeviceList": {
      "type": "object",
      "properties": {
        "devices": {"type": "array", "items": {"$ref": "#/components/schemas/Device"}}
      }
    },
    "Device": {
      "type": "object",
      "properties": {
        "hostname":   {"type": "string"},
        "hairPinning": {"type": ["boolean", "null"]},
        "latency":    {"type": ["number", "null"]},
        "nullFirst":  {"type": ["null", "string"]},
        "splitDNS":   {"type": ["object", "null"], "additionalProperties": {"type": "array", "items": {"type": "string"}}}
      }
    }
  }}
}`

func deviceProps(t *testing.T, spec *oas.Spec) map[string]oas.Schema {
	t.Helper()
	op, ok := spec.Ops["listTailnetDevices"]
	if !ok {
		t.Fatal("listTailnetDevices was not parsed")
	}
	devices, ok := op.Response.Properties["devices"]
	if !ok {
		t.Fatal("response has no devices property")
	}
	if devices.Items == nil {
		t.Fatal("devices array has no items schema")
	}
	if len(devices.Items.Properties) == 0 {
		t.Fatal("the Device schema resolved with no properties")
	}
	return devices.Items.Properties
}

// A 3.1 type array must yield the concrete type plus Nullable, exactly as the 3.0
// `nullable: true` spelling would.
func TestParseSpec_TypeArrayYieldsConcreteTypeAndNullable(t *testing.T) {
	spec, err := oas.ParseSpec([]byte(typeArraySpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	props := deviceProps(t, spec)

	for _, tc := range []struct {
		field    string
		wantType string
	}{
		{"hostname", "string"}, // plain string form still works
		{"hairPinning", "boolean"},
		{"latency", "number"},
		{"nullFirst", "string"}, // "null" first must not win
		{"splitDNS", "object"},
	} {
		got, ok := props[tc.field]
		if !ok {
			t.Errorf("field %q missing from the resolved Device schema", tc.field)
			continue
		}
		if got.Type != tc.wantType {
			t.Errorf("%s.Type = %q, want %q — a 3.1 type array parsed into a string leaves this "+
				"empty, and Classify then cannot see a type change on this field at all",
				tc.field, got.Type, tc.wantType)
		}
		wantNullable := tc.field != "hostname"
		if got.Nullable != wantNullable {
			t.Errorf("%s.Nullable = %v, want %v", tc.field, got.Nullable, wantNullable)
		}
	}
}

// The consequence, asserted through Classify rather than through the parser: a
// nullable field changing its concrete type upstream must be reported. This is the
// test that would have stayed green while the drift went unnoticed.
func TestClassify_DetectsTypeChangeInsideATypeArray(t *testing.T) {
	oldSpec, err := oas.ParseSpec([]byte(typeArraySpec))
	if err != nil {
		t.Fatal(err)
	}
	// hairPinning flips from boolean to string — a decoder-breaking change.
	changed := []byte(replaceOnce(typeArraySpec,
		`"hairPinning": {"type": ["boolean", "null"]}`,
		`"hairPinning": {"type": ["string", "null"]}`))
	newSpec, err := oas.ParseSpec(changed)
	if err != nil {
		t.Fatal(err)
	}

	changes := oas.Classify(oldSpec, newSpec, []string{"listTailnetDevices"})
	for _, c := range changes {
		if c.Kind == oas.TypeChanged {
			return
		}
	}
	t.Errorf("Classify reported no TypeChanged for hairPinning boolean→string. Got %d "+
		"change(s): %+v. With type arrays unparsed both sides read as the empty type, and the "+
		"type-change check is guarded on both being non-empty, so the drift is invisible.",
		len(changes), changes)
}

// A typed map (`additionalProperties` holding a schema) is how the spec models
// SplitDns, DnsConfiguration.splitDNS and DevicePostureAttributes.attributes — 11
// occurrences, several reached by consumed GETs. Leaving it unmodeled makes those
// nodes indistinguishable from an object with no properties at all, so neither a
// value-type change nor the map disappearing can be told apart from noise.
func TestParseSpec_TypedMapValueSchemaIsModeled(t *testing.T) {
	spec, err := oas.ParseSpec([]byte(typeArraySpec))
	if err != nil {
		t.Fatal(err)
	}
	props := deviceProps(t, spec)

	split, ok := props["splitDNS"]
	if !ok {
		t.Fatal("splitDNS missing from the resolved Device schema")
	}
	if split.AdditionalProperties == nil {
		t.Fatal("splitDNS.AdditionalProperties is nil: the typed-map value schema was dropped, so " +
			"an object-with-no-properties and a map-of-string-arrays look identical")
	}
	if got := split.AdditionalProperties.Type; got != "array" {
		t.Errorf("splitDNS value type = %q, want array", got)
	}
	if split.AdditionalProperties.Items == nil || split.AdditionalProperties.Items.Type != "string" {
		t.Errorf("splitDNS value items = %+v, want a string array", split.AdditionalProperties.Items)
	}
}

func replaceOnce(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		panic("fixture no longer contains " + old)
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
