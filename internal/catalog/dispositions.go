package catalog

// Signal-disposition manifest (#390).
//
// Metrics()/LogEvents() say WHAT we emit. This file says what each emitted
// signal is FOR — whether a dashboard panel charts it, an alert rule watches it,
// a recording rule folds it, or it is deliberately query-only or deliberately
// unsurfaced — and gates the two against each other, so a new metric fails CI
// until someone records a disposition for it.
//
// The dispositions live in an out-of-band JSON manifest rather than on the
// metricdoc descriptors for two reasons: the descriptors are declared next to
// their emit sites across two dozen packages, so a disposition field there would
// be reviewed a line at a time and never as a whole; and the answer depends on
// artifacts that live OUTSIDE the Go build (the generated dashboards and the
// rule manifests), which no struct tag can be checked against.
//
// Regenerate with:
//
//	go test ./internal/catalog -run TestSignalDispositionsInSync -update
//
// Regeneration is deliberately NON-SILENCING. It fills in only what the shipped
// artifacts PROVE — visualized, alertable, recorded — and prunes signals that no
// longer exist. It never invents pending_panel, because that is a claim about
// intent that only a human can make. A signal it cannot prove a reference for
// lands with an EMPTY disposition list, and an empty list always fails the gate,
// so running -update can only move work forward.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rknightion/tailscale2otel/v5/internal/appcatalog"
)

// SignalDispositionsFile is the manifest path, relative to this package
// directory. Tests and the regeneration flag share this constant.
const SignalDispositionsFile = "signal_dispositions.json"

//go:embed signal_dispositions.json
var embeddedSignalDispositions []byte

// Signal kinds. A manifest row is identified by (Kind, Name), because a metric
// and a log event may legitimately share a dotted name.
const (
	// KindMetric is a metricdoc.Metric descriptor.
	KindMetric = "metric"
	// KindLogEvent is a metricdoc.LogEvent descriptor.
	KindLogEvent = "log_event"
)

// Surfaces. Operational and self-observability coverage are reported separately
// throughout: they answer different questions ("can an operator see their
// tailnet?" vs "can an operator see the exporter?"), and merging them lets a
// well-covered fleet story hide a blind exporter.
const (
	// SurfaceOperational is telemetry about the tailnet being observed.
	SurfaceOperational = "operational"
	// SurfaceSelfObs is telemetry about tailscale2otel itself.
	SurfaceSelfObs = "self_obs"
)

// Disposition records what a curated surface does with one emitted signal.
type Disposition string

const (
	// DispVisualized means at least one panel in the generated dashboard queries it.
	DispVisualized Disposition = "visualized"
	// DispAlertable means at least one alert rule expression queries it.
	DispAlertable Disposition = "alertable"
	// DispRecorded means a recording rule consumes or produces it.
	DispRecorded Disposition = "recorded"
	// DispDrivesAVariable means a dashboard TEMPLATE VARIABLE queries it — a
	// hidden presence sentinel driving conditionalRendering, or a filter dropdown
	// populated by label_values().
	//
	// Deliberately NOT visualized, and deliberately still recorded (#527). A
	// sentinel's label_values() call is a real reference, so counting it as
	// "visualized" let a signal satisfy the every-signal-reaches-a-panel bar while
	// appearing on no panel an operator can see —
	// tailscale.subnet_routes.advertised sat in exactly that state, covered only
	// by the has_subnet sentinel, and stayed invisible until #526 deleted the row
	// that sentinel gated. The failure mode is a signal invisible to operators and
	// to the gate at once, and the gate gets QUIETER about it the longer the
	// sentinel lives.
	DispDrivesAVariable Disposition = "drives_a_variable"
)

