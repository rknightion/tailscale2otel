package catalog_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/tailscale2otel/v4/internal/catalog"
)

// updateManifest rewrites the committed signal-disposition manifest (and the
// coverage doc derived from it) instead of asserting them. Run:
//
//	go test ./internal/catalog -run TestSignalDispositionsInSync -update
//	go test ./internal/catalog -run TestSignalCoverageDocInSync -update
//
// Regeneration only records what the shipped artifacts PROVE — it never invents an
// intent disposition — so a new signal lands with an EMPTY disposition list, which
// always fails the gate. Running -update can move work forward, never hide it.
//
// There is deliberately no disposition meaning "this signal does not need a panel".
// raw_only and omitted were exactly that and #526 deleted both: between them they
// had accrued 35 signals that an operator could not see anywhere, because relabelling
// an awkward signal is always easier than paneling it. The only escapes now are the
// three structural classes in StructuralExemptions(), each an individually justified
// entry. The transitional pending_panel ledger that #526 used to drain the backlog
// was deleted with the last row it held.
var updateManifest = flag.Bool("update", false, "rewrite the committed signal-disposition manifest and coverage doc")

const coverageDocPath = "../../docs/signal-coverage.md"

// exprKeys are the document keys whose STRING values hold a query. Restricting
// extraction to these is what keeps a metric merely NAMED in a panel description
// or an alert annotation from counting as a reference — every rule file is full
// of prose that names metrics, and treating that as coverage would make the
// manifest claim surfaces that do not exist.
var exprKeys = map[string]bool{"expr": true, "query": true}

// collectStrings walks a decoded JSON/YAML document and appends every string
// value stored under one of keys.
func collectStrings(node any, keys map[string]bool, key string, out *[]string) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			collectStrings(child, keys, k, out)
		}
	case []any:
		for _, child := range v {
			collectStrings(child, keys, key, out)
		}
	case string:
		if keys[key] {
			*out = append(*out, v)
		}
	}
}

// dashboardExprs is every query the generated dashboard runs (panel queries and
// template-variable queries alike).
func dashboardExprs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, path := range dashboardPaths {
		b, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative artifact path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		before := len(out)
		collectStrings(doc, exprKeys, "", &out)
		if len(out) == before {
			// Per-file, not just on the total: after the #526 split a whole
			// artifact failing to contribute would otherwise be masked by the
			// other one, and every "visualized" claim resting on it would be
			// vacuously unprovable.
			t.Fatalf("%s yielded no queries; the extraction is broken", path)
		}
	}
	return out
}

// ruleExprs splits the shipped rule manifests' expressions into the ones an ALERT
// evaluates and the ones a RECORDING rule evaluates.
//
// The rules are Grafana-managed `rules.alerting.grafana.app/v0alpha1` manifests,
// one JSON file per rule, so the split is by `kind` (AlertRule vs RecordingRule)
// rather than by the presence of a `record` key the way the old provisioning and
// Prometheus formats nested it.
func ruleExprs(t *testing.T, dir string) (alerting, recording []string) {
	t.Helper()
	for _, path := range ruleManifests(t, dir) {
		b, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative artifact path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		var exprs []string
		collectStrings(doc, exprKeys, "", &exprs)
		switch doc["kind"] {
		case "AlertRule":
			alerting = append(alerting, exprs...)
		case "RecordingRule":
			recording = append(recording, exprs...)
		default:
			t.Errorf("%s has kind %v, expected AlertRule or RecordingRule", path, doc["kind"])
		}
	}
	return alerting, recording
}

// ruleManifests lists the shipped rule manifests, excluding the folder manifest.
//
// It fails on an empty result rather than returning one: every assertion built on
// these files would otherwise pass vacuously if the directory moved or the
// generator's output naming changed.
func ruleManifests(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if filepath.Base(m) == "_folder.json" {
			continue
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no rule manifests; either the directory moved or the generator's "+
			"output changed, and every assertion built on it is vacuous", dir)
	}
	sort.Strings(out)
	return out
}

