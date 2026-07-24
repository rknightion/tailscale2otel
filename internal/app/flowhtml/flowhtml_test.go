package flowhtml_test

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v3/internal/app/flowhtml"
	"github.com/rknightion/tailscale2otel/v3/internal/app/flowsdata"
)

func render(t *testing.T, p flowsdata.Page) string {
	t.Helper()
	var buf bytes.Buffer
	if err := flowhtml.Render(&buf, p); err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Handy when iterating on the page by eye: FLOWHTML_DUMP=/path/page.html.
	if dst := os.Getenv("FLOWHTML_DUMP"); dst != "" {
		if err := os.WriteFile(dst, buf.Bytes(), 0o600); err != nil {
			t.Fatalf("dump: %v", err)
		}
	}
	return buf.String()
}

func samplePage() flowsdata.Page {
	return flowsdata.Page{
		ServiceName: "tailscale2otel",
		Version:     "v2.3.4",
		Tailnets:    []string{"one.example.com", "two.example.com"},
		Tailnet:     "one.example.com",
		Retention:   "6h0m0s",
		RefreshMs:   5000,
	}
}

func TestRender_InjectsPageValues(t *testing.T) {
	out := render(t, samplePage())

	for _, want := range []string{
		"tailscale2otel", "v2.3.4", "6h0m0s",
		`"one.example.com"`, `"two.example.com"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page does not contain %q", want)
		}
	}
	// html/template pads a value interpolated into a JS context with spaces, so
	// match the assignment rather than an exact byte sequence.
	if !regexp.MustCompile(`REFRESH_MS\s*=\s*5000\s*;`).MatchString(out) {
		t.Error("the configured refresh interval did not reach the page")
	}
}

// html/template rewrites a value it cannot prove safe in a JS context to the
// literal "ZgotmplZ". On a page whose entire behavior is JavaScript, that is a
// silent break: the page loads and does nothing. Assert it never happens.
func TestRender_NoEscapingArtifacts(t *testing.T) {
	out := render(t, samplePage())
	for _, bad := range []string{"ZgotmplZ", "/* ZgotmplZ */", "%!"} {
		if strings.Contains(out, bad) {
			t.Errorf("template escaping artifact %q in the rendered page", bad)
		}
	}
}

// The page must render on an isolated tailnet with no route to the internet.
func TestRender_IsSelfContained(t *testing.T) {
	out := render(t, samplePage())
	for _, bad := range []string{
		`src="http`, `src='http`, `src="//`,
		`href="http`, `href='http`, `href="//`,
		"@import", "url(http", "url(//",
		"<link rel=\"stylesheet\"",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("page fetches an external resource (%q)", bad)
		}
	}
}

// A single-tailnet deployment is the common case and must not render a selector
// with one option in it.
func TestRender_SingleTailnet(t *testing.T) {
	p := samplePage()
	p.Tailnets = []string{"solo.example.com"}
	out := render(t, p)
	if !strings.Contains(out, `"solo.example.com"`) {
		t.Error("the tailnet name is missing from the page")
	}
	if !strings.Contains(out, `id="tailnetWrap" style="display:none"`) {
		t.Error("the tailnet selector should start hidden; JS reveals it only for >1 tailnet")
	}
}

// A name from the control plane is not trusted input. It reaches both an HTML
// text node and a JS string literal, and must be escaped in both.
func TestRender_EscapesHostileNames(t *testing.T) {
	p := samplePage()
	p.Tailnets = []string{`</script><img src=x onerror=alert(1)>`}
	p.Tailnet = p.Tailnets[0]
	p.Version = `<script>alert(2)</script>`
	out := render(t, p)

	if strings.Contains(out, "<img src=x") {
		t.Error("a tailnet name escaped into live markup")
	}
	if strings.Contains(out, "<script>alert(2)</script>") {
		t.Error("the version string escaped into live markup")
	}
	// The closing tag must not be able to terminate the inline script block.
	if strings.Contains(out, `"</script>`) {
		t.Error("an injected value can close the script element")
	}
}