// Every disposition is DERIVED — read off the shipped artifacts — and there is
// deliberately no value a human may assign.
//
// #526 removed all three that ever existed. raw_only and omitted let an awkward
// signal be re-labeled instead of paneled: "deliberately query-only" is
// indistinguishable, from the outside, from "nobody got round to it", and 35
// signals had accrued under the two of them. pending_panel replaced them as an
// explicitly TRANSITIONAL, shrink-only ledger of signals with a panel scheduled,
// and was deleted the moment it emptied — which is the only reason it was safe to
// have at all.
//
// So a signal that reaches no panel now has exactly one remedy: give it a panel.
// The sole escapes are the three STRUCTURAL exemption classes below, none of which
// is a judgement call about whether a signal is worth charting.

// Structural exemption classes. A signal in one of these cannot be required to
// appear on a panel for a reason about the SHAPE of the signal, not about
// anyone's opinion of its value.
const (
	// ExemptHistogramBase is the base name of a histogram or summary. It is never
	// queried directly — every panel queries _bucket/_sum/_count — so requiring
	// the base name to appear would be requiring a query nobody should write.
	ExemptHistogramBase = "histogram_base"
	// ExemptTemplatedEvent is an event name templated at emit time
	// (tailscale.webhook.<type>), so no literal selector can name it. A panel
	// covers it by matching the prefix, which no token match can attribute back.
	ExemptTemplatedEvent = "templated_event"
	// ExemptRecordedOnly is consumed only by a recording rule whose OUTPUT metric
	// is itself paneled, so the operator does see it — folded.
	//
	// This class matches ZERO signals today: nothing is recorded without also
	// being visualized. It exists so the gate does not have to be reopened later.
	// Do not go looking for members.
	ExemptRecordedOnly = "recorded_only"
)

// StructuralExemption is one signal the catalog->panel gate cannot require a
// panel for. Every entry carries its own written reason: this is a list of
// individually justified rows, not a category anyone may join.
type StructuralExemption struct {
	// Kind and Name identify the signal, as in a manifest row.
	Kind, Name string
	// Class is one of the three Exempt* constants.
	Class string
	// Reason says why, for this specific signal.
	Reason string
	// Output is the paneled metric a recorded_only signal folds into. Empty for
	// the other two classes.
	Output string
}

// derivedDispositions are the three a shipped artifact can PROVE. They are
// computed, never chosen: the manifest must claim exactly the ones the dashboard
// and rule files reference, in both directions.
var derivedDispositions = []Disposition{
	DispVisualized, DispAlertable, DispRecorded, DispDrivesAVariable,
}

// dispositionRank orders a disposition list for display: proven surfaces first,
// in increasing distance from the operator's eye.
var dispositionRank = map[Disposition]int{
	DispVisualized:      0,
	DispAlertable:       1,
	DispRecorded:        2,
	DispDrivesAVariable: 3,
}

func isDerived(d Disposition) bool {
	return d == DispVisualized || d == DispAlertable || d == DispRecorded ||
		d == DispDrivesAVariable
}
func isKnown(d Disposition) bool { return isDerived(d) }

// Signal is one catalog entry flattened to the identity the manifest keys on.
// It carries no description or unit: this file is about what a signal is FOR,
// and docs/metrics.md already owns what it means.
type Signal struct {
	Kind     string // KindMetric | KindLogEvent
	Name     string // dotted OTEL source name
	PromName string // normalized Prometheus name; empty for a log event
	Surface  string // SurfaceOperational | SurfaceSelfObs
}

func (s Signal) key() string { return s.Kind + "\x00" + s.Name }

// Signals flattens the whole in-code catalog into the rows the manifest must
// account for, sorted by (Kind, Name).
func Signals() []Signal {
	var out []Signal
	for _, m := range Metrics() {
		out = append(out, Signal{
			Kind:     KindMetric,
			Name:     m.Name,
			PromName: m.PromName(),
			Surface:  surfaceFor(m.Group),
		})
	}
	for _, e := range LogEvents() {
		out = append(out, Signal{
			Kind:    KindLogEvent,
			Name:    e.Name,
			Surface: surfaceFor(e.Group),
		})
	}
	sortSignals(out)
	return out
}

// surfaceFor derives the surface from the descriptor's docs group. It is derived
// rather than declared so a new self-obs metric cannot be filed under operational
// coverage by forgetting a field.
func surfaceFor(group string) string {
	if group == appcatalog.GroupSelfObs {
		return SurfaceSelfObs
	}
	return SurfaceOperational
}

