package pii

import "net/http"

// boundedStatusDescriptions is the admit-set of span status descriptions that are
// CODE-DEFINED classes rather than free text (#473).
//
// A span's status description is the one free-text surface the OTEL API gives an
// instrumented package, and most writers put an error string in it — which can
// embed anything the upstream error carried. Under the free-text policy that whole
// surface has to go. But a description that is a fixed literal chosen by our own
// code (a receiver's reject reason, an HTTP status text) names an error CLASS: it
// has bounded cardinality, is identical on every occurrence, and can never contain
// an identifier. Keeping those is what preserves diagnosis when the free-text
// details are stripped — otherwise disabling the category turns every failed span
// into an indistinguishable "[redacted]".
//
// Membership is decided here rather than at the call sites because the policy is
// applied centrally in the span exporter (internal/telemetry.piiSpanExporter),
// which sees only the finished span.
//
// A description that is NOT listed fails CLOSED: it is replaced wholesale. So a
// new literal added at a call site loses its diagnostic value until it is listed
// here — never the other way round.
// TestBoundedStatusDescriptionsCoverEveryLiteralSetStatus enforces that.
var boundedStatusDescriptions = map[string]bool{
	// internal/webhook (HMAC-verified webhook receiver).
	"method not allowed":            true,
	"request body exceeds max size": true,
	"failed to read request body":   true,
	"failed to parse webhook body":  true,
	// internal/webhook signature-verification reasons (Server.verify). Not string
	// literals at the SetStatus call, so the source guard cannot see them.
	"missing_signature":   true,
	"malformed_signature": true,
	"stale_timestamp":     true,
	"future_timestamp":    true,
	"bad_signature":       true,

	// internal/stream (Splunk-HEC receiver).
	"auth required":         true,
	"unauthorized":          true,
	"overloaded":            true,
	"body too large":        true,
	"could not read body":   true,
	"too many records":      true,
	"could not parse body":  true,
	"corrupt batch":         true,
	"record decode failure": true,
}

// httpStatusTexts holds every net/http status text, which internal/tsapi's
// transport uses as the status description for a >=400 response
// (http.StatusText(status)). It is a closed, code-defined set naming the HTTP
// error class and carries no request detail, so it survives the free-text policy
// the same way the literals above do.
var httpStatusTexts = func() map[string]bool {
	m := make(map[string]bool, 64)
	for code := 100; code < 600; code++ {
		if txt := http.StatusText(code); txt != "" {
			m[txt] = true
		}
	}
	return m
}()

// BoundedStatusDescription reports whether desc is a code-defined error class
// rather than free text, and therefore survives a disabled free_text_details.
func BoundedStatusDescription(desc string) bool {
	return boundedStatusDescriptions[desc] || httpStatusTexts[desc]
}

// RedactStatusDescription applies the free-text policy to a span status
// description (#473).
//
// Collector errors, recovered panic text and upstream HTTP error strings all
// reach the status description, which is the same class of content as an
// exception.message or an audit detail. Routing it through RedactBody alone was
// the bypass: that only scrubs values carried as ATTRIBUTES, so disabling
// free_text_details removed the exception event while leaving the identical text
// in the status description.
//
// With free_text_details disabled, anything that is not a bounded code-defined
// class (see BoundedStatusDescription) is replaced wholesale. The span's status
// CODE is untouched, so a failed span is still visibly failed; callers that want
// finer diagnosis under this policy should record a bounded class attribute
// (internal/collector's scrape span sets error.type, a three-value enum).
//
// With the category enabled, behavior is unchanged: the description still gets the
// attr-value scrub so an identifier from some other disabled category cannot ride
// along inside it.
func (r *Redactor) RedactStatusDescription(desc string, attrs map[string]any) string {
	if !r.anyOff || desc == "" {
		return desc
	}
	if r.disabled(CatFreeTextDetails) && !BoundedStatusDescription(desc) {
		return bodyRedactedPlaceholder
	}
	return r.RedactBody(desc, nil, attrs)
}
