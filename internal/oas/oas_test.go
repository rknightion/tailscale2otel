package oas_test

import (
	"os"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/oas"
)

const refSpec = `{
  "paths": {"/d": {"get": {"operationId": "listTailnetDevices",
    "responses": {"200": {"content": {"application/json": {"schema": {
      "type": "object",
      "properties": {"devices": {"type": "array", "items": {"$ref": "#/components/schemas/Device"}}}
    }}}}}}}},
  "components": {"schemas": {"Device": {"type": "object",
    "properties": {"nodeId": {"type": "string"}, "blocksIncomingConnections": {"type": "boolean"}}}}}
}`

func TestParseSpec_ResolvesRefs(t *testing.T) {
	s, err := oas.ParseSpec([]byte(refSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op, ok := s.Ops["listTailnetDevices"]
	if !ok {
		t.Fatal("op missing")
	}
	item := op.Response.Properties["devices"].Items
	if item == nil || item.Type != "object" {
		t.Fatalf("devices items not resolved: %+v", item)
	}
	if item.Properties["nodeId"].Type != "string" {
		t.Fatalf("nodeId not resolved: %+v", item.Properties["nodeId"])
	}
}

func TestParseSpec_RealVendoredSpec(t *testing.T) {
	b, err := os.ReadFile("../../spec/tailscale-api.json")
	if err != nil {
		t.Fatalf("read vendored spec: %v", err)
	}
	s, err := oas.ParseSpec(b)
	if err != nil {
		t.Fatalf("ParseSpec(vendored): %v", err)
	}
	if _, ok := s.Ops["listTailnetDevices"]; !ok {
		t.Fatal("vendored spec missing listTailnetDevices")
	}
}

// TestParseSpec_CycleSafe proves that a schema that $ref-references itself
// (a classic self-referential cycle) parses without hanging or stack-overflowing.
const cyclicSpec = `{
  "paths": {"/tree": {"get": {"operationId": "getTree",
    "responses": {"200": {"content": {"application/json": {"schema": {
      "$ref": "#/components/schemas/TreeNode"
    }}}}}}}},
  "components": {"schemas": {"TreeNode": {
    "type": "object",
    "properties": {
      "value": {"type": "string"},
      "children": {"type": "array", "items": {"$ref": "#/components/schemas/TreeNode"}}
    }
  }}}
}`

func TestParseSpec_CycleSafe(t *testing.T) {
	s, err := oas.ParseSpec([]byte(cyclicSpec))
	if err != nil {
		t.Fatalf("ParseSpec cyclic: %v", err)
	}
	op, ok := s.Ops["getTree"]
	if !ok {
		t.Fatal("op missing")
	}
	// Root should be an object with a "value" property.
	if op.Response.Type != "object" {
		t.Fatalf("root type = %q, want object", op.Response.Type)
	}
	if op.Response.Properties["value"].Type != "string" {
		t.Fatalf("value property: %+v", op.Response.Properties["value"])
	}
	// children is an array; at some depth the Items will be nil (cycle capped).
	children := op.Response.Properties["children"]
	if children.Type != "array" {
		t.Fatalf("children type = %q, want array", children.Type)
	}
	// The test just verifies we terminated without panic/hang — no assertion on Items depth.
}

// TestParseSpec_RequestRequired verifies RequestRequired is populated from
// requestBody.content["application/json"].schema.required.
const reqBodySpec = `{
  "paths": {"/things": {"get": {"operationId": "createThing",
    "requestBody": {"content": {"application/json": {"schema": {
      "type": "object",
      "required": ["name", "type"],
      "properties": {"name": {"type": "string"}, "type": {"type": "string"}}
    }}}},
    "responses": {"200": {"content": {"application/json": {"schema": {"type": "object"}}}}}
  }}}
}`

func TestParseSpec_RequestRequired(t *testing.T) {
	s, err := oas.ParseSpec([]byte(reqBodySpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op, ok := s.Ops["createThing"]
	if !ok {
		t.Fatal("op missing")
	}
	if len(op.RequestRequired) != 2 {
		t.Fatalf("RequestRequired = %v, want [name type]", op.RequestRequired)
	}
}

// TestParseSpec_Enum verifies enum values are captured.
const enumSpec = `{
  "paths": {"/status": {"get": {"operationId": "getStatus",
    "responses": {"200": {"content": {"application/json": {"schema": {
      "type": "object",
      "properties": {"state": {"type": "string", "enum": ["active", "inactive", "pending"]}}
    }}}}}}}},
  "components": {"schemas": {}}
}`

func TestParseSpec_Enum(t *testing.T) {
	s, err := oas.ParseSpec([]byte(enumSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op := s.Ops["getStatus"]
	state := op.Response.Properties["state"]
	if len(state.Enum) != 3 {
		t.Fatalf("Enum = %v, want 3 values", state.Enum)
	}
	if state.Enum[0] != "active" {
		t.Fatalf("Enum[0] = %q, want active", state.Enum[0])
	}
}

// TestParseSpec_Format verifies the Format field is captured.
const formatSpec = `{
  "paths": {"/ts": {"get": {"operationId": "getTimestamp",
    "responses": {"200": {"content": {"application/json": {"schema": {
      "type": "object",
      "properties": {"created": {"type": "string", "format": "date-time"}}
    }}}}}}}},
  "components": {"schemas": {}}
}`

func TestParseSpec_Format(t *testing.T) {
	s, err := oas.ParseSpec([]byte(formatSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op := s.Ops["getTimestamp"]
	created := op.Response.Properties["created"]
	if created.Format != "date-time" {
		t.Fatalf("Format = %q, want date-time", created.Format)
	}
}

// TestParseSpec_SkipsNon200 verifies we only capture the 200 response.
const multiStatusSpec = `{
  "paths": {"/x": {"get": {"operationId": "getX",
    "responses": {
      "200": {"content": {"application/json": {"schema": {"type": "object"}}}},
      "404": {"content": {"application/json": {"schema": {"type": "string"}}}}
    }
  }}}
}`

func TestParseSpec_SkipsNon200(t *testing.T) {
	s, err := oas.ParseSpec([]byte(multiStatusSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op := s.Ops["getX"]
	if op.Response.Type != "object" {
		t.Fatalf("Response.Type = %q, expected object from 200", op.Response.Type)
	}
}

// TestParseSpec_SkipsNonJSONContent ensures non-JSON content types are skipped.
const nonJSONSpec = `{
  "paths": {"/dl": {"get": {"operationId": "downloadFile",
    "responses": {"200": {"content": {
      "text/plain": {"schema": {"type": "string"}},
      "application/json": {"schema": {"type": "object", "properties": {"url": {"type": "string"}}}}
    }}}}}},
  "components": {"schemas": {}}
}`

func TestParseSpec_SkipsNonJSONContent(t *testing.T) {
	s, err := oas.ParseSpec([]byte(nonJSONSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op := s.Ops["downloadFile"]
	if _, ok := op.Response.Properties["url"]; !ok {
		t.Fatal("expected url property from application/json schema")
	}
}

// TestParseSpec_NullableField verifies the Nullable field is parsed.
const nullableSpec = `{
  "paths": {"/n": {"get": {"operationId": "getNullable",
    "responses": {"200": {"content": {"application/json": {"schema": {
      "type": "object",
      "properties": {
        "a": {"type": "string", "nullable": true},
        "b": {"type": "string"}
      }
    }}}}}}}},
  "components": {"schemas": {}}
}`

func TestParseSpec_NullableField(t *testing.T) {
	s, err := oas.ParseSpec([]byte(nullableSpec))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	op := s.Ops["getNullable"]
	if !op.Response.Properties["a"].Nullable {
		t.Fatal("expected a to be nullable")
	}
	if op.Response.Properties["b"].Nullable {
		t.Fatal("expected b to not be nullable")
	}
}

// TestParseSpec_DepthCap verifies the depth cap (12) prevents infinite recursion
// on a deeply nested (but non-cyclic) structure.
func TestParseSpec_DepthCap(t *testing.T) {
	// Build a spec with 20 levels of nesting — should parse without panic.
	schema := `{"type": "string"}`
	for i := 0; i < 20; i++ {
		schema = `{"type": "object", "properties": {"child": ` + schema + `}}`
	}
	spec := `{"paths":{"/deep":{"get":{"operationId":"deepOp","responses":{"200":{"content":{"application/json":{"schema":` + schema + `}}}}}}},"components":{"schemas":{}}}`
	if _, err := oas.ParseSpec([]byte(spec)); err != nil {
		t.Fatalf("ParseSpec deep: %v", err)
	}
}
