// Package configexport renders the complete effective application
// configuration as a deterministically-keyed, redacted projection.
//
// It exists as a leaf package rather than living in internal/app because three
// separate surfaces need the same projection and the same redaction rules:
// the admin /api/config.json endpoint (#320), the -print-effective-config CLI
// flag (#309), and the support bundle (#321). A CLI flag and a bundle writer
// cannot call an unexported helper inside package app, and a second copy of
// the redaction rules is exactly the failure mode this tracker has hit three
// times already (#297's stale window, #320's own URL redaction, #316's
// certificate reloader) — every time, one copy was wrong.
//
// The output type stays in internal/app/statusdata, which is a
// dependency-free leaf serving both the HTML page and the JSON endpoint; this
// package depends on it rather than the other way round, so that property is
// preserved.
package configexport

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/config"
	"github.com/rknightion/tailscale2otel/v4/internal/redact"
)

// secretConfigType and durationConfigType are the two config-package scalar
// types Build treats specially: config.Secret (redact by type,
// #320) and config.Duration (render as a human duration string rather than a
// raw int64 of nanoseconds).
var (
	secretConfigType   = reflect.TypeOf(config.Secret(""))
	durationConfigType = reflect.TypeOf(config.Duration(0))
)

// Build reflects over the effective *config.Config and produces a
// complete, deterministically-keyed projection (#320): every config key,
// dotted-path keyed to match the TS2OTEL_* env-var convention (env joins
// levels with "__"; this joins with "."). It supersedes nothing in
// redactedConfigSummary above — that hand-picked subset stays for backward
// compatibility — but is the field to read for "the whole effective config".
//
// Unlike a hand-maintained field list (the exact failure #320 is about), this
// walk cannot silently drop a newly added config field: it is a generic
// reflect.Value walk over every EXPORTED struct field, so a new field is
// visited automatically. TestBuildFullConfigMap_CompletenessMatchesDefaultKeys
// asserts the key set this produces for config.Default() matches the key set
// internal/config/completeness_test.go's own flattenYAMLKeys derives from the
// same value, so an addition this walker's type-switch cannot render (an
// unhandled Go kind) still fails loudly rather than being silently omitted.
//
// Redaction is by TYPE, never by field name:
//   - A config.Secret-typed leaf (scalar, or a map[string]Secret entry) never
//     renders its Value — only Set + Source (see renderSecretField).
//   - A generic map[string]string ALSO redacts its values (keys only). Nothing
//     marks "this string map can carry a credential" at the type level the way
//     map[string]Secret does — that conversion (#73) is precisely what lets
//     otlp.headers and the node-metrics per-target headers be handled by the
//     first rule instead. The two remaining plain string maps in this config
//     (profiling.pyroscope.tags, a node-metrics target's labels) are, as far
//     as this code can tell, operator-supplied non-secret tags/labels — but
//     nothing enforces that, so a plain string map redacts its values here as
//     a conservative default rather than trusting the current field list to
//     stay that way.
func Build(cfg *config.Config) map[string]statusdata.ConfigFieldValue {
	out := map[string]statusdata.ConfigFieldValue{}
	walkConfigField(reflect.ValueOf(cfg).Elem(), "", out)
	return out
}

// configYAMLFieldName returns f's dotted-path segment: the name portion of its
// `yaml` struct tag (options after a comma, if any were ever added, are
// ignored), or "" for a field with no yaml tag (skipped by the caller — today
// that is only the three unexported bookkeeping fields on Config, which
// reflect already excludes via PkgPath before this is even called).
func configYAMLFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

// walkConfigField renders v (a config.Config, or any value reachable from one)
// into out, keyed by path. See Build for the redaction rules.
func walkConfigField(v reflect.Value, path string, out map[string]statusdata.ConfigFieldValue) {
	switch v.Kind() {
	case reflect.Pointer:
		// The only pointer field in config.Config is
		// NodeMetricsTarget.TLS (*NodeMetricsTargetTLS); nil (the common case —
		// plain HTTP targets) renders as an explicit empty leaf rather than
		// being silently skipped, so the key is still present.
		if v.IsNil() {
			out[path] = statusdata.ConfigFieldValue{}
			return
		}
		walkConfigField(v.Elem(), path, out)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue // unexported bookkeeping field (unknownEnv, ...) — not a config key
			}
			name := configYAMLFieldName(f)
			if name == "" {
				continue
			}
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			fv := v.Field(i)
			if fv.Type() == secretConfigType {
				out[childPath] = renderSecretField(v, t, i)
				continue
			}
			walkConfigField(fv, childPath, out)
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Struct {
			// A struct slice (tailnets, streaming/webhook routes, node-metrics
			// targets) is the #320 multi-tailnet/dynamic-list case: recurse into
			// whatever is actually configured, index by position. An empty slice
			// still needs a leaf entry (matching the convention
			// internal/config/completeness_test.go's flattenYAMLKeys already
			// uses: an empty YAML list is a leaf, not something to recurse into).
			if v.Len() == 0 {
				out[path] = statusdata.ConfigFieldValue{Value: []any{}}
				return
			}
			for i := 0; i < v.Len(); i++ {
				walkConfigField(v.Index(i), fmt.Sprintf("%s.%d", path, i), out)
			}
			return
		}
		// A scalar slice ([]string, e.g. metric_allow) is not sensitive — render
		// it directly, nil or not (json encodes a nil []string as null, an empty
		// one as [], same as everywhere else in this codebase).
		out[path] = statusdata.ConfigFieldValue{Value: v.Interface()}
	case reflect.Map:
		renderMapField(v, path, out)
	case reflect.String:
		// A string field is not sensitive by TYPE the way config.Secret is, but
		// several (otlp.endpoint, profiling.pyroscope.server_address, a
		// node-metrics target's url, an object store's endpoint) are
		// operator-facing URLs that MAY carry embedded userinfo or a signed
		// query — exactly the credential shape buildStatus already strips with
		// redact.URL for the few fields it names explicitly
		// (TestStatusJSON_NoURLCredentials). Rather than hand-listing those
		// fields here too (the #320 failure mode), detect the SHAPE instead:
		// any string containing "://" is passed through redact.URL, whatever
		// field it came from. A non-URL string (log levels, listen addresses
		// like "127.0.0.1:9091", collector names, enum values) never contains
		// "://" and is rendered unchanged — redact.URL itself would mistake a
		// bare "host:port" for a URL with an opaque scheme and mangle it into
		// "(unparseable url)", so the "://" gate matters and is not redundant.
		out[path] = renderStringValue(v.String())
	default:
		if v.Type() == durationConfigType {
			out[path] = statusdata.ConfigFieldValue{Value: time.Duration(v.Int()).String()}
			return
		}
		out[path] = statusdata.ConfigFieldValue{Value: v.Interface()}
	}
}

