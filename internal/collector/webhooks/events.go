package webhooks

import "slices"

// eventVocabulary is the CLOSED set of webhook subscription categories, taken
// verbatim from the vendored OpenAPI spec
// (components.schemas.Webhook.properties.subscriptions.items enum, 18 values).
//
// Do NOT confuse this with the ~100-value audit-log event enum at
// components.parameters.event — different namespace entirely. A value from that
// list appearing here would be a bug, and would fold to eventOther.
//
// Order is the spec's, not alphabetical: node lifecycle, then policy, then user
// lifecycle, then the two routing warnings. Keep it that way so a diff against
// the spec reads straight down.
var eventVocabulary = []string{
	"nodeCreated",
	"nodeNeedsApproval",
	"nodeApproved",
	"nodeKeyExpiringInOneDay",
	"nodeKeyExpired",
	"nodeDeleted",
	"nodeSigned",
	"nodeNeedsSignature",
	"policyUpdate",
	"userCreated",
	"userNeedsApproval",
	"userSuspended",
	"userRestored",
	"userDeleted",
	"userApproved",
	"userRoleUpdated",
	"subnetIPForwardingNotEnabled",
	"exitNodeIPForwardingNotEnabled",
}

// eventOther is the fold target for any subscription value not in the
// vocabulary. Upstream adds event categories without warning, and this metric's
// label would otherwise be unbounded — a single unknown value from a live
// tailnet would mint a permanent new series.
const eventOther = "other"

// eventSet is the membership lookup, built once.
var eventSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(eventVocabulary))
	for _, e := range eventVocabulary {
		m[e] = struct{}{}
	}
	return m
}()

// EventVocabulary returns the documented event categories plus the "other" fold
// target, in emission order. This is the exact label set the zero-seeded
// coverage gauge spans, so its length is the metric's series count.
//
// It returns a fresh slice on every call so a caller cannot mutate the
// package-level order out from under the emitter.
func EventVocabulary() []string {
	out := make([]string, 0, len(eventVocabulary)+1)
	out = append(out, eventVocabulary...)
	return append(out, eventOther)
}

// foldEvent maps a wire value onto the bounded vocabulary.
func foldEvent(v string) string {
	if _, ok := eventSet[v]; ok {
		return v
	}
	return eventOther
}

// isKnownEvent reports whether v is a documented category. Used to tell an
// operator's typo in desired_events ("nodeCreatd") apart from a genuinely
// uncovered category — folding both to "other" would make a config error look
// like a coverage gap forever.
func isKnownEvent(v string) bool {
	_, ok := eventSet[v]
	return ok
}

// splitDesired partitions the configured desired-event list into the recognized
// categories (deduplicated, in vocabulary order so emission is deterministic)
// and the unrecognized raw entries.
func splitDesired(desired []string) (known, unknown []string) {
	seenKnown := make(map[string]struct{}, len(desired))
	seenUnknown := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		if isKnownEvent(d) {
			if _, dup := seenKnown[d]; !dup {
				seenKnown[d] = struct{}{}
				known = append(known, d)
			}
			continue
		}
		if _, dup := seenUnknown[d]; !dup {
			seenUnknown[d] = struct{}{}
			unknown = append(unknown, d)
		}
	}
	slices.SortFunc(known, func(a, b string) int {
		return slices.Index(eventVocabulary, a) - slices.Index(eventVocabulary, b)
	})
	slices.Sort(unknown)
	return known, unknown
}
