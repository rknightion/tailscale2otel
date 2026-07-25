package pii

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactBodyFastPathAllOn(t *testing.T) {
	r := New(allOn())
	body := "flow 100.64.0.1:5252 -> 8.8.8.8:443 tx=1B"
	if got := r.RedactBody(body, []Category{CatFreeTextDetails}, map[string]any{"source.address": "100.64.0.1"}); got != body {
		t.Fatalf("all-on RedactBody changed body: %q", got)
	}
}

func TestRedactBodyEmptyBody(t *testing.T) {
	c := allOn()
	c[CatFreeTextDetails] = false
	r := New(c)
	if got := r.RedactBody("", []Category{CatFreeTextDetails}, nil); got != "" {
		t.Fatalf("empty body should stay empty, got %q", got)
	}
}

// Whole-body replacement (mechanism B): a standalone free-text body whose category
// is disabled is replaced entirely.
func TestRedactBodyWholeBodyReplacedWhenBodyPIICategoryOff(t *testing.T) {
	c := allOn()
	c[CatFreeTextDetails] = false
	r := New(c)
	got := r.RedactBody("upstream said: node 100.64.0.9 is unreachable (secret detail)",
		[]Category{CatFreeTextDetails}, nil)
	if strings.Contains(got, "unreachable") || strings.Contains(got, "100.64.0.9") {
		t.Fatalf("free_text off: whole body should be replaced, got %q", got)
	}
}

func TestRedactBodyWholeBodyKeptWhenBodyPIICategoryOn(t *testing.T) {
	c := allOn()
	c[CatExternalIPs] = false // some other category off, but not the body's
	r := New(c)
	body := "upstream error: connection refused"
	if got := r.RedactBody(body, []Category{CatFreeTextDetails}, nil); got != body {
		t.Fatalf("free_text on: body should be unchanged, got %q", got)
	}
}

// Attr-value scrub (mechanism A): a mixed body keeps its non-PII structure while a
// disabled-category attribute value is removed wherever it appears.
func TestRedactBodyScrubsDisabledAttrValueKeepsStructure(t *testing.T) {
	c := allOn()
	c[CatTailscaleIPs] = false
	r := New(c)
	attrs := map[string]any{
		"source.address":      "100.64.0.1", // tailscale IP -> redacted
		"destination.address": "8.8.8.8",    // external IP -> kept
		"network.transport":   "tcp",
	}
	got := r.RedactBody("tcp 100.64.0.1:5252 -> 8.8.8.8:443 tx=1B", nil, attrs)
	if strings.Contains(got, "100.64.0.1") {
		t.Fatalf("tailscale_ips off: source IP should be scrubbed from body, got %q", got)
	}
	if !strings.Contains(got, "8.8.8.8") {
		t.Fatalf("external IP should remain in body, got %q", got)
	}
	if !strings.Contains(got, "tcp") || !strings.Contains(got, "tx=1B") {
		t.Fatalf("non-PII body structure should be preserved, got %q", got)
	}
}

func TestRedactBodyKeepsAttrValueWhenCategoryOn(t *testing.T) {
	c := allOn()
	c[CatExternalIPs] = false // only external off
	r := New(c)
	got := r.RedactBody("tcp 100.64.0.1:5252 tx=1B", nil, map[string]any{"source.address": "100.64.0.1"})
	if !strings.Contains(got, "100.64.0.1") {
		t.Fatalf("tailscale IP must survive when only external_ips off, got %q", got)
	}
}

// Categories are independent: disabling hostnames must not scrub an IP value.
func TestRedactBodyCategoryIndependence(t *testing.T) {
	c := allOn()
	c[CatHostnames] = false
	r := New(c)
	got := r.RedactBody("tcp 100.64.0.1:5252 tx=1B", nil, map[string]any{"source.address": "100.64.0.1"})
	if !strings.Contains(got, "100.64.0.1") {
		t.Fatalf("hostnames off must not scrub a tailscale IP, got %q", got)
	}
}

// --- #472 (security:SEC-09): deterministic overlap handling ---