func sortSignals(s []Signal) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Kind != s[j].Kind {
			return s[i].Kind < s[j].Kind
		}
		return s[i].Name < s[j].Name
	})
}

// ArtifactRefs is the set of catalog-owned identifiers each shipped artifact
// references, split by the kind of surface that references them. The caller
// extracts them (see internal/catalog/dispositions_test.go, which reuses the same
// PromQL extraction the dashboard-reference tests use); this package only decides
// what they imply.
type ArtifactRefs struct {
	// Dashboard is every identifier queried by a PANEL in the generated dashboard
	// family. Panels only — a template-variable query goes in DashboardVariable,
	// because only a panel puts a signal in front of an operator (#527).
	Dashboard map[string]bool
	// DashboardVariable is every identifier queried by a dashboard TEMPLATE
	// VARIABLE: a presence sentinel, or a filter dropdown's label_values() call.
	DashboardVariable map[string]bool
	// Alerting is every identifier queried by an ALERT rule expression, across
	// both the Grafana-managed and the Prometheus rule files.
	Alerting map[string]bool
	// Recording is every identifier queried by a RECORDING rule expression.
	Recording map[string]bool
}

// references reports whether set mentions s. A metric matches on its normalized
// Prometheus name or on any of the suffixed series Prometheus derives from a
// histogram, since a panel legitimately queries `_bucket`/`_sum`/`_count` rather
// than the base name. A log event matches on its dotted EventName, which is how a
// LogQL selector names it.
func (r ArtifactRefs) references(set map[string]bool, s Signal) bool {
	if s.Kind == KindLogEvent {
		return set[s.Name]
	}
	if s.PromName == "" {
		return false
	}
	if set[s.PromName] {
		return true
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if set[s.PromName+suffix] {
			return true
		}
	}
	return false
}

// Observed returns the dispositions the shipped artifacts prove for s, in
// canonical order. An empty result means no curated surface references it at
// all, which is precisely the case a human has to classify.
func (r ArtifactRefs) Observed(s Signal) []Disposition {
	var out []Disposition
	if r.references(r.Dashboard, s) {
		out = append(out, DispVisualized)
	}
	if r.references(r.Alerting, s) {
		out = append(out, DispAlertable)
	}
	if r.references(r.Recording, s) {
		out = append(out, DispRecorded)
	}
	if r.references(r.DashboardVariable, s) {
		out = append(out, DispDrivesAVariable)
	}
	return out
}

// SignalDisposition is one manifest row.
type SignalDisposition struct {
	Kind         string        `json:"kind"`
	Name         string        `json:"name"`
	PromName     string        `json:"prom_name,omitempty"`
	Surface      string        `json:"surface"`
	Dispositions []Disposition `json:"dispositions"`
	Note         string        `json:"note,omitempty"`
}

func (s SignalDisposition) key() string { return s.Kind + "\x00" + s.Name }

func (s SignalDisposition) has(d Disposition) bool {
	for _, got := range s.Dispositions {
		if got == d {
			return true
		}
	}
	return false
}

// SignalDispositionBaseline is the on-disk shape of signal_dispositions.json.
type SignalDispositionBaseline struct {
	// Doc is a leading "//" key so the file explains itself to a reviewer.
	Doc []string `json:"//"`
	// Signals is sorted by (Kind, Name) so diffs stay minimal.
	Signals []SignalDisposition `json:"signals"`
}

