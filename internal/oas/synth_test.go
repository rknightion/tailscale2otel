package oas_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rknightion/tailscale2otel/internal/oas"
)

func TestSynthesizeBody_ObjectWithArray(t *testing.T) {
	s := oas.Schema{Type: "object", Properties: map[string]oas.Schema{
		"devices": {Type: "array", Items: &oas.Schema{Type: "object",
			Properties: map[string]oas.Schema{"nodeId": {Type: "string"}, "n": {Type: "integer"}}}},
	}}
	body := oas.SynthesizeBody(s, 6)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("synth body invalid JSON: %v (%s)", err, body)
	}
	arr, ok := got["devices"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("devices = %v, want 1-element array", got["devices"])
	}
}

// TestSynthesizeBody_DateTime asserts that a string/date-time field synthesizes
// a value that parses as RFC3339.
func TestSynthesizeBody_DateTime(t *testing.T) {
	s := oas.Schema{Type: "object", Properties: map[string]oas.Schema{
		"created": {Type: "string", Format: "date-time"},
	}}
	body := oas.SynthesizeBody(s, 4)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, body)
	}
	v, ok := got["created"].(string)
	if !ok {
		t.Fatalf("created = %T(%v), want string", got["created"], got["created"])
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		t.Fatalf("created %q does not parse as RFC3339: %v", v, err)
	}
}

// TestSynthesizeBody_Primitives checks string/integer/boolean synthesis.
func TestSynthesizeBody_Primitives(t *testing.T) {
	cases := []struct {
		name     string
		schema   oas.Schema
		wantJSON string
	}{
		{"string", oas.Schema{Type: "string"}, `"x"`},
		{"integer", oas.Schema{Type: "integer"}, `1`},
		{"number", oas.Schema{Type: "number"}, `1`},
		{"boolean", oas.Schema{Type: "boolean"}, `false`},
		{"unknown", oas.Schema{Type: ""}, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := oas.SynthesizeBody(tc.schema, 4)
			if string(body) != tc.wantJSON {
				t.Fatalf("got %s, want %s", body, tc.wantJSON)
			}
		})
	}
}

// TestSynthesizeBody_Enum checks that the first enum value is used.
func TestSynthesizeBody_Enum(t *testing.T) {
	s := oas.Schema{Type: "string", Enum: []string{"active", "inactive"}}
	body := oas.SynthesizeBody(s, 4)
	if string(body) != `"active"` {
		t.Fatalf("got %s, want \"active\"", body)
	}
}

// TestSynthesizeBody_EmptyObject checks that an object with nil Properties emits {}.
func TestSynthesizeBody_EmptyObject(t *testing.T) {
	s := oas.Schema{Type: "object"}
	body := oas.SynthesizeBody(s, 4)
	if string(body) != `{}` {
		t.Fatalf("got %s, want {}", body)
	}
}

// TestSynthesizeBody_EmptyArray checks that an array with nil Items emits [].
func TestSynthesizeBody_EmptyArray(t *testing.T) {
	s := oas.Schema{Type: "array"}
	body := oas.SynthesizeBody(s, 4)
	if string(body) != `[]` {
		t.Fatalf("got %s, want []", body)
	}
}

// TestSynthesizeBody_DepthGuard checks that depth exhaustion returns null.
func TestSynthesizeBody_DepthGuard(t *testing.T) {
	// Build a deeply nested object and synth with maxDepth=0 — must return null
	// without panicking.
	s := oas.Schema{Type: "object", Properties: map[string]oas.Schema{
		"child": {Type: "string"},
	}}
	body := oas.SynthesizeBody(s, 0)
	if string(body) != `null` {
		t.Fatalf("got %s, want null at depth 0", body)
	}
}

// TestSynthesizeBody_AlwaysValidJSON checks that every schema variant produces
// valid JSON.
func TestSynthesizeBody_AlwaysValidJSON(t *testing.T) {
	schemas := []oas.Schema{
		{Type: "object"},
		{Type: "array"},
		{Type: "string"},
		{Type: "string", Format: "date-time"},
		{Type: "string", Enum: []string{"a"}},
		{Type: "integer"},
		{Type: "number"},
		{Type: "boolean"},
		{Type: ""},
		{Type: "object", Properties: map[string]oas.Schema{
			"x": {Type: "string"},
			"y": {Type: "integer"},
		}},
		{Type: "array", Items: &oas.Schema{Type: "boolean"}},
	}
	for _, s := range schemas {
		body := oas.SynthesizeBody(s, 6)
		if !json.Valid(body) {
			t.Errorf("schema %+v produced invalid JSON: %s", s, body)
		}
	}
}