// renderStringValue renders one string config value. A string is not sensitive
// by TYPE the way config.Secret is, but several (otlp.endpoint,
// profiling.pyroscope.server_address, a node-metrics target's url, an object
// store's endpoint) are operator-facing URLs that MAY carry embedded userinfo
// or a signed query — exactly the credential shape buildStatus already strips
// with redact.URL for the few fields it names explicitly
// (TestStatusJSON_NoURLCredentials). Rather than hand-listing those fields
// here too (the #320 failure mode), this detects the SHAPE: any string
// containing "://" goes through redact.URL, whatever field or map value it
// came from.
//
// The "://" gate is load-bearing, not redundant. redact.URL on a bare
// "127.0.0.1:9091" listen address parses it as a URL with the opaque scheme
// "127.0.0.1" and mangles it into "(unparseable url)", so applying it
// unconditionally would corrupt every *.listen field.
//
// This is the ONE definition of the rule, shared by struct string fields and
// string map values — they had drifted into two spellings, and only one of
// them redacted.
func renderStringValue(s string) statusdata.ConfigFieldValue {
	if strings.Contains(s, "://") {
		return statusdata.ConfigFieldValue{Value: redact.URL(s)}
	}
	return statusdata.ConfigFieldValue{Value: s}
}

// renderMapField renders a map field (otlp.headers, a node-metrics target's
// headers/labels, profiling.pyroscope.tags) as one output entry per key
// (sorted, for a deterministic map iteration order), or a single empty-object
// leaf when the map has no entries — again mirroring the config completeness
// test's "empty collection is a leaf" convention.
//
// map[string]Secret entries (otlp.headers, a node-metrics target's headers)
// are redacted by TYPE, like any other Secret. Plain map[string]string entries
// (profiling.pyroscope.tags, a node-metrics target's labels) are NOT: those
// values are already published verbatim to the observability backend as
// profile tags and metric attributes (see pyroscopeConfig and
// nodemetrics.mergeAttrs), so hiding them on a local admin page protects
// nothing while costing exactly the operator asking "why is my label not
// applied" the one place they could have checked. They still pass through the
// same URL-shape redaction as any other string.
func renderMapField(v reflect.Value, path string, out map[string]statusdata.ConfigFieldValue) {
	if v.Len() == 0 {
		out[path] = statusdata.ConfigFieldValue{Value: map[string]any{}}
		return
	}
	keys := make([]string, 0, v.Len())
	for _, k := range v.MapKeys() {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)
	elemType := v.Type().Elem()
	for _, k := range keys {
		mv := v.MapIndex(reflect.ValueOf(k))
		childPath := path + "." + k
		switch {
		case elemType == secretConfigType:
			set := mv.Interface().(config.Secret) != ""
			source := "unset"
			if set {
				source = "value" // map entries have no "*_file" sibling convention
			}
			out[childPath] = statusdata.ConfigFieldValue{Secret: true, Set: set, Source: source}
		case elemType.Kind() == reflect.String:
			out[childPath] = renderStringValue(mv.String())
		default:
			out[childPath] = statusdata.ConfigFieldValue{Value: mv.Interface()}
		}
	}
}

// renderSecretField renders the config.Secret-typed field at index idx of
// struct value sv (type st): Set reports whether it holds a value, and Source
// distinguishes "unset", "value" (set directly — via YAML or a TS2OTEL_* env
// var; Load resolves both into the same field, so they cannot be told apart
// after the fact), or "file" — resolved from a paired "*_file" sibling field
// by the #169 seam-freeze convention (config.resolveSecretFiles), which this
// finds generically by field NAME (f.Name+"File") rather than a hand-maintained
// list, so it also covers the one Secret field that convention does NOT
// pre-resolve at Load: NodeMetricsTarget.BearerToken, whose BearerTokenFile is
// read fresh per scrape by the node-metrics collector instead. A file path set
// with no value ever loaded into this field still correctly reports
// source=file, because filePath != "" is checked before set.
func renderSecretField(sv reflect.Value, st reflect.Type, idx int) statusdata.ConfigFieldValue {
	f := st.Field(idx)
	secret := sv.Field(idx).Interface().(config.Secret)
	set := secret != ""
	source := "unset"
	if ff, ok := st.FieldByName(f.Name + "File"); ok && ff.Type.Kind() == reflect.String {
		if sv.FieldByName(f.Name+"File").String() != "" {
			source = "file"
		} else if set {
			source = "value"
		}
	} else if set {
		source = "value"
	}
	return statusdata.ConfigFieldValue{Secret: true, Set: set, Source: source}
}
