package oas

// Request surface: parameters, success statuses and media types (#432).
//
// Drift detection used to stop at the 200 application/json response body, which
// left three dimensions of the contract invisible. Measured against the vendored
// spec at the time this was written, across the 18 consumed operations:
//
//   - 34 parameters (20 path, 13 query, 1 header). THIRTY of the thirty-four are
//     $refs into components.parameters, and thirty are declared at the PATH-ITEM
//     level rather than on the operation, so a parser that reads only inline
//     `operation.parameters` finds four. Both merges matter.
//   - 2 parameters carry a default (listUsers `type`, `role`). A default moving
//     upstream changes what every poll collects while every response field still
//     decodes cleanly — drift that no response-body diff can see.
//   - one parameter's schema is itself a $ref (logType), so parameter schemas
//     need the same components resolution response schemas get.
//   - every consumed operation declares exactly one success status ("200") and
//     getPolicyFile alone offers a second response media type
//     (application/hujson) beside the application/json we decode.

import (
	"encoding/json"
	"sort"
	"strings"
)

// Param is one parameter of a modeled operation, with the path-item level merged
// in and $refs into components.parameters resolved.
type Param struct {
	// Name is the parameter name as sent on the wire.
	Name string
	// In is the location: "path", "query", "header" or "cookie".
	In string
	// Required reports whether the spec marks the parameter required. Absent is
	// false, which is also what OpenAPI says (path parameters are required by
	// rule, but the vendored spec states it explicitly on all of them).
	Required bool
	// Default is the JSON-encoded schema default, or "" when the spec gives
	// none. Encoded rather than typed so a bool, string and number default all
	// compare with ==; the report prints it verbatim.
	Default string
	// Schema is the parameter's resolved schema.
	Schema Schema
}

// Key is the parameter's identity for diffing: name alone can repeat across
// locations, so both are needed.
func (p Param) Key() string { return p.In + ":" + p.Name }

