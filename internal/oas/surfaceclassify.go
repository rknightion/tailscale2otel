package oas

// Classification of request-surface drift (#432). The parsing half is in
// surface.go; the severity policy table is in classify.go.

import (
	"fmt"
	"sort"
)

// emitFunc records one change at the kind's default severity.
type emitFunc func(kind ChangeKind, detail string)

// emitAsFunc records one change at an explicit severity, for the two kinds whose
// severity depends on WHICH media type moved.
type emitAsFunc func(kind ChangeKind, severity Severity, detail string)

// classifyParams diffs two parameter lists keyed on (in, name).
//
// The key is both parts on purpose: a parameter moving from the path to the query
// string keeps its name and changes how the request must be built, so keying on
// name alone would report that as no change at all.
func classifyParams(oldParams, newParams []Param, emit emitFunc) {
	oldByKey := paramIndex(oldParams)
	newByKey := paramIndex(newParams)

	for _, key := range sortedKeys(oldByKey) {
		oldParam := oldByKey[key]
		newParam, stillThere := newByKey[key]
		if !stillThere {
			emit(ParamRemoved, fmt.Sprintf("%s: parameter removed", key))
			continue
		}

		switch {
		case !oldParam.Required && newParam.Required:
			emit(ParamBecameRequired, fmt.Sprintf("%s: became required", key))
		case oldParam.Required && !newParam.Required:
			emit(ParamBecameOptional, fmt.Sprintf("%s: became optional", key))
		}

		// Guarded on both types being known, matching the response-field rule: an
		// unparsed type on either side is unknown, not changed.
		if oldParam.Schema.Type != newParam.Schema.Type &&
			oldParam.Schema.Type != "" && newParam.Schema.Type != "" {
			emit(ParamTypeChanged, fmt.Sprintf("%s: %s → %s",
				key, oldParam.Schema.Type, newParam.Schema.Type))
		}

		if oldParam.Default != newParam.Default {
			emit(ParamDefaultChanged, fmt.Sprintf("%s: default %s → %s",
				key, defaultOrNone(oldParam.Default), defaultOrNone(newParam.Default)))
		}

		classifyStringSets(oldParam.Schema.Enum, newParam.Schema.Enum,
			ParamEnumValueRemoved, ParamEnumValueAdded, key+": enum value", emit)
	}

	for _, key := range sortedKeys(newByKey) {
		if _, existed := oldByKey[key]; existed {
			continue
		}
		if newByKey[key].Required {
			emit(RequiredParamAdded, fmt.Sprintf("%s: required parameter added", key))
			continue
		}
		emit(OptionalParamAdded, fmt.Sprintf("%s: optional parameter added", key))
	}
}

// classifyStringSets reports members removed and added between two string sets,
// used for success statuses and for parameter enums.
func classifyStringSets(oldVals, newVals []string, removed, added ChangeKind, label string, emit emitFunc) {
	oldSet, newSet := toStringSet(oldVals), toStringSet(newVals)
	for _, v := range sortedSet(oldSet) {
		if !newSet[v] {
			emit(removed, fmt.Sprintf("%s %q removed", label, v))
		}
	}
	for _, v := range sortedSet(newSet) {
		if !oldSet[v] {
			emit(added, fmt.Sprintf("%s %q added", label, v))
		}
	}
}

// classifyMediaTypes reports media types removed and added. Removal is Breaking
// only for the media type we actually decode; losing one we never request (the
// application/hujson getPolicyFile offers alongside JSON) is informational.
func classifyMediaTypes(oldVals, newVals []string, removed, added ChangeKind, label string, emitAs emitAsFunc) {
	oldSet, newSet := toStringSet(oldVals), toStringSet(newVals)
	for _, mt := range sortedSet(oldSet) {
		if newSet[mt] {
			continue
		}
		severity := Info
		if mt == decodedMediaType {
			severity = SeverityOf(removed)
		}
		emitAs(removed, severity, fmt.Sprintf("%s media type %q removed", label, mt))
	}
	for _, mt := range sortedSet(newSet) {
		if !oldSet[mt] {
			emitAs(added, SeverityOf(added), fmt.Sprintf("%s media type %q added", label, mt))
		}
	}
}

// defaultOrNone renders a JSON-encoded default for a report, spelling absence
// explicitly so "default (none) → \"all\"" reads as the behavioral change it is.
func defaultOrNone(d string) string {
	if d == "" {
		return "(none)"
	}
	return d
}

func paramIndex(ps []Param) map[string]Param {
	out := make(map[string]Param, len(ps))
	for _, p := range ps {
		out[p.Key()] = p
	}
	return out
}

func sortedKeys(m map[string]Param) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
