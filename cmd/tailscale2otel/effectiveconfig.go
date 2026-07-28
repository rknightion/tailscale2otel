package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/rknightion/tailscale2otel/v3/internal/config"
	"github.com/rknightion/tailscale2otel/v3/internal/configexport"
	"gopkg.in/yaml.v3"
)

// effectiveConfigEntry is one row of -print-effective-config's output: the
// dotted config key alongside its effective, redacted value (see
// internal/configexport for the redaction rules) and, when
// -print-effective-config-provenance is set, which layer produced it.
//
// This is a flat, explicitly-keyed SLICE (sorted by Key), not a nested
// object keyed by dotted path — a slice is deterministic to encode in both
// JSON and YAML without any extra ordering work, where a map is only
// guaranteed deterministic for encoding/json (which sorts map[string]T keys)
// and NOT for yaml.v3 (which does not).
type effectiveConfigEntry struct {
	Key string `json:"key" yaml:"key"`
	// Value is the field's redacted rendering; always omitted for a Secret
	// field (see configexport.ConfigFieldValue).
	Value any `json:"value,omitempty" yaml:"value,omitempty"`
	// Secret marks a field whose value is never shown — only presence and
	// origin (Set/Source below).
	Secret bool `json:"secret,omitempty" yaml:"secret,omitempty"`
	// Set reports whether a Secret field holds a value. Only meaningful when
	// Secret is true.
	Set bool `json:"set,omitempty" yaml:"set,omitempty"`
	// Source is "unset", "value", or "file" for a Secret field only — see
	// configexport.ConfigFieldValue.Source. Empty for a non-secret field.
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
	// Provenance is "default", "file", or "env": which configuration layer
	// produced this key's effective value, for EVERY key (secret or not).
	// Only populated when -print-effective-config-provenance was passed —
	// left as the empty string (omitted from both JSON and YAML) otherwise,
	// so a reader cannot mistake "not requested" for "default".
	Provenance string `json:"provenance,omitempty" yaml:"provenance,omitempty"`
}

// runPrintEffectiveConfig loads the config at configPath the same way the
// server does, renders EVERY effective key through internal/configexport's
// redaction rules (#320) — the single seam this and the admin status page
// and the support bundle all share, so there is exactly one redaction rule
// to get right, not three — and prints the result as one machine-readable
// document (JSON by default, or YAML), then exits. It never starts the
// exporter and never offers an unredacted mode (#309's own acceptance
// criterion): there is no flag, anywhere in this command, that bypasses
// configexport's redaction.
//
// format must be "json" or "yaml"; any other value is a usage error (exit
// 2), the same class of error flag.Parse itself would produce, rather than
// silently falling back to a default. On a config load failure the error
// goes to stderr and stdout stays completely empty (#309's own
// error/stdout split) — never a partial or garbage effective-config
// document.
func runPrintEffectiveConfig(configPath, format string, withProvenance bool, stdout, stderr io.Writer) int {
	if format != "json" && format != "yaml" {
		fmt.Fprintf(stderr, "invalid -print-effective-config-format %q: must be \"json\" or \"yaml\"\n", format)
		return 2
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "config invalid: %v\n", err)
		return 1
	}

	var entries []effectiveConfigEntry
	if withProvenance {
		fields, provErr := configexport.BuildWithProvenance(cfg, configPath)
		if provErr != nil {
			fmt.Fprintf(stderr, "compute config provenance: %v\n", provErr)
			return 1
		}
		entries = make([]effectiveConfigEntry, 0, len(fields))
		for key, v := range fields {
			entries = append(entries, effectiveConfigEntry{
				Key:        key,
				Value:      v.Value,
				Secret:     v.Secret,
				Set:        v.Set,
				Source:     v.Source,
				Provenance: string(v.Provenance),
			})
		}
	} else {
		fields := configexport.Build(cfg)
		entries = make([]effectiveConfigEntry, 0, len(fields))
		for key, v := range fields {
			entries = append(entries, effectiveConfigEntry{
				Key:    key,
				Value:  v.Value,
				Secret: v.Secret,
				Set:    v.Set,
				Source: v.Source,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })

	if format == "yaml" {
		enc := yaml.NewEncoder(stdout)
		enc.SetIndent(2)
		if err := enc.Encode(entries); err != nil {
			fmt.Fprintf(stderr, "encode effective config: %v\n", err)
			return 1
		}
		if err := enc.Close(); err != nil {
			fmt.Fprintf(stderr, "encode effective config: %v\n", err)
			return 1
		}
		return 0
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		fmt.Fprintf(stderr, "encode effective config: %v\n", err)
		return 1
	}
	return 0
}
