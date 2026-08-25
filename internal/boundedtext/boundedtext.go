// Package boundedtext provides UTF-8-safe byte ceilings for untrusted text
// retained in bounded stores.
package boundedtext

import (
	"strings"
	"unicode/utf8"
)

// String returns a valid-UTF-8 prefix of s no longer than max bytes.
func String(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if len(s) <= max {
		return s
	}
	keep := max
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep]
}

// StringsBudget bounds each value and their aggregate byte size in slice
// order. Earlier values have priority when the aggregate budget is exhausted.
func StringsBudget(values []string, perValue, total int) {
	remaining := total
	for i := range values {
		limit := perValue
		if remaining < limit {
			limit = remaining
		}
		values[i] = String(values[i], limit)
		remaining -= len(values[i])
		if remaining < 0 {
			remaining = 0
		}
	}
}
