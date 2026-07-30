package statushtml_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/app/statusdata"
	"github.com/rknightion/tailscale2otel/v4/internal/app/statushtml"
)

// pollerGuardSubstring names one exact construct createPoller (#328) relies
// on for an acceptance criterion, plus how many times it must appear. Most
// guards are checked by presence alone, but the stale-response gate
// (mySeq <= maxSeenSeq) is written once in the success path and once in the
// error path — a mutation that deletes only the success-path copy would
// slip past a bare Contains check (the error-path copy still matches),
// which is exactly the "passes with the guard half-gone" shape this repo's
// vacuous-assertion history warns about. Counting occurrences closes that
// gap; see TestRender_PollerGuardsPresent's negative-testing pass, which
// deleted exactly one of the two copies to confirm this catches it.
type pollerGuardSubstring struct {
	text  string
	count int
}

var pollerGuardSubstrings = []pollerGuardSubstring{
	// single-flight: a tick that lands mid-flight is folded into the next
	// scheduled tick rather than firing a second concurrent request.
	{"if (inFlight) { scheduleNext(baseMs); return; }", 1},
	// per-call timeout via AbortController.
	{`var timeoutId = controller ? setTimeout(function(){ timedOut = true; controller.abort(); }, timeoutMs) : null;`, 1},
	// capped exponential backoff on failure.
	{"backoffMs = baseMs > 0 ? Math.min(maxMs, Math.max(baseMs, backoffMs * 2)) : baseMs;", 1},
	// hidden tabs stop periodic work.
	{`document.addEventListener("visibilitychange", function(){`, 1},
	// recovery refreshes immediately and resets backoff (only while
	// periodic polling is actually enabled).
	{"} else if (!paused){", 1},
	// stale responses cannot overwrite newer state (sequence-number gate),
	// once per settle path (success, error).
	{"if (mySeq <= maxSeenSeq) return;", 2},
}

// TestRender_PollerGuardsPresent asserts every construct #328 requires is
// present verbatim, and exactly as many times as expected, in the rendered
// page — not merely a same-shaped paraphrase, and not merely "at least
// once" for the two-copy stale-response gate. Each entry here was deleted
// individually as part of this change's negative-testing pass to confirm
// this test fails for the right reason before being restored.
func TestRender_PollerGuardsPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{RefreshMs: 3000}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range pollerGuardSubstrings {
		if got := strings.Count(out, want.text); got != want.count {
			t.Errorf("rendered page has %d occurrence(s) of poller guard %q, want %d", got, want.text, want.count)
		}
	}
}

// TestRender_StatusPollerWiring asserts the status page actually routes its
// polling through statusPoller rather than a bare setInterval/refresh() —
// the fetch is split into fetchStatus/applyStatus, the poller is
// constructed against /api/status.json with the configured refresh
// interval, and every former direct refresh() call site (refresh-now
// button, RDNS purge, the tailnet selector, pause/resume, initial load) now
// goes through the poller's start()/stop()/kick() instead. The old
// pollTimer/startPolling/setInterval(refresh path must be gone entirely —
// their survival alongside the new code would mean two competing pollers.
func TestRender_StatusPollerWiring(t *testing.T) {
	var buf bytes.Buffer
	if err := statushtml.Render(&buf, statusdata.Status{RefreshMs: 3000}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"function fetchStatus(signal){",
		"fetch('/api/status.json',{cache:'no-store', signal:signal})",
		"function applyStatus(d){",
		"var statusPoller = createPoller({",
		"fetchFn: fetchStatus,",
		"onResult: applyStatus,",
		// refresh-now button: an explicit user request always fetches now.
		"rn.addEventListener('click', function(ev){ ev.preventDefault(); statusPoller.kick(); });",
		// RDNS purge success: the cache changed, so the next render must
		// reflect it immediately rather than waiting for the next tick.
		"if(m) m.textContent='purged '+d.purged+' entr'+(d.purged===1?'y':'ies'); statusPoller.kick(); })",
		// tailnet selector: switching the view re-fetches for the new scope.
		"syncTailnetURL();\n    statusPoller.kick();",
		// pause/resume: Pause stops the periodic poller outright; Resume
		// fetches immediately (kick) and re-arms the periodic timer (start).
		"if(paused){ statusPoller.stop(); if(btn) btn.textContent='Resume'; }",
		"else { if(btn) btn.textContent='Pause'; statusPoller.kick(); statusPoller.start(); }",
		// initial load: one immediate fetch, then the periodic timer starts.
		"statusPoller.kick();\n  statusPoller.start();\n  setInterval(tickFreshness, 1000);",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"function startPolling(",
		"pollTimer",
		"setInterval(refresh,",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered page still contains the pre-#328 construct %q", unwanted)
		}
	}
}

// extractPollerHelper pulls the createPoller block out of a page template's
// RAW source (not rendered output) between the two sentinel comment lines
// every copy carries. Reading the source directly — rather than executing
// the template — means the three packages' own render data (status vs.
// flows vs. events) never has to line up for this comparison to be
// meaningful.
func extractPollerHelper(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	src := string(raw)
	start := strings.Index(src, "// === shared poller helper (#328)")
	if start < 0 {
		t.Fatalf("%s: shared poller helper start marker not found", path)
	}
	endMarker := "// === end shared poller helper ==="
	end := strings.Index(src[start:], endMarker)
	if end < 0 {
		t.Fatalf("%s: shared poller helper end marker not found", path)
	}
	return src[start : start+end+len(endMarker)]
}

// TestPollerHelperIdenticalAcrossPages asserts createPoller is byte-for-byte
// identical across all three embedded pages. The three pages are separate
// Go templates and cannot share a Go function, so textual identity — pinned
// by this test — is how "one rule, not three drifting spellings" stays
// enforceable (see CLAUDE.md's note on #297/#316/#309 for why this pattern
// keeps recurring in this repo).
func TestPollerHelperIdenticalAcrossPages(t *testing.T) {
	statusBlock := extractPollerHelper(t, "page.html.tmpl")
	flowBlock := extractPollerHelper(t, "../flowhtml/page.html.tmpl")
	eventsBlock := extractPollerHelper(t, "../eventshtml/page.html.tmpl")

	if statusBlock != flowBlock {
		t.Errorf("createPoller in statushtml and flowhtml have diverged")
	}
	if statusBlock != eventsBlock {
		t.Errorf("createPoller in statushtml and eventshtml have diverged")
	}
}
