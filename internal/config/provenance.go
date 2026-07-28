package config

import (
	"fmt"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	env "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// Source names which layer's value survived to become the effective value of
// a config key: the built-in default, the YAML file, or a TS2OTEL_*
// environment variable. It is intentionally its own type (not a bare string)
// with exactly three defined constants, so nothing that returns a Source can
// accidentally return secret content instead — the compiler enforces the
// "never leak a value, only its origin" contract #309 asks for.
type Source string

const (
	// SourceDefault means neither the file nor the environment overrode the
	// built-in default for this key.
	SourceDefault Source = "default"
	// SourceFile means the YAML file set or changed this key, and no
	// environment variable overrode that.
	SourceFile Source = "file"
	// SourceEnv means a TS2OTEL_* environment variable set or changed this
	// key — env is the highest-precedence layer, so this wins over both file
	// and default whenever it applies.
	SourceEnv Source = "env"
)

// Provenance reports, for every effective configuration key, which layer's
// value survived: SourceDefault, SourceFile, or SourceEnv. Keys use the same
// dotted-path convention as internal/configexport.Build (koanf's own key
// delimiter, which is "."), so the two can be joined by key directly.
//
// Provenance never returns a value, only a Source — see Source's doc comment
// for why that is a type-level guarantee, not just a convention. This means
// it is always safe to print Provenance's result even for a config.Secret
// field: "env beat file" carries no information about what the secret is.
//
// # Why this is a second pass rather than something Load records
//
// Once file and environment values are merged into the Config struct by
// Load's single koanf instance, "value" is all that is left — the layer that
// produced it is gone (this is exactly why renderSecretField's "value"
// source, in internal/configexport, cannot distinguish YAML from env
// either). Recording provenance therefore has to happen while the layers are
// still distinguishable, which means walking the same layers Load does.
//
// Provenance is NOT a guess reconstructed from Load's *output* — it mirrors
// Load's own steps 1-3 (defaults, optional file, environment) using the
// IDENTICAL koanf providers, prefix, delimiter and env transform Load uses,
// snapshotting the merged view after each stage and diffing consecutive
// snapshots. Given the same inputs (the same file content and the same
// environment) it produces exactly the layer assignment Load's own merge
// would have made — it just does the diffing Load itself has no reason to
// keep.
//
// Two things this deliberately does NOT attempt, both out of scope for a
// second, read-only pass and both already handled elsewhere:
//   - Load's steps 0 and 2b (rejecting a TS2OTEL_* var that indexes a
//     struct-slice, or a file key that maps to no known config key) are hard
//     Load errors; Provenance is only ever called after Load has already
//     succeeded (see cmd/tailscale2otel), so those cases cannot occur here.
//   - Steps 4-5 (#310 path resolution, *_file secret resolution) are
//     post-processing over the merged struct, not a fourth layering source:
//     a "*_file" field's OWN provenance (file vs env) is already reported
//     like any other string key by this same mechanism, and
//     internal/configexport already reports a Secret field resolved that way
//     as Source: "file" using the sibling-field convention — this function
//     does not need to re-derive that.
//
// The one honest caveat: this performs its own independent read of path and
// the environment, a moment after (not atomically with) whatever Load call
// it is describing. A file or environment change landing in that window
// would make Provenance's answer describe a slightly different load than the
// one that actually ran. For the CLI's own use (load, then immediately ask
// for provenance, single-shot process) this window is not realistically
// observable.
func Provenance(path string) (map[string]Source, error) {
	k := koanf.New(keyDelim)

	if err := k.Load(structs.Provider(Default(), "yaml"), nil); err != nil {
		return nil, fmt.Errorf("load defaults: %w", err)
	}
	afterDefaults := snapshotKeys(k)

	if path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	afterFile := snapshotKeys(k)

	if err := k.Load(env.Provider(keyDelim, env.Opt{
		Prefix:        EnvPrefix,
		TransformFunc: envTransform,
	}), nil); err != nil {
		return nil, fmt.Errorf("load environment: %w", err)
	}
	afterEnv := snapshotKeys(k)

	out := make(map[string]Source, len(afterEnv))
	for key, envVal := range afterEnv {
		switch {
		case !valuesEqual(envVal, afterFile[key]):
			out[key] = SourceEnv
		case !valuesEqual(afterFile[key], afterDefaults[key]):
			out[key] = SourceFile
		default:
			out[key] = SourceDefault
		}
	}
	return out, nil
}

// valuesEqual compares two leaf snapshot values for provenance purposes.
// Plain reflect.DeepEqual is NOT enough here: the defaults snapshot comes
// from structs.Provider walking the real, typed *Config (so a leaf can be a
// config.Secret or a config.Duration, or a real []string), while the
// file/env snapshots come from koanf's YAML/env parsing (always an
// untyped string, float64/int, bool, or []interface{}) — decoding into the
// typed struct only happens later, in Load's own mapstructure step, which
// Provenance deliberately does not repeat (see Provenance's doc comment).
// Comparing those directly makes a file that writes the SAME value as the
// default (e.g. this repo's own config.example.yaml writing admin.auth.token:
// "" over a default of "", or status_refresh_interval: 5s over a default of
// 5s) look like a change:
//   - config.Secret("") != string("") as Go values — confirmed live against
//     config.example.yaml before this fix landed (admin.auth.token reported
//     SourceFile despite matching the default byte for byte).
//   - config.Duration doesn't embed time.Duration's String() method (it is a
//     distinct named int64 type, not an alias), so its DEFAULT-layer
//     rendering is a raw nanosecond integer, while the FILE/env layer is
//     still the unparsed YAML string ("5s") — "5000000000" vs "5s" for an
//     identical value.
//
// normalizeValue below renders both sides into ONE canonical text form
// first, so the comparison is by MEANING, not by which layer's Go type
// produced it.
func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return normalizeValue(a) == normalizeValue(b)
}

