package oas

// Boundary variants (#433): the set of payload shapes a decoder has to survive,
// derived from an operation's own response schema rather than hand-written per
// operation.
//
// SynthesizeBody gives one representative body, so before this every consumed
// operation was decoded exactly once against a single happy-path shape. The
// shapes that actually break decoders were covered for ONE of the eighteen
// consumed operations, by hand.
//
// Two expectations, and every variant carries exactly one of them:
//
//	MustDecode — the decoder is required to accept it. Null, an empty container of
//	  the right shape, nulled nullable fields, extreme values, an unknown enum
//	  member and an additive field are all things the real API can produce or will
//	  produce after a benign release.
//	MustError — the decoder is required to REJECT it. A wrong container shape that
//	  "decodes" reports zero rows instead of surfacing that the response moved, and
//	  silence is the worst outcome available. Verified against all 18 consumed
//	  operations before being asserted: every one rejects a mismatched container and
//	  a scalar body, except getPolicyFile, which reads raw HuJSON bytes by design
//	  and is already FuzzSkip.
//
// What is deliberately NOT here: spec-declared numeric bounds. The vendored spec
// declares `minimum`/`maximum` on three consumed response fields in total, none of
// which constrains a Go decode, so the extreme-value variant uses wire-plausible
// extremes instead of pretending the spec bounds mean something.

import (
	"encoding/json"
	"math"
	"strings"
)

// BoundaryKind names one boundary shape.
type BoundaryKind string

const (
	// BoundaryNull is a bare JSON null body.
	BoundaryNull BoundaryKind = "null"
	// BoundaryEmptyContainer is an empty container MATCHING the response's own
	// top-level shape: {} for an object response, [] for a bare-array one.
	BoundaryEmptyContainer BoundaryKind = "empty_container"
	// BoundaryMinimalValues is every scalar at its minimum and every nullable field
	// null. Nullability is spec-derived (3.1 type arrays, parsed since #431).
	BoundaryMinimalValues BoundaryKind = "minimal_values"
	// BoundaryExtremeValues is long strings, integer ceilings and multi-element
	// arrays.
	BoundaryExtremeValues BoundaryKind = "extreme_values"
	// BoundaryUnknownEnum replaces every declared enum member with a value outside
	// the enum.
	BoundaryUnknownEnum BoundaryKind = "unknown_enum"
	// BoundaryAdditiveField injects an unknown key into every object, at every
	// level.
	BoundaryAdditiveField BoundaryKind = "additive_field"
	// BoundaryWrongShape is an empty container of the WRONG top-level shape.
	BoundaryWrongShape BoundaryKind = "wrong_shape"
	// BoundaryScalarBody is a bare JSON scalar where a container is expected.
	BoundaryScalarBody BoundaryKind = "scalar_body"
)

// AllBoundaryKinds returns every kind BoundaryVariants emits, in emission order.
func AllBoundaryKinds() []BoundaryKind {
	return []BoundaryKind{
		BoundaryNull,
		BoundaryEmptyContainer,
		BoundaryMinimalValues,
		BoundaryExtremeValues,
		BoundaryUnknownEnum,
		BoundaryAdditiveField,
		BoundaryWrongShape,
		BoundaryScalarBody,
	}
}

// TypeDependent reports whether a kind's body depends on the schema's declared
// FIELD types. It is false for the shape-only kinds, which is what lets the six
// operations whose OAS field types disagree with the wire (see the contract
// package's oasMistypedOps) still get boundary coverage for those.
func (k BoundaryKind) TypeDependent() bool {
	switch k {
	case BoundaryNull, BoundaryEmptyContainer, BoundaryWrongShape, BoundaryScalarBody:
		return false
	default:
		return true
	}
}

// Variant is one synthesized boundary body and the expectation that goes with it.
type Variant struct {
	Kind BoundaryKind
	Body []byte
	// MustDecode requires the decoder to accept the body without error.
	MustDecode bool
	// MustError requires the decoder to reject it. Exactly one of MustDecode and
	// MustError is true for every variant — a variant that permits both outcomes
	// asserts nothing.
	MustError bool
}

// unknownEnumMember is a value no upstream enum will ever contain. Enum members in
// this spec are SCREAMING_SNAKE or lower-case words.
const unknownEnumMember = "ts2otel-unknown-enum-member"

// additiveKey is the injected unknown field. Prefixed so a test can recognize it
// and so it cannot collide with an upstream name.
const additiveKey = "ts2otelUnexpectedField"

// BoundaryVariants returns the boundary payloads for a response schema, in a
// deterministic order. maxDepth bounds recursion exactly as SynthesizeBody's does.
func BoundaryVariants(s Schema, maxDepth int) []Variant {
	container, wrongContainer := "{}", "[]"
	if s.Type == "array" {
		container, wrongContainer = "[]", "{}"
	}

	must := func(kind BoundaryKind, body []byte) Variant {
		return Variant{Kind: kind, Body: body, MustDecode: true}
	}
	reject := func(kind BoundaryKind, body string) Variant {
		return Variant{Kind: kind, Body: []byte(body), MustError: true}
	}

	return []Variant{
		must(BoundaryNull, []byte("null")),
		must(BoundaryEmptyContainer, []byte(container)),
		must(BoundaryMinimalValues, marshalVariant(s, maxDepth, modeMinimal)),
		must(BoundaryExtremeValues, marshalVariant(s, maxDepth, modeExtreme)),
		must(BoundaryUnknownEnum, marshalVariant(s, maxDepth, modeUnknownEnum)),
		must(BoundaryAdditiveField, marshalVariant(s, maxDepth, modeAdditive)),
		reject(BoundaryWrongShape, wrongContainer),
		reject(BoundaryScalarBody, `"ts2otel-scalar-body"`),
	}
}

