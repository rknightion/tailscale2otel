package pii

import (
	"slices"
	"strings"
)

// bodyRedactedPlaceholder replaces a value removed from a log body.
const bodyRedactedPlaceholder = "[redacted]"

// Category is a PII/identifier class an operator can toggle off.
type Category string

const (
	CatEmails           Category = "emails"
	CatUserDisplayNames Category = "user_display_names"
	CatUserIDs          Category = "user_ids"
	CatHostnames        Category = "hostnames"
	CatNodeIDs          Category = "node_ids"
	CatTailscaleIPs     Category = "tailscale_ips"
	CatInternalIPs      Category = "internal_ips"
	CatExternalIPs      Category = "external_ips"
	CatServiceAddrs     Category = "service_addrs"
	CatEndpointPaths    Category = "endpoint_paths"
	CatNetworkTopology  Category = "network_topology"
	CatTailnetName      Category = "tailnet_name"
	CatFreeTextDetails  Category = "free_text_details"
)

// AllCategories is the canonical ordered list (used by config, self-obs, tests).
var AllCategories = []Category{
	CatEmails, CatUserDisplayNames, CatUserIDs, CatHostnames, CatNodeIDs,
	CatTailscaleIPs, CatInternalIPs, CatExternalIPs, CatServiceAddrs,
	CatEndpointPaths, CatNetworkTopology, CatTailnetName, CatFreeTextDetails,
}

// Categories is the enabled/disabled state per category (true = emitted).
type Categories map[Category]bool

// ipCategories are the three range-classified IP categories. A value that must be
// classified into one of them but cannot be is unsafe while ANY of them is off.
var ipCategories = []Category{CatTailscaleIPs, CatInternalIPs, CatExternalIPs}

// Redactor decides, per attribute, whether it must be dropped given the enabled categories.
type Redactor struct {
	enabled  Categories
	anyOff   bool
	anyIPOff bool
}

// New builds a Redactor. If every category is enabled (or the map is nil), the filter
// methods are no-ops (fast path). A nil/absent category counts as enabled.
func New(c Categories) *Redactor {
	r := &Redactor{enabled: c}
	for _, cat := range AllCategories {
		if v, ok := c[cat]; ok && !v {
			r.anyOff = true
			break
		}
	}
	for _, cat := range ipCategories {
		if v, ok := c[cat]; ok && !v {
			r.anyIPOff = true
			break
		}
	}
	return r
}

// disabled reports whether category cat is explicitly turned off.
func (r *Redactor) disabled(cat Category) bool {
	v, ok := r.enabled[cat]
	return ok && !v
}

// redactKey reports whether (key,value) belongs to a disabled category.
func (r *Redactor) redactKey(key string, value any) bool {
	if ipValueKeys[key] {
		s, _ := value.(string)
		if cls := classifyIP(s); cls != ipNotIP {
			if cat, ok := categoryForIPClass(cls); ok {
				return r.disabled(cat)
			}
			return false
		}
		if fb, ok := ipKeyFallback[key]; ok {
			return r.disabled(fb)
		}
		// Fail closed (#465): this key's value is ONLY ever an IP address (it has no
		// ipKeyFallback), so a non-empty value that will not classify has an unknown
		// permitted category. Emitting it would let malformed or re-encoded address
		// text carry an address past a disabled IP category, so it is dropped while
		// any IP category is off. The rejected value is never logged or echoed —
		// only the boolean decision leaves this function.
		return r.anyIPOff && nonEmptyValue(value)
	}
	if cat, ok := keyCategory[key]; ok {
		return r.disabled(cat)
	}
	if nonIdentifier[key] {
		return false
	}
	// Unknown key (e.g. nodemetrics pass-through label): value-classify IPs only.
	if s, ok := value.(string); ok {
		if cls := classifyIP(s); cls != ipNotIP {
			if cat, ok := categoryForIPClass(cls); ok {
				return r.disabled(cat)
			}
		}
	}
	return false
}

// nonEmptyValue reports whether value carries anything at all. A nil attribute or
// an empty string has nothing to disclose, so the fail-closed rule leaves it alone
// (dropping it would only churn the attribute set). Any other type reaching an
// IP-valued key is already off-contract, so it counts as non-empty.
func nonEmptyValue(value any) bool {
	if value == nil {
		return false
	}
	if s, ok := value.(string); ok {
		return s != ""
	}
	return true
}

// Merge filters attrs for additive instruments (counter/histogram). Drops redacted keys.
func (r *Redactor) Merge(attrs map[string]any) map[string]any {
	if !r.anyOff || len(attrs) == 0 {
		return attrs
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if r.redactKey(k, v) {
			continue
		}
		out[k] = v
	}
	return out
}

