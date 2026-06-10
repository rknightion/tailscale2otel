// Package oas provides a minimal stdlib-only OpenAPI 3.x spec parser with
// $ref resolution, bounded to the subset needed for drift detection and
// schema-driven fuzz testing.
//
// Only GET operations are modeled. $ref resolution is cycle-safe via a visited
// set and a hard depth cap (maxRefDepth).
package oas

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxRefDepth is the maximum $ref-follow depth before we stop descending and
// leave sub-schema Properties/Items nil. This breaks self-referential cycles
// and prevents unbounded recursion on legitimately deep specs.
const maxRefDepth = 12

// Spec is the minimal parsed OpenAPI document: GET operations keyed by
// operationId, with $ref already resolved against components.schemas.
type Spec struct {
	Ops        map[string]Operation // key = operationId
	components map[string]rawSchema // unexported; raw components.schemas for resolution
}

// Operation is a parsed GET operation from the OpenAPI spec.
type Operation struct {
	ID              string
	Method          string   // always "get"
	RequestRequired []string // required fields from requestBody.content["application/json"].schema.required
	Response        Schema   // 200 application/json schema, $ref-resolved
}

// Schema is a resolved subset of JSON Schema. Refs are followed at parse time.
// Fields not present in the source document are left as zero values.
//
// Composition keywords (anyOf/oneOf/allOf) and additionalProperties are
// intentionally NOT modeled: a node using only those resolves to Type "object"
// with a nil Properties (or to the empty Schema for a bare anyOf). This is by
// design — drift on those loosely-typed fields (e.g. audit old/new, posture
// attribute maps) does not break our decoders, which read them as
// json.RawMessage / map[string]any. Synthesized fuzz bodies therefore cannot
// vary those shapes; cover them with hand-written payloads instead. An empty
// Properties on an object is not a parser bug.
type Schema struct {
	Type       string            // "object","array","string","integer","number","boolean",""
	Format     string            // e.g. "date-time" — drives valid-value synthesis
	Properties map[string]Schema // non-nil for objects with properties
	Items      *Schema           // non-nil for arrays
	Enum       []string          // string enum values, if any
	Nullable   bool
}

// rawSchema is the raw JSON representation of an OpenAPI schema node,
// used for lazy $ref resolution without full pre-parsing.
type rawSchema map[string]json.RawMessage

// ParseSpec parses an OpenAPI JSON document and returns a Spec with all GET
// operations indexed by operationId. $refs of the form
// #/components/schemas/<Name> are resolved recursively. ParseSpec is
// cycle-safe: a visited set and a depth cap of 12 bound recursion.
func ParseSpec(jsonBytes []byte) (*Spec, error) {
	// Decode the top-level document into a generic map.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return nil, fmt.Errorf("oas: unmarshal document: %w", err)
	}

	// Extract components.schemas into a map of raw schema nodes.
	components, err := parseComponents(doc)
	if err != nil {
		return nil, err
	}

	spec := &Spec{
		Ops:        make(map[string]Operation),
		components: components,
	}

	// Extract paths.
	rawPaths, ok := doc["paths"]
	if !ok {
		return spec, nil
	}
	var paths map[string]json.RawMessage
	if err := json.Unmarshal(rawPaths, &paths); err != nil {
		return nil, fmt.Errorf("oas: unmarshal paths: %w", err)
	}

	for _, pathItemRaw := range paths {
		var pathItem map[string]json.RawMessage
		if err := json.Unmarshal(pathItemRaw, &pathItem); err != nil {
			continue
		}

		opRaw, hasGet := pathItem["get"]
		if !hasGet {
			continue
		}

		op, err := parseOperation(opRaw, spec.components)
		if err != nil || op == nil {
			continue
		}
		spec.Ops[op.ID] = *op
	}

	return spec, nil
}

