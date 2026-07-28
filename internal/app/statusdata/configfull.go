package statusdata

// ConfigFieldValue is one leaf of the complete effective-configuration export
// (issue #320): the /api/config.json endpoint previously exposed only a small
// hand-picked subset (ConfigSummary's named fields above), which meant the one
// endpoint an operator reaches for to debug a config problem was exactly the
// one that omitted the field they needed. ConfigFull fixes that by reflecting
// over the live *config.Config (internal/app/status.go's buildFullConfigMap)
// and rendering EVERY key, keyed by the same dotted path the rest of the repo
// already uses for config keys (matching the TS2OTEL_* env-var convention with
// "." in place of "__", e.g. "otlp.endpoint", "tailnets.0.auth.method").
//
// Redaction here is structural, not name-based: a field is only ever rendered
// through the Secret/Set/Source form below when its SOURCE TYPE is
// config.Secret, or (a narrower, deliberate exception — see status.go) when it
// is a generic map[string]string, which can carry a credential in a value as
// easily as a header can and has no type-level marker to say otherwise. Every
// other field renders through Value. There is no field-name denylist anywhere
// in this path.
type ConfigFieldValue struct {
	// Value is the field's plain rendering for a non-secret field: a string,
	// bool, number, []string, or (for an EMPTY map/slice, matching the leaf
	// convention internal/config/completeness_test.go already uses for the
	// config-key surface) an empty placeholder. Always omitted when Secret is
	// true — a secret-rendered field never carries a Value, so there is no
	// code path that can accidentally populate both.
	Value any `json:"value,omitempty"`
	// Secret marks a field whose value is never shown — only presence and
	// origin. True for every config.Secret-typed field, and for every
	// non-empty entry of a generic map[string]string (see status.go for why).
	Secret bool `json:"secret,omitempty"`
	// Set reports whether the field holds a value. Only meaningful when Secret
	// is true.
	Set bool `json:"set,omitempty"`
	// Source is "unset", "value" (supplied directly — via the YAML file or a
	// TS2OTEL_* env var; Load resolves both into the same struct field, so
	// they cannot be told apart once loaded), or "file" (resolved from the
	// paired "*_file" sibling by resolveSecretFiles, or — for the node-metrics
	// per-scrape bearer_token_file — supplied only as a file path with no
	// value ever loaded into this field). Only meaningful when Secret is true.
	Source string `json:"source,omitempty"`
}
