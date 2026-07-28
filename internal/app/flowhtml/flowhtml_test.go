package flowhtml_test

import (
	"bytes"
	"os"
	"reflect"
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

func TestRender_ExplainsReporterTrustAndPrivacyOmissions(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range []string{
		"Verified reporter",
		"reporter_node_id",
		"reporter_trust",
		"reporter_consistency",
		"Destination Logging",
		"observed completeness",
		"privacy-driven omissions",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the flow view is missing reporter/completeness contract %q", want)
		}
	}
}

// "Undecidable" is not "unexplained", and the page must say so where an operator
// reads it, not only in the Go doc comments.
// #296: server-side filtering and cursor pagination for the recent-connection
// list. The seven filter inputs and the load-more control are static markup
// (the page carries no per-request data — everything arrives from
// /api/flows.json), so asserting their exact rendered elements is not at risk
// of the template/JS duplication trap: there is exactly one place these tags
// are written.
func TestRender_RecentConnectionsHasSevenServerSideFilterInputs(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range []string{
		`id="filterDevice"`, `id="filterAddr"`, `id="filterService"`,
		`id="filterIdentity"`, `id="filterType"`, `id="filterVerdict"`, `id="filterPath"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("recent-connections filter input %q is missing from the rendered page", want)
		}
	}
}

// The load-more control and the matched/returned/retained status line must
// both exist as real elements the JS can populate — not just be described in
// prose.
func TestRender_RecentConnectionsHasLoadMoreAndStatusLine(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range []string{
		`id="flowLoadMore"`, `id="flowPageNote"`, `id="flowCount"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("recent-connections page is missing %q", want)
		}
	}
}

// The page must say, in terms an operator reads, that filtering runs against
// the full retained ring rather than just the visible page — the entire
// point of #296 over the old client-side filter.
func TestRender_ExplainsServerSideFilteringReachesTheWholeRing(t *testing.T) {
	out := render(t, samplePage())
	if !strings.Contains(out, "load more") {
		t.Error("the recent-connections section does not mention load more")
	}
	if !strings.Contains(out, "FULL retained ring") {
		t.Error("the recent-connections section does not say filtering covers the whole ring, not just the visible page")
	}
}

func TestRender_ExplainsTheUndecidableVerdict(t *testing.T) {
	out := render(t, samplePage())
	if !strings.Contains(out, "Never a finding.") {
		t.Error("the undecidable verdict is not explained as a non-finding")
	}
	if !strings.Contains(out, "No policy has been collected yet") {
		t.Error("the page cannot distinguish an uncollected policy from a clean one")
	}
}

// #298: window, end, tailnet, selection, matrix mode/cell and all seven
// filters must round-trip through query parameters using the SAME names
// /api/flows.json already accepts, so the page URL and the API URL never
// grow two vocabularies. This asserts the vocabulary directly against the
// URL-builder function's own source, rather than a phrase the JS merely
// mirrors — deleting any one of these `p.set(...)` lines from urlFromState
// makes this fail.
func TestRender_ShareableURLCarriesTheFullViewState(t *testing.T) {
	out := render(t, samplePage())
	fn := extractJSFunction(t, out, "urlFromState")
	for _, want := range []string{
		`p.set("window", state.window)`,
		`p.set("end", state.end)`,
		`p.set("tailnet", state.tailnet)`,
		`p.set("selected", state.selected)`,
		`p.set("matrix", state.matrixKind)`,
		`p.set("cell_src", state.cell.src)`,
		`p.set("cell_dst", state.cell.dst)`,
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("urlFromState is missing %q — a piece of view state that would silently stop round-tripping through the URL", want)
		}
	}
	if !strings.Contains(fn, "FILTER_KEYS.forEach") {
		t.Error("urlFromState does not fold the seven server-side filters into the URL")
	}
	// The seven server-side filter names, reused verbatim rather than a
	// second page-only vocabulary next to /api/flows.json's own — declared
	// once, in FILTER_KEYS, which urlFromState and applyStateFromLocation
	// both walk.
	for _, k := range []string{"device", "addr", "service", "identity", "type", "verdict", "path"} {
		if !strings.Contains(out, `"`+k+`"`) {
			t.Errorf("FILTER_KEYS does not mention the filter %q", k)
		}
	}
}

