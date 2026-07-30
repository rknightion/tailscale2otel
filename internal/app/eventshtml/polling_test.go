package eventshtml_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/app/eventsdata"
	"github.com/rknightion/tailscale2otel/v4/internal/app/eventshtml"
)

// pollerGuardSubstring names one exact construct createPoller (#328) relies
// on, plus how many times it must appear. The stale-response gate
// (mySeq <= maxSeenSeq) is written once in the success path and once in the
// error path — a bare presence check would still pass with only one of the
// two copies deleted, so this counts occurrences instead. Mirrors the
// identically-named table in internal/app/statushtml/polling_test.go — the
// createPoller helper is byte-for-byte identical across the three pages
// (see TestPollerHelperIdenticalAcrossPages in the statushtml package), so
// the same literal constructs, with the same counts, must be present here
// too.
type pollerGuardSubstring struct {
	text  string
	count int
}

var pollerGuardSubstrings = []pollerGuardSubstring{
	{"if (inFlight) { scheduleNext(baseMs); return; }", 1},
	{`var timeoutId = controller ? setTimeout(function(){ timedOut = true; controller.abort(); }, timeoutMs) : null;`, 1},
	{"backoffMs = baseMs > 0 ? Math.min(maxMs, Math.max(baseMs, backoffMs * 2)) : baseMs;", 1},
	{`document.addEventListener("visibilitychange", function(){`, 1},
	{"} else if (!paused){", 1},
	{"if (mySeq <= maxSeenSeq) return;", 2},
}

func renderEvents(t *testing.T, p eventsdata.Page) string {
	t.Helper()
	var buf bytes.Buffer
	if err := eventshtml.Render(&buf, p); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// TestRender_PollerGuardsPresent asserts every construct #328 requires is
// present verbatim in the rendered events page.
func TestRender_PollerGuardsPresent(t *testing.T) {
	out := renderEvents(t, eventsdata.Page{ServiceName: "tailscale2otel", Version: "vtest", RefreshMs: 5000})
	for _, want := range pollerGuardSubstrings {
		if got := strings.Count(out, want.text); got != want.count {
			t.Errorf("rendered page has %d occurrence(s) of poller guard %q, want %d", got, want.text, want.count)
		}
	}
}

// TestRender_EventsPollerWiring asserts the events page routes its polling
// through eventsPoller instead of the old bare `if (refreshMs > 0) {
// setInterval(poll, refreshMs); }`: the fetch is split into
// fetchEvents/applyEvents, eventsPoller is constructed against
// /api/events.json with the page's refreshMs as baseMs, poll() is a thin
// facade over eventsPoller.kick() (so btnApply/btnClear keep working
// unchanged), and refreshMs<=0 still disables ONLY the periodic leg
// (eventsPoller.start() is skipped) while the unconditional initial fetch
// (eventsPoller.kick()) survives — exactly the pre-#328 behavior of
// "poll() once always, setInterval only if refreshMs>0".
func TestRender_EventsPollerWiring(t *testing.T) {
	out := renderEvents(t, eventsdata.Page{ServiceName: "tailscale2otel", Version: "vtest", RefreshMs: 5000})
	for _, want := range []string{
		"function fetchEvents(signal){",
		"function applyEvents(data){",
		"var eventsPoller = createPoller({",
		"fetchFn: fetchEvents,",
		"onResult: applyEvents,",
		"baseMs: refreshMs",
		"function poll(){\n    eventsPoller.kick();\n  }",
		"eventsPoller.kick();\n  if (refreshMs > 0) { eventsPoller.start(); }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"setInterval(poll, refreshMs)",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered page still contains the pre-#328 construct %q", unwanted)
		}
	}
}

// TestRender_EventsPollerRefreshMsZeroStillDisablesPeriodicOnly asserts the
// refreshMs<=0 "disable auto-refresh" knob still reaches createPoller: the
// page always renders the unconditional eventsPoller.kick() (one fetch at
// load, regardless of refreshMs) followed by the same
// `if (refreshMs > 0)`-gated eventsPoller.start() call regardless of the
// value passed in — createPoller's own baseMs<=0 branch is what actually
// keeps a zero/negative RefreshMs from arming a periodic timer at runtime
// (see TestRender_PollerGuardsPresent's backoff/scheduleNext assertions),
// so the template-level contract this test pins is simply that the gate
// is still wired to the SAME refreshMs value that flows into baseMs, not a
// second, potentially-diverged copy.
func TestRender_EventsPollerRefreshMsZeroStillDisablesPeriodicOnly(t *testing.T) {
	out := renderEvents(t, eventsdata.Page{ServiceName: "tailscale2otel", Version: "vtest", RefreshMs: 0})
	if !strings.Contains(out, "eventsPoller.kick();\n  if (refreshMs > 0) { eventsPoller.start(); }") {
		t.Fatalf("rendered page does not gate the periodic leg on refreshMs while always doing the initial kick")
	}
	if !strings.Contains(out, "baseMs: refreshMs") {
		t.Fatalf("eventsPoller is not constructed with the page's own refreshMs as baseMs")
	}
}