// refSet turns a batch of query strings into the set of catalog-owned signal
// identifiers they reference: metric names via the same extraction the
// dashboard-reference tests use (metricRef, with label-matcher VALUES stripped
// first so a value that looks like an identifier is not mistaken for one), and
// log events by their dotted EventName, which is how a LogQL selector names them.
func refSet(texts []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range texts {
		body := stripMatcherValues(s)
		for _, m := range metricRef.FindAllString(body, -1) {
			out[m] = true
		}
	}
	joined := strings.Join(texts, "\n")
	for _, e := range catalog.LogEvents() {
		if strings.Contains(joined, e.Name) {
			out[e.Name] = true
		}
	}
	return out
}

// artifactRefs is what the three shipped artifacts actually reference. It is the
// only evidence the gate accepts for a visualized/alertable/recorded claim.
func artifactRefs(t *testing.T) catalog.ArtifactRefs {
	t.Helper()
	alerting, recording := ruleExprs(t, rulesDir)
	return catalog.ArtifactRefs{
		Dashboard: refSet(dashboardExprs(t)),
		Alerting:  refSet(alerting),
		Recording: refSet(recording),
	}
}

func loadManifest(t *testing.T) *catalog.SignalDispositionBaseline {
	t.Helper()
	raw, err := os.ReadFile(catalog.SignalDispositionsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", catalog.SignalDispositionsFile, err)
	}
	base, err := catalog.LoadSignalDispositions(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", catalog.SignalDispositionsFile, err)
	}
	return base
}

// TestSignalDispositionsInSync is the #390 gate: every catalog metric and log
// event must carry at least one disposition, the manifest must not describe
// signals the code no longer emits, and every visualized/alertable/recorded claim
// must match what the shipped dashboard and rule files actually reference.
func TestSignalDispositionsInSync(t *testing.T) {
	signals := catalog.Signals()
	refs := artifactRefs(t)
	base := loadManifest(t)

	if *updateManifest {
		merged, added, removed := catalog.MergeSignalDispositions(base, signals, refs)
		out, err := catalog.MarshalSignalDispositions(merged)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(catalog.SignalDispositionsFile, out, 0o644); err != nil { //nolint:gosec // committed data file
			t.Fatalf("write %s: %v", catalog.SignalDispositionsFile, err)
		}
		t.Logf("regenerated %s: %d signals (%d added, %d removed)",
			catalog.SignalDispositionsFile, len(merged.Signals), len(added), len(removed))
		for _, a := range added {
			t.Logf("  ADDED: %s %s", a.Kind, a.Name)
		}
		for _, r := range removed {
			t.Logf("  REMOVED: %s %s", r.Kind, r.Name)
		}
		for _, s := range merged.Signals {
			if len(s.Dispositions) == 0 {
				t.Logf("  NEEDS A HUMAN (%s): %s %s — give it a panel, or (rarely) a structural exemption",
					s.Surface, s.Kind, s.Name)
			}
		}
		return
	}

	if problems := catalog.ValidateSignalDispositions(base, signals, refs); problems.Len() > 0 {
		t.Fatalf("signal-disposition manifest broken (%d problem(s)):\n%s\n"+
			"Run: go test ./internal/catalog -run TestSignalDispositionsInSync -update\n"+
			"then give every signal that still reaches no panel a panel. A disposition is "+
			"a RECORD of where a signal appears, not a way to excuse one from appearing.",
			problems.Len(), problems)
	}
}

// TestSignalDispositions_EmbeddedCopyMatchesDisk guards against a stale compiled-in
// manifest: the coverage doc renders from the embedded copy, so the two must agree.
func TestSignalDispositions_EmbeddedCopyMatchesDisk(t *testing.T) {
	if *updateManifest {
		t.Skip("regenerating; the embed refreshes on the next build")
	}
	embedded, err := catalog.EmbeddedSignalDispositions()
	if err != nil {
		t.Fatalf("embedded manifest: %v", err)
	}
	disk := loadManifest(t)
	if disk == nil {
		t.Fatalf("%s is missing", catalog.SignalDispositionsFile)
	}
	if len(embedded.Signals) != len(disk.Signals) {
		t.Fatalf("embedded manifest has %d signals, disk has %d — rebuild",
			len(embedded.Signals), len(disk.Signals))
	}
}

