package contract

// Evidence sanitization (#424).
//
// The live lane runs against Rob's real tailnet but reports into a PUBLIC
// GitHub issue via the report-drift composite action. Everything that crosses
// that boundary goes through SanitizeEvidence first.
//
// The rule is: strip what identifies the tailnet or a person, keep what makes
// the failure diagnosable. Operation ids, decode-error text, JSON field names,
// status codes and Go type names all survive — they are the entire value of the
// report and none of them identify anything.

import (
	"regexp"
	"sort"
	"strings"
)

// Redaction placeholders. They are deliberately chosen to match none of the
// patterns below, so sanitizing an already-sanitized report is a no-op
// (TestSanitizeEvidence_IsIdempotent).
const (
	redactedGeneric = "[redacted]"
	redactedSecret  = "[redacted-secret]"
	redactedEmail   = "[redacted-email]"
	redactedHost    = "[redacted-host]"
	redactedIP      = "[redacted-ip]"
	redactedNode    = "[redacted-node]"
	redactedDevice  = "[redacted-device]"
	redactedTailnet = "[redacted-tailnet]"
	redactedService = "[redacted-service]"
)

// Each rule is applied in order. Order matters where one pattern would
// otherwise consume text another needs (see the notes on individual rules).
var sanitizeRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Credentials first: an Authorization header value can contain anything, and
	// redacting it wholesale is safer than pattern-matching each token flavor.
	{regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic|token)\s+)\S+`), "${1}" + redactedSecret},
	// Tailscale API/auth keys wherever else they appear (logs, URLs, error text).
	{regexp.MustCompile(`\btskey-[A-Za-z0-9_-]+`), redactedSecret},

	// Email addresses (invite recipients, login names, contact addresses). Run
	// before the hostname rules: the domain half would otherwise be left behind.
	{regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), redactedEmail},

	// MagicDNS names — <host>.<tailnet-stable-id>.ts.net. Both halves identify.
	{regexp.MustCompile(`(?i)\b[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.ts\.net\b`), redactedHost},

	// IPv6. Two shapes, both anchored on \b so they can never start mid-word and
	// eat a diagnostic (e.g. the ":" runs in "decode failed: json: cannot …").
	// The uncompressed form demands 4+ groups so a clock time like 00:00:00 is
	// not mistaken for an address; the compressed form demands a literal "::".
	{regexp.MustCompile(`\b[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4}){3,7}\b`), redactedIP},
	{regexp.MustCompile(`\b[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4})*::(?:[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4})*)?`), redactedIP},

	// IPv4 (CGNAT 100.64/10 tailnet addresses, and anything else — an external
	// peer address seen in a flow log is no less identifying).
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), redactedIP},

	// Stable node ids: an opaque token ending in the CNTRL suffix.
	{regexp.MustCompile(`\b[A-Za-z0-9]{2,}CNTRL\b`), redactedNode},

	// Path segments carrying an identifier. These run last so the placeholders
	// substituted above collapse into the path-specific one rather than the other
	// way round; either way a second pass is stable.
	{regexp.MustCompile(`(/device/)[^/\s?"]+`), "${1}" + redactedDevice},
	{regexp.MustCompile(`(/tailnet/)[^/\s?"]+`), "${1}" + redactedTailnet},
	{regexp.MustCompile(`(/services/)[^/\s?"]+`), "${1}" + redactedService},
}

// SanitizeEvidence redacts tailnet- and person-identifying values from text
// destined for a public issue, while preserving the diagnostic content.
//
// extra holds caller-supplied literals to redact as well — in practice the
// tailnet name, which the tool cannot recognize by shape. Blank and
// whitespace-only entries are IGNORED: the CI call site interpolates an
// environment variable, and redacting "" would replace every position in the
// string and destroy the whole report.
//
// SanitizeEvidence is idempotent: the workflow may pipe a report through more
// than once, and a second pass must not mangle the placeholders.
func SanitizeEvidence(text string, extra ...string) string {
	// Caller literals first, longest first, so a longer literal is not left
	// half-redacted by a shorter one that is a substring of it.
	lits := make([]string, 0, len(extra))
	for _, e := range extra {
		if strings.TrimSpace(e) != "" {
			lits = append(lits, e)
		}
	}
	sort.Slice(lits, func(i, j int) bool { return len(lits[i]) > len(lits[j]) })
	for _, l := range lits {
		text = strings.ReplaceAll(text, l, redactedGeneric)
	}

	for _, r := range sanitizeRules {
		text = r.re.ReplaceAllString(text, r.repl)
	}
	return text
}
