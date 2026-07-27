package main

import (
	"fmt"
	"regexp"
	"strings"
)

// builtins maps every Grafana global template token this repo's artifacts may
// legitimately use to a stand-in that parses in the position the token appears.
// The point is only to make the surrounding expression parseable — the VALUES
// are arbitrary and deliberately unrealistic-but-valid, so nobody mistakes this
// for a simulation of how Grafana actually interpolates at query time.
//
//	token             substituted with   why
//	----------------  -----------------  --------------------------------------
//	$__rate_interval  5m                 always a range-selector duration
//	$__interval       1m                 always a duration
//	$__interval_ms    60000              the millisecond twin, a bare number
//	$__auto           5m                 Loki's range-selector duration
//	$__range          6h                 the dashboard time range, as a duration
//	$__range_s        21600              same, in seconds — a bare number
//	$__range_ms       21600000           same, in milliseconds — a bare number
//	$__from           1700000000000      epoch-ms integer
//	$__to             1700003600000      epoch-ms integer
//
// A `$__`-prefixed token that is NOT in this table is an error rather than a
// guess: an unrecognized global is far more likely to be a typo than a token
// Grafana would have resolved.
var builtins = map[string]string{
	"__rate_interval": "5m",
	"__interval":      "1m",
	"__interval_ms":   "60000",
	"__auto":          "5m",
	"__range":         "6h",
	"__range_s":       "21600",
	"__range_ms":      "21600000",
	"__from":          "1700000000000",
	"__to":            "1700003600000",
}

// Stand-ins for dashboard template variables, chosen per position — see
// substitute for how the position is decided.
const (
	stringStandIn   = ".*" // inside a string literal: a valid regex and a valid literal
	durationStandIn = "5m" // directly inside a range selector
	numberStandIn   = "1"  // anywhere else, e.g. topk($topn, ...)
)

// tokenRe matches all three Grafana templating spellings: `$name`, `${name}` /
// `${name:format}`, and the legacy `[[name]]` / `[[name:format]]`.
var tokenRe = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*(?::[A-Za-z0-9_]+)?\}|\$[A-Za-z_][A-Za-z0-9_]*|\[\[[A-Za-z_][A-Za-z0-9_]*(?::[A-Za-z0-9_]+)?\]\]`)

// interpolation describes what templating a given artifact is allowed to use.
type interpolation struct {
	// templated is false for rule files. Grafana alerting and Prometheus both
	// evaluate rule expressions with no dashboard and no time-range picker, so
	// ANY templating token there is a bug that would 500 at evaluation time,
	// not something to substitute away.
	templated bool
	declared  map[string]bool
}

// dashboardInterpolation allows templating, restricted to the dashboard's own
// declared variables plus the built-in globals.
func dashboardInterpolation(declared map[string]bool) interpolation {
	return interpolation{templated: true, declared: declared}
}

// ruleInterpolation forbids templating outright.
func ruleInterpolation() interpolation {
	return interpolation{}
}

// substitute replaces every Grafana templating token in expr with a stand-in
// that parses, and reports the first token it cannot account for.
//
// Position matters: a variable inside a string literal must be replaced by
// something that keeps the literal valid, one inside a range selector by a
// duration, and one used as an argument (topk($topn, …)) by a number. Getting
// this wrong turns a healthy expression into a spurious parse error, which is
// worse than not checking it at all.
func substitute(expr string, ctx interpolation) (string, error) {
	mask := stringMask(expr)

	var b strings.Builder
	last := 0
	for _, loc := range tokenRe.FindAllStringIndex(expr, -1) {
		start, end := loc[0], loc[1]
		token := expr[start:end]
		name := tokenName(token)

		if !ctx.templated {
			return "", fmt.Errorf("%s: rule expressions get no templating — Grafana and Prometheus both evaluate them verbatim, so this token reaches the query engine as-is", token)
		}

		var replacement string
		switch {
		case strings.HasPrefix(name, "__"):
			v, ok := builtins[name]
			if !ok {
				return "", fmt.Errorf("%s: unknown Grafana built-in token (known: %s)", token, strings.Join(sortedKeys(builtins), ", "))
			}
			replacement = v
		case !ctx.declared[name]:
			return "", fmt.Errorf("$%s: not declared by this dashboard — spec.variables[] has no variable named %q", name, name)
		case mask[start]:
			replacement = stringStandIn
		case inRangeSelector(expr, start, end):
			replacement = durationStandIn
		default:
			replacement = numberStandIn
		}

		b.WriteString(expr[last:start])
		b.WriteString(replacement)
		last = end
	}
	b.WriteString(expr[last:])
	out := b.String()

	if err := checkNoStrayDollar(out); err != nil {
		return "", err
	}
	return out, nil
}

// tokenName strips the sigils and any `:format` suffix off a matched token.
func tokenName(token string) string {
	name := token
	switch {
	case strings.HasPrefix(name, "${"):
		name = name[2 : len(name)-1]
	case strings.HasPrefix(name, "[["):
		name = name[2 : len(name)-2]
	default:
		name = name[1:]
	}
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	return name
}

// checkNoStrayDollar is the backstop for anything tokenRe did not recognize.
//
// A `$` inside a string literal that is not followed by an identifier start is
// left alone: that is a regex end-anchor (`=~"foo$"`), which is legitimate
// PromQL. Everywhere else a surviving `$` means a malformed token.
func checkNoStrayDollar(expr string) error {
	mask := stringMask(expr)
	for i := 0; i < len(expr); i++ {
		if expr[i] != '$' {
			continue
		}
		next := byte(0)
		if i+1 < len(expr) {
			next = expr[i+1]
		}
		identish := next == '{' || next == '_' ||
			(next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z')
		if mask[i] && !identish {
			continue // regex anchor
		}
		return fmt.Errorf("unsubstituted `$` at offset %d — malformed Grafana template token", i)
	}
	return nil
}

// stringMask marks every byte that sits inside a PromQL/LogQL string literal
// (double-quoted, single-quoted, or backquoted-raw). The opening and closing
// quotes themselves are marked false, so a token starting immediately after the
// quote is still reported as "inside".
func stringMask(expr string) []bool {
	mask := make([]bool, len(expr))
	var quote byte
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		switch {
		case quote == 0:
			if c == '"' || c == '\'' || c == '`' {
				quote = c
			}
		case c == '\\' && quote != '`':
			// Escape sequence: the escaped byte cannot close the literal.
			mask[i] = true
			if i+1 < len(expr) {
				i++
				mask[i] = true
			}
		case c == quote:
			quote = 0
		default:
			mask[i] = true
		}
	}
	return mask
}

// inRangeSelector reports whether expr[start:end] is the entire body of a
// bracketed range or subquery selector, i.e. it needs a duration rather than a
// number.
func inRangeSelector(expr string, start, end int) bool {
	i := start - 1
	for i >= 0 && (expr[i] == ' ' || expr[i] == '\t') {
		i--
	}
	if i < 0 || expr[i] != '[' {
		return false
	}
	j := end
	for j < len(expr) && (expr[j] == ' ' || expr[j] == '\t') {
		j++
	}
	return j < len(expr) && (expr[j] == ']' || expr[j] == ':')
}