// normalizeValue renders v as text for valuesEqual's comparison: a
// config.Duration becomes its human duration string (matching how it would
// have been written in YAML or an env var), and everything else falls
// through to fmt.Sprint, which already renders content rather than concrete
// type (config.Secret("x") and string("x") both print "x"; []string{"a"}
// and []interface{}{"a"} both print "[a]").
func normalizeValue(v any) string {
	if d, ok := v.(Duration); ok {
		return time.Duration(d).String()
	}
	return fmt.Sprint(v)
}

// snapshotKeys captures every key currently in k, keyed by its dotted path,
// so two snapshots taken before/after a k.Load call can be diffed to see
// exactly which keys that layer changed.
//
// This flattens k.Raw() itself, rather than using k.Keys()/k.Get(), because
// koanf's own Keys() does NOT index into a slice-of-maps the way
// internal/configexport.Build does: after loading a YAML file directly (as
// opposed to structs.Provider walking an actual populated Go struct), a
// multi-tailnet "tailnets" list comes back as ONE key holding a
// []map[string]any value, not "tailnets.0.name" / "tailnets.1.name" — a real
// gap found by TestProvenance_MultiTailnet. flattenTree below applies the
// identical "recurse into a slice-of-maps by index, else treat as a leaf"
// rule Build uses, so the two packages' dotted keys line up.
func snapshotKeys(k *koanf.Koanf) map[string]any {
	out := map[string]any{}
	flattenTree("", k.Raw(), out)
	return out
}

// flattenTree recurses into v (a map[string]any, a []any of map[string]any
// entries, or a leaf) and records one entry per dotted path into out — the
// same "map recurses by key, slice-of-maps recurses by index, anything else
// is a leaf" shape internal/configexport.Build's reflect walk uses over the
// typed Config struct, applied here to koanf's untyped raw tree instead.
func flattenTree(prefix string, v any, out map[string]any) {
	switch tv := v.(type) {
	case map[string]any:
		// An empty map (otlp.headers, profiling.pyroscope.tags with nothing
		// configured) is a LEAF, not "zero children to recurse into" —
		// otherwise it never gets an entry in the snapshot at all, and
		// BuildWithProvenance silently reports no provenance for it (found
		// live against config.example.yaml: otlp.headers and
		// profiling.pyroscope.tags both came back with no "provenance" key).
		// This matches internal/configexport.Build's own "empty collection
		// is a leaf" convention. The root call (prefix == "", the whole
		// config tree) is exempted so a config that somehow decoded to zero
		// top-level keys doesn't collapse into one meaningless "" entry.
		if len(tv) == 0 && prefix != "" {
			out[prefix] = v
			return
		}
		for key, child := range tv {
			flattenTree(joinKey(prefix, key), child, out)
		}
	case []any:
		if isSliceOfMaps(tv) {
			for i, child := range tv {
				flattenTree(fmt.Sprintf("%s%s%d", prefix, sep(prefix), i), child, out)
			}
			return
		}
		out[prefix] = v
	default:
		out[prefix] = v
	}
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + keyDelim + key
}

func sep(prefix string) string {
	if prefix == "" {
		return ""
	}
	return keyDelim
}

// isSliceOfMaps reports whether every element of s is a map[string]any — the
// "struct slice" shape (tailnets, streaming/webhook routes, node-metrics
// targets) that gets recursed into by index, as opposed to a scalar slice
// ([]string, e.g. metric_allow) which is a leaf. An empty slice is not a
// slice of maps (nothing to recurse into) and is treated as a leaf, matching
// Build's own empty-collection convention.
func isSliceOfMaps(s []any) bool {
	if len(s) == 0 {
		return false
	}
	for _, e := range s {
		if _, ok := e.(map[string]any); !ok {
			return false
		}
	}
	return true
}
