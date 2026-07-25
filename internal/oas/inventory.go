package oas

// Operation inventory: the every-verb census of an OpenAPI document (#423).
//
// Spec.Ops deliberately models GET operations only — it exists to drive decode
// drift and schema-driven fuzz on the endpoints we consume, and a non-GET
// operation has neither. But "has upstream published a new read endpoint?" is a
// different question that Ops cannot answer at all:
//
//   - a newly published GET we do NOT consume is absent from ConsumedOpIDs(),
//     so Classify never looks at it;
//   - read-only operations are not always GET. Tailscale publishes
//     POST /tailnet/{tailnet}/acl/validate (validateAndTestPolicyFile), which
//     requires only policy_file:read and mutates nothing.
//
// AllOperations is therefore a cheap, schema-free index of EVERY operation in
// the document — id, verb and path only. It does not touch Ops.

import (
	"encoding/json"
	"strings"
)

// httpVerbs is the set of path-item keys that denote an operation. Anything else
// in a path item (parameters, servers, summary, $ref, …) is not an operation.
var httpVerbs = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// OperationRef is the identity of one operation in an OpenAPI document, with no
// schema attached.
type OperationRef struct {
	// ID is the operationId.
	ID string
	// Method is the lower-case HTTP verb.
	Method string
	// Path is the templated path, e.g. "/tailnet/{tailnet}/devices".
	Path string
	// Summary is the operation's one-line summary, when the document has one.
	// Useful context on a drift report; never load-bearing.
	Summary string
}

// ReadCapable reports whether a verb is inherently read-only. GET and HEAD are;
// every other verb may be, but only per-operation knowledge can say so, which is
// what the operation-disposition baseline records.
func ReadCapable(method string) bool {
	switch strings.ToLower(method) {
	case "get", "head":
		return true
	default:
		return false
	}
}

// AllOperations returns every operation in the document keyed by operationId,
// across all verbs. Operations without an operationId are skipped — they cannot
// be tracked in a baseline. A duplicate operationId (invalid OpenAPI) keeps the
// first occurrence encountered.
func (s *Spec) AllOperations() map[string]OperationRef {
	out := make(map[string]OperationRef, len(s.allOps))
	for k, v := range s.allOps {
		out[k] = v
	}
	return out
}

// parseAllOperations builds the every-verb index from the raw paths object.
func parseAllOperations(rawPaths json.RawMessage) map[string]OperationRef {
	out := map[string]OperationRef{}

	var paths map[string]json.RawMessage
	if err := json.Unmarshal(rawPaths, &paths); err != nil {
		return out
	}

	for path, pathItemRaw := range paths {
		var pathItem map[string]json.RawMessage
		if err := json.Unmarshal(pathItemRaw, &pathItem); err != nil {
			continue
		}
		for _, verb := range httpVerbs {
			opRaw, ok := pathItem[verb]
			if !ok {
				continue
			}
			var opMap map[string]json.RawMessage
			if err := json.Unmarshal(opRaw, &opMap); err != nil {
				continue
			}
			var id string
			if err := json.Unmarshal(opMap["operationId"], &id); err != nil || id == "" {
				continue // no operationId — untrackable
			}
			if _, dupe := out[id]; dupe {
				continue
			}
			var summary string
			if raw, ok := opMap["summary"]; ok {
				_ = json.Unmarshal(raw, &summary)
			}
			out[id] = OperationRef{ID: id, Method: verb, Path: path, Summary: summary}
		}
	}
	return out
}
