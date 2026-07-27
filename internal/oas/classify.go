package oas

import (
	"fmt"
	"sort"
	"strings"
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

	// Request-surface kinds (#432). Before these, drift detection stopped at the
	// response body, so a parameter becoming required, a default moving, or the
	// media type we decode disappearing were all undetectable.

	// RequiredParamAdded means new declares a required parameter old did not:
	// every request we already send is now missing a mandatory field.
	RequiredParamAdded ChangeKind = "required_param_added"
	// OptionalParamAdded means new declares an optional parameter old did not.
	OptionalParamAdded ChangeKind = "optional_param_added"
	// ParamRemoved means a parameter old declared is gone from new. We may still
	// be sending it; upstream ignoring an unknown parameter silently changes what
	// we collect.
	ParamRemoved ChangeKind = "param_removed"
	// ParamBecameRequired means an optional parameter is now required.
	ParamBecameRequired ChangeKind = "param_became_required"
	// ParamBecameOptional means a required parameter is now optional.
	ParamBecameOptional ChangeKind = "param_became_optional"
	// ParamTypeChanged means a parameter's schema type differs.
	ParamTypeChanged ChangeKind = "param_type_changed"
	// ParamDefaultChanged means a parameter's default moved. Behavioral: every
	// response field still decodes, but what upstream returns when we omit the
	// parameter has changed.
	ParamDefaultChanged ChangeKind = "param_default_changed"
	// ParamEnumValueRemoved means a value we may be sending is no longer accepted.
	ParamEnumValueRemoved ChangeKind = "param_enum_value_removed"
	// ParamEnumValueAdded means the parameter accepts a value it did not before.
	ParamEnumValueAdded ChangeKind = "param_enum_value_added"
	// SuccessStatusRemoved means a 2xx status old declared is gone from new.
	SuccessStatusRemoved ChangeKind = "success_status_removed"
	// SuccessStatusAdded means new declares a 2xx status old did not.
	SuccessStatusAdded ChangeKind = "success_status_added"
	// ResponseMediaTypeRemoved means the success response no longer offers a media
	// type it used to. Breaking only for the one we actually decode.
	ResponseMediaTypeRemoved ChangeKind = "response_media_type_removed"
	// ResponseMediaTypeAdded means the success response offers a new media type.
	ResponseMediaTypeAdded ChangeKind = "response_media_type_added"
	// RequestMediaTypeRemoved means the request body no longer accepts a media
	// type it used to.
	RequestMediaTypeRemoved ChangeKind = "request_media_type_removed"
	// RequestMediaTypeAdded means the request body accepts a new media type.
	RequestMediaTypeAdded ChangeKind = "request_media_type_added"
	// OperationUnmodeled means a consumed operation is absent from the modeled
	// operation set of one or both specs, so it has NO drift coverage. Spec.Ops is
	// GET-only, so this is what fires the day a consumed operation is not a GET.
	OperationUnmodeled ChangeKind = "operation_unmodeled"
)

// AllChangeKinds returns every kind this package can emit, in a stable order.
// Used by the severity-policy guard and by report legends.
func AllChangeKinds() []ChangeKind {
	return []ChangeKind{
		EndpointRemoved,
		RemovedResponseField,
		TypeChanged,
		NewRequiredRequestField,
		EnumValueRemoved,
		EnumValueAdded,
		NewOptionalField,
		RequiredParamAdded,
		OptionalParamAdded,
		ParamRemoved,
		ParamBecameRequired,
		ParamBecameOptional,
		ParamTypeChanged,
		ParamDefaultChanged,
		ParamEnumValueRemoved,
		ParamEnumValueAdded,
		SuccessStatusRemoved,
		SuccessStatusAdded,
		ResponseMediaTypeRemoved,
		ResponseMediaTypeAdded,
		RequestMediaTypeRemoved,
		RequestMediaTypeAdded,
		OperationUnmodeled,
	}
}