// parseComponents extracts components.schemas from the document.
func parseComponents(doc map[string]json.RawMessage) (map[string]rawSchema, error) {
	result := make(map[string]rawSchema)

	rawComponents, ok := doc["components"]
	if !ok {
		return result, nil
	}

	var components map[string]json.RawMessage
	if err := json.Unmarshal(rawComponents, &components); err != nil {
		return result, fmt.Errorf("oas: unmarshal components: %w", err)
	}

	rawSchemas, ok := components["schemas"]
	if !ok {
		return result, nil
	}

	var schemas map[string]json.RawMessage
	if err := json.Unmarshal(rawSchemas, &schemas); err != nil {
		return result, fmt.Errorf("oas: unmarshal components.schemas: %w", err)
	}

	for name, schemaRaw := range schemas {
		var rs rawSchema
		if err := json.Unmarshal(schemaRaw, &rs); err != nil {
			continue
		}
		result[name] = rs
	}

	return result, nil
}

// parseOperation parses a single GET operation JSON blob into an Operation.
// Returns nil, nil if the operation has no operationId (skip it).
func parseOperation(opRaw json.RawMessage, components map[string]rawSchema) (*Operation, error) {
	var opMap map[string]json.RawMessage
	if err := json.Unmarshal(opRaw, &opMap); err != nil {
		return nil, fmt.Errorf("oas: unmarshal operation: %w", err)
	}

	rawID, ok := opMap["operationId"]
	if !ok {
		return nil, nil //nolint:nilnil // intentional: no operationId means skip
	}
	var opID string
	if err := json.Unmarshal(rawID, &opID); err != nil || opID == "" {
		return nil, nil //nolint:nilnil
	}

	op := &Operation{
		ID:     opID,
		Method: "get",
	}

	// Parse 200 application/json response schema.
	op.Response = parseResponseSchema(opMap, components)

	// Parse requestBody.content["application/json"].schema.required.
	op.RequestRequired = parseRequestRequired(opMap, components)

	return op, nil
}

// parseResponseSchema extracts and resolves the 200 application/json schema.
func parseResponseSchema(opMap map[string]json.RawMessage, components map[string]rawSchema) Schema {
	rawResponses, ok := opMap["responses"]
	if !ok {
		return Schema{}
	}
	var responses map[string]json.RawMessage
	if err := json.Unmarshal(rawResponses, &responses); err != nil {
		return Schema{}
	}

	raw200, ok := responses["200"]
	if !ok {
		return Schema{}
	}
	var resp200 map[string]json.RawMessage
	if err := json.Unmarshal(raw200, &resp200); err != nil {
		return Schema{}
	}

	return extractJSONSchema(resp200, components)
}

// extractJSONSchema pulls application/json schema from a response or
// requestBody content map and resolves it.
func extractJSONSchema(contentHolder map[string]json.RawMessage, components map[string]rawSchema) Schema {
	rawContent, ok := contentHolder["content"]
	if !ok {
		return Schema{}
	}
	var content map[string]json.RawMessage
	if err := json.Unmarshal(rawContent, &content); err != nil {
		return Schema{}
	}

	rawMediaType, ok := content["application/json"]
	if !ok {
		return Schema{}
	}
	var mediaType map[string]json.RawMessage
	if err := json.Unmarshal(rawMediaType, &mediaType); err != nil {
		return Schema{}
	}

	rawSchemaBytes, ok := mediaType["schema"]
	if !ok {
		return Schema{}
	}

	var schemaMap rawSchema
	if err := json.Unmarshal(rawSchemaBytes, &schemaMap); err != nil {
		return Schema{}
	}

	return resolveSchema(schemaMap, components, make(map[string]bool), 0)
}

// parseRequestRequired extracts required fields from
// requestBody.content["application/json"].schema.required.
func parseRequestRequired(opMap map[string]json.RawMessage, components map[string]rawSchema) []string {
	rawRB, ok := opMap["requestBody"]
	if !ok {
		return nil
	}
	var rb map[string]json.RawMessage
	if err := json.Unmarshal(rawRB, &rb); err != nil {
		return nil
	}

	rawContent, ok := rb["content"]
	if !ok {
		return nil
	}
	var content map[string]json.RawMessage
	if err := json.Unmarshal(rawContent, &content); err != nil {
		return nil
	}

	rawMT, ok := content["application/json"]
	if !ok {
		return nil
	}
	var mt map[string]json.RawMessage
	if err := json.Unmarshal(rawMT, &mt); err != nil {
		return nil
	}

	rawSch, ok := mt["schema"]
	if !ok {
		return nil
	}
	var schMap rawSchema
	if err := json.Unmarshal(rawSch, &schMap); err != nil {
		return nil
	}

	// Optionally follow a top-level $ref before reading required.
	schMap = followRef(schMap, components, make(map[string]bool), 0)

	rawRequired, ok := schMap["required"]
	if !ok {
		return nil
	}
	var required []string
	if err := json.Unmarshal(rawRequired, &required); err != nil {
		return nil
	}
	return required
}

