package contract

// Consumed-field disposition contract (#422).
//
// FieldInventory() (fields.go) says WHAT we decode. This file says what we DO
// with each decoded field, and gates the two against each other so that a
// consumed type gaining a field fails CI until someone records a disposition
// for it.
//
// The dispositions live in an out-of-band JSON baseline rather than in struct
// tags for two reasons: four consumed response types are declared in the
// upstream tailscale-client-go library and cannot be annotated in this repo at
// all, and keeping every disposition in one file makes the contract reviewable
// as a single diff.
//
// Regenerate with:
//
//	go test ./internal/tsapi/contract -run TestFieldDispositionsInSync -update
//
// Regeneration is deliberately NON-silencing: it adds newly discovered fields
// with an EMPTY disposition and prunes fields that no longer exist, but it never
// invents a disposition. An empty disposition always fails the gate, so running
// -update can only ever move work forward, never hide it.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FieldDispositionsFile is the baseline path, relative to this package
// directory. Tests and the regeneration flag share this constant.
const FieldDispositionsFile = "field_dispositions.json"

//go:embed field_dispositions.json
var embeddedFieldDispositions []byte

// Disposition records what tailscale2otel does with one decoded response field.
type Disposition string

const (
	// DispEmitted means the value reaches OTLP — as a metric value, a metric
	// attribute, or a log-record attribute.
	DispEmitted Disposition = "emitted"
	// DispEnrichment means the value feeds a cache, derived state, or a gating
	// decision, and is never emitted verbatim.
	DispEnrichment Disposition = "enrichment"
	// DispExcluded means the value is decoded but deliberately dropped, for
	// privacy (PII) or cardinality reasons. The note must say which.
	DispExcluded Disposition = "excluded"
	// DispUnhandled means the value is decoded but has no consumer yet. An
	// acknowledged gap, not an oversight.
	DispUnhandled Disposition = "unhandled"
)

// validDispositions is the closed set. Anything else — including the empty
// string a regeneration leaves on a new field — fails the gate.
var validDispositions = map[Disposition]bool{
	DispEmitted:    true,
	DispEnrichment: true,
	DispExcluded:   true,
	DispUnhandled:  true,
}

// FieldDisposition is one baseline row.
type FieldDisposition struct {
	Op          string      `json:"op"`
	Path        string      `json:"path"`
	Type        string      `json:"type"`
	Disposition Disposition `json:"disposition"`
	Note        string      `json:"note,omitempty"`
}

// key is the identity of a row: (operationId, field path).
func (f FieldDisposition) key() string { return f.Op + "\x00" + f.Path }

// FieldDispositionBaseline is the on-disk shape of field_dispositions.json.
type FieldDispositionBaseline struct {
	// Doc is a leading "//" key so the file explains itself to a reviewer.
	Doc []string `json:"//"`
	// Fields is sorted by (Op, Path) so diffs stay minimal.
	Fields []FieldDisposition `json:"fields"`
}

// baselineDoc is the self-describing header written into the generated file.
var baselineDoc = []string{
	"Consumed-field disposition contract (#422). Generated + hand-dispositioned.",
	"Regenerate: go test ./internal/tsapi/contract -run TestFieldDispositionsInSync -update",
	"Regeneration adds new fields with an EMPTY disposition and prunes removed ones;",
	"it never assigns a disposition. Fill every empty one in by hand — an empty or",
	"unknown disposition fails TestFieldDispositionsInSync.",
	"disposition: emitted | enrichment | excluded | unhandled",
	"  emitted    - reaches OTLP as a metric value/attribute or log attribute",
	"  enrichment - feeds a cache, derived state or a gating decision; never emitted verbatim",
	"  excluded   - decoded but deliberately dropped; the note must say privacy or cardinality",
	"  unhandled  - decoded, no consumer yet; an acknowledged gap",
}

// LoadFieldDispositions parses a baseline from raw JSON.
func LoadFieldDispositions(raw []byte) (*FieldDispositionBaseline, error) {
	var b FieldDispositionBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("contract: parse field dispositions: %w", err)
	}
	return &b, nil
}

// EmbeddedFieldDispositions returns the compiled-in baseline. Callers that do
// not have the repository checked out (e.g. tools/apidrift rendering a coverage
// report) use this rather than a filesystem path.
func EmbeddedFieldDispositions() (*FieldDispositionBaseline, error) {
	return LoadFieldDispositions(embeddedFieldDispositions)
}

// MergeFieldDispositions reconciles an existing baseline against the live
// inventory: existing dispositions and notes are preserved verbatim, fields new
// to the inventory are appended with an EMPTY disposition, and rows whose field
// no longer exists are dropped. The result is sorted by (Op, Path).
//
// added and removed are returned for reporting.
func MergeFieldDispositions(base *FieldDispositionBaseline, inv []InventoryEntry) (merged *FieldDispositionBaseline, added []InventoryEntry, removed []FieldDisposition) {
	existing := map[string]FieldDisposition{}
	if base != nil {
		for _, f := range base.Fields {
			existing[f.key()] = f
		}
	}

	live := map[string]bool{}
	out := &FieldDispositionBaseline{Doc: baselineDoc}
	for _, e := range inv {
		row := FieldDisposition{Op: e.Op, Path: e.Path, Type: e.Type}
		live[row.key()] = true
		if prev, ok := existing[row.key()]; ok {
			row.Disposition = prev.Disposition
			row.Note = prev.Note
		} else {
			added = append(added, e)
		}
		out.Fields = append(out.Fields, row)
	}
	if base != nil {
		for _, f := range base.Fields {
			if !live[f.key()] {
				removed = append(removed, f)
			}
		}
	}

	sort.Slice(out.Fields, func(i, j int) bool {
		if out.Fields[i].Op != out.Fields[j].Op {
			return out.Fields[i].Op < out.Fields[j].Op
		}
		return out.Fields[i].Path < out.Fields[j].Path
	})
	sort.Slice(removed, func(i, j int) bool { return removed[i].key() < removed[j].key() })
	return out, added, removed
}

