package contract

// Upstream operation classification (#423).
//
// This is deliberately a DIFFERENT question from decode drift. internal/oas'
// Classify answers "did a response body we already decode change shape?" and
// only ever looks at ConsumedOpIDs(). It is structurally blind to an operation
// we do not consume — which is exactly where a newly published read endpoint
// appears.
//
// So: enumerate EVERY operation the spec publishes (all verbs, via
// Spec.AllOperations) and require each to carry a disposition. A refreshed spec
// that introduces an unclassified operation fails this gate; the api-drift
// workflow turns that into a deduped advisory issue on its own lane label, so a
// new-endpoint notice never gets mixed into a decode-break issue.
//
// Regenerate with:
//
//	go test ./internal/tsapi/contract -run TestOperationDispositionsInSync -update
//
// As with the field baseline, regeneration adds rows with an EMPTY disposition
// and never invents one.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rknightion/tailscale2otel/v4/internal/oas"
)

// OperationDispositionsFile is the baseline path, relative to this package dir.
const OperationDispositionsFile = "operation_dispositions.json"

//go:embed operation_dispositions.json
var embeddedOperationDispositions []byte

// OpDisposition is the decision recorded against one upstream operation.
type OpDisposition string

const (
	// OpConsumed means tailscale2otel already calls this operation. Every id in
	// ConsumedOpIDs() must carry this and nothing else.
	OpConsumed OpDisposition = "consumed"
	// OpImplement means we intend to consume it; there should be a tracking issue.
	OpImplement OpDisposition = "implement"
	// OpRedundant means the data is already available from an operation we consume.
	OpRedundant OpDisposition = "redundant"
	// OpPrivacyRejected means we will not consume it: the payload is PII or
	// otherwise something we have decided never to observe. The note must say why.
	OpPrivacyRejected OpDisposition = "privacy-rejected"
	// OpUnstable means the endpoint is alpha/undocumented/known-flaky and not
	// safe to build a signal on yet.
	OpUnstable OpDisposition = "unstable"
	// OpParked means considered and consciously deferred — no plan, no objection.
	OpParked OpDisposition = "parked"
	// OpWrite means the operation mutates state. tailscale2otel is read-only, so
	// this is a permanent "not applicable", not a backlog item.
	OpWrite OpDisposition = "write"
)

var validOpDispositions = map[OpDisposition]bool{
	OpConsumed:        true,
	OpImplement:       true,
	OpRedundant:       true,
	OpPrivacyRejected: true,
	OpUnstable:        true,
	OpParked:          true,
	OpWrite:           true,
}

// OperationDisposition is one baseline row.
type OperationDisposition struct {
	ID          string        `json:"id"`
	Method      string        `json:"method"`
	Path        string        `json:"path"`
	Disposition OpDisposition `json:"disposition"`
	Note        string        `json:"note,omitempty"`
}

// OperationDispositionBaseline is the on-disk shape of operation_dispositions.json.
type OperationDispositionBaseline struct {
	Doc        []string               `json:"//"`
	Operations []OperationDisposition `json:"operations"`
}

var operationBaselineDoc = []string{
	"Upstream operation classification (#423). Generated + hand-dispositioned.",
	"Every operation the vendored spec publishes, across ALL verbs — not just the ones we consume.",
	"Regenerate: go test ./internal/tsapi/contract -run TestOperationDispositionsInSync -update",
	"Regeneration adds new operations with an EMPTY disposition and prunes ones upstream removed;",
	"it never assigns a disposition. An empty or unknown disposition fails TestOperationDispositionsInSync.",
	"disposition: consumed | implement | redundant | privacy-rejected | unstable | parked | write",
	"  consumed         - already called by internal/tsapi (must match the contract manifest)",
	"  implement        - we intend to consume it; expect a tracking issue",
	"  redundant        - the data already arrives via an operation we consume",
	"  privacy-rejected - deliberately never observed; the note must say why",
	"  unstable         - alpha/undocumented/flaky; not safe to build a signal on",
	"  parked           - considered and deferred; no plan, no objection",
	"  write            - mutates state; tailscale2otel is read-only, permanently N/A",
}

