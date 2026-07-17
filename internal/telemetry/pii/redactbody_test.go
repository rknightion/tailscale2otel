package pii

import (
	"strings"
	"testing"
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