// ValidateFieldDispositions reports every way the baseline and the live
// inventory disagree. An empty result means the contract holds.
//
// It is deliberately strict in both directions: an undocumented new field is a
// gap, and a stale row is a lie about what we decode.
func ValidateFieldDispositions(base *FieldDispositionBaseline, inv []InventoryEntry) []string {
	var problems []string

	byKey := map[string]FieldDisposition{}
	dupes := map[string]bool{}
	if base != nil {
		for _, f := range base.Fields {
			if _, ok := byKey[f.key()]; ok && !dupes[f.key()] {
				dupes[f.key()] = true
				problems = append(problems, fmt.Sprintf("duplicate baseline row: %s / %s", f.Op, f.Path))
			}
			byKey[f.key()] = f
		}
	}

	live := map[string]bool{}
	for _, e := range inv {
		k := e.Op + "\x00" + e.Path
		live[k] = true
		f, ok := byKey[k]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"NEW decoded field with no disposition: %s / %s (%s)", e.Op, e.Path, e.Type))
			continue
		}
		switch {
		case f.Disposition == "":
			problems = append(problems, fmt.Sprintf(
				"empty disposition: %s / %s — assign one of emitted|enrichment|excluded|unhandled", e.Op, e.Path))
		case !validDispositions[f.Disposition]:
			problems = append(problems, fmt.Sprintf(
				"unknown disposition %q: %s / %s", f.Disposition, e.Op, e.Path))
		case f.Disposition == DispExcluded && strings.TrimSpace(f.Note) == "":
			problems = append(problems, fmt.Sprintf(
				"excluded field needs a note saying privacy or cardinality: %s / %s", e.Op, e.Path))
		}
	}

	if base != nil {
		for _, f := range base.Fields {
			if !live[f.key()] {
				problems = append(problems, fmt.Sprintf(
					"STALE baseline row — field no longer decoded: %s / %s", f.Op, f.Path))
			}
		}
	}

	sort.Strings(problems)
	return problems
}

// FieldCoverageReport renders the baseline as reviewable Markdown: a per-op
// disposition breakdown plus the full field table.
//
// The report is derived entirely from static type reflection and the committed
// baseline, so it contains NO live tailnet data — safe to attach to a CI summary
// or a public issue.
func FieldCoverageReport(base *FieldDispositionBaseline) string {
	var b strings.Builder
	b.WriteString("# Consumed-field disposition coverage\n\n")
	b.WriteString("Derived from static reflection over the decoded response types plus the\n")
	b.WriteString("committed disposition baseline. Contains no live tailnet data.\n\n")

	if base == nil || len(base.Fields) == 0 {
		b.WriteString("_Baseline is empty._\n")
		return b.String()
	}

	order := []Disposition{DispEmitted, DispEnrichment, DispExcluded, DispUnhandled}
	total := map[Disposition]int{}
	perOp := map[string]map[Disposition]int{}
	var ops []string
	unassigned := 0
	for _, f := range base.Fields {
		if perOp[f.Op] == nil {
			perOp[f.Op] = map[Disposition]int{}
			ops = append(ops, f.Op)
		}
		perOp[f.Op][f.Disposition]++
		total[f.Disposition]++
		if !validDispositions[f.Disposition] {
			unassigned++
		}
	}
	sort.Strings(ops)

	b.WriteString("| operation | emitted | enrichment | excluded | unhandled | unassigned | total |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, op := range ops {
		c := perOp[op]
		n := 0
		for _, v := range c {
			n += v
		}
		bad := n
		for _, d := range order {
			bad -= c[d]
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d |\n",
			op, c[DispEmitted], c[DispEnrichment], c[DispExcluded], c[DispUnhandled], bad, n)
	}
	fmt.Fprintf(&b, "| **all** | %d | %d | %d | %d | %d | %d |\n",
		total[DispEmitted], total[DispEnrichment], total[DispExcluded], total[DispUnhandled],
		unassigned, len(base.Fields))

	b.WriteString("\n## Fields\n\n| operation | field | disposition | note |\n| --- | --- | --- | --- |\n")
	for _, f := range base.Fields {
		d := string(f.Disposition)
		if d == "" {
			d = "**UNASSIGNED**"
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", f.Op, f.Path, d, f.Note)
	}
	return b.String()
}

// MarshalFieldDispositions renders a baseline back to the exact on-disk bytes:
// two-space-indented JSON with a trailing newline.
func MarshalFieldDispositions(b *FieldDispositionBaseline) ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("contract: marshal field dispositions: %w", err)
	}
	return append(out, '\n'), nil
}