// OperationInventory returns every operation the spec publishes, sorted by id.
func OperationInventory(spec *oas.Spec) []oas.OperationRef {
	all := spec.AllOperations()
	out := make([]oas.OperationRef, 0, len(all))
	for _, op := range all {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// LoadOperationDispositions parses a baseline from raw JSON.
func LoadOperationDispositions(raw []byte) (*OperationDispositionBaseline, error) {
	var b OperationDispositionBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("contract: parse operation dispositions: %w", err)
	}
	return &b, nil
}

// EmbeddedOperationDispositions returns the compiled-in baseline.
func EmbeddedOperationDispositions() (*OperationDispositionBaseline, error) {
	return LoadOperationDispositions(embeddedOperationDispositions)
}

// MergeOperationDispositions reconciles a baseline against a spec inventory.
// Existing dispositions and notes survive; new operations arrive with an empty
// disposition; operations upstream has withdrawn are dropped.
func MergeOperationDispositions(base *OperationDispositionBaseline, inv []oas.OperationRef) (merged *OperationDispositionBaseline, added []oas.OperationRef, removed []OperationDisposition) {
	existing := map[string]OperationDisposition{}
	if base != nil {
		for _, o := range base.Operations {
			existing[o.ID] = o
		}
	}

	live := map[string]bool{}
	out := &OperationDispositionBaseline{Doc: operationBaselineDoc}
	for _, e := range inv {
		row := OperationDisposition{ID: e.ID, Method: e.Method, Path: e.Path}
		live[e.ID] = true
		if prev, ok := existing[e.ID]; ok {
			row.Disposition = prev.Disposition
			row.Note = prev.Note
		} else {
			added = append(added, e)
		}
		out.Operations = append(out.Operations, row)
	}
	if base != nil {
		for _, o := range base.Operations {
			if !live[o.ID] {
				removed = append(removed, o)
			}
		}
	}

	sort.Slice(out.Operations, func(i, j int) bool { return out.Operations[i].ID < out.Operations[j].ID })
	sort.Slice(removed, func(i, j int) bool { return removed[i].ID < removed[j].ID })
	return out, added, removed
}

// ValidateOperationDispositions reports every disagreement between the baseline
// and the spec inventory. consumedIDs is the manifest's operation list; every id
// in it must be dispositioned "consumed" and nothing else.
//
// A new operation is reported with a READ-CAPABLE marker when its verb is
// inherently read-only, so the drift report can lead with the ones that are
// candidate signal sources.
func ValidateOperationDispositions(base *OperationDispositionBaseline, inv []oas.OperationRef, consumedIDs []string) []string {
	var problems []string

	byID := map[string]OperationDisposition{}
	dupes := map[string]bool{}
	if base != nil {
		for _, o := range base.Operations {
			if _, ok := byID[o.ID]; ok && !dupes[o.ID] {
				dupes[o.ID] = true
				problems = append(problems, fmt.Sprintf("duplicate baseline row: %s", o.ID))
			}
			byID[o.ID] = o
		}
	}

	consumed := map[string]bool{}
	for _, id := range consumedIDs {
		consumed[id] = true
	}

	live := map[string]bool{}
	for _, e := range inv {
		live[e.ID] = true
		o, ok := byID[e.ID]
		if !ok {
			kind := "operation"
			if oas.ReadCapable(e.Method) {
				kind = "READ-CAPABLE operation"
			}
			problems = append(problems, fmt.Sprintf(
				"NEW upstream %s with no disposition: %s (%s %s)%s",
				kind, e.ID, strings.ToUpper(e.Method), e.Path, summarySuffix(e.Summary)))
			continue
		}
		switch {
		case o.Disposition == "":
			problems = append(problems, fmt.Sprintf(
				"empty disposition: %s — assign one of consumed|implement|redundant|privacy-rejected|unstable|parked|write", e.ID))
		case !validOpDispositions[o.Disposition]:
			problems = append(problems, fmt.Sprintf("unknown disposition %q: %s", o.Disposition, e.ID))
		case o.Disposition == OpPrivacyRejected && strings.TrimSpace(o.Note) == "":
			problems = append(problems, fmt.Sprintf("privacy-rejected operation needs a note: %s", e.ID))
		}
		if consumed[e.ID] && o.Disposition != OpConsumed && o.Disposition != "" {
			problems = append(problems, fmt.Sprintf(
				"%s is in the contract manifest but dispositioned %q — a consumed operation must be \"consumed\"",
				e.ID, o.Disposition))
		}
		if !consumed[e.ID] && o.Disposition == OpConsumed {
			problems = append(problems, fmt.Sprintf(
				"%s is dispositioned \"consumed\" but is not in the contract manifest", e.ID))
		}
		if o.Method != e.Method || o.Path != e.Path {
			problems = append(problems, fmt.Sprintf(
				"%s moved upstream: baseline says %s %s, spec says %s %s",
				e.ID, strings.ToUpper(o.Method), o.Path, strings.ToUpper(e.Method), e.Path))
		}
	}

	if base != nil {
		for _, o := range base.Operations {
			if !live[o.ID] {
				problems = append(problems, fmt.Sprintf("WITHDRAWN upstream — baseline row has no operation: %s", o.ID))
			}
		}
	}

	// Consumed ids the spec no longer publishes at all are a manifest problem,
	// but reporting them here too costs nothing and beats a silent gap.
	for _, id := range consumedIDs {
		if !live[id] {
			problems = append(problems, fmt.Sprintf("consumed operation %q is absent from the spec", id))
		}
	}

	sort.Strings(problems)
	return problems
}

// summarySuffix renders an operation summary as a parenthetical, or nothing.
func summarySuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return " — " + s
}

