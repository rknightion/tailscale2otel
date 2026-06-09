package config

import (
	"encoding/json"
	"log/slog"
)

// Secret is a string-typed config value that holds a credential (API key, OAuth
// client secret, token, password). It redacts itself in all formatting and
// logging output so a stray slog/fmt dump of the config can never leak the
// value; call Reveal to obtain the real string at the point of legitimate use.
type Secret string

// Reveal returns the underlying secret value for legitimate use (e.g. building an
// Authorization header). This is the only way to read the value, so credential
// reads are greppable.
func (s Secret) Reveal() string { return string(s) }

// redact reports the safe rendering: empty stays empty (nothing to hide), a set
// value becomes "REDACTED".
func (s Secret) redact() string {
	if s == "" {
		return ""
	}
	return "REDACTED"
}

// String redacts under the fmt %v/%s/%q verbs.
func (s Secret) String() string { return s.redact() }

// GoString redacts under the fmt %#v verb.
func (s Secret) GoString() string { return `config.Secret("` + s.redact() + `")` }

// LogValue redacts under slog (the app's structured logger).
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.redact()) }

// MarshalJSON redacts under encoding/json (which ignores String()): a future
// debug endpoint or config dump must not become a credential leak.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.redact()) }

// MarshalYAML redacts under yaml marshaling for the same reason.
func (s Secret) MarshalYAML() (any, error) { return s.redact(), nil }