// manifestDoc is the self-describing header written into the generated file.
var manifestDoc = []string{
	"Signal disposition manifest (#390). Generated + hand-dispositioned.",
	"Regenerate: go test ./internal/catalog -run TestSignalDispositionsInSync -update",
	"Regeneration records only what the deploy/grafana dashboard FAMILY (tailnet +",
	"health) and the deploy/alerts rule manifests PROVE, and prunes signals the code",
	"no longer emits. It",
	"assigns nothing by hand: every disposition is DERIVED from those artifacts, so an",
	"empty disposition list means the signal is on no surface at all, and that fails",
	"TestSignalDispositionsInSync.",
	"surface: operational (telemetry about the tailnet) | self_obs (about the exporter)",
	"dispositions (a set; derived ones are checked against the artifacts both ways):",
	"  visualized - queried by >=1 panel or template variable in the generated dashboard",
	"  alertable  - queried by >=1 alert rule expression",
	"  recorded   - consumed by >=1 recording rule expression",
	"  drives_a_variable - queried by a dashboard TEMPLATE VARIABLE (a presence",
	"                  sentinel, or a filter dropdown). NOT visualized: a sentinel's",
	"                  label_values() call is a real reference but puts nothing on",
	"                  screen, and counting it let a signal clear the every-signal-",
	"                  reaches-a-panel bar while being invisible (#527).",
	"There is NO disposition meaning \"this signal does not need a panel\". raw_only and",
	"omitted were exactly that and #526 deleted both after 35 signals had accrued under",
	"them, unreachable by any operator; the transitional pending_panel ledger that",
	"replaced them was deleted in turn once it emptied. The only escapes are the three",
	"structural exemption classes in StructuralExemptions().",
}

// LoadSignalDispositions parses a manifest from raw JSON.
func LoadSignalDispositions(raw []byte) (*SignalDispositionBaseline, error) {
	var b SignalDispositionBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("catalog: parse signal dispositions: %w", err)
	}
	return &b, nil
}

// EmbeddedSignalDispositions returns the compiled-in manifest, for callers that
// render coverage without the repository checked out.
func EmbeddedSignalDispositions() (*SignalDispositionBaseline, error) {
	return LoadSignalDispositions(embeddedSignalDispositions)
}

