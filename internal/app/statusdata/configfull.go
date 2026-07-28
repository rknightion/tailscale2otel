package statusdata

// ConfigFieldValue is one leaf of the complete effective-configuration export
// (issue #320): the /api/config.json endpoint previously exposed only a small
// hand-picked subset (ConfigSummary's named fields above), which meant the one
// endpoint an operator reaches for to debug a config problem was exactly the
// one that omitted the field they needed. ConfigFull fixes that by reflecting
// over the live *config.Config (internal/configexport.Build)
// and rendering EVERY key, keyed by the same dotted path the rest of the repo
// already uses for config keys (matching the TS2OTEL_* env-var convention with
// "." in place of "__", e.g. "otlp.endpoint", "tailnets.0.auth.method").
//
// Redaction here is structural, not name-based: a field is only ever rendered
// through the Secret/Set/Source form below when its SOURCE TYPE is
// config.Secret — including a map[string]Secret entry, which is how
// otlp.headers and the node-metrics per-target headers are covered. A generic
// map[string]string (profiling.pyroscope.tags, a node-metrics target's labels)
// is NOT redacted: those values are already published verbatim to the backend
// as profile tags and metric attributes, so hiding them locally protects
// nothing and costs the operator asking "why is my label not applied" the one
// place they could check. Every other field renders through Value, with any
// string containing "://" passed through redact.URL to strip embedded
// userinfo. There is no field-name denylist anywhere in this path.
type ConfigFieldValue struct {
	// Value is the field's plain rendering for a non-secret field: a string,
	// bool, number, []string, or (for an EMPTY map/slice, matching the leaf
	// convention internal/config/completeness_test.go already uses for the
	// config-key surface) an empty placeholder. Always omitted when Secret is
	// true — a secret-rendered field never carries a Value, so there is no
	// code path that can accidentally populate both.
	Value any `json:"value,omitempty"`
	// Secret marks a field whose value is never shown — only presence and
	// origin. True for every config.Secret-typed field, including every entry
	// of a map[string]Secret. A generic map[string]string is not secret-typed
	// and renders through Value (see internal/configexport for why).
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
