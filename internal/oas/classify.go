package oas

import (
	"fmt"
	"sort"
)

// ChangeKind describes the kind of drift detected between two spec versions.
type ChangeKind string

const (
	// EndpointRemoved means the operation exists in old but not in new.
	EndpointRemoved ChangeKind = "endpoint_removed"
	// RemovedResponseField means a response property path present in old is absent in new.
	RemovedResponseField ChangeKind = "removed_response_field"
	// TypeChanged means a property path appears in both but its Type differs.
	TypeChanged ChangeKind = "type_changed"
	// NewRequiredRequestField means a new required request field is present in new but absent in old.
	NewRequiredRequestField ChangeKind = "new_required_request_field"
	// EnumValueRemoved means an enum value present in old was removed from new.
	EnumValueRemoved ChangeKind = "enum_value_removed"
	// EnumValueAdded means an enum value was added in new that was not in old.
	// We map unknown enum values to "other" so this is non-breaking (Info severity).
	EnumValueAdded ChangeKind = "enum_value_added"
	// NewOptionalField means a response property is present in new but absent in old.
	NewOptionalField ChangeKind = "new_optional_field"
)

// Severity classifies how urgently a change needs attention.
type Severity string

const (
	// Breaking means the change will break our decoders or callers.
	Breaking Severity = "breaking"
	// Warning means the change is concerning and warrants review.
	Warning Severity = "warning"
	// Info means the change is benign and requires no action.
	Info Severity = "info"
)

// Change describes one unit of drift between two spec versions.
type Change struct {
	OpID     string
	Kind     ChangeKind
	Detail   string
	Severity Severity
}

// severityRank maps severities to integer rank for sorting (lower = higher priority).
func severityRank(s Severity) int {
	switch s {
	case Breaking:
		return 0
	case Warning:
		return 1
	case Info:
		return 2
	default:
		return 99
	}
}

// Classify diffs old → new for the given operationIds and returns a sorted
// []Change. Only operations in opIDs are compared; others are ignored. The
// returned slice is sorted by (Severity rank: Breaking>Warning>Info, then OpID,
// then Detail).
func Classify(old, new *Spec, opIDs []string) []Change {
	var changes []Change

	for _, id := range opIDs {
		oldOp, inOld := old.Ops[id]
		newOp, inNew := new.Ops[id]

		if inOld && !inNew {
			changes = append(changes, Change{
				OpID:     id,
				Kind:     EndpointRemoved,
				Detail:   fmt.Sprintf("operation %q removed", id),
				Severity: Breaking,
			})
			continue
		}

		if !inOld {
			// Not in old — nothing to diff.
			continue
		}

		// Both present: diff response property paths.
		oldPaths := flattenPaths(oldOp.Response, "")
		newPaths := flattenPaths(newOp.Response, "")

		// Check for removed and type-changed fields.
		for path, oldSchema := range oldPaths {
			newSchema, exists := newPaths[path]
			if !exists {
				changes = append(changes, Change{
					OpID:     id,
					Kind:     RemovedResponseField,
					Detail:   path,
					Severity: Breaking,
				})
				continue
			}
			// Type changed.
			if oldSchema.typ != newSchema.typ && oldSchema.typ != "" && newSchema.typ != "" {
				changes = append(changes, Change{
					OpID:     id,
					Kind:     TypeChanged,
					Detail:   fmt.Sprintf("%s: %s → %s", path, oldSchema.typ, newSchema.typ),
					Severity: Breaking,
				})
			}
			// Enum changes (only when both have non-empty enum lists).
			if len(oldSchema.enum) > 0 || len(newSchema.enum) > 0 {
				oldSet := toStringSet(oldSchema.enum)
				newSet := toStringSet(newSchema.enum)
				// Removed enum values.
				for v := range oldSet {
					if !newSet[v] {
						changes = append(changes, Change{
							OpID:     id,
							Kind:     EnumValueRemoved,
							Detail:   fmt.Sprintf("%s: enum value %q removed", path, v),
							Severity: Warning,
						})
					}
				}
				// Added enum values.
				for v := range newSet {
					if !oldSet[v] {
						changes = append(changes, Change{
							OpID:     id,
							Kind:     EnumValueAdded,
							Detail:   fmt.Sprintf("%s: enum value %q added", path, v),
							Severity: Info,
						})
					}
				}
			}
		}

		// Check for new fields.
		for path := range newPaths {
			if _, exists := oldPaths[path]; !exists {
				changes = append(changes, Change{
					OpID:     id,
					Kind:     NewOptionalField,
					Detail:   path,
					Severity: Info,
				})
			}
		}

		// Diff RequestRequired.
		oldReqSet := toStringSet(oldOp.RequestRequired)
		for _, field := range newOp.RequestRequired {
			if !oldReqSet[field] {
				changes = append(changes, Change{
					OpID:     id,
					Kind:     NewRequiredRequestField,
					Detail:   fmt.Sprintf("required request field %q added", field),
					Severity: Breaking,
				})
			}
		}
	}

	// Sort by (severity rank, OpID, Detail).
	sort.Slice(changes, func(i, j int) bool {
		ri, rj := severityRank(changes[i].Severity), severityRank(changes[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if changes[i].OpID != changes[j].OpID {
			return changes[i].OpID < changes[j].OpID
		}
		return changes[i].Detail < changes[j].Detail
	})

	return changes
}

// HasActionable reports whether any change is Breaking or Warning.
// Info-only → false. Empty slice → false.
func HasActionable(cs []Change) bool {
	for _, c := range cs {
		if c.Severity == Breaking || c.Severity == Warning {
			return true
		}
	}
	return false
}

// pathSchema is a flattened schema leaf: type and enum values.
type pathSchema struct {
	typ  string
	enum []string
}

// flattenPaths descends a Schema and returns a map from dotted path to
// pathSchema for all leaf properties (non-object, non-array nodes, plus
// object nodes with no children, and array items that are not objects).
//
// Arrays are descended into their Items with a "[]" suffix on the path.
// Objects are descended into their Properties with a ".<key>" suffix.
//
// Only leaf nodes (non-object or objects with no Properties) are stored
// so that type and enum comparisons are consistent between callers.
func flattenPaths(s Schema, prefix string) map[string]pathSchema {
	result := make(map[string]pathSchema)
	flattenInto(result, s, prefix)
	return result
}

// flattenInto is the recursive implementation of flattenPaths.
func flattenInto(out map[string]pathSchema, s Schema, prefix string) {
	switch s.Type {
	case "object":
		if len(s.Properties) == 0 {
			// Leaf object (no children): record it.
			if prefix != "" {
				out[prefix] = pathSchema{typ: s.Type, enum: s.Enum}
			}
			return
		}
		for name, child := range s.Properties {
			childPrefix := name
			if prefix != "" {
				childPrefix = prefix + "." + name
			}
			flattenInto(out, child, childPrefix)
		}
	case "array":
		if s.Items == nil {
			// Array with no items schema: treat as leaf.
			if prefix != "" {
				out[prefix] = pathSchema{typ: s.Type, enum: s.Enum}
			}
			return
		}
		itemPrefix := prefix + "[]"
		flattenInto(out, *s.Items, itemPrefix)
	default:
		// Scalar or unknown: record as leaf.
		if prefix != "" {
			out[prefix] = pathSchema{typ: s.Type, enum: s.Enum}
		}
	}
}

// toStringSet converts a slice of strings to a set (map[string]bool).
func toStringSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