// Back/forward must actually work: a pushState/replaceState pair driven by
// state, and a popstate listener that re-applies the URL and re-fetches.
// Without the listener, the browser would change the address bar on back but
// the page would keep showing whatever it already had — indistinguishable
// from the listener being deleted entirely, which is why this checks for the
// wiring rather than just the word "popstate" appearing anywhere on the page.
func TestRender_BackForwardIsWired(t *testing.T) {
	out := render(t, samplePage())
	if !strings.Contains(out, `history.pushState(`) {
		t.Error("no history.pushState call — selecting a device or changing the window would not create a history entry")
	}
	if !strings.Contains(out, `history.replaceState(`) {
		t.Error("no history.replaceState call — the address bar would never reflect state without spamming history")
	}
	fn := extractJSFunction(t, out, "init")
	if !strings.Contains(fn, `window.addEventListener("popstate"`) {
		t.Error(`init() does not wire a "popstate" listener — back/forward would not change the view`)
	}
	if !strings.Contains(fn, "applyStateFromLocation();\n  syncURL(false);") {
		t.Error("init() does not restore state from the URL before the first poll")
	}
}

// A single keystroke in a filter box must NOT grow the history stack — only
// syncURL(false) (replaceState) may run per keystroke; a real navigation
// (a select's change, the clear-filters button, picking a window, selecting
// a device, picking a matrix cell) pushes a new entry instead. Asserting
// both call shapes catches the two ways this can regress: a debounced
// keystroke that starts pushing, or a real navigation that stops doing so.
func TestRender_FilterTypingReplacesButDiscretePicksPush(t *testing.T) {
	out := render(t, samplePage())
	fn := extractJSFunction(t, out, "init")
	// Exact snippets, not a bare "onFilterChanged(false) appears somewhere" —
	// two calls with the right two arguments in the WRONG two places would
	// satisfy a looser check just as happily.
	if !strings.Contains(fn, "if (debounced){ onFilterChanged(true); return; }") {
		t.Error(`the immediate (select) filter path does not call onFilterChanged(true) (pushState) on its own branch`)
	}
	if !strings.Contains(fn, "filterDebounce = setTimeout(function(){ onFilterChanged(false); }, 300);") {
		t.Error(`the debounced text-filter path does not call onFilterChanged(false) (replaceState) — typing would flood the back button with one entry per keystroke`)
	}
	if !strings.Contains(fn, "onFilterChanged(true);\n  });\n  el(\"flowLoadMore\")") {
		t.Error(`the clear-filters button does not call onFilterChanged(true) (pushState) right before wiring flowLoadMore`)
	}
	for _, want := range []string{
		`state.window = b.getAttribute("data-win");
      state.end = null;
      setLive(true);
      syncURL(true);`,
		`state.matrixKind = b.getAttribute("data-kind");`,
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("a discrete navigation in init() is missing its expected wiring: %q", want)
		}
	}
}

// The whole point of failing safe: an unparseable window, a future/garbage
// end, an unknown tailnet or matrix kind, and a stale selection/cell must all
// degrade to the default view rather than an error or a blank screen.
func TestRender_URLStateFailsSafeOnInvalidValues(t *testing.T) {
	out := render(t, samplePage())
	applyFn := extractJSFunction(t, out, "applyStateFromLocation")
	for _, want := range []string{
		`state.window = validWindow(win) ? win : "1h"`,
		`(tn && TAILNETS && TAILNETS.indexOf(tn) !== -1) ? tn : TAILNET`,
		`state.matrixKind = (mk && MATRIX_KINDS[mk]) ? mk : "tag"`,
	} {
		if !strings.Contains(applyFn, want) {
			t.Errorf("applyStateFromLocation is missing the fail-safe fallback %q", want)
		}
	}
	if !strings.Contains(out, `function dropStaleSelection(d){`) {
		t.Error("no dropStaleSelection function — a shared URL naming a device that has since left the tailnet would filter the view down to nothing forever")
	}
	dropFn := extractJSFunction(t, out, "dropStaleSelection")
	if !strings.Contains(dropFn, "state.selected = null") {
		t.Error("dropStaleSelection does not clear a selected device once the data no longer contains it")
	}
	if !strings.Contains(dropFn, "state.cell = null") {
		t.Error("dropStaleSelection does not clear a stale matrix cell")
	}
	renderFn := extractJSFunction(t, out, "render")
	if !strings.HasPrefix(strings.TrimSpace(renderFn[strings.Index(renderFn, "{")+1:]), "dropStaleSelection(d);") {
		t.Error("render(d) does not call dropStaleSelection before drawing the page from possibly-stale selection state")
	}
}

