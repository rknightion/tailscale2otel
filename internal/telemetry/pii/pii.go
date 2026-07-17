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

// Redactor decides, per attribute, whether it must be dropped given the enabled categories.
type Redactor struct {
	enabled Categories
	anyOff  bool
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
		return false
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
	for k, v := range attrs {
		if !r.redactKey(k, v) {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		body = strings.ReplaceAll(body, s, bodyRedactedPlaceholder)
	}
	return body
}
