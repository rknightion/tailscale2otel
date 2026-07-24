package flowhtml_test

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v2/internal/app/flowhtml"
	"github.com/rknightion/tailscale2otel/v2/internal/app/flowsdata"
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