// variantMode selects how scalars are valued and whether unknown keys are added.
type variantMode int

const (
	modeMinimal variantMode = iota
	modeExtreme
	modeUnknownEnum
	modeAdditive
)

// marshalVariant renders one mode to JSON. Marshal of a map[string]any sorts keys,
// so output is byte-stable across runs — which matters because a failing table
// entry reports its body and has to be reproducible.
func marshalVariant(s Schema, maxDepth int, mode variantMode) []byte {
	b, err := json.Marshal(variantValue(s, maxDepth, mode))
	if err != nil {
		// Unreachable: every value built below is a plain map, slice, string,
		// number, bool or nil. Emit valid JSON rather than a nil body, which would
		// fail as malformed and hide the real cause.
		return []byte("null")
	}
	return b
}

// variantValue builds the Go value for one mode at the given remaining depth.
func variantValue(s Schema, depth int, mode variantMode) any {
	if depth <= 0 {
		return nil
	}

	switch s.Type {
	case "object":
		obj := make(map[string]any, len(s.Properties)+1)
		for k, v := range s.Properties {
			obj[k] = variantValue(v, depth-1, mode)
		}
		if s.AdditionalProperties != nil {
			// A typed map: give it one entry so the value schema is exercised.
			obj["ts2otelMapKey"] = variantValue(*s.AdditionalProperties, depth-1, mode)
		}
		// The additive field goes ONLY into objects with declared properties. In a
		// typed map every key is a legitimate entry that must carry the map's value
		// type, so injecting a boolean there builds a WRONG-SHAPE body and calls it
		// additive — which is how the first draft of this produced four "decoder
		// rejects an additive field" failures that were all my own bug (splitDNS,
		// clientConnectivity.latency, the posture attribute/expiry maps).
		if mode == modeAdditive && s.AdditionalProperties == nil {
			obj[additiveKey] = true
		}
		return obj

	case "array":
		if s.Items == nil {
			return []any{}
		}
		if mode == modeExtreme {
			// More than one element, so a decoder that only ever reads the first is
			// exercised past it.
			return []any{
				variantValue(*s.Items, depth-1, mode),
				variantValue(*s.Items, depth-1, mode),
				variantValue(*s.Items, depth-1, mode),
			}
		}
		return []any{variantValue(*s.Items, depth-1, mode)}

	case "string":
		return variantString(s, mode)

	case "integer":
		return variantInteger(s, mode)

	case "number":
		if mode == modeMinimal {
			if s.Nullable {
				return nil
			}
			return 0
		}
		// Large but exactly representable in float64 and within float32's range, so
		// a `format: float` field neither overflows nor fails to parse.
		return 1e15

	case "boolean":
		if s.Nullable && mode == modeMinimal {
			return nil
		}
		return mode != modeMinimal

	default:
		// Unknown or empty type — the loosely-decoded nodes (audit old/new, posture
		// attributes). JSON null is what SynthesizeBody emits too.
		return nil
	}
}

// variantString values a string field per mode.
func variantString(s Schema, mode variantMode) any {
	// A date-time field always carries a VALID timestamp. An empty string where a
	// timestamp is expected is a real but distinct quirk — external devices send ""
	// for created (#48) — covered where it actually occurs. Asserting it on every
	// timestamp field would be asserting wire behavior upstream does not exhibit.
	if s.Format == "date-time" {
		return "2026-01-01T00:00:00Z"
	}

	switch mode {
	case modeMinimal:
		if s.Nullable {
			return nil
		}
		return ""
	case modeUnknownEnum:
		if len(s.Enum) > 0 {
			return unknownEnumMember
		}
		return "x"
	case modeExtreme:
		if len(s.Enum) > 0 {
			// Stay inside the enum: this variant is about value SIZE, and an
			// out-of-enum value is what BoundaryUnknownEnum is for.
			return s.Enum[0]
		}
		return strings.Repeat("x", 512)
	default:
		if len(s.Enum) > 0 {
			return s.Enum[0]
		}
		return "x"
	}
}

// variantInteger values an integer field per mode.
//
// A plain `integer` gets the int32 ceiling and only `format: int64` gets the int64
// one: nothing in the spec promises a plain integer's Go field is 64-bit, and
// asserting MaxInt64 everywhere would be asserting a range upstream never claimed.
func variantInteger(s Schema, mode variantMode) any {
	if mode == modeMinimal {
		if s.Nullable {
			return nil
		}
		return 0
	}
	if mode == modeExtreme {
		if s.Format == "int64" {
			return int64(math.MaxInt64)
		}
		return int64(math.MaxInt32)
	}
	return 1
}
