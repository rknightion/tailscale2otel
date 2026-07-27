package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CI's docs-catalog job trusts this tool's exit code to decide whether
// docs/metrics.md has drifted from the in-code telemetry catalog. These tests
// check that trust: before #437 the module had no tests, so a `go test` leg over
// it would have passed vacuously and a `-check` that could never fail would have
// looked identical to a working gate.

// fixture writes a copy of the real docs/metrics.md into a temp dir, so the tests
// exercise the same markers and content the gate does rather than a synthetic
// stand-in that could diverge from it.
func fixture(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("../../docs/metrics.md")
	if err != nil {
		t.Fatalf("read the real doc: %v", err)
	}
	p := filepath.Join(t.TempDir(), "metrics.md")
	if err := os.WriteFile(p, src, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The committed doc is in sync on a clean tree, so -check must accept it. If this
// fails, either the doc was committed stale or the renderer is non-deterministic —
// both make the CI gate meaningless in opposite directions.
func TestRun_CheckAcceptsTheCommittedDoc(t *testing.T) {
	if err := run(fixture(t), true, false); err != nil {
		t.Fatalf("-check on the committed doc: %v", err)
	}
}

// The whole point of the gate. Corrupting the generated region must be detected;
// a -check that cannot fail would let every future catalog change ship with a
// stale doc.
func TestRun_CheckDetectsDriftInsideTheMarkers(t *testing.T) {
	p := fixture(t)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Remove one table row from INSIDE a generated region.
	//
	// The "inside" part is load-bearing and was initially got wrong here: the doc
	// opens with a hand-written OTLP-naming example table whose rows look
	// identical to generated ones, and editing that is correctly NOT drift, since
	// Render only owns the marked regions. A test that removed the first
	// `| `tailscale` line anywhere passed while proving nothing.
	lines := strings.Split(string(body), "\n")
	var inGenerated, dropped bool
	var kept []string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "<!-- BEGIN GENERATED"):
			inGenerated = true
		case strings.HasPrefix(l, "<!-- END GENERATED"):
			inGenerated = false
		case inGenerated && !dropped && strings.HasPrefix(l, "| `tailscale"):
			dropped = true
			continue
		}
		kept = append(kept, l)
	}
	if !dropped {
		t.Fatal("found no metric row inside a generated region; the fixture no longer matches the doc shape")
	}
	if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	err = run(p, true, false)
	if err == nil {
		t.Fatal("-check accepted a doc with a generated row removed")
	}
	if !strings.Contains(err.Error(), "out of date") {
		t.Errorf("error = %v, want it to say the doc is out of date and how to regenerate", err)
	}
}

// -write must restore exactly what -check demands, or the two halves of the gate
// disagree and an operator is told to regenerate with a command that does not
// satisfy the check. Asserted as a round trip through real drift.
func TestRun_WriteRestoresWhatCheckDemands(t *testing.T) {
	p := fixture(t)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(body), "<!-- END GENERATED -->", "| `drifted.metric` | x |\n<!-- END GENERATED -->", 1)
	if broken == string(body) {
		t.Fatal("no END GENERATED marker found; the fixture no longer matches the doc shape")
	}
	if err := os.WriteFile(p, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(p, true, false); err == nil {
		t.Fatal("-check accepted an injected row inside a generated region")
	}

	if err := run(p, false, true); err != nil {
		t.Fatalf("-write: %v", err)
	}
	if err := run(p, true, false); err != nil {
		t.Errorf("-check still fails after -write, so the two halves of the gate disagree: %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(body) {
		t.Error("-write did not restore the doc byte-for-byte, so regeneration is not a pure " +
			"function of the catalog and the drift gate would flap")
	}
}

// A path that does not exist must be an error, not a silent success — the gate
// runs from the repo root and a wrong working directory is the likely mistake.
func TestRun_MissingFileIsAnError(t *testing.T) {
	if err := run(filepath.Join(t.TempDir(), "absent.md"), true, false); err == nil {
		t.Error("run accepted a nonexistent path")
	}
}
