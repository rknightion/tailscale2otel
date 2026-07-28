package config

import (
	"fmt"
	"sort"
	"strings"
)

// unknownFileKeys returns the sorted set of fileKeys (the dotted config keys
// found in a YAML config file, as returned by koanf.Keys() on a
// file-only-loaded koanf instance) that are neither a known config key nor a
// child of a known collection key (a map or list-of-structs field such as
// otlp.headers or tailnets — see keyExtendsKnown in env.go). Unlike
// unknownEnvVars (advisory only, for TS2OTEL_* variables), the result here is
// used by Load to build a hard error: issue #303 requires an unknown YAML key
// to fail Load outright, not just warn.
func unknownFileKeys(fileKeys, validKeys []string) []string {
	valid := make(map[string]bool, len(validKeys))
	for _, k := range validKeys {
		valid[k] = true
	}
	var unknown []string
	for _, key := range fileKeys {
		if valid[key] || keyExtendsKnown(key, valid) {
			continue
		}
		unknown = append(unknown, key)
	}
	sort.Strings(unknown)
	return unknown
}

// maxSuggestDistance caps how close an unknown key must be to a valid one
// before it is offered as a suggestion, so a wholly unrelated key never gets a
// nonsense "did you mean" — an edit distance of 2, or a third of the key's own
// length (whichever is larger), stays a "plausible typo" bound rather than
// matching arbitrary short keys.
func maxSuggestDistance(key string) int {
	third := len(key) / 3
	if third > 2 {
		return third
	}
	return 2
}

// closestValidKey returns the validKeys entry with the smallest Levenshtein
// distance to key, provided that distance is within maxSuggestDistance(key).
// It returns "" when nothing is close enough to be worth suggesting.
func closestValidKey(key string, validKeys []string) string {
	best := ""
	bestDist := -1
	limit := maxSuggestDistance(key)
	for _, candidate := range validKeys {
		d := levenshtein(key, candidate)
		if d > limit {
			continue
		}
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = candidate
		}
	}
	return best
}

// levenshtein computes the classic edit distance between a and b using
// stdlib-only, O(len(a)*len(b)) dynamic programming over a single row.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// removedKeys names configuration keys this project used to accept, mapped to
// what an operator should do about each one.
//
// They need their own message for two reasons. A removed key is not a typo, so
// the nearest-neighbor suggestion actively misleads — "destination_service"
// sits two edits from "destination_port", a real and unrelated setting, and an
// operator who takes that suggestion has silently changed their cardinality.
//
// The second reason is a promise this repository already made. CHANGELOG.md and
// the chart notes for 0.13.0 both told operators that a leftover
// destination_service: would be SILENTLY IGNORED. Rejecting unknown keys breaks
// that promise: an upgrade documented as safe now fails at startup. Failing is
// still right — a key that does nothing should not look like it does something —
// but the operator must be told which promise changed, not sent hunting for a
// spelling mistake.
//
// Add an entry here whenever a key is removed, and keep it for at least a major.
var removedKeys = map[string]string{
	"cardinality.flow.destination_service": "removed in 0.13.0: tailscale.dst.service is now emitted " +
		"unconditionally on both flow metric families, so the toggle governs nothing — delete this key",
}

// unknownKeyError builds the user-facing hard error for one or more unknown
// YAML config keys, naming every offending dotted path and, where a close
// valid key exists, suggesting it. It never echoes a config VALUE — only key
// paths, which are safe to log (see the secret-hygiene requirement in #303).
func unknownKeyError(unknown, validKeys []string) error {
	parts := make([]string, len(unknown))
	for i, key := range unknown {
		if why, ok := removedKeys[key]; ok {
			parts[i] = fmt.Sprintf("%q (%s)", key, why)
			continue
		}
		if suggestion := closestValidKey(key, validKeys); suggestion != "" {
			parts[i] = fmt.Sprintf("%q (did you mean %q?)", key, suggestion)
		} else {
			parts[i] = fmt.Sprintf("%q", key)
		}
	}
	return fmt.Errorf("unknown configuration key(s) in config file: %s — remove them or fix the "+
		"typo; unrecognized keys are rejected rather than silently ignored", strings.Join(parts, ", "))
}
