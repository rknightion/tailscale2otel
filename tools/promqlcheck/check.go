package main

import (
	"github.com/prometheus/prometheus/promql/parser"
)

// promParser is the real Prometheus 3.x expression parser — the same code the
// server uses, so "it parses here" means "Mimir will accept it".
//
// Every experimental option is left off deliberately: an expression that only
// parses with an experimental feature enabled would parse here and then be
// rejected by a stock Grafana Cloud datasource, which is the failure this tool
// is supposed to prevent.
var promParser = parser.NewParser(parser.Options{})

// checkExpr runs the checks that apply to e, recording any failure on rep.
//
// Variable checking is language-agnostic and runs on everything. Syntax
// checking only runs on PromQL: LogQL and TraceQL are counted and reported as
// UNPARSED rather than quietly passed (see the package comment).
func checkExpr(rep *Report, e Expr, ctx interpolation) {
	substituted, err := substitute(e.Raw, ctx)
	if err != nil {
		rep.fail(e, "%v", err)
		return
	}
	if e.Lang != LangPromQL {
		return
	}
	if _, err := promParser.ParseExpr(substituted); err != nil {
		// The parser reports offsets against the SUBSTITUTED text, so echo that
		// text whenever substitution actually changed something — otherwise the
		// reported column points into a string the reader cannot see.
		if substituted != e.Raw {
			rep.fail(e, "promql parse error: %v\n    (after template substitution: %s)", err, substituted)
			return
		}
		rep.fail(e, "promql parse error: %v", err)
	}
}
