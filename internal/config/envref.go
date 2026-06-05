package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// The environment-variable reference (docs/env-vars.md) is generated from
// config.example.yaml: every leaf key, its default value as written, and its
// inline comment become one table row. config.example.yaml is itself locked to
// Default() by TestExampleConfigMatchesDefaults / TestExampleConfigCoversEveryKey,
// so the example is a faithful, single source for the reference. The generated
// table lives between these markers; prose outside them is hand-written.
const (
	envDocBegin = "<!-- BEGIN GENERATED: env-vars -->"
	envDocEnd   = "<!-- END GENERATED: env-vars -->"
)

// envRow is one row of the environment-variable reference.
type envRow struct {
	Key      string // dotted config key
	EnvVar   string // TS2OTEL_* variable name ("" when file-only)
	Default  string // default value as written in config.example.yaml
	Desc     string // the field's inline comment
	FileOnly bool   // structured value (map / list of structs) — no flat env var
	List     bool   // []string field — the env value is comma-separated
}

// envVarName derives the environment variable for a dotted config key: prepend
// EnvPrefix, replace the key delimiter with the "__" nesting delimiter, and
// uppercase. Single underscores inside a name are preserved (client_id stays
// CLIENT_ID), which makes the transform unambiguous in this direction.
func envVarName(key string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(key, keyDelim, envNestDelim))
}

// envReferenceRows walks config.example.yaml in document order and returns one
// row per leaf key (a scalar, a list, or an empty map/struct-list).
func envReferenceRows(exampleYAML []byte) ([]envRow, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(exampleYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse example yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("example yaml: expected a top-level mapping")
	}

	var rows []envRow
	var walk func(prefix, inherited string, m *yaml.Node)
	walk = func(prefix, inherited string, m *yaml.Node) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			k, v := m.Content[i], m.Content[i+1]
			key := k.Value
			if prefix != "" {
				key = prefix + keyDelim + k.Value
			}
			// A non-empty mapping is a section to recurse into; an empty
			// mapping ({}) is a leaf (a file-only map like otlp.headers).
			if v.Kind == yaml.MappingNode && len(v.Content) > 0 {
				// Children that carry no comment of their own (e.g. a
				// collector's bare `enabled:`) inherit this section's comment.
				walk(key, firstNonEmpty(nodeComment(k), inherited), v)
				continue
			}
			rows = append(rows, leafRow(key, k, v, inherited))
		}
	}
	walk("", "", doc.Content[0])
	return rows, nil
}

func leafRow(key string, k, v *yaml.Node, inherited string) envRow {
	r := envRow{Key: key, Desc: firstNonEmpty(nodeComment(v), nodeComment(k), inherited)}
	switch v.Kind {
	case yaml.MappingNode: // empty map {} → file-only
		r.FileOnly = true
		r.Default = "{}"
	case yaml.SequenceNode:
		r.Default = seqDefault(v)
		if listEnvKeys[key] {
			r.List = true
			r.EnvVar = envVarName(key)
		} else {
			r.FileOnly = true // list of structs (collectors.node_metrics.targets)
		}
	default: // scalar
		r.Default = scalarDefault(v)
		r.EnvVar = envVarName(key)
	}
	return r
}

// nodeComment returns a node's trailing inline comment ("# …"), stripped. Only
// LineComment is used (never HeadComment) so the section banner lines above a
// key are not mistaken for its description.
func nodeComment(n *yaml.Node) string {
	return strings.TrimSpace(strings.TrimPrefix(n.LineComment, "#"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func scalarDefault(v *yaml.Node) string {
	if v.Value == "" {
		return `""`
	}
	return v.Value
}

func seqDefault(v *yaml.Node) string {
	items := make([]string, 0, len(v.Content))
	for _, it := range v.Content {
		items = append(items, it.Value)
	}
	return "[" + strings.Join(items, ", ") + "]"
}

// renderEnvReference produces the generated block (the markdown table plus the
// file-only note) for docs/env-vars.md.
func renderEnvReference(exampleYAML []byte) (string, error) {
	rows, err := envReferenceRows(exampleYAML)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("| Environment variable | Default | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	var fileOnly []string
	for _, r := range rows {
		if r.FileOnly {
			fileOnly = append(fileOnly, r.Key)
			continue
		}
		desc := escapePipes(r.Desc)
		if r.List {
			desc += " _(comma-separated list)_"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.EnvVar, escapePipes(r.Default), desc)
	}
	if len(fileOnly) > 0 {
		b.WriteString("\n**File-only** — these take structured values (a map or a list of objects) and must be set in the YAML config, not via an environment variable: ")
		for i, k := range fileOnly {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "`%s`", k)
		}
		b.WriteString(".\n")
	}
	return b.String(), nil
}

// spliceEnvReference replaces the content between the generated markers in doc
// with block, preserving all hand-written prose outside them.
func spliceEnvReference(doc, block string) (string, error) {
	bi := strings.Index(doc, envDocBegin)
	ei := strings.Index(doc, envDocEnd)
	if bi < 0 || ei < 0 || ei < bi {
		return "", fmt.Errorf("docs/env-vars.md is missing the %q / %q markers", envDocBegin, envDocEnd)
	}
	return doc[:bi+len(envDocBegin)] + "\n\n" + block + "\n" + doc[ei:], nil
}

func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
