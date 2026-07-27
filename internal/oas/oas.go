// Package oas provides a minimal stdlib-only OpenAPI 3.x spec parser with
// $ref resolution, bounded to the subset needed for drift detection and
// schema-driven fuzz testing.
//
// Only GET operations are modeled WITH SCHEMAS (Spec.Ops) — schemas exist to
// drive decode drift and fuzz, and only GETs have a consumed response body.
// Spec.AllOperations (inventory.go) separately indexes EVERY operation of every
// verb by id/method/path, with no schema, for new-operation detection.
//
// $ref resolution is cycle-safe via a visited set and a hard depth cap
// (maxRefDepth).
//
// Consequence of GET-only, and why it is not fixed by widening Ops: a CONSUMED
// operation that is not a GET gets no drift coverage at all. Widening Ops would
// give every write operation a schema nothing asks for, and the fuzz/decode
// harnesses are built on Ops being the set of things with a consumed response
// body. Instead Classify REPORTS the gap (OperationUnmodeled, Warning) rather
// than skipping it in silence, which is what it did before #432. Today the
// nearest candidate is validateAndTestPolicyFile — a read-only POST
// dispositioned `implement`, not `consumed`.
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

// MaxRefDepth exposes the resolution cap. A document cannot be allowed to choose
// the recursion depth of anything that walks these schemas — flattenPaths,
// SynthesizeBody and BoundaryVariants all do — so the fuzz targets assert against
// this rather than against a number copied into a test.
const MaxRefDepth = maxRefDepth

// Spec is the minimal parsed OpenAPI document: GET operations keyed by
// operationId, with $ref already resolved against components.schemas.
//
// Ops is GET-only by design (see the package doc). For the every-verb operation
// census used by new-operation detection, call AllOperations.
type Spec struct {
	Ops        map[string]Operation    // key = operationId; GET only
	components map[string]rawSchema    // unexported; raw components.schemas for resolution
	parameters map[string]rawSchema    // unexported; raw components.parameters for resolution
	allOps     map[string]OperationRef // unexported; every verb, id/method/path only
}

// Operation is a parsed GET operation from the OpenAPI spec.
//
// Everything below the response body — parameters, success statuses, media types
// — is the request surface added by #432. See surface.go for why each dimension
// is here and what the vendored spec actually contains.
type Operation struct {
	ID     string
	Method string // always "get"
	// Path is the templated path the operation lives at. Carried so a drift
	// report can say WHERE a change is, which is the evidence a maintainer needs
	// to check it upstream.
	Path string
	// Parameters is the merged path-item + operation-level parameter list,
	// $ref-resolved and sorted by (In, Name).
	Parameters []Param
	// RequestRequired is the required fields of
	// requestBody.content["application/json"].schema. Latent while every consumed
	// operation is a GET; see TestVendoredSpec_NoConsumedOperationHasARequestBody.
	RequestRequired []string
	// RequestMediaTypes is the sorted media types the request body accepts.
	RequestMediaTypes []string
	// Response is the selected success response's application/json schema,
	// $ref-resolved.
	Response Schema
	// SuccessStatuses is the sorted 2xx response codes the operation declares.
	SuccessStatuses []string
	// ResponseMediaTypes is the sorted media types of the selected success
	// response.
	ResponseMediaTypes []string
}

// Schema is a resolved subset of JSON Schema. Refs are followed at parse time.
// Fields not present in the source document are left as zero values.
//
// The COMPOSITION keywords anyOf/oneOf/allOf remain intentionally unmodeled: a
// node using only those resolves to Type "object" with a nil Properties (or to the
// empty Schema for a bare anyOf). This is by design — drift on those loosely-typed
// fields does not break our decoders, which read them as json.RawMessage /
// map[string]any. In the vendored spec they are also nearly unreachable: `oneOf`
// appears once (a POST request body) and `allOf` once (VIPServiceInfoPut,
// referenced only from a PUT request body), and ParseSpec keeps GET operations
// only, so neither is reachable at all. `anyOf` appears on audit old/new and the
// posture attribute map, which are deliberately decoded loosely. Synthesized fuzz
// bodies therefore cannot vary those shapes; cover them with hand-written payloads
// instead. An empty Properties on an object is not a parser bug.
//
// additionalProperties IS modeled (see AdditionalProperties): the spec uses it for
// real typed maps that consumed GETs return — SplitDns, DnsConfiguration.splitDNS,
// DevicePostureAttributes.attributes — and leaving it out made those nodes
// indistinguishable from an object with no properties, so a change to the map's
// VALUE type looked like nothing at all (#431).
type Schema struct {
	Type       string            // "object","array","string","integer","number","boolean",""
	Format     string            // e.g. "date-time" — drives valid-value synthesis
	Properties map[string]Schema // non-nil for objects with properties
	Items      *Schema           // non-nil for arrays
	Enum       []string          // string enum values, if any
	Nullable   bool
	// AdditionalProperties is the VALUE schema of a typed map, non-nil only when
	// the source node gave additionalProperties a schema. The boolean forms
	// (`additionalProperties: true|false`) carry no value type and leave this nil;
	// neither appears in the vendored spec.
	AdditionalProperties *Schema
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
	components, err := parseComponents(doc, "schemas")
	if err != nil {
		return nil, err
	}
	// components.parameters, resolved the same way: 30 of the 34 parameters on
	// consumed operations are $refs into it (#432).
	parameters, err := parseComponents(doc, "parameters")
	if err != nil {
		return nil, err
	}

	spec := &Spec{
		Ops:        make(map[string]Operation),
		components: components,
		parameters: parameters,
		allOps:     map[string]OperationRef{},
	}

	// Extract paths.
	rawPaths, ok := doc["paths"]
	if !ok {
		return spec, nil
	}
	spec.allOps = parseAllOperations(rawPaths)
	var paths map[string]json.RawMessage
	if err := json.Unmarshal(rawPaths, &paths); err != nil {
		return nil, fmt.Errorf("oas: unmarshal paths: %w", err)
	}

	for path, pathItemRaw := range paths {
		var pathItem map[string]json.RawMessage
		if err := json.Unmarshal(pathItemRaw, &pathItem); err != nil {
			continue
		}

		opRaw, hasGet := pathItem["get"]
		if !hasGet {
			continue
		}

		op, err := parseOperation(opRaw, path, pathItem["parameters"], spec)
		if err != nil || op == nil {
			continue
		}
		spec.Ops[op.ID] = *op
	}

	return spec, nil
}

