package flowhtml_test

import (
	"strings"
	"testing"
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

// TestRender_PollerGuardsPresent asserts every construct #328 requires is
// present verbatim, and exactly as many times as expected, in the rendered
// flow page.
func TestRender_PollerGuardsPresent(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range pollerGuardSubstrings {
		if got := strings.Count(out, want.text); got != want.count {
			t.Errorf("rendered page has %d occurrence(s) of poller guard %q, want %d", got, want.text, want.count)
		}
	}
}

// TestRender_FlowsPollerWiring asserts the flow page routes its polling
// through flowsPoller instead of the old state.timer/setInterval(poll,
// pair: the fetch is split into fetchFlows/applyFlows, flowsPoller is
// constructed against /api/flows.json with the configured refresh interval,
// poll() is a thin facade over flowsPoller.kick() (so every one of its many
// pre-existing call sites — window presets, filters, popstate — keeps
// working unchanged), and schedule() only arms/disarms the periodic timer
// via start()/stop() rather than owning a competing setInterval of its own.
func TestRender_FlowsPollerWiring(t *testing.T) {
	out := render(t, samplePage())
	for _, want := range []string{
		"function fetchFlows(signal){",
		"function applyFlows(d){",
		"var flowsPoller = createPoller({",
		"fetchFn: fetchFlows,",
		"onResult: applyFlows,",
		"function poll(){\n  flowsPoller.kick();\n}",
		"if (state.end) flowsPoller.stop(); else flowsPoller.start();",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"state.timer",
		"setInterval(poll,",
		"clearInterval(state.timer)",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered page still contains the pre-#328 construct %q", unwanted)
		}
	}
}