// Identity filters attrs for point-in-time instruments (gauge/updowncounter). If the
// datapoint carries >=1 identity key and ALL present identity keys are redacted,
// suppress=true. Otherwise it drops every redacted attr (identity or not) and emits.
func (r *Redactor) Identity(attrs map[string]any) (map[string]any, bool) {
	if !r.anyOff || len(attrs) == 0 {
		return attrs, false
	}
	identityPresent, identityAllRedacted := 0, true
	for k, v := range attrs {
		if identityKeys[k] {
			identityPresent++
			if !r.redactKey(k, v) {
				identityAllRedacted = false
			}
		}
	}
	if identityPresent > 0 && identityAllRedacted {
		return nil, true
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if r.redactKey(k, v) {
			continue
		}
		out[k] = v
	}
	return out, false
}

// Log filters attrs for a log record. Drops redacted attrs; never suppresses.
func (r *Redactor) Log(attrs map[string]any) map[string]any {
	return r.Merge(attrs)
}

// RedactBody returns a log body with disabled-category identifiers removed. Log
// bodies bypass the attribute filter, so without this a value dropped from the
// attributes could still leave the process in the body (#197). Two rules apply,
// in order:
//
//   - Whole-body replacement: if any category in bodyPII is disabled, the body is
//     a standalone free-text value (a raw upstream error, a webhook message) whose
//     entire content belongs to that category, so it is replaced wholesale. Emit
//     sites declare bodyPII for such bodies (telemetry.Event.BodyPII).
//   - Attr-value scrub: for a MIXED body that embeds identifier values which are
//     also carried as attributes (flow addresses, a key description, an app name),
//     every attribute value that would itself be redacted (its category disabled)
//     is removed wherever it appears in the body, leaving the non-PII structure
//     (transport, byte counts, scope counts, ...) intact.
//
// When no category is disabled (fast path) or the body is empty it is returned
// unchanged, so a fully-enabled deployment keeps byte-identical bodies.
func (r *Redactor) RedactBody(body string, bodyPII []Category, attrs map[string]any) string {
	if !r.anyOff || body == "" {
		return body
	}
	if slices.ContainsFunc(bodyPII, r.disabled) {
		return bodyRedactedPlaceholder
	}
	return scrubValues(body, r.sensitiveValues(attrs))
}

// sensitiveValues returns the deduplicated attribute values that must not appear
// in a body, ordered LONGEST FIRST with a lexicographic tie-break (#472).
//
// The order is the whole point: attrs is a Go map, so ranging it yields a
// different order on every call. Independent replacements in that order are not
// deterministic, and when one candidate is a substring of another, replacing the
// shorter one first leaves the longer one's tail behind as a recognizable
// remnant. Sorting longest-first (ties broken lexicographically so equal-length
// candidates also have a fixed order) makes the result a pure function of the
// candidate SET rather than of map iteration order.
func (r *Redactor) sensitiveValues(attrs map[string]any) []string {
	var vals []string
	for k, v := range attrs {
		if !r.redactKey(k, v) {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		vals = append(vals, s)
	}
	if len(vals) < 2 {
		return vals
	}
	slices.Sort(vals)
	vals = slices.Compact(vals) // the same value under two keys is one candidate
	slices.SortStableFunc(vals, func(a, b string) int { return len(b) - len(a) })
	return vals
}

// scrubValues replaces every occurrence of vals in body with the placeholder in a
// single left-to-right pass.
//
// A single pass (rather than one strings.ReplaceAll per candidate) is what makes
// the marker un-matchable: bytes already emitted as "[redacted]" are never
// re-examined, so a candidate like "red" cannot match inside a placeholder
// written for some other value. At each position the longest candidate wins,
// because vals is ordered longest-first.
//
// UTF-8 validity is preserved: non-matching bytes are copied verbatim, and a
// valid-UTF-8 needle can only ever match at a rune boundary inside a valid-UTF-8
// haystack (a valid encoding never starts with a continuation byte), so a match
// can never split a rune.
//
// Cost is O(len(body) x len(vals)) worst case, and in practice one array lookup
// per byte: firstByte gates the candidate scan to positions that could start a
// match at all. See BenchmarkRedactBody* for the bound on a large body.
func scrubValues(body string, vals []string) string {
	if len(vals) == 0 {
		return body
	}
	var firstByte [256]bool
	for _, v := range vals {
		firstByte[v[0]] = true
	}
	var b strings.Builder
	last := 0 // start of the run not yet copied out
	for i := 0; i < len(body); {
		if !firstByte[body[i]] {
			i++
			continue
		}
		n := 0
		for _, v := range vals {
			if strings.HasPrefix(body[i:], v) {
				n = len(v)
				break
			}
		}
		if n == 0 {
			i++
			continue
		}
		if last == 0 {
			b.Grow(len(body) + len(bodyRedactedPlaceholder))
		}
		b.WriteString(body[last:i])
		b.WriteString(bodyRedactedPlaceholder)
		i += n
		last = i
	}
	if last == 0 { // nothing matched: return the original string, not a copy
		return body
	}
	b.WriteString(body[last:])
	return b.String()
}
