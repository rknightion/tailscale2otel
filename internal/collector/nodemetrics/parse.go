package nodemetrics

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// sample is one parsed metric line, resolved against the family TYPE that
// precedes it. cumulative is true for counters and for the _bucket/_sum/_count
// components of histogram and summary families (which are treated like counters).
type sample struct {
	name       string
	labels     map[string]string
	value      float64
	help       string
	cumulative bool
}

// familyType is the parsed "# TYPE" of a metric family.
type familyType int

const (
	typeUnknown familyType = iota
	typeCounter
	typeGauge
	typeHistogram
	typeSummary
	typeUntyped
)

// parse reads the Prometheus text exposition format from r and returns one
// sample per valid metric line. HELP/TYPE comment lines configure the family
// metadata applied to following samples; blank and malformed lines are skipped
// robustly. parse only returns an error for an underlying read failure.
func parse(r io.Reader, maxSamples int) ([]sample, error) {
	types := map[string]familyType{}
	helps := map[string]string{}
	var out []sample

	sc := bufio.NewScanner(r)
	// Allow long lines (large histograms / many labels).
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			parseComment(line, types, helps)
			continue
		}
		s, ok := parseSample(line, types, helps)
		if ok {
			if maxSamples > 0 && len(out) >= maxSamples {
				return nil, fmt.Errorf("nodemetrics: sample count exceeds max_samples (%d)", maxSamples)
			}
			out = append(out, s)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseComment handles "# HELP name text" and "# TYPE name kind" lines, updating
// the family help/type maps keyed by metric family name. Other comments are
// ignored.
func parseComment(line string, types map[string]familyType, helps map[string]string) {
	body := strings.TrimSpace(strings.TrimPrefix(line, "#"))
	switch {
	case strings.HasPrefix(body, "HELP"):
		rest := strings.TrimSpace(strings.TrimPrefix(body, "HELP"))
		name, text, _ := strings.Cut(rest, " ")
		if name != "" {
			helps[name] = unescapeHelp(strings.TrimSpace(text))
		}
	case strings.HasPrefix(body, "TYPE"):
		rest := strings.TrimSpace(strings.TrimPrefix(body, "TYPE"))
		name, kind, _ := strings.Cut(rest, " ")
		if name != "" {
			types[name] = parseType(strings.TrimSpace(kind))
		}
	}
}

func parseType(s string) familyType {
	switch s {
	case "counter":
		return typeCounter
	case "gauge":
		return typeGauge
	case "histogram":
		return typeHistogram
	case "summary":
		return typeSummary
	case "untyped":
		return typeUntyped
	default:
		return typeUnknown
	}
}

// parseSample parses a single sample line of the form
//
//	name value [timestamp]
//	name{k="v",k2="v2"} value [timestamp]
//
// resolving its family metadata. The trailing timestamp, if present, is ignored.
// It returns ok=false for any malformed line.
func parseSample(line string, types map[string]familyType, helps map[string]string) (sample, bool) {
	var name string
	var labels map[string]string
	var rest string

	if i := strings.IndexByte(line, '{'); i >= 0 {
		name = strings.TrimSpace(line[:i])
		var (
			consumed int
			ok       bool
		)
		labels, consumed, ok = parseLabelSet(line[i+1:])
		if !ok {
			return sample{}, false
		}
		rest = strings.TrimSpace(line[i+1+consumed:])
	} else {
		// name value [ts]
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return sample{}, false
		}
		name = fields[0]
		rest = strings.Join(fields[1:], " ")
	}

	if name == "" {
		return sample{}, false
	}

	// rest = "value [timestamp]". Take the first whitespace-delimited field as
	// the value; ignore any trailing timestamp.
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return sample{}, false
	}
	value, ok := parseValue(fields[0])
	if !ok {
		return sample{}, false
	}

	cumulative := isCumulative(name, types)
	if cumulative && math.IsNaN(value) {
		// Counter components must not be NaN; drop the sample entirely.
		return sample{}, false
	}
	return sample{
		name:       name,
		labels:     labels,
		value:      value,
		help:       helps[familyName(name, types)],
		cumulative: cumulative,
	}, true
}

