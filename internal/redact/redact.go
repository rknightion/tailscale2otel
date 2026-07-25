// Package redact strips reusable credential material out of values that are
// about to reach a lower-trust surface — a log line, a span, the admin status
// page or its JSON API.
//
// It is a deliberate leaf package (standard library only) so every layer, from
// config validation up to the admin HTTP handlers, can depend on it without
// creating an import cycle.
package redact

import (
	"net/url"
	"strings"
)

// placeholder replaces the whole value when it cannot be parsed. Echoing an
// unparseable URL is the one case where redaction cannot be reasoned about, so
// the value is dropped entirely rather than passed through.
const placeholder = "(unparseable url)"

// marker is what replaces each removed piece. It is deliberately not empty so a
// reader can tell "there was a credential here" from "there was nothing here".
const marker = "redacted"

// URL returns raw with every credential-bearing part removed while keeping the
// parts that make it useful for diagnostics:
//
//   - userinfo (https://user:password@host/...) is replaced with "redacted"
//   - every query VALUE is replaced with "redacted"; the query KEY names are
//     kept, because knowing that a request carried "api_key" or "sig" is the
//     diagnostic signal and the key name is not itself a secret
//   - the fragment is dropped entirely (nothing downstream needs it, and a
//     fragment is a common place to hide a token)
//   - scheme, host, port and path survive unchanged
//
// An empty input returns empty. An unparseable input returns a fixed
// placeholder rather than the original string.
//
// Path segments are NOT redacted: a credential embedded in a path is
// indistinguishable from a legitimate route, so callers that accept such URLs
// must reject them at config time instead.
func URL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return placeholder
	}
	if u.User != nil {
		u.User = url.User(marker)
	}
	if u.RawQuery != "" {
		u.RawQuery = redactQuery(u.RawQuery)
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// HasUserinfo reports whether raw is a parseable URL carrying userinfo
// credentials (the "user:password@" form). Config validation uses it to reject
// credentials embedded in a URL where a dedicated secret field expresses the
// same thing. An unparseable value is not classified here — a separate shape
// check owns that — so this never turns a parse problem into a credential
// verdict.
func HasUserinfo(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.User != nil
}

// redactQuery replaces every value in an already-encoded query string, keeping
// key order and duplicates. It works on the raw string rather than url.Values
// so the output stays a faithful (if redacted) rendering of what was
// configured, instead of a re-sorted, re-encoded approximation.
func redactQuery(rawQuery string) string {
	parts := strings.Split(rawQuery, "&")
	out := parts[:0]
	for _, p := range parts {
		if p == "" {
			continue
		}
		k, _, ok := strings.Cut(p, "=")
		if !ok {
			// A bare flag ("?debug") carries no value to strip.
			out = append(out, k)
			continue
		}
		out = append(out, k+"="+marker)
	}
	return strings.Join(out, "&")
}