// followRef follows a $ref in a rawSchema (if present) and returns the
// resolved rawSchema. Returns the original if no $ref or if resolution fails.
// visited and depth are used for cycle/depth tracking.
func followRef(rs rawSchema, components map[string]rawSchema, visited map[string]bool, depth int) rawSchema {
	rawRef, ok := rs["$ref"]
	if !ok {
		return rs
	}
	var ref string
	if err := json.Unmarshal(rawRef, &ref); err != nil {
		return rs
	}
	name, ok := refName(ref)
	if !ok {
		return rs
	}
	if visited[name] || depth >= maxRefDepth {
		return nil
	}
	target, ok := components[name]
	if !ok {
		return rs
	}
	return target
}

// resolveSchema converts a rawSchema into a Schema, following $refs
// and descending into properties and items. visited and depth guard
// against cycles and unbounded recursion.
func resolveSchema(rs rawSchema, components map[string]rawSchema, visited map[string]bool, depth int) Schema {
	if depth >= maxRefDepth {
		return Schema{}
	}

	// Follow $ref if present.
	if rawRef, ok := rs["$ref"]; ok {
		var ref string
		if err := json.Unmarshal(rawRef, &ref); err != nil {
			return Schema{}
		}
		name, ok := refName(ref)
		if !ok {
			return Schema{}
		}
		if visited[name] {
			// Cycle detected — stop descending.
			return Schema{}
		}
		target, ok := components[name]
		if !ok {
			return Schema{}
		}
		// Mark visited for cycle detection, then descend.
		newVisited := cloneVisited(visited)
		newVisited[name] = true
		return resolveSchema(target, components, newVisited, depth+1)
	}

	var s Schema

	// type
	if rawType, ok := rs["type"]; ok {
		_ = json.Unmarshal(rawType, &s.Type)
	}

	// format
	if rawFmt, ok := rs["format"]; ok {
		_ = json.Unmarshal(rawFmt, &s.Format)
	}

	// nullable
	if rawNullable, ok := rs["nullable"]; ok {
		_ = json.Unmarshal(rawNullable, &s.Nullable)
	}

	// enum — only string values
	if rawEnum, ok := rs["enum"]; ok {
		var enumVals []json.RawMessage
		if err := json.Unmarshal(rawEnum, &enumVals); err == nil {
			for _, ev := range enumVals {
				var sv string
				if err := json.Unmarshal(ev, &sv); err == nil {
					s.Enum = append(s.Enum, sv)
				}
			}
		}
	}

	// properties (object)
	if rawProps, ok := rs["properties"]; ok {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(rawProps, &props); err == nil && len(props) > 0 {
			s.Properties = make(map[string]Schema, len(props))
			for propName, propRaw := range props {
				var propRS rawSchema
				if err := json.Unmarshal(propRaw, &propRS); err == nil {
					s.Properties[propName] = resolveSchema(propRS, components, visited, depth+1)
				}
			}
		}
	}

	// items (array)
	if rawItems, ok := rs["items"]; ok {
		var itemRS rawSchema
		if err := json.Unmarshal(rawItems, &itemRS); err == nil {
			resolved := resolveSchema(itemRS, components, visited, depth+1)
			s.Items = &resolved
		}
	}

	return s
}

// refName extracts the schema name from a $ref of the form
// #/components/schemas/<Name>. Returns "", false for other forms.
func refName(ref string) (string, bool) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	name := ref[len(prefix):]
	if name == "" {
		return "", false
	}
	return name, true
}

// cloneVisited returns a shallow copy of visited so child branches don't
// pollute sibling branches (sibling properties can legitimately reuse the
// same $ref; only same-path re-entry is a cycle).
func cloneVisited(visited map[string]bool) map[string]bool {
	clone := make(map[string]bool, len(visited)+1)
	for k, v := range visited {
		clone[k] = v
	}
	return clone
}