// isCumulative reports whether a metric name should be treated as a cumulative
// (counter-like) series. A bare counter is cumulative; the _bucket/_sum/_count
// components of a histogram or summary family are cumulative; everything else
// (gauge, untyped, unknown) is not.
func isCumulative(name string, types map[string]familyType) bool {
	if ft, ok := types[name]; ok {
		return ft == typeCounter
	}
	// No direct type: it may be a component of a histogram/summary family.
	if _, ft, ok := componentFamily(name, types); ok {
		return ft == typeHistogram || ft == typeSummary
	}
	return false
}

// familyName returns the family name used to look up HELP text: the component's
// base family name for histogram/summary components, otherwise the name itself.
func familyName(name string, types map[string]familyType) string {
	if _, ok := types[name]; ok {
		return name
	}
	if base, _, ok := componentFamily(name, types); ok {
		return base
	}
	return name
}

// componentFamily, for a series name ending in _bucket/_sum/_count, returns the
// base family name and its declared type if that base family is a known
// histogram or summary.
func componentFamily(name string, types map[string]familyType) (string, familyType, bool) {
	for _, suf := range []string{"_bucket", "_sum", "_count"} {
		if base, cut := strings.CutSuffix(name, suf); cut {
			if ft, ok := types[base]; ok {
				return base, ft, true
			}
		}
	}
	return "", typeUnknown, false
}

// parseLabelSet parses a Prometheus label set from s, which is the text
// immediately AFTER the opening '{'. It returns the parsed labels and the number
// of bytes of s consumed up to and including the matching closing '}', so the
// caller can resume after it. It is quote-aware: label values are arbitrary
// quoted strings that may legally contain unescaped '}' and ',' (only \\, \" and
// \n are escapes), so the terminating '}' is the first one found OUTSIDE a
// quoted value. ok is false on any malformed list (the whole line is then dropped).
func parseLabelSet(s string) (labels map[string]string, consumed int, ok bool) {
	out := map[string]string{}
	orig := s
	// consumedTo computes bytes consumed when the cursor `cur` points at the
	// closing '}', including that brace.
	consumedTo := func(cur string) int { return len(orig) - len(cur) + 1 }

	s = strings.TrimLeft(s, " \t")
	if len(s) > 0 && s[0] == '}' { // empty label set: {}
		return out, consumedTo(s), true
	}
	for {
		s = strings.TrimLeft(s, " \t")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return nil, 0, false
		}
		key := strings.TrimSpace(s[:eq])
		if key == "" {
			return nil, 0, false
		}
		s = strings.TrimLeft(s[eq+1:], " \t")
		if len(s) == 0 || s[0] != '"' {
			return nil, 0, false // value must be a quoted string
		}
		val, after, vok := scanQuoted(s)
		if !vok {
			return nil, 0, false
		}
		out[key] = val
		s = strings.TrimLeft(after, " \t")
		if len(s) == 0 {
			return nil, 0, false // unterminated (no closing '}')
		}
		switch s[0] {
		case ',':
			s = strings.TrimLeft(s[1:], " \t")
			if len(s) > 0 && s[0] == '}' { // tolerate a trailing comma
				return out, consumedTo(s), true
			}
		case '}':
			return out, consumedTo(s), true
		default:
			return nil, 0, false
		}
	}
}

// unescapeHelp unescapes a Prometheus HELP text, where only "\\" (backslash) and
// "\n" (newline) are defined escapes.
func unescapeHelp(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// scanQuoted consumes a leading double-quoted, backslash-escaped string from s
// (which must start with '"') and returns the unescaped value plus the remainder
// after the closing quote. ok=false if the string is unterminated.
func scanQuoted(s string) (value, remainder string, ok bool) {
	if len(s) == 0 || s[0] != '"' {
		return "", "", false
	}
	var b strings.Builder
	i := 1
	for i < len(s) {
		ch := s[i]
		if ch == '\\' {
			if i+1 >= len(s) {
				return "", "", false
			}
			switch s[i+1] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			default:
				// Unknown escape: keep the escaped char verbatim.
				b.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		if ch == '"' {
			return b.String(), s[i+1:], true
		}
		b.WriteByte(ch)
		i++
	}
	return "", "", false // unterminated
}

// parseValue parses a Prometheus float value, handling Inf/+Inf/-Inf and NaN.
func parseValue(s string) (float64, bool) {
	switch s {
	case "+Inf", "Inf":
		return math.Inf(1), true
	case "-Inf":
		return math.Inf(-1), true
	case "NaN":
		return math.NaN(), true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
