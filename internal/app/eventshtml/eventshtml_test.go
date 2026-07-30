package eventshtml_test

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/app/eventsdata"
	"github.com/rknightion/tailscale2otel/v4/internal/app/eventshtml"
)

func TestRender_ProducesTheRequiredMarkupIDs(t *testing.T) {
	var buf bytes.Buffer
	p := eventsdata.Page{ServiceName: "tailscale2otel", Version: "vtest", RefreshMs: 5000}
	if err := eventshtml.Render(&buf, p); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	// Assert rendered MARKUP (an id= a real element carries), never a bare
	// phrase — every human-facing string on this page exists twice (once in
	// the Go template, once in the JS that rebuilds the table on poll), so a
	// bare-phrase assertion would pass even with the table deleted, as long
	// as the JS string literal survived.
	for _, id := range []string{
		`id="eventsBody"`, `id="statRetained"`, `id="statCapacity"`,
		`id="statRecorded"`, `id="statEvicted"`, `id="statMatched"`,
		`id="fSource"`, `id="fSeverity"`, `id="fActor"`, `id="fAction"`,
		`id="fTarget"`, `id="fType"`, `id="fErrors"`, `id="btnApply"`,
		`id="btnClear"`, `id="errBanner"`, `id="truncBanner"`, `id="emptyState"`,
	} {
		if !strings.Contains(out, id) {
			t.Errorf("rendered page missing required element %s", id)
		}
	}

	if !strings.Contains(out, "tailscale2otel") || !strings.Contains(out, "vtest") {
		t.Errorf("rendered page missing service name/version")
	}
	if !strings.Contains(out, strconv.Itoa(p.RefreshMs)) {
		t.Errorf("rendered page missing the RefreshMs value %d", p.RefreshMs)
	}
	if !strings.Contains(out, "/api/events.json") {
		t.Errorf("rendered page does not reference /api/events.json")
	}
}

// TestRender_NoExternalAssets guards the admin CSP contract (#322): the page
// must be entirely self-contained, since default-src 'none' permits nothing
// else. A CDN script/style/font/image reference would render fine locally
// and only fail once served behind the real CSP, so this is asserted here
// rather than relying on the CSP test alone to catch it.
func TestRender_NoExternalAssets(t *testing.T) {
	var buf bytes.Buffer
	if err := eventshtml.Render(&buf, eventsdata.Page{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, forbidden := range []string{
		`src="http`, `src='http`, `src="//`,
		`href="http`, `href='http`, `href="//`,
		"@import", "url(http", "url(//",
		"<link rel=\"stylesheet\"",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("rendered page contains an external reference (%q); the page must be self-contained", forbidden)
		}
	}
}

// TestRender_ErrorAndTruncationBannersAreLiveRegions asserts the two banners
// that report a fetch failure and a truncated-window warning are announced by
// assistive tech when their text is set, not just shown visually — the same
// treatment flowhtml's #errBanner/#truncBanner got in #327 (this page's
// closing keyword, #512, is the seam #327 left for this page).
func TestRender_ErrorAndTruncationBannersAreLiveRegions(t *testing.T) {
	var buf bytes.Buffer
	if err := eventshtml.Render(&buf, eventsdata.Page{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<div id="errBanner" class="banner" role="alert"></div>`,
		`<div id="truncBanner" class="banner" role="status"></div>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
}

// TestRender_FocusVisibleStylesPresent asserts a focus-visible rule exists,
// per #327/#512: the page's dark palette makes the browser default outline
// hard to see, so filter inputs, selects and buttons need an explicit ring
// for keyboard users specifically (not for mouse clicks, which
// :focus-visible already excludes).
func TestRender_FocusVisibleStylesPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := eventshtml.Render(&buf, eventsdata.Page{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), ":focus-visible{outline:") {
		t.Error("no :focus-visible outline rule present")
	}
}

// TestRender_ReducedMotionRulePresent asserts a prefers-reduced-motion rule
// exists in the stylesheet. This page has no perceptible animation (no
// charts, no force-directed layout, no auto-scrolling) — unlike flowhtml's
// topology simulation, there is nothing here to skip. The rule is defensive,
// matching statushtml's own documented-as-defensive treatment in #327, not a
// behavior fix, and is asserted only so the page doesn't silently regress if
// animation is added later without this guard.
func TestRender_ReducedMotionRulePresent(t *testing.T) {
	var buf bytes.Buffer
	if err := eventshtml.Render(&buf, eventsdata.Page{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "@media (prefers-reduced-motion: reduce){") {
		t.Error("no prefers-reduced-motion rule present")
	}
}

// TestPageDTOCannotCarryACredential mirrors flowhtml's own copy of this test
// (#322) at the point where the template actually consumes the DTO, as a
// second line of defense alongside eventsdata's own reflection test.
func TestPageDTOCannotCarryACredential(t *testing.T) {
	t.Parallel()
	rt := reflect.TypeOf(eventsdata.Page{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		name := strings.ToLower(f.Name)
		for _, bad := range []string{"token", "secret", "password", "auth", "credential", "key"} {
			if strings.Contains(name, bad) {
				t.Errorf("eventsdata.Page.%s looks like a credential (%q). The events page's state "+
					"round-trips through the URL, and URLs land in history, logs, referrers and "+
					"pasted messages — nothing credential-shaped may reach this struct.", f.Name, bad)
			}
		}
		if f.Type.String() == "config.Secret" {
			t.Errorf("eventsdata.Page.%s is a config.Secret; no credential may reach the events page", f.Name)
		}
	}
}
