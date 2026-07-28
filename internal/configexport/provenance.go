package configexport

import (
	"github.com/rknightion/tailscale2otel/v3/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v3/internal/config"
)

// FieldWithProvenance is one leaf of BuildWithProvenance's output: everything
// Build's ConfigFieldValue already reports (redacted value, or Secret/Set/
// Source for a config.Secret-typed field), plus which layer produced the
// EFFECTIVE value overall — the built-in default, the YAML file, or a
// TS2OTEL_* environment variable (see config.Source).
//
// Provenance is independent of ConfigFieldValue.Source: the latter exists
// only for a config.Secret field and distinguishes "unset" / "value" /
// "file" (where "value" cannot tell YAML from env apart — see
// renderSecretField). Provenance is populated for EVERY key, secret or not,
// and can make exactly that YAML-vs-env distinction, because it is computed
// while the layers are still separate (see config.Provenance) rather than
// reconstructed from the merged struct.
type FieldWithProvenance struct {
	statusdata.ConfigFieldValue `json:",inline" yaml:",inline"`
	// Provenance is "default", "file", or "env" — never a value, so this is
	// safe to include even for a Secret-typed key.
	Provenance config.Source `json:"provenance,omitempty" yaml:"provenance,omitempty"`
}

// BuildWithProvenance renders the same complete, redacted projection Build
// does, with one addition: each entry also carries Provenance, the layer
// that produced its effective value (config.Provenance). Build's own
// signature and behavior are unchanged by this — BuildWithProvenance calls
// Build internally and decorates its result, rather than Build growing a
// parameter, because another caller depends on Build's exact shape today.
//
// configPath is passed straight to config.Provenance and must be the SAME
// path used to produce cfg (via config.Load), or the provenance reported
// here will describe a different load than the one cfg actually reflects.
func BuildWithProvenance(cfg *config.Config, configPath string) (map[string]FieldWithProvenance, error) {
	fields := Build(cfg)
	prov, err := config.Provenance(configPath)
	if err != nil {
		return nil, err
	}
	out := make(map[string]FieldWithProvenance, len(fields))
	for key, v := range fields {
		out[key] = FieldWithProvenance{
			ConfigFieldValue: v,
			Provenance:       prov[key],
		}
	}
	return out, nil
}