// parseComponents extracts components.<kind> from the document as raw nodes.
// kind is "schemas" or "parameters".
func parseComponents(doc map[string]json.RawMessage, kind string) (map[string]rawSchema, error) {
	result := make(map[string]rawSchema)

	rawComponents, ok := doc["components"]
	if !ok {
		return result, nil
	}

	var components map[string]json.RawMessage
	if err := json.Unmarshal(rawComponents, &components); err != nil {
		return result, fmt.Errorf("oas: unmarshal components: %w", err)
	}

	rawSchemas, ok := components[kind]
	if !ok {
		return result, nil
	}

	var schemas map[string]json.RawMessage
	if err := json.Unmarshal(rawSchemas, &schemas); err != nil {
		return result, fmt.Errorf("oas: unmarshal components.%s: %w", kind, err)
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
// path is the templated path it lives at and pathItemParams is the path item's
// own `parameters` array, which applies to every operation beneath it.
// Returns nil, nil if the operation has no operationId (skip it).
func parseOperation(opRaw json.RawMessage, path string, pathItemParams json.RawMessage, spec *Spec) (*Operation, error) {
	components := spec.components

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
		Path:   path,
	}

	// Request surface (#432): parameters, success statuses, media types.
	op.Parameters = parseParams(pathItemParams, opMap, spec.parameters, components)
	op.SuccessStatuses = successStatuses(opMap)

	// Parse the selected success response's application/json schema.
	if resp, ok := selectSuccessResponse(opMap); ok {
		op.ResponseMediaTypes = mediaTypes(resp)
		op.Response = extractJSONSchema(resp, components)
	}

	// Parse requestBody.content["application/json"].schema.required.
	op.RequestRequired = parseRequestRequired(opMap, components)
	if rb, ok := requestBodyObject(opMap); ok {
		op.RequestMediaTypes = mediaTypes(rb)
	}

	return op, nil
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

	// type — OpenAPI 3.1 permits a type ARRAY, which is how this spec spells
	// nullability: `"type": ["boolean","null"]`. The 3.0 `nullable: true` keyword
	// does not appear in it at all. Unmarshalling an array into a string silently
	// fails and leaves Type empty, and Classify's type-change check is guarded on
	// both sides being non-empty, so an unparsed type array makes a field's type
	// change undetectable rather than merely unknown (#431).
	if rawType, ok := rs["type"]; ok {
		if err := json.Unmarshal(rawType, &s.Type); err != nil {
			var types []string
			if json.Unmarshal(rawType, &types) == nil {
				for _, t := range types {
					if t == "null" {
						s.Nullable = true
						continue
					}
					// First non-null wins. A union of two concrete types cannot be
					// represented here, and none appears in the vendored spec; taking
					// the first is strictly better than taking none, because it keeps
					// the type-change check alive on the common nullable case.
					if s.Type == "" {
						s.Type = t
					}
				}
			}
		}
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

	// additionalProperties (typed map). Only the SCHEMA form carries a value type;
	// the boolean form unmarshals into rawSchema with an error and is left nil.
	if rawAP, ok := rs["additionalProperties"]; ok {
		var apRS rawSchema
		if err := json.Unmarshal(rawAP, &apRS); err == nil && len(apRS) > 0 {
			resolved := resolveSchema(apRS, components, visited, depth+1)
			s.AdditionalProperties = &resolved
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