// normalizeDispositions sorts a disposition set into canonical order and drops
// duplicates, so the on-disk file is diff-stable however a row was hand-edited.
func normalizeDispositions(ds []Disposition) []Disposition {
	if len(ds) == 0 {
		return []Disposition{}
	}
	seen := map[Disposition]bool{}
	out := make([]Disposition, 0, len(ds))
	for _, d := range ds {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ri, oki := dispositionRank[out[i]]
		rj, okj := dispositionRank[out[j]]
		if oki != okj {
			return oki // unknown values sort last, where they are easy to spot
		}
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

// CanonicalizeSignalDispositions returns b with its doc header refreshed, its
// rows sorted and its disposition sets normalized. It changes no human decision —
// it only puts the file into the exact shape MarshalSignalDispositions writes, so
// a hand-edit is detectable as a formatting drift rather than silently reflowed
// on the next unrelated regeneration.
func CanonicalizeSignalDispositions(b *SignalDispositionBaseline) *SignalDispositionBaseline {
	out := &SignalDispositionBaseline{Doc: manifestDoc}
	if b == nil {
		return out
	}
	out.Signals = make([]SignalDisposition, len(b.Signals))
	copy(out.Signals, b.Signals)
	for i := range out.Signals {
		out.Signals[i].Dispositions = normalizeDispositions(out.Signals[i].Dispositions)
	}
	sort.Slice(out.Signals, func(i, j int) bool { return out.Signals[i].key() < out.Signals[j].key() })
	return out
}

// MergeSignalDispositions reconciles an existing manifest against the live
// catalog and the shipped artifacts.
//
// The derived dispositions are always recomputed from the artifacts, so a panel
// added or deleted since the last run is picked up. A hand-assigned raw_only or
// omitted survives only while the signal still has no curated surface: once one
// appears, the artifacts win and the stale intent value is dropped. Notes are
// preserved verbatim. Rows whose signal no longer exists are pruned.
//
// added and removed are returned for reporting.
func MergeSignalDispositions(base *SignalDispositionBaseline, signals []Signal, refs ArtifactRefs) (merged *SignalDispositionBaseline, added []Signal, removed []SignalDisposition) {
	existing := map[string]SignalDisposition{}
	if base != nil {
		for _, s := range base.Signals {
			existing[s.key()] = s
		}
	}

	live := map[string]bool{}
	out := &SignalDispositionBaseline{Doc: manifestDoc}
	for _, s := range signals {
		row := SignalDisposition{Kind: s.Kind, Name: s.Name, PromName: s.PromName, Surface: s.Surface}
		live[s.key()] = true
		prev, known := existing[s.key()]
		if !known {
			added = append(added, s)
		} else {
			row.Note = prev.Note
		}

		// Every disposition is derived, so regeneration simply restates what the
		// artifacts show. Nothing is carried forward from the previous manifest
		// except the human-written note.
		row.Dispositions = normalizeDispositions(refs.Observed(s))
		out.Signals = append(out.Signals, row)
	}

	if base != nil {
		for _, s := range base.Signals {
			if !live[s.key()] {
				removed = append(removed, s)
			}
		}
	}

	sort.Slice(out.Signals, func(i, j int) bool { return out.Signals[i].key() < out.Signals[j].key() })
	sort.Slice(removed, func(i, j int) bool { return removed[i].key() < removed[j].key() })
	return out, added, removed
}

// SignalProblems groups contract violations by surface, so operational coverage
// and self-observability coverage can be read independently — the point of
// keeping the two apart in the first place.
type SignalProblems map[string][]string

func (p SignalProblems) add(surface, format string, args ...any) {
	if surface != SurfaceOperational && surface != SurfaceSelfObs {
		surface = "unknown surface " + surface
	}
	p[surface] = append(p[surface], fmt.Sprintf(format, args...))
}

// Len is the total number of problems across every surface.
func (p SignalProblems) Len() int {
	n := 0
	for _, v := range p {
		n += len(v)
	}
	return n
}

// String renders the problems surface by surface, deterministically.
func (p SignalProblems) String() string {
	surfaces := make([]string, 0, len(p))
	for s := range p {
		surfaces = append(surfaces, s)
	}
	sort.Strings(surfaces)

	var b strings.Builder
	for _, s := range surfaces {
		msgs := append([]string(nil), p[s]...)
		sort.Strings(msgs)
		fmt.Fprintf(&b, "  [%s] %d problem(s):\n", s, len(msgs))
		for _, m := range msgs {
			fmt.Fprintf(&b, "    - %s\n", m)
		}
	}
	return b.String()
}

// ValidateSignalDispositions reports every way the manifest, the live catalog and
// the shipped artifacts disagree. An empty result means the contract holds.
//
// It is strict in both directions on purpose. An undocumented new signal is a
// coverage gap nobody decided about; a stale row is a lie about what we emit; and
// a disposition that does not match the artifacts is worse than no manifest at
// all, because it is a coverage claim a reader would believe.
func ValidateSignalDispositions(base *SignalDispositionBaseline, signals []Signal, refs ArtifactRefs) SignalProblems {
	problems := SignalProblems{}

	byKey := map[string]SignalDisposition{}
	dupes := map[string]bool{}
	if base != nil {
		for _, row := range base.Signals {
			if _, ok := byKey[row.key()]; ok && !dupes[row.key()] {
				dupes[row.key()] = true
				problems.add(row.Surface, "duplicate manifest row: %s %s", row.Kind, row.Name)
			}
			byKey[row.key()] = row
		}
	}

	live := map[string]bool{}
	for _, s := range signals {
		live[s.key()] = true
		row, ok := byKey[s.key()]
		if !ok {
			problems.add(s.Surface, "NEW signal with no disposition: %s %s — decide what it is for", s.Kind, s.Name)
			continue
		}
		validateRow(problems, s, row, refs)
	}

	if base != nil {
		for _, row := range base.Signals {
			if !live[row.key()] {
				problems.add(row.Surface,
					"STALE manifest row — the code no longer emits %s %s", row.Kind, row.Name)
			}
		}
	}
	return problems
}

// validateRow checks one manifest row against its live signal and the artifacts.
func validateRow(problems SignalProblems, s Signal, row SignalDisposition, refs ArtifactRefs) {
	if row.PromName != s.PromName {
		problems.add(s.Surface, "%s %s: prom_name is %q but the catalog normalizes to %q",
			s.Kind, s.Name, row.PromName, s.PromName)
	}
	if row.Surface != s.Surface {
		problems.add(s.Surface, "%s %s: surface is %q but the descriptor's group derives %q",
			s.Kind, s.Name, row.Surface, s.Surface)
	}

	if len(row.Dispositions) == 0 {
		// A structurally exempt signal has no disposition to record and must not be
		// forced to invent one: the exemption IS its disposition, and it is stated
		// in StructuralExemptions() with a per-signal reason rather than as a value
		// in this file, so the reason cannot be edited away without also removing
		// the exemption.
		if isStructurallyExempt(s) {
			return
		}
		problems.add(s.Surface, "%s %s: empty disposition — after -update this means no dashboard, "+
			"alert or recording rule references it. Give it a PANEL: raw_only and omitted were "+
			"deleted in #526 precisely so this could not be resolved by re-labeling",
			s.Kind, s.Name)
		return
	}
	for _, d := range row.Dispositions {
		if !isKnown(d) {
			problems.add(s.Surface, "%s %s: unknown disposition %q", s.Kind, s.Name, d)
			return
		}
	}

	// Every derived disposition must match the artifacts, in BOTH directions: a
	// claim with no reference is a lie, and a reference with no claim means the
	// manifest under-reports coverage and would soon be trusted anyway.
	observed := map[Disposition]bool{}
	for _, d := range refs.Observed(s) {
		observed[d] = true
	}
	for _, d := range derivedDispositions {
		switch {
		case row.has(d) && !observed[d]:
			problems.add(s.Surface, "%s %s: claims %s, but no %s references %s",
				s.Kind, s.Name, d, artifactFor(d), refName(s))
		case !row.has(d) && observed[d]:
			problems.add(s.Surface, "%s %s: is %s (%s references %s) but the manifest does not say so",
				s.Kind, s.Name, d, artifactFor(d), refName(s))
		}
	}
}

// artifactFor names the artifact that proves a derived disposition, so a failure
// says where to look.
func artifactFor(d Disposition) string {
	switch d {
	case DispVisualized:
		return "PANEL in the deploy/grafana dashboard family"
	case DispAlertable:
		return "alert rule expression in deploy/alerts"
	case DispRecorded:
		return "recording rule expression in deploy/alerts"
	case DispDrivesAVariable:
		return "template variable in the deploy/grafana dashboard family"
	default:
		return "artifact"
	}
}

// refName is the spelling a query has to use for this signal.
func refName(s Signal) string {
	if s.Kind == KindLogEvent {
		return s.Name
	}
	return s.PromName
}

// MarshalSignalDispositions renders a manifest back to the exact on-disk bytes:
// two-space-indented JSON with a trailing newline.
func MarshalSignalDispositions(b *SignalDispositionBaseline) ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("catalog: marshal signal dispositions: %w", err)
	}
	return append(out, '\n'), nil
}

