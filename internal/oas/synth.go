package oas

import "encoding/json"

// SynthesizeBody returns one representative JSON body for s: all properties
// present, trivial typed values. Recurse with maxDepth guard — returns JSON
// null once depth is exhausted to prevent unbounded recursion on deep or cyclic
// schemas.
//
// Synthesis rules:
//   - object  → {"prop": <synthesized>} for each property; nil Properties → {}
//   - array   → [<synthesized items>] with one element; nil Items → []
//   - string  → Enum[0] if non-empty; "2026-01-01T00:00:00Z" if Format=="date-time"; else "x"
//   - integer / number → 1
//   - boolean → false
//   - unknown/empty Type → null
//
// The returned slice is always valid JSON.
func SynthesizeBody(s Schema, maxDepth int) []byte {
	b, _ := json.Marshal(synthesizeValue(s, maxDepth))
	return b
}

// synthesizeValue returns a Go value that json.Marshal will render to the
// representative JSON for s at the given remaining depth.
func synthesizeValue(s Schema, depth int) any {
	if depth <= 0 {
		return nil
	}

	switch s.Type {
	case "object":
		obj := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			obj[k] = synthesizeValue(v, depth-1)
		}
		return obj

	case "array":
		if s.Items == nil {
			return []any{}
		}
		return []any{synthesizeValue(*s.Items, depth-1)}

	case "string":
		if len(s.Enum) > 0 {
			return s.Enum[0]
		}
		if s.Format == "date-time" {
			return "2026-01-01T00:00:00Z"
		}
		return "x"

	case "integer", "number":
		return 1

	case "boolean":
		return false

	default:
		// unknown or empty type — emit JSON null
		return nil
	}
}