// parseParams builds an operation's merged, resolved, deterministically ordered
// parameter list. pathItemParams is the path item's own `parameters` array, which
// applies to every operation under that path; opMap is the operation object.
//
// An operation-level parameter with the same (in, name) as a path-item one
// overrides it, per OpenAPI. Order is (In, Name) ascending so a JSON drift report
// does not reshuffle itself between runs — the scheduled lane maintains ONE
// deduplicated tracking issue, and churn in it is indistinguishable from news.
func parseParams(pathItemParams json.RawMessage, opMap map[string]json.RawMessage, params map[string]rawSchema, components map[string]rawSchema) []Param {
	merged := map[string]Param{}

	add := func(raw json.RawMessage) {
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return
		}
		for _, entry := range list {
			var rs rawSchema
			if err := json.Unmarshal(entry, &rs); err != nil {
				continue
			}
			p, ok := resolveParam(rs, params, components)
			if !ok {
				continue
			}
			merged[p.Key()] = p
		}
	}

	if len(pathItemParams) > 0 {
		add(pathItemParams)
	}
	if raw, ok := opMap["parameters"]; ok {
		add(raw) // operation level last: it wins on a key collision
	}

	if len(merged) == 0 {
		return nil
	}
	out := make([]Param, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].In != out[j].In {
			return out[i].In < out[j].In
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// resolveParam turns one raw parameter node into a Param, following a
// #/components/parameters/<Name> $ref first. Returns false for a node with no
// usable name, which cannot be tracked.
func resolveParam(rs rawSchema, params map[string]rawSchema, components map[string]rawSchema) (Param, bool) {
	if rawRef, ok := rs["$ref"]; ok {
		var ref string
		if err := json.Unmarshal(rawRef, &ref); err != nil {
			return Param{}, false
		}
		name, ok := componentName(ref, "parameters")
		if !ok {
			return Param{}, false
		}
		target, ok := params[name]
		if !ok {
			return Param{}, false
		}
		rs = target
	}

	var p Param
	if raw, ok := rs["name"]; ok {
		_ = json.Unmarshal(raw, &p.Name)
	}
	if p.Name == "" {
		return Param{}, false
	}
	if raw, ok := rs["in"]; ok {
		_ = json.Unmarshal(raw, &p.In)
	}
	if raw, ok := rs["required"]; ok {
		_ = json.Unmarshal(raw, &p.Required)
	}
	if raw, ok := rs["schema"]; ok {
		var schemaRS rawSchema
		if err := json.Unmarshal(raw, &schemaRS); err == nil {
			p.Schema = resolveSchema(schemaRS, components, make(map[string]bool), 0)
			// The default lives on the schema node, not the parameter, and is
			// read from the RAW node: resolveSchema does not model it, and a
			// $ref'd schema's default (if upstream ever adds one) is picked up
			// by re-reading the followed node below.
			p.Default = rawDefault(schemaRS, components)
		}
	}
	return p, true
}

// rawDefault returns the JSON-encoded `default` of a schema node, following one
// $ref hop, or "" when there is none.
func rawDefault(rs rawSchema, components map[string]rawSchema) string {
	if raw, ok := rs["default"]; ok {
		return string(compactJSON(raw))
	}
	followed := followRef(rs, components, make(map[string]bool), 0)
	if followed == nil {
		return ""
	}
	if raw, ok := followed["default"]; ok {
		return string(compactJSON(raw))
	}
	return ""
}

// compactJSON strips insignificant whitespace so the same default spelled with
// different formatting compares equal.
func compactJSON(raw json.RawMessage) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// successStatuses returns the operation's 2xx response codes, sorted. "default"
// is NOT a success status: the vendored spec uses it for error shapes only, and
// treating it as success would make an operation that lost its 200 look fine.
func successStatuses(opMap map[string]json.RawMessage) []string {
	responses, ok := responseMap(opMap)
	if !ok {
		return nil
	}
	var out []string
	for code := range responses {
		if strings.HasPrefix(code, "2") && len(code) == 3 {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// selectSuccessResponse picks the response object whose body we decode: "200"
// when present, otherwise the lowest 2xx. Hardcoding "200" (as this used to)
// turns a success-status change upstream into an empty response schema, which
// Classify then reports as every single field having been removed — technically
// loud, but it names the wrong defect.
func selectSuccessResponse(opMap map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	responses, ok := responseMap(opMap)
	if !ok {
		return nil, false
	}
	codes := successStatuses(opMap)
	if len(codes) == 0 {
		return nil, false
	}
	chosen := codes[0]
	for _, c := range codes {
		if c == "200" {
			chosen = c
			break
		}
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(responses[chosen], &resp); err != nil {
		return nil, false
	}
	return resp, true
}

// responseMap decodes the operation's `responses` object.
func responseMap(opMap map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	raw, ok := opMap["responses"]
	if !ok {
		return nil, false
	}
	var responses map[string]json.RawMessage
	if err := json.Unmarshal(raw, &responses); err != nil {
		return nil, false
	}
	return responses, true
}

// mediaTypes returns the sorted media types of a response or requestBody object.
func mediaTypes(holder map[string]json.RawMessage) []string {
	raw, ok := holder["content"]
	if !ok {
		return nil
	}
	var content map[string]json.RawMessage
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil
	}
	out := make([]string, 0, len(content))
	for mt := range content {
		out = append(out, mt)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// requestBodyObject decodes the operation's requestBody object.
func requestBodyObject(opMap map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	raw, ok := opMap["requestBody"]
	if !ok {
		return nil, false
	}
	var rb map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rb); err != nil {
		return nil, false
	}
	return rb, true
}

// componentName extracts <Name> from a #/components/<kind>/<Name> $ref.
func componentName(ref, kind string) (string, bool) {
	prefix := "#/components/" + kind + "/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	name := ref[len(prefix):]
	if name == "" {
		return "", false
	}
	return name, true
}