// URLs land in browser history, proxy/server logs and pasted chat messages —
// none of which the admin token (a Basic/Bearer credential the browser holds,
// never JS-visible) or the tailnet's compiled policy text belong in. This is
// a durable guard against ever adding either to the page's own JavaScript:
// deleting it is not possible without deleting the words themselves.
func TestRender_ShareableURLNeverCarriesCredentialsOrPolicyText(t *testing.T) {
	out := strings.ToLower(render(t, samplePage()))
	// Not "credential": the existing fetch() calls legitimately pass
	// {credentials:"same-origin"} (a same-origin cookie/Basic-auth opt-in
	// with nothing to do with the URL), predating this feature.
	for _, bad := range []string{"token", "authorization", "password", "bearer"} {
		if strings.Contains(out, bad) {
			t.Errorf("the flow view's rendered output contains %q; no admin credential belongs anywhere on this page", bad)
		}
	}
}

func TestRender_ExportSectionStatesItsOwnProvenance(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range []string{
		"never persistent full history",
		"states its own window, filters, and matched/returned/retained counts",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the export section is missing the provenance statement %q", want)
		}
	}
}

// extractJSFunction returns the source of a top-level `function name(...){`
// declaration in the rendered page, from its opening brace to the matching
// closing one. Tests use it to assert wiring inside the right function
// rather than merely somewhere in a 60KB script block, which would pass
// just as happily with the call in a dead function or a comment.
func extractJSFunction(t *testing.T, out, name string) string {
	t.Helper()
	marker := "function " + name + "("
	start := strings.Index(out, marker)
	if start < 0 {
		t.Fatalf("no %q function found in the rendered page", marker)
	}
	open := strings.Index(out[start:], "{")
	if open < 0 {
		t.Fatalf("function %s has no opening brace", name)
	}
	open += start
	depth := 0
	for i := open; i < len(out); i++ {
		switch out[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return out[open : i+1]
			}
		}
	}
	t.Fatalf("function %s never closes", name)
	return ""
}

// The string check above proves today's template mentions no credential, but
// it would NOT catch someone adding one to the DTO: rendering {{.AdminToken}}
// emits the token's VALUE, not the word "token", so the page could start
// carrying a credential into shareable URLs with every existing test green.
//
// The real guarantee is structural — flowsdata.Page has no field a credential
// could travel in, so the JS that builds the URL has nothing to put there
// (#298). This pins that, and fails the moment the DTO grows a way to break it.
func TestPageDTOCannotCarryACredential(t *testing.T) {
	t.Parallel()
	rt := reflect.TypeOf(flowsdata.Page{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		name := strings.ToLower(f.Name)
		for _, bad := range []string{"token", "secret", "password", "auth", "credential", "key"} {
			if strings.Contains(name, bad) {
				t.Errorf("flowsdata.Page.%s looks like a credential (%q). The flow page's state "+
					"round-trips through the URL, and URLs land in history, logs, referrers and "+
					"pasted messages — nothing credential-shaped may reach this struct.", f.Name, bad)
			}
		}
		if f.Type.String() == "config.Secret" {
			t.Errorf("flowsdata.Page.%s is a config.Secret; no credential may reach the flow page", f.Name)
		}
	}
}