// kindSeverity is the severity policy, as a table rather than a literal at each
// emit site. A kind added without an entry gets the zero Severity, which
// HasActionable treats as non-actionable and severityRank buckets at 99 — the
// change would exist and be silently ignored. TestClassify_EveryKindHasARankedSeverity
// walks AllChangeKinds against this map so that cannot happen.
//
// Three axes, per #432's "distinguish additive compatible changes from
// breaking/behavioral changes":
//
//	Breaking — our requests or decoders stop working.
//	Warning  — behavioral: everything still parses, but what we collect changes.
//	Info      — purely additive; no action.
var kindSeverity = map[ChangeKind]Severity{
	EndpointRemoved:          Breaking,
	RemovedResponseField:     Breaking,
	TypeChanged:              Breaking,
	NewRequiredRequestField:  Breaking,
	EnumValueRemoved:         Warning,
	EnumValueAdded:           Info,
	NewOptionalField:         Info,
	RequiredParamAdded:       Breaking,
	OptionalParamAdded:       Info,
	ParamRemoved:             Warning,
	ParamBecameRequired:      Breaking,
	ParamBecameOptional:      Info,
	ParamTypeChanged:         Breaking,
	ParamDefaultChanged:      Warning,
	ParamEnumValueRemoved:    Warning,
	ParamEnumValueAdded:      Info,
	SuccessStatusRemoved:     Breaking,
	SuccessStatusAdded:       Info,
	ResponseMediaTypeRemoved: Breaking, // overridden to Info for a media type we never decode
	ResponseMediaTypeAdded:   Info,
	RequestMediaTypeRemoved:  Breaking, // same override
	RequestMediaTypeAdded:    Info,
	OperationUnmodeled:       Warning,
}

// SeverityOf returns the default severity for a kind. Two kinds are refined at
// the emit site — a media type disappearing is Breaking only when it is the
// application/json we decode — and those overrides are marked in kindSeverity.
func SeverityOf(k ChangeKind) Severity { return kindSeverity[k] }