// TestSignalSurfaceIsDerived pins the one field a hand-edit could get wrong in a
// way nothing else notices: Surface is a function of the descriptor's Group, not
// a choice, and it is what keeps self-observability coverage reported separately
// from operational coverage.
func TestSignalSurfaceIsDerived(t *testing.T) {
	var selfObs, operational int
	for _, s := range catalog.Signals() {
		switch s.Surface {
		case catalog.SurfaceSelfObs:
			selfObs++
			// Everything on this surface is about the exporter process: its own
			// tailscale2otel.* signals plus the standard OTEL process.* runtime
			// metrics. A tailscale.* signal landing here means a descriptor was
			// filed under the wrong docs group.
			if !strings.HasPrefix(s.Name, "tailscale2otel.") && !strings.HasPrefix(s.Name, "process.") {
				t.Errorf("%s %s is on the self_obs surface but is neither a tailscale2otel.* nor a "+
					"process.* signal", s.Kind, s.Name)
			}
		case catalog.SurfaceOperational:
			operational++
		default:
			t.Errorf("%s %s has surface %q, which is neither %q nor %q",
				s.Kind, s.Name, s.Surface, catalog.SurfaceOperational, catalog.SurfaceSelfObs)
		}
	}
	if selfObs == 0 || operational == 0 {
		t.Fatalf("expected both surfaces to be populated, got self_obs=%d operational=%d — the "+
			"separation this manifest exists to preserve would be vacuous", selfObs, operational)
	}
}

// TestSignalDispositionsAreCanonical checks the on-disk file is exactly what
// MarshalSignalDispositions produces from its own contents: sorted rows, sorted
// deduped disposition lists, stable key order, trailing newline. A hand-edit that
// appends a row at the end or repeats a disposition fails here rather than
// producing a noisy diff on the next regeneration.
func TestSignalDispositionsAreCanonical(t *testing.T) {
	if *updateManifest {
		t.Skip("regenerating")
	}
	raw, err := os.ReadFile(catalog.SignalDispositionsFile)
	if err != nil {
		t.Fatalf("read %s: %v", catalog.SignalDispositionsFile, err)
	}
	base, err := catalog.LoadSignalDispositions(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", catalog.SignalDispositionsFile, err)
	}
	want, err := catalog.MarshalSignalDispositions(catalog.CanonicalizeSignalDispositions(base))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != string(want) {
		t.Errorf("%s is not in canonical form; regenerate it with "+
			"`go test ./internal/catalog -run TestSignalDispositionsInSync -update`", catalog.SignalDispositionsFile)
	}
}

// TestSignalCoverageDocInSync keeps docs/signal-coverage.md derived from the
// manifest rather than from a text search over the dashboard and rule files. It
// rides the normal `go test` run, so the doc cannot drift without a red test.
func TestSignalCoverageDocInSync(t *testing.T) {
	base := loadManifest(t)
	if base == nil {
		t.Fatalf("%s is missing", catalog.SignalDispositionsFile)
	}
	current, err := os.ReadFile(filepath.Clean(coverageDocPath))
	if err != nil {
		t.Fatalf("read %s: %v", coverageDocPath, err)
	}
	want, err := catalog.SpliceSignalCoverage(string(current), catalog.SignalCoverageReport(base))
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	if *updateManifest {
		if err := os.WriteFile(coverageDocPath, []byte(want), 0o644); err != nil { //nolint:gosec // G306: generated docs file is intentionally world-readable
			t.Fatalf("write %s: %v", coverageDocPath, err)
		}
		t.Logf("regenerated %s", coverageDocPath)
		return
	}
	if want != string(current) {
		t.Errorf("docs/signal-coverage.md is out of date with %s; regenerate with "+
			"`go test ./internal/catalog -run TestSignalCoverageDocInSync -update` and commit the result",
			catalog.SignalDispositionsFile)
	}
}