// Generated-region markers in docs/signal-coverage.md. Prose outside them is
// preserved verbatim, the same convention docs/metrics.md and docs/env-vars.md use.
const (
	coverageDocBegin = "<!-- BEGIN GENERATED: signal-coverage -->"
	coverageDocEnd   = "<!-- END GENERATED -->"
)

// SpliceSignalCoverage replaces the content between the generated markers in doc
// with block, preserving all hand-written prose outside them.
func SpliceSignalCoverage(doc, block string) (string, error) {
	bi := strings.Index(doc, coverageDocBegin)
	ei := strings.Index(doc, coverageDocEnd)
	if bi < 0 || ei < 0 || ei < bi {
		return "", fmt.Errorf("docs/signal-coverage.md is missing the %q / %q markers",
			coverageDocBegin, coverageDocEnd)
	}
	return doc[:bi+len(coverageDocBegin)] + "\n\n" + block + "\n" + doc[ei:], nil
}

// surfaceTitles renders a surface key as a heading, in reporting order.
var surfaceTitles = []struct{ key, title string }{
	{SurfaceOperational, "Operational signals"},
	{SurfaceSelfObs, "Self-observability signals"},
}

// SignalCoverageReport renders the manifest as the Markdown body of
// docs/signal-coverage.md: a per-surface summary followed by a per-surface table
// of every signal and what it is for.
//
// It is derived entirely from the committed manifest, which is itself gated
// against the catalog and the shipped artifacts — so the published coverage
// numbers cannot drift from reality without a red test, and nothing here comes
// from a text search over prose.
func SignalCoverageReport(base *SignalDispositionBaseline) string {
	var b strings.Builder
	if base == nil || len(base.Signals) == 0 {
		b.WriteString("_The manifest is empty._\n")
		return b.String()
	}

	perSurface := map[string]map[Disposition]int{}
	totals := map[string]int{}
	rows := map[string][]SignalDisposition{}
	for _, s := range base.Signals {
		if perSurface[s.Surface] == nil {
			perSurface[s.Surface] = map[Disposition]int{}
		}
		for _, d := range s.Dispositions {
			perSurface[s.Surface][d]++
		}
		totals[s.Surface]++
		rows[s.Surface] = append(rows[s.Surface], s)
	}

	b.WriteString("## Summary\n\n")
	b.WriteString("A signal can carry more than one disposition, so the columns do not sum to the total.\n\n")
	b.WriteString("| surface | signals | visualized | alertable | recorded | drives a variable |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, s := range surfaceTitles {
		c := perSurface[s.key]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d |\n",
			s.key, totals[s.key],
			c[DispVisualized], c[DispAlertable], c[DispRecorded], c[DispDrivesAVariable])
	}

	for _, s := range surfaceTitles {
		list := rows[s.key]
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", s.title)
		b.WriteString("| signal | kind | queried as | disposition | note |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, row := range list {
			queried := row.PromName
			if row.Kind == KindLogEvent {
				// Double quotes, not LogQL's backticks: a backtick inside an inline
				// code span ends the span and mangles the rest of the row.
				queried = `event_name="` + row.Name + `"`
			}
			ds := make([]string, 0, len(row.Dispositions))
			for _, d := range normalizeDispositions(row.Dispositions) {
				ds = append(ds, string(d))
			}
			joined := strings.Join(ds, ", ")
			if joined == "" {
				joined = "**UNASSIGNED**"
			}
			fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s |\n",
				row.Name, row.Kind, queried, joined, escapePipes(row.Note))
		}
	}

	return b.String()
}