// decodedMediaType is the only media type any decoder here parses. A success
// response losing it is fatal however many others remain; losing any other
// (getPolicyFile's application/hujson is the sole real example) is informational.
const decodedMediaType = "application/json"

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
	OpID string
	Kind ChangeKind
	// Where locates the change for a human reading the scheduled lane's tracking
	// issue: "GET /tailnet/{tailnet}/devices". Empty when neither spec models the
	// operation and there is therefore no path to name (#432).
	Where    string
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

		// where names the operation for a human. Prefer the candidate's path; fall
		// back to the baseline's when the operation has been removed.
		where := ""
		switch {
		case inNew:
			where = strings.ToUpper(newOp.Method) + " " + newOp.Path
		case inOld:
			where = strings.ToUpper(oldOp.Method) + " " + oldOp.Path
		}
		emit := func(kind ChangeKind, detail string) {
			changes = append(changes, Change{
				OpID:     id,
				Kind:     kind,
				Where:    where,
				Detail:   detail,
				Severity: SeverityOf(kind),
			})
		}
		emitAs := func(kind ChangeKind, severity Severity, detail string) {
			changes = append(changes, Change{
				OpID: id, Kind: kind, Where: where, Detail: detail, Severity: severity,
			})
		}

		if inOld && !inNew {
			emit(EndpointRemoved, fmt.Sprintf("operation %q removed", id))
			continue
		}

		if !inOld {
			// Absent from the baseline's modeled operations, so there is nothing to
			// diff against and this operation has NO drift coverage. Spec.Ops is
			// GET-only by design, so a consumed operation of any other verb lands
			// here — silently, until #432 made it a reported change. See the
			// package doc for why widening Ops is not the fix.
			detail := fmt.Sprintf("consumed operation %q is not modeled in the baseline spec, "+
				"so it has no schema, parameter or media-type drift coverage", id)
			if !inNew {
				detail = fmt.Sprintf("consumed operation %q is modeled in neither spec "+
					"(Spec.Ops is GET-only), so it has no drift coverage at all", id)
			}
			emit(OperationUnmodeled, detail)
			continue
		}

		classifyParams(oldOp.Parameters, newOp.Parameters, emit)
		classifyStringSets(oldOp.SuccessStatuses, newOp.SuccessStatuses,
			SuccessStatusRemoved, SuccessStatusAdded, "success status", emit)
		classifyMediaTypes(oldOp.ResponseMediaTypes, newOp.ResponseMediaTypes,
			ResponseMediaTypeRemoved, ResponseMediaTypeAdded, "response", emitAs)
		classifyMediaTypes(oldOp.RequestMediaTypes, newOp.RequestMediaTypes,
			RequestMediaTypeRemoved, RequestMediaTypeAdded, "request", emitAs)

		// Both present: diff response property paths.
		oldPaths := flattenPaths(oldOp.Response, "")
		newPaths := flattenPaths(newOp.Response, "")

		// Check for removed and type-changed fields.
		for path, oldSchema := range oldPaths {
			newSchema, exists := newPaths[path]
			if !exists {
				emit(RemovedResponseField, path)
				continue
			}
			// Type changed.
			if oldSchema.typ != newSchema.typ && oldSchema.typ != "" && newSchema.typ != "" {
				emit(TypeChanged, fmt.Sprintf("%s: %s → %s", path, oldSchema.typ, newSchema.typ))
			}
			// Enum changes (only when both have non-empty enum lists).
			if len(oldSchema.enum) > 0 || len(newSchema.enum) > 0 {
				oldSet := toStringSet(oldSchema.enum)
				newSet := toStringSet(newSchema.enum)
				// Removed enum values.
				for v := range oldSet {
					if !newSet[v] {
						emit(EnumValueRemoved, fmt.Sprintf("%s: enum value %q removed", path, v))
					}
				}
				// Added enum values.
				for v := range newSet {
					if !oldSet[v] {
						emit(EnumValueAdded, fmt.Sprintf("%s: enum value %q added", path, v))
					}
				}
			}
		}

		// Check for new fields.
		for path := range newPaths {
			if _, exists := oldPaths[path]; !exists {
				emit(NewOptionalField, path)
			}
		}

		// Diff RequestRequired.
		oldReqSet := toStringSet(oldOp.RequestRequired)
		for _, field := range newOp.RequestRequired {
			if !oldReqSet[field] {
				emit(NewRequiredRequestField, fmt.Sprintf("required request field %q added", field))
			}
		}
	}

	return SortChanges(changes)
}

// SortChanges orders changes by (severity rank, OpID, Kind, Detail) in place and
// returns the slice. Exported so a report renderer can order a set it did not get
// straight from Classify and still match the tool's output exactly — the scheduled
// lane compares rendered bodies to decide whether to update its tracking issue,
// so any reordering reads as news.
//
// Kind is in the key because two changes can share an operation and a severity;
// without it their relative order came down to Detail alone, which is fine, but
// stating the full key makes the guarantee checkable.
func SortChanges(changes []Change) []Change {
	sort.SliceStable(changes, func(i, j int) bool {
		ri, rj := severityRank(changes[i].Severity), severityRank(changes[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if changes[i].OpID != changes[j].OpID {
			return changes[i].OpID < changes[j].OpID
		}
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Detail < changes[j].Detail
	})
	return changes
}

// SeverityAction describes, in one word, what a severity obliges the reader to
// do. A tracking issue read months later needs this: "warning" alone says
// nothing about whether to act.
func SeverityAction(s Severity) string {
	switch s {
	case Breaking:
		return "breaking — requests or decoders stop working; fix before the next release"
	case Warning:
		return "behavioral — everything still parses, but what we collect changes; review"
	case Info:
		return "additive — no action needed"
	default:
		return "unclassified — treat as breaking until triaged"
	}
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