// freeTextOff builds a Redactor with only free_text_details disabled, so every
// CatFreeTextDetails-classified attribute value below is a redaction candidate.
func freeTextOff() *Redactor {
	c := allOn()
	c[CatFreeTextDetails] = false
	return New(c)
}

// A shorter sensitive value that is a PREFIX of a longer one must not be replaced
// first: doing so leaves the longer value's tail ("-primary") in the body, which is
// a recognizable remnant of a value the operator asked to be removed.
func TestRedactBodyNestedValuesRemoveTheLongestMatch(t *testing.T) {
	r := freeTextOff()
	attrs := map[string]any{
		"tailscale.key.description": "deploy-key",
		"value":                     "deploy-key-primary-2026",
	}
	const body = "rotated deploy-key-primary-2026 for tailnet"
	for i := 0; i < 200; i++ {
		got := r.RedactBody(body, nil, attrs)
		if strings.Contains(got, "primary") || strings.Contains(got, "2026") {
			t.Fatalf("iteration %d: remnant of the longer sensitive value survived: %q", i, got)
		}
		if want := "rotated " + bodyRedactedPlaceholder + " for tailnet"; got != want {
			t.Fatalf("iteration %d: got %q, want %q", i, got, want)
		}
	}
}

// Output must not depend on Go map iteration order. Overlapping, identical and
// adjacent candidates are all present so any order-sensitive implementation
// produces at least two distinct outputs across enough iterations.
func TestRedactBodyDeterministicAcrossMapIterationOrder(t *testing.T) {
	r := freeTextOff()
	attrs := map[string]any{
		"tailscale.key.description": "alpha",
		"value":                     "alpha-beta",
		"tailscale.target.name":     "alpha-beta-gamma",
		"error.message":             "beta",
		"tailscale.oauth_app.name":  "gamma",
		"tailscale.acl.rule":        "alpha", // identical value under a second key
		"network.transport":         "tcp",   // never a candidate
	}
	const body = "tcp alpha-beta-gamma alpha beta gamma alphabeta"
	first := r.RedactBody(body, nil, attrs)
	for i := 1; i < 500; i++ {
		if got := r.RedactBody(body, nil, attrs); got != first {
			t.Fatalf("iteration %d produced %q, first run produced %q (map-order dependent)", i, got, first)
		}
	}
	for _, leak := range []string{"alpha", "beta", "gamma"} {
		if strings.Contains(first, leak) {
			t.Fatalf("sensitive value %q survived: %q", leak, first)
		}
	}
	if !strings.Contains(first, "tcp") {
		t.Fatalf("non-PII structure was lost: %q", first)
	}
}

// The replacement marker must not become matchable itself: a candidate that is a
// substring of "[redacted]" must not match inside a placeholder emitted for some
// other value.
func TestRedactBodyPlaceholderCannotCreateNewMatches(t *testing.T) {
	r := freeTextOff()
	attrs := map[string]any{
		"tailscale.key.description": "supersecret",
		"value":                     "red",    // substring of "[redacted]"
		"error.message":             "redact", // ditto, longer
	}
	got := r.RedactBody("supersecret", nil, attrs)
	if got != bodyRedactedPlaceholder {
		t.Fatalf("placeholder was re-matched: got %q, want %q", got, bodyRedactedPlaceholder)
	}
	if strings.Count(got, bodyRedactedPlaceholder) != 1 {
		t.Fatalf("expected exactly one placeholder, got %q", got)
	}
}

