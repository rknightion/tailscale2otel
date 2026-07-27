package main

import (
	"fmt"
	"io"
	"sort"
)

// Lang is the query language a single extracted expression is written in.
//
// Only PromQL is actually parsed (see the package comment for why LogQL and
// TraceQL are not); the other values exist so the summary can state loudly how
// much of each artifact went UNCHECKED.
type Lang string

const (
	LangPromQL     Lang = "promql"
	LangLogQL      Lang = "logql"
	LangTraceQL    Lang = "traceql"
	LangServerSide Lang = "server-side" // Grafana `__expr__` node, not a query at all
)

// Expr is one query expression extracted from a generated artifact, carrying
// enough locator to find it again by hand.
type Expr struct {
	File  string // repo-relative path of the artifact it came from
	Where string // in-file locator: panel title/id, or group/uid/title
	Lang  Lang
	Raw   string // the expression exactly as it appears in the artifact
}

// String renders the locator prefix shared by verbose listings and failures.
func (e Expr) String() string {
	return fmt.Sprintf("%s: %s [%s]", e.File, e.Where, e.Lang)
}

// Failure is one expression that did not pass its check.
type Failure struct {
	Expr
	Reason string
}

// String renders a single actionable line: where it is, what is wrong, and the
// expression itself so the reader never has to open the artifact to guess.
func (f Failure) String() string {
	return fmt.Sprintf("%s\n    %s\n    expr: %s", f.Expr.String(), f.Reason, f.Raw)
}

// Report accumulates everything one run learned.
type Report struct {
	Counts   map[Lang]int
	Checked  []Expr
	Failures []Failure
}

func newReport() *Report {
	return &Report{Counts: map[Lang]int{}}
}

// record notes that an expression was seen, whether or not it was parsed.
func (r *Report) record(e Expr) {
	r.Counts[e.Lang]++
	r.Checked = append(r.Checked, e)
}

// fail records a check failure against an already-recorded expression.
func (r *Report) fail(e Expr, format string, args ...any) {
	r.Failures = append(r.Failures, Failure{Expr: e, Reason: fmt.Sprintf(format, args...)})
}

// write prints the report. Verbose listings come first so the failures and the
// summary stay at the bottom where a CI log gets truncated from.
func (r *Report) write(w io.Writer, verbose bool) {
	if verbose {
		for _, e := range r.Checked {
			fmt.Fprintf(w, "%s\n    %s\n", e.String(), e.Raw)
		}
		fmt.Fprintln(w)
	}

	for _, f := range r.Failures {
		fmt.Fprintf(w, "FAIL %s\n", f.String())
	}
	if len(r.Failures) > 0 {
		fmt.Fprintln(w)
	}

	// Every non-PromQL language is named explicitly in its own line, because a
	// bare "checked N" summary would let a reader assume the whole artifact was
	// validated when in fact whole panels were skipped.
	unparsed := r.Counts[LangLogQL] + r.Counts[LangTraceQL]
	if unparsed > 0 {
		fmt.Fprintf(w, "NOT PARSED: %d logql + %d traceql expressions were extracted and\n",
			r.Counts[LangLogQL], r.Counts[LangTraceQL])
		fmt.Fprintln(w, "  variable-checked, but their SYNTAX was NOT validated — no usable parser.")
		fmt.Fprintln(w, "  See the package comment in tools/promqlcheck/main.go for why.")
	}

	fmt.Fprintf(w, "promqlcheck: checked %d promql, %d logql, %d traceql, %d server-side; %d failures\n",
		r.Counts[LangPromQL], r.Counts[LangLogQL], r.Counts[LangTraceQL],
		r.Counts[LangServerSide], len(r.Failures))
}

// sortedKeys returns a map's keys in a stable order, so the report a CI log
// shows is byte-identical between runs of the same input.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