// TestSignalCoverageDocIsListedInNav catches the quiet half of adding a docs page:
// a page absent from the zensical nav is built but unreachable, so nobody ever
// reads the coverage table this issue exists to publish.
func TestSignalCoverageDocIsListedInNav(t *testing.T) {
	b, err := os.ReadFile("../../zensical.toml")
	if err != nil {
		t.Fatalf("read zensical.toml: %v", err)
	}
	if !strings.Contains(string(b), "signal-coverage.md") {
		t.Error("docs/signal-coverage.md is not in the zensical nav, so the published site never links it")
	}
}

// TestObservedDispositionsAreProvable is a self-check on the evidence extraction:
// at least one signal must be provably visualized, one alertable and one recorded.
// If any of the three extractions silently broke, every claim of that kind in the
// manifest would fail the gate at once — but a future refactor could equally
// "fix" that by deleting the claims, so assert the extraction directly.
func TestObservedDispositionsAreProvable(t *testing.T) {
	refs := artifactRefs(t)
	counts := map[catalog.Disposition]int{}
	for _, s := range catalog.Signals() {
		for _, d := range refs.Observed(s) {
			counts[d]++
		}
	}
	for _, d := range []catalog.Disposition{catalog.DispVisualized, catalog.DispAlertable, catalog.DispRecorded} {
		if counts[d] == 0 {
			t.Errorf("no catalog signal is %s according to the shipped artifacts; the evidence "+
				"extraction for that surface is broken", d)
		}
	}
}

// sortedDispositionValues is a tiny helper the table-driven cases below share.
func sortedDispositionValues(ds []catalog.Disposition) string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = string(d)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// TestValidateSignalDispositions_RejectsContradictions drives the validator
// against synthetic inputs, because the committed manifest is (by definition)
// clean and would exercise none of the failure paths.
func TestValidateSignalDispositions_RejectsContradictions(t *testing.T) {
	metric := catalog.Signal{
		Kind:     catalog.KindMetric,
		Name:     "tailscale.example",
		PromName: "tailscale_example_ratio",
		Surface:  catalog.SurfaceOperational,
	}
	shown := catalog.ArtifactRefs{Dashboard: map[string]bool{"tailscale_example_ratio": true}}
	unseen := catalog.ArtifactRefs{}

	cases := []struct {
		desc  string
		rows  []catalog.SignalDisposition
		refs  catalog.ArtifactRefs
		wants string
	}{
		{
			desc:  "no row at all",
			rows:  nil,
			refs:  unseen,
			wants: "NEW signal with no disposition",
		},
		{
			desc:  "empty disposition list",
			rows:  []catalog.SignalDisposition{{Kind: metric.Kind, Name: metric.Name, PromName: metric.PromName, Surface: metric.Surface}},
			refs:  unseen,
			wants: "empty disposition",
		},
		{
			desc: "unknown disposition",
			rows: []catalog.SignalDisposition{{Kind: metric.Kind, Name: metric.Name, PromName: metric.PromName, Surface: metric.Surface,
				Dispositions: []catalog.Disposition{"charted"}}},
			refs:  unseen,
			wants: "unknown disposition",
		},
		{
			desc: "claims visualized with no panel",
			rows: []catalog.SignalDisposition{{Kind: metric.Kind, Name: metric.Name, PromName: metric.PromName, Surface: metric.Surface,
				Dispositions: []catalog.Disposition{catalog.DispVisualized}}},
			refs:  unseen,
			wants: "claims visualized",
		},
		// #526 deleted the last hand-assignable disposition, so "the manifest claims
		// something the artifacts do not show" is now the ONLY class of contradiction
		// left — which is why the cases below all pin an identity field wrong while
		// the disposition itself is honest.
		{
			desc: "an unseen signal claiming nothing still fails",
			rows: []catalog.SignalDisposition{{Kind: metric.Kind, Name: metric.Name, PromName: metric.PromName, Surface: metric.Surface,
				Dispositions: nil}},
			refs:  unseen,
			wants: "empty disposition",
		},
		{
			desc: "wrong prom name",
			rows: []catalog.SignalDisposition{{Kind: metric.Kind, Name: metric.Name, PromName: "tailscale_example", Surface: metric.Surface,
				Dispositions: []catalog.Disposition{catalog.DispVisualized}}},
			refs:  shown,
			wants: "prom_name",
		},
		{
			desc: "wrong surface",
			rows: []catalog.SignalDisposition{{Kind: metric.Kind, Name: metric.Name, PromName: metric.PromName, Surface: catalog.SurfaceSelfObs,
				Dispositions: []catalog.Disposition{catalog.DispVisualized}}},
			refs:  shown,
			wants: "surface",
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			base := &catalog.SignalDispositionBaseline{Signals: c.rows}
			problems := catalog.ValidateSignalDispositions(base, []catalog.Signal{metric}, c.refs)
			if problems.Len() == 0 {
				t.Fatalf("expected a problem mentioning %q, got none", c.wants)
			}
			if !strings.Contains(problems.String(), c.wants) {
				t.Errorf("problems do not mention %q:\n%s", c.wants, problems)
			}
		})
	}

	// A clean row must produce nothing, or every case above proves only that the
	// validator complains indiscriminately.
	clean := &catalog.SignalDispositionBaseline{Signals: []catalog.SignalDisposition{{
		Kind: metric.Kind, Name: metric.Name, PromName: metric.PromName, Surface: metric.Surface,
		Dispositions: []catalog.Disposition{catalog.DispVisualized},
	}}}
	if problems := catalog.ValidateSignalDispositions(clean, []catalog.Signal{metric}, shown); problems.Len() > 0 {
		t.Errorf("a correct row was rejected:\n%s", problems)
	}
}