// Every occurrence of a repeated value goes, and adjacent occurrences do not merge
// into a single placeholder.
func TestRedactBodyRepeatedAndAdjacentValues(t *testing.T) {
	r := freeTextOff()
	attrs := map[string]any{"tailscale.key.description": "sekret"}
	got := r.RedactBody("a sekret b sekretsekret c", nil, attrs)
	want := "a " + bodyRedactedPlaceholder + " b " + bodyRedactedPlaceholder + bodyRedactedPlaceholder + " c"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Multi-byte values must be removed whole and the surviving body must stay valid
// UTF-8: a byte-wise scrub that split a rune would corrupt the log record.
func TestRedactBodyUnicodeValuesStayValidUTF8(t *testing.T) {
	r := freeTextOff()
	attrs := map[string]any{
		"tailscale.key.description": "clé-privée-🔑",
		"value":                     "münchen",
	}
	body := "rotated clé-privée-🔑 on münchen — done ✅"
	got := r.RedactBody(body, nil, attrs)
	if strings.Contains(got, "clé") || strings.Contains(got, "🔑") || strings.Contains(got, "münchen") {
		t.Fatalf("unicode sensitive value survived: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("redaction produced invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "✅") || !strings.Contains(got, "—") {
		t.Fatalf("non-PII multi-byte content was damaged: %q", got)
	}
}

// A body with no candidate occurrence is returned unchanged (identity, not a copy
// with different bytes).
func TestRedactBodyNoMatchIsUnchanged(t *testing.T) {
	r := freeTextOff()
	const body = "tcp 100.64.0.1:5252 tx=1B"
	if got := r.RedactBody(body, nil, map[string]any{"tailscale.key.description": "absent"}); got != body {
		t.Fatalf("body with no candidate occurrence changed: %q", got)
	}
}

// --- benchmarks: bound the worst case for a large log body (#472) ---

// benchBody builds an n-byte body interleaving benign text with candidate values.
func benchBody(n int, vals []string) string {
	var b strings.Builder
	i := 0
	for b.Len() < n {
		b.WriteString("2026-07-25T00:00:00Z flow record seq=")
		b.WriteString(vals[i%len(vals)])
		b.WriteString(" transport=tcp tx=1024 rx=2048 proto=6 padding-padding-padding\n")
		i++
	}
	return b.String()
}

func benchAttrs(vals []string) map[string]any {
	keys := []string{
		"tailscale.key.description", "value", "tailscale.target.name", "error.message",
		"tailscale.oauth_app.name", "tailscale.acl.rule", "tailscale.audit.details",
		"tailscale.audit.old", "tailscale.audit.new", "tailscale.device.posture.details",
	}
	attrs := make(map[string]any, len(vals))
	for i, v := range vals {
		attrs[keys[i%len(keys)]+"."+strconv.Itoa(i)] = v
	}
	// Ensure at least the classified keys carry candidates.
	for i, k := range keys {
		attrs[k] = vals[i%len(vals)]
	}
	return attrs
}

func benchVals(n int) []string {
	vals := make([]string, n)
	for i := range vals {
		vals[i] = "sensitive-value-" + strconv.Itoa(i)
	}
	return vals
}

// 1 MiB body, 10 overlapping-length candidates: the realistic upper bound for a
// single emitted log body.
func BenchmarkRedactBodyLargeBody(b *testing.B) {
	r := freeTextOff()
	vals := benchVals(10)
	attrs := benchAttrs(vals)
	body := benchBody(1<<20, vals)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.RedactBody(body, nil, attrs)
	}
}

// Same body size with no candidate present: the common case must stay cheap.
func BenchmarkRedactBodyLargeBodyNoMatch(b *testing.B) {
	r := freeTextOff()
	vals := benchVals(10)
	attrs := benchAttrs(benchVals(10))
	for k := range attrs {
		attrs[k] = "no-such-value-in-the-body-" + k
	}
	body := benchBody(1<<20, vals)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.RedactBody(body, nil, attrs)
	}
}

// Pathological candidate count on a 1 MiB body: bounds the per-position candidate
// scan when an attribute set is unusually wide.
func BenchmarkRedactBodyManyCandidates(b *testing.B) {
	r := freeTextOff()
	vals := benchVals(64)
	attrs := benchAttrs(vals)
	body := benchBody(1<<20, vals)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.RedactBody(body, nil, attrs)
	}
}

// A typical emitted flow/audit log body.
func BenchmarkRedactBodyTypical(b *testing.B) {
	r := freeTextOff()
	vals := benchVals(4)
	attrs := benchAttrs(vals)
	body := benchBody(4<<10, vals)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.RedactBody(body, nil, attrs)
	}
}