// OperationCoverageReport renders the operation classification as Markdown:
// counts per disposition, then the full table. Built from the vendored spec and
// the committed baseline only — no live data.
func OperationCoverageReport(base *OperationDispositionBaseline) string {
	var b strings.Builder
	b.WriteString("# Upstream operation classification\n\n")
	b.WriteString("Every operation the vendored Tailscale OpenAPI spec publishes, across all verbs,\n")
	b.WriteString("with the decision recorded against it. No live tailnet data.\n\n")

	if base == nil || len(base.Operations) == 0 {
		b.WriteString("_Baseline is empty._\n")
		return b.String()
	}

	order := []OpDisposition{OpConsumed, OpImplement, OpRedundant, OpPrivacyRejected, OpUnstable, OpParked, OpWrite}
	counts := map[OpDisposition]int{}
	unassigned := 0
	for _, o := range base.Operations {
		counts[o.Disposition]++
		if !validOpDispositions[o.Disposition] {
			unassigned++
		}
	}
	b.WriteString("| disposition | count |\n| --- | ---: |\n")
	for _, d := range order {
		fmt.Fprintf(&b, "| %s | %d |\n", d, counts[d])
	}
	fmt.Fprintf(&b, "| **unassigned** | %d |\n| **total** | %d |\n\n", unassigned, len(base.Operations))

	b.WriteString("## Read-capable operations we do not consume\n\n")
	b.WriteString("| operation | method | path | disposition | note |\n| --- | --- | --- | --- | --- |\n")
	gaps := 0
	for _, o := range base.Operations {
		if o.Disposition == OpConsumed || !oas.ReadCapable(o.Method) {
			continue
		}
		gaps++
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s |\n", o.ID, strings.ToUpper(o.Method), o.Path, dispOrUnassigned(o.Disposition), o.Note)
	}
	if gaps == 0 {
		b.WriteString("| _none_ | | | | |\n")
	}

	b.WriteString("\n## All operations\n\n")
	b.WriteString("| operation | method | path | disposition | note |\n| --- | --- | --- | --- | --- |\n")
	for _, o := range base.Operations {
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s |\n", o.ID, strings.ToUpper(o.Method), o.Path, dispOrUnassigned(o.Disposition), o.Note)
	}
	return b.String()
}

func dispOrUnassigned(d OpDisposition) string {
	if d == "" {
		return "**UNASSIGNED**"
	}
	return string(d)
}

// MarshalOperationDispositions renders a baseline back to the on-disk bytes.
func MarshalOperationDispositions(b *OperationDispositionBaseline) ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("contract: marshal operation dispositions: %w", err)
	}
	return append(out, '\n'), nil
}
