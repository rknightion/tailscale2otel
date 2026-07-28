package apicontract

import (
	"fmt"
	"sort"
	"strings"
)

// Flatten walks a schema built by BuildSchema and returns every LEAF path
// mapped to its type signature — e.g. "service.name" -> "string",
// "collectors[].runs" -> "integer", "full{}.value" -> "any" (a map's value
// substructure is suffixed "{}", an array's item substructure "[]", matching
// internal/config/schema_test.go's "*" convention for the same idea).
//
// This is the unit a compatibility Baseline is built and checked against: a
// path present in a committed baseline must still resolve to the SAME type
// signature here, or the response has broken a guarantee a consumer may
// depend on (see CompareBaseline).
func Flatten(schema map[string]any) map[string]string {
	out := map[string]string{}
	flatten(schema, "", out)
	return out
}

func flatten(node map[string]any, path string, out map[string]string) {
	if props, ok := node["properties"].(map[string]any); ok {
		for name, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok {
				panic(fmt.Sprintf("apicontract: property %q at %q is not an object", name, path))
			}
			next := name
			if path != "" {
				next = path + "." + name
			}
			flatten(child, next, out)
		}
		return
	}
	if items, ok := node["items"].(map[string]any); ok {
		flatten(items, path+"[]", out)
		return
	}
	if ap, ok := node["additionalProperties"].(map[string]any); ok {
		flatten(ap, path+"{}", out)
		return
	}
	if path == "" {
		return // root carried no properties/items/additionalProperties — nothing to flatten
	}
	out[path] = typeSignature(node)
}

// typeSignature renders a leaf schema node's "type" as a stable string:
// a bare type name ("string"), a sorted "|"-joined union for a nullable
// field ("null|string"), or "any" for a field with no "type" at all (the
// dynamic-value case — see schemaForType's Interface branch).
func typeSignature(node map[string]any) string {
	switch t := node["type"].(type) {
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, v := range t {
			parts[i] = fmt.Sprint(v)
		}
		sort.Strings(parts)
		return strings.Join(parts, "|")
	default:
		return "any"
	}
}