func escapePipes(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func isStructurallyExempt(s Signal) bool {
	for _, e := range structuralExemptions {
		if e.Kind == s.Kind && e.Name == s.Name {
			return true
		}
	}
	return false
}

func StructuralExemptions() []StructuralExemption {
	return append([]StructuralExemption(nil), structuralExemptions...)
}

var structuralExemptions = []StructuralExemption{
	{
		Kind:  KindLogEvent,
		Name:  "tailscale.webhook.<type>",
		Class: ExemptTemplatedEvent,
		Reason: "The event name is built at emit time from the webhook's `type` field, so " +
			"the catalog entry is a TEMPLATE and no literal `event_name=\"...\"` selector " +
			"can ever equal it. The webhook panels select the family with a regex " +
			"(event_name=~\"tailscale.webhook..+\"), which covers every real event this " +
			"template stands for — but a token match cannot attribute that regex back to " +
			"the template, and widening the extractor to try would start matching " +
			"unrelated names by prefix.",
	},
}

// Two of the three classes have NO members today, which is expected and is not a
// gap to go filling:
//
//   - histogram_base: ArtifactRefs.references already credits a query on
//     _bucket/_sum/_count back to the base signal, so a histogram whose buckets
//     are charted is `visualized` without needing an exemption at all. The class
//     stays defined because that crediting is a property of the extractor, not a
//     guarantee — if it were ever narrowed, these would need naming individually.
//   - recorded_only: nothing is `recorded` without also being `visualized`.
//     Forward-looking, exactly as #526 states. Do not go looking for members.
