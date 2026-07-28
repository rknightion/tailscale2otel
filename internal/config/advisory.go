package config

import "strings"

// Advisory is one non-fatal configuration warning, split into the config key it
// concerns and the explanation.
//
// It exists because Warnings() returned prose that was logged once at startup
// and otherwise reduced to a COUNT on tailscale2otel.config.warnings. An
// operator alerting on that gauge learned that something was wrong and then had
// to go and find the startup log to learn what — on a pod that may well have
// restarted since (#319).
//
// Advisories are DERIVED from Warnings rather than replacing it: every warning
// message already begins with the key it is about, by a convention every call
// site follows, so splitting is enough to make the list navigable without
// rewriting thirty-odd messages. Warnings() remains the string form for the
// startup log, and a test asserts the two are the same set — otherwise the page
// becomes a second, quieter source of truth.
type Advisory struct {
	// Key is the dotted config path the advisory concerns
	// ("tailscale.auth.method"), or "" when the message does not start with one.
	Key string `json:"key,omitempty"`
	// Message is the warning VERBATIM, not the remainder after the key was
	// split off. Two of the existing messages open with a clause rather than a
	// bare key ("prometheus.enabled with no prometheus.auth.token on ...") and
	// one carries its value inline ("tailscale.auth.method=apikey: ..."), so
	// anything other than the original text loses information. Key is metadata
	// for grouping and filtering; Message is the record.
	Message string `json:"message"`
}

// Advisories returns Warnings() split into key and message, in the same order.
func (c *Config) Advisories() []Advisory {
	warnings := c.Warnings()
	out := make([]Advisory, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, Advisory{Key: advisoryKey(w), Message: w})
	}
	return out
}

// advisoryKey pulls the config key a warning is about off the front of its
// message. Every message opens with the setting it concerns, in one of three
// shapes seen across the current set:
//
//	"some.key=value: explanation"
//	"some.key: explanation"
//	"some.key is served on ...: explanation"
//
// so the FIRST token decides it, not the text before the first colon — that
// would swallow a whole clause for the third shape.
//
// It is deliberately strict: anything that is not a plausible dotted config path
// yields "" rather than being promoted to a key, because a key column holding
// half a sentence is worse than an empty one.
func advisoryKey(w string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(w), " ")
	// Trim what separates the key from the rest: "key=value" and "key:".
	head, _, _ = strings.Cut(head, "=")
	head = strings.TrimRight(head, ":,.")
	if !isConfigPath(head) {
		return ""
	}
	return head
}

// isConfigPath reports whether s looks like a dotted config key rather than
// prose: non-empty, no whitespace, and made only of the characters config paths
// use.
func isConfigPath(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '[', r == ']':
		default:
			return false
		}
	}
	// A bare word with no dot is a sentence opener far more often than a config
	// key, and every real key in this config is at least two segments deep.
	return strings.Contains(s, ".")
}