// TestValidateSignalDispositions_ReportsStaleRows: a manifest row for a signal the
// code no longer emits is a lie about what we emit, and it is exactly what a
// rename leaves behind.
func TestValidateSignalDispositions_ReportsStaleRows(t *testing.T) {
	base := &catalog.SignalDispositionBaseline{Signals: []catalog.SignalDisposition{{
		Kind: catalog.KindMetric, Name: "tailscale.gone", PromName: "tailscale_gone",
		Surface: catalog.SurfaceOperational, Dispositions: []catalog.Disposition{catalog.DispVisualized},
	}}}
	problems := catalog.ValidateSignalDispositions(base, nil, catalog.ArtifactRefs{})
	if !strings.Contains(problems.String(), "STALE") {
		t.Errorf("expected a STALE row report, got:\n%s", problems)
	}
}

// TestValidateSignalDispositions_GroupsBySurface is requirement 5: a reader must
// be able to see self-observability coverage independently of operational
// coverage, so the report is keyed by surface rather than being one flat list.
func TestValidateSignalDispositions_GroupsBySurface(t *testing.T) {
	signals := []catalog.Signal{
		{Kind: catalog.KindMetric, Name: "tailscale.op", PromName: "tailscale_op", Surface: catalog.SurfaceOperational},
		{Kind: catalog.KindMetric, Name: "tailscale2otel.self", PromName: "tailscale2otel_self", Surface: catalog.SurfaceSelfObs},
	}
	problems := catalog.ValidateSignalDispositions(nil, signals, catalog.ArtifactRefs{})
	if len(problems[catalog.SurfaceOperational]) != 1 || len(problems[catalog.SurfaceSelfObs]) != 1 {
		t.Fatalf("expected one problem per surface, got %#v", map[string]int{
			catalog.SurfaceOperational: len(problems[catalog.SurfaceOperational]),
			catalog.SurfaceSelfObs:     len(problems[catalog.SurfaceSelfObs]),
		})
	}
	rendered := problems.String()
	if !strings.Contains(rendered, catalog.SurfaceSelfObs) || !strings.Contains(rendered, catalog.SurfaceOperational) {
		t.Errorf("rendered problems do not name both surfaces:\n%s", rendered)
	}
}

