package telemetry_test

import (
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/telemetry"
	"github.com/rknightion/tailscale2otel/v2/internal/telemetry/pii"
)

func bodyOf(t *testing.T, exp *recordingLogExporter) string {
	t.Helper()
	recs := exp.all()
	if len(recs) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(recs))
	}
	return recs[0].Body().AsString()
}

// A standalone free-text body (BodyPII set) is replaced wholesale when its
// category is disabled — the raw identifier must not survive in the body just
// because bodies bypass the attribute filter (#197).
func TestEmitterBody_FreeTextWholeBodyRedactedWhenCategoryOff(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatFreeTextDetails] = false
	e, _, exp := newPIITestEmitter(t, cats)
	e.LogEvent(telemetry.Event{
		Name:    "tailscale.logstream.error",
		Body:    "dial tcp 100.64.0.9:5252: connection refused",
		BodyPII: []pii.Category{pii.CatFreeTextDetails},
		Attrs:   telemetry.Attrs{"tailscale.logstream.type": "configuration"},
	})
	if b := bodyOf(t, exp); strings.Contains(b, "100.64.0.9") || strings.Contains(b, "connection refused") {
		t.Fatalf("free_text off: free-text body must be replaced, got %q", b)
	}
}

func TestEmitterBody_FreeTextKeptWhenCategoryOn(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatExternalIPs] = false // some other category off, not the body's
	e, _, exp := newPIITestEmitter(t, cats)
	body := "dial tcp: connection refused"
	e.LogEvent(telemetry.Event{
		Name:    "tailscale.logstream.error",
		Body:    body,
		BodyPII: []pii.Category{pii.CatFreeTextDetails},
	})
	if b := bodyOf(t, exp); b != body {
		t.Fatalf("free_text on: body must be unchanged, got %q", b)
	}
}

// A mixed body that embeds an identifier also carried as an attribute keeps its
// non-PII structure while the disabled-category value is scrubbed.
func TestEmitterBody_AttrValueScrubbedKeepsStructure(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatTailscaleIPs] = false
	e, _, exp := newPIITestEmitter(t, cats)
	e.LogEvent(telemetry.Event{
		Name: "tailscale.network.flow",
		Body: "tcp virtual 100.64.0.1:5252 -> 8.8.8.8:443 tx=10B rx=20B",
		Attrs: telemetry.Attrs{
			"source.address":      "100.64.0.1",
			"destination.address": "8.8.8.8",
			"network.transport":   "tcp",
		},
	})
	b := bodyOf(t, exp)
	if strings.Contains(b, "100.64.0.1") {
		t.Errorf("tailscale_ips off: source IP must be scrubbed from body, got %q", b)
	}
	if !strings.Contains(b, "8.8.8.8") {
		t.Errorf("external IP must remain, got %q", b)
	}
	if !strings.Contains(b, "tx=10B") || !strings.Contains(b, "tcp") {
		t.Errorf("non-PII body structure must be preserved, got %q", b)
	}
}

// Categories are independent: disabling hostnames must not scrub an IP from a body.
func TestEmitterBody_CategoryIndependence(t *testing.T) {
	cats := allOnCats()
	cats[pii.CatHostnames] = false
	e, _, exp := newPIITestEmitter(t, cats)
	e.LogEvent(telemetry.Event{
		Name:  "tailscale.network.flow",
		Body:  "tcp 100.64.0.1:5252 tx=1B",
		Attrs: telemetry.Attrs{"source.address": "100.64.0.1"},
	})
	if b := bodyOf(t, exp); !strings.Contains(b, "100.64.0.1") {
		t.Fatalf("hostnames off must not scrub a tailscale IP from body, got %q", b)
	}
}

func TestEmitterBody_AllOnUnchanged(t *testing.T) {
	e, _, exp := newPIITestEmitter(t, nil)
	body := "tcp 100.64.0.1:5252 -> 8.8.8.8:443 tx=1B"
	e.LogEvent(telemetry.Event{
		Name:    "tailscale.network.flow",
		Body:    body,
		BodyPII: []pii.Category{pii.CatFreeTextDetails},
		Attrs:   telemetry.Attrs{"source.address": "100.64.0.1"},
	})
	if b := bodyOf(t, exp); b != body {
		t.Fatalf("all-on: body must be byte-identical, got %q", b)
	}
}