func TestRender_ZeroPage(t *testing.T) {
	// Everything empty: the shell must still be a complete document rather than
	// failing or half-rendering.
	out := render(t, flowsdata.Page{})
	if !strings.HasPrefix(out, "<!DOCTYPE html>") || !strings.Contains(out, "</html>") {
		t.Error("the zero page did not render a complete document")
	}
	// With no configured interval the page falls back to a sane poll cadence
	// rather than polling in a tight loop.
	if !regexp.MustCompile(`REFRESH_MS\s*=\s*5000\s*;`).MatchString(out) {
		t.Error("missing the refresh-interval fallback")
	}
}

// The policy section is a DIAGNOSTIC. The live data cannot distinguish a real
// gap in a policy from a subtlety this reading of it misses — subnet routers are
// the usual culprit — and Tailscale itself carried every connection the section
// reports. Language implying otherwise is the one thing that would make the
// feature actively harmful, so it is asserted rather than left to review.
func TestRender_PolicySectionIsNeverAccusatory(t *testing.T) {
	out := strings.ToLower(render(t, samplePage()))
	for _, bad := range []string{"violation", "violate", "breach", "unauthorized", "leak"} {
		if strings.Contains(out, bad) {
			t.Errorf("the page uses %q; the policy section reports leads, not findings", bad)
		}
	}
	for _, want := range []string{
		"diagnostic, not an audit",
		"idle is not dead",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page is missing the caveat %q", want)
		}
	}
}

// An unexercised list is meaningless without the window it covers: several rules
// on a real tailnet legitimately go hours without firing.
func TestRender_UnexercisedRulesStateTheirWindow(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range []string{`id="unexercisedWindow"`, "Widen the window"} {
		if !strings.Contains(out, want) {
			t.Errorf("the unexercised-rules list does not state its window (%q missing)", want)
		}
	}
}

// The path section's numbers only mean something if the page says what they
// count. Relayed CONNECTIONS and relayed BYTES differ by two orders of magnitude
// on real data — 11.6% of connections but 0.4% of bytes — because DERP carries
// the handshakes while bulk traffic goes direct. A reader who takes one figure
// for the other draws the opposite conclusion.
func TestRender_PathSectionSaysWhatItCounts(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range []string{
		`id="pathStrip"`,
		`id="tblPeerPaths"`,
		`id="derpRegions"`,
		"connections, not bytes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the path section is missing %q", want)
		}
	}
}

// Region IDs are all Tailscale's API exposes — the DERP map endpoint is not
// served. Inventing names for them would be a guess an operator could act on, so
// the page shows the ID and says why.
func TestRender_DoesNotInventDERPRegionNames(t *testing.T) {
	out := render(t, samplePage())
	if !strings.Contains(out, "does not expose") {
		t.Error("the page does not explain why DERP regions are shown as IDs")
	}
	// A handful of the real region names, none of which we can justify printing.
	for _, name := range []string{"London", "Frankfurt", "Amsterdam", "Nuremberg"} {
		if strings.Contains(out, name) {
			t.Errorf("the page names DERP region %q; no API supplies that mapping", name)
		}
	}
}

// A peer reached over DERP is slower, not compromised. The section reports a
// property of the network, and must not read as a problem with the device.
func TestRender_PathSectionIsNotAlarming(t *testing.T) {
	out := render(t, samplePage())
	if !strings.Contains(out, "Relaying is not a failure") {
		t.Error("the path section does not say that a relayed peer is working, only slower")
	}
	for _, bad := range []string{"degraded peer", "bad peer", "broken peer"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Errorf("the path section calls a relayed peer %q", bad)
		}
	}
}

// "Undecidable" is not "unexplained", and the page must say so where an operator
// reads it, not only in the Go doc comments.
func TestRender_ExplainsTheUndecidableVerdict(t *testing.T) {
	out := render(t, samplePage())
	if !strings.Contains(out, "Never a finding.") {
		t.Error("the undecidable verdict is not explained as a non-finding")
	}
	if !strings.Contains(out, "No policy has been collected yet") {
		t.Error("the page cannot distinguish an uncollected policy from a clean one")
	}
}