// TestMergeSignalDispositions_NeverInventsAHumanChoice is the non-silencing
// guarantee: -update fills in what the artifacts prove and nothing else, so a new
// signal it cannot prove a reference for stays empty and keeps the gate red.
func TestMergeSignalDispositions_NeverInventsAHumanChoice(t *testing.T) {
	seen := catalog.Signal{Kind: catalog.KindMetric, Name: "tailscale.seen", PromName: "tailscale_seen", Surface: catalog.SurfaceOperational}
	unseen := catalog.Signal{Kind: catalog.KindMetric, Name: "tailscale.unseen", PromName: "tailscale_unseen", Surface: catalog.SurfaceOperational}
	refs := catalog.ArtifactRefs{
		Dashboard: map[string]bool{"tailscale_seen": true},
		Alerting:  map[string]bool{"tailscale_seen": true},
	}

	merged, added, removed := catalog.MergeSignalDispositions(nil, []catalog.Signal{unseen, seen}, refs)
	if len(added) != 2 || len(removed) != 0 {
		t.Fatalf("added=%d removed=%d, want 2/0", len(added), len(removed))
	}
	if merged.Signals[0].Name != "tailscale.seen" {
		t.Errorf("rows are not sorted by name: %+v", merged.Signals)
	}
	if got := sortedDispositionValues(merged.Signals[0].Dispositions); got != "alertable,visualized" {
		t.Errorf("proven signal got %q, want %q", got, "alertable,visualized")
	}
	if len(merged.Signals[1].Dispositions) != 0 {
		t.Errorf("unproven signal got %v, want an empty list so the gate stays red",
			merged.Signals[1].Dispositions)
	}

	// An unproven row stays empty no matter how many times regeneration runs — it
	// cannot be settled by re-running the tool, only by giving the signal a panel.
	// This is what #526 bought by deleting the last hand-assignable disposition:
	// there is no longer any value a human can write here to make the gate go green.
	merged.Signals[1].Note = "a note is not evidence"
	again, _, _ := catalog.MergeSignalDispositions(merged, []catalog.Signal{unseen, seen}, refs)
	if len(again.Signals[1].Dispositions) != 0 {
		t.Errorf("an unproven signal gained a disposition from regeneration: %v",
			again.Signals[1].Dispositions)
	}
	if again.Signals[1].Note != "a note is not evidence" {
		t.Errorf("the hand-written note was lost on regeneration, got %q", again.Signals[1].Note)
	}
	// It settles the moment the artifact actually references it.
	refs.Dashboard["tailscale_unseen"] = true
	third, _, _ := catalog.MergeSignalDispositions(again, []catalog.Signal{unseen, seen}, refs)
	if got := sortedDispositionValues(third.Signals[1].Dispositions); got != "visualized" {
		t.Errorf("a now-referenced signal was not recorded as visualized, got %q", got)
	}

	// Dropping a signal from the catalog prunes its row.
	pruned, _, gone := catalog.MergeSignalDispositions(third, []catalog.Signal{seen}, refs)
	if len(pruned.Signals) != 1 || len(gone) != 1 {
		t.Errorf("pruning failed: %d rows, %d removed", len(pruned.Signals), len(gone))
	}
}

// TestSignalCoverageReport_SeparatesSurfaces: the published table is the whole
// point of the manifest, so it must actually break the numbers out per surface.
func TestSignalCoverageReport_SeparatesSurfaces(t *testing.T) {
	rep := catalog.SignalCoverageReport(loadManifest(t))
	for _, want := range []string{"Operational", "Self-observability", "visualized", "recorded"} {
		if !strings.Contains(rep, want) {
			t.Errorf("coverage report never mentions %q", want)
		}
	}
}
